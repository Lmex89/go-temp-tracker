package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sensors-temp/internal/store"
	"sensors-temp/internal/timeutil"
	"time"

	"database/sql"

	"github.com/jackc/pgx/v5"
)

// sqliteCopySource streams rows from SQLite into pgx's CopyFrom protocol.
// It implements pgx.CopyFromSource so the migration never holds the entire
// table in memory. Like a Python generator/iterator that yields one row at a time.
type sqliteCopySource struct {
	rows *sql.Rows
	err  error
}

// Next advances to the next row. Returns false when done or on error.
func (s *sqliteCopySource) Next() bool {
	if s.err != nil {
		return false
	}
	next := s.rows.Next()
	if !next {
		s.err = s.rows.Err()
	}
	return next
}

// Values returns the current row as PostgreSQL-compatible values.
// SQLite stores created_at as a UTC string; we parse it to time.Time so
// Postgres inserts it as a proper TIMESTAMPTZ.
func (s *sqliteCopySource) Values() ([]interface{}, error) {
	var sensor, metricType, unit, createdAt string
	var tempC float64
	if err := s.rows.Scan(&sensor, &tempC, &metricType, &unit, &createdAt); err != nil {
		return nil, err
	}

	ts, err := time.Parse(timeutil.DBLayout, createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at %q: %w", createdAt, err)
	}

	return []interface{}{sensor, tempC, metricType, unit, ts.UTC()}, nil
}

// Err returns any iteration error encountered by Next().
func (s *sqliteCopySource) Err() error {
	return s.err
}

// main only wires os.Args/os.Stdout/os.Stderr into run and turns its result
// into a process exit code. Python analogy: sys.exit(main(sys.argv[1:])).
// Keeping the logic in run() lets tests call the command in-process with
// their own buffers instead of spawning a subprocess.
func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// errCommitUncertain marks a COMMIT that failed in a way that does not tell us
// whether PostgreSQL applied it (for example the connection dropped). The
// operator must inspect the target before retrying.
var errCommitUncertain = errors.New("commit result UNCERTAIN")

// targetState says what we can truthfully claim about the PostgreSQL target
// when a migration fails. Python analogy: a custom exception carrying a field.
type targetState int

const (
	// stateUnknown: no honest claim is possible (e.g. the rollback itself failed).
	stateUnknown targetState = iota
	// stateUntouched: we failed before opening any PostgreSQL transaction.
	stateUntouched
	// stateRolledBack: a transaction was open and its rollback was confirmed.
	stateRolledBack
)

// migrationError wraps a failure with the target state. Go has no exception
// subclasses: callers use errors.As to read the extra field, and Unwrap keeps
// errors.Is (errCommitUncertain) working through the wrapper.
type migrationError struct {
	state targetState
	err   error
}

func (e *migrationError) Error() string { return e.err.Error() }
func (e *migrationError) Unwrap() error { return e.err }

// rowsPerSecond avoids +Inf/NaN: float division by zero does not raise in Go
// (unlike Python's ZeroDivisionError), it silently yields Inf or NaN.
func rowsPerSecond(copied int64, elapsed time.Duration) float64 {
	if copied <= 0 || elapsed <= 0 {
		return 0
	}
	return float64(copied) / elapsed.Seconds()
}

// run executes the migration and returns the process exit code
// (0 = success, 1 = failure, 2 = bad command line).
// Python analogy: the body of main(argv, stdout, stderr). Passing the writers
// in (instead of using sys.stdout) lets tests capture the output, and returning
// an int instead of calling sys.exit lets tests assert the exit code.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("migrate-to-postgres", flag.ContinueOnError)
	fs.SetOutput(stderr)
	sqlitePath := fs.String("sqlite", store.DefaultSQLitePath, "Source SQLite database file")
	postgresURL := fs.String("postgres", "", "Target PostgreSQL URL (defaults to DATABASE_URL, then docker-compose defaults)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// Flag, then DATABASE_URL, then docker-compose default (shared with the service).
	*postgresURL = store.ResolvePostgresDSN(*postgresURL)

	// Never print the DSN: it may hold the password. Show host:port/database.
	fmt.Fprintf(stdout, "Migrating %s -> %s\n", *sqlitePath, describeTarget(*postgresURL))

	if err := migrate(context.Background(), stdout, *sqlitePath, *postgresURL); err != nil {
		// Errors from pgx/url.Parse may echo the DSN or password: redact the text
		// only, so errors.Is below still sees the original error chain.
		fmt.Fprintln(stderr, redactText(err.Error(), *postgresURL))
		// Only claim what is true: "left as they were" needs a confirmed rollback.
		var me *migrationError
		switch {
		case errors.Is(err, errCommitUncertain):
			fmt.Fprintln(stderr, "The commit may or may not have been applied: inspect the target readings table before retrying.")
		case errors.As(err, &me) && me.state == stateUntouched:
			fmt.Fprintln(stderr, "Migration FAILED before touching PostgreSQL: the target was not changed.")
		case errors.As(err, &me) && me.state == stateRolledBack:
			fmt.Fprintln(stderr, "Migration FAILED before commit: the transaction was rolled back, so the target readings and identity sequence were left as they were.")
		default:
			fmt.Fprintln(stderr, "Migration FAILED and the target state could not be confirmed: inspect the target readings table before retrying.")
		}
		return 1
	}
	return 0
}

// migrate replaces the target readings with a consistent snapshot of the
// SQLite readings. Everything that changes PostgreSQL (schema, truncate, copy,
// verification) runs in ONE transaction, so any error before COMMIT rolls the
// target back. Python analogy: `with conn:` around the whole job -- commit on
// success, rollback on exception.
func migrate(ctx context.Context, stdout io.Writer, sqlitePath, postgresURL string) (err error) {
	// Tag every failure with what we can claim about the target. This deferred
	// func runs last (defers are LIFO) and may replace the named result err,
	// like a try/finally that re-raises a wrapped exception.
	var tx pgx.Tx
	committed, rolledBack := false, false
	defer func() {
		if err == nil || committed {
			return
		}
		switch {
		case tx == nil:
			err = &migrationError{stateUntouched, err}
		case rolledBack:
			err = &migrationError{stateRolledBack, err}
		default:
			err = &migrationError{stateUnknown, err}
		}
	}()

	// Open SQLite -- same driver the main app uses.
	sqliteDB, err := store.Open("sqlite", sqlitePath)
	if err != nil {
		return fmt.Errorf("Failed to open SQLite: %w", err)
	}
	defer sqliteDB.Close()

	if err := sqliteDB.Ping(); err != nil {
		return fmt.Errorf("Failed to connect to SQLite: %w", err)
	}

	// One read transaction = one snapshot (the DB runs in WAL mode). The count
	// and the row stream below both read from it, so they always agree even if
	// temp-tracker keeps writing. Writes made after the first read are invisible.
	snapshot, err := sqliteDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("Failed to start SQLite snapshot: %w", err)
	}
	defer snapshot.Rollback() // read-only: nothing to commit

	var total int64
	if err := snapshot.QueryRow("SELECT COUNT(*) FROM readings").Scan(&total); err != nil {
		return fmt.Errorf("Failed to count SQLite rows: %w", err)
	}
	fmt.Fprintf(stdout, "Source rows (snapshot): %d\n", total)
	fmt.Fprintln(stdout, "Rows written to SQLite after this snapshot are not included; to be exact, stop temp-tracker and re-run.")

	// Native pgx API (not database/sql) so we can use CopyFrom for bulk loading.
	pgConn, err := pgx.Connect(ctx, postgresURL)
	if err != nil {
		return fmt.Errorf("Failed to connect to PostgreSQL: %w", err)
	}
	defer pgConn.Close(ctx)

	tx, err = pgConn.Begin(ctx)
	if err != nil {
		tx = nil // a failed Begin opened nothing
		return fmt.Errorf("Failed to start PostgreSQL transaction: %w", err)
	}
	// Registered after pgConn's Close, so (LIFO) it runs BEFORE the connection
	// closes: the rollback needs a live connection to be confirmed.
	defer func() {
		if err != nil && !committed {
			rolledBack = tx.Rollback(ctx) == nil
		}
	}()

	// Schema DDL is transactional in PostgreSQL, so it is undone on rollback too.
	if _, err := tx.Exec(ctx, store.PostgresSchema); err != nil {
		return fmt.Errorf("Failed to create Postgres schema: %w", err)
	}

	// Destructive by design (a full replace), but only once the commit succeeds.
	// Back up PostgreSQL first: this is not a substitute for a backup.
	if _, err := tx.Exec(ctx, "TRUNCATE TABLE readings RESTART IDENTITY"); err != nil {
		return fmt.Errorf("Failed to truncate Postgres readings: %w", err)
	}

	// Stream rows from the snapshot. Order by id keeps the migration deterministic.
	rows, err := snapshot.Query("SELECT sensor, temp_c, metric_type, unit, created_at FROM readings ORDER BY id")
	if err != nil {
		return fmt.Errorf("Failed to query SQLite: %w", err)
	}
	defer rows.Close()

	fmt.Fprintln(stdout, "Copy started (pending)")
	start := time.Now()
	copied, err := tx.CopyFrom(ctx, pgx.Identifier{"readings"},
		[]string{"sensor", "temp_c", "metric_type", "unit", "created_at"}, &sqliteCopySource{rows: rows})
	if err != nil {
		return fmt.Errorf("Failed to copy rows to PostgreSQL: %w", err)
	}
	elapsed := time.Since(start)
	fmt.Fprintf(stdout, "Copied %d rows in %v (%.0f rows/sec), pending commit\n", copied, elapsed, rowsPerSecond(copied, elapsed))

	var pgCount int64
	if err := tx.QueryRow(ctx, "SELECT COUNT(*) FROM readings").Scan(&pgCount); err != nil {
		return fmt.Errorf("Failed to count Postgres rows: %w", err)
	}
	if copied != total || pgCount != total {
		return fmt.Errorf("Verification failed, count mismatch: snapshot=%d copied=%d target=%d", total, copied, pgCount)
	}

	committed = true // from here neither "untouched" nor "rolled back" may be claimed
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%w: %v", errCommitUncertain, err)
	}
	fmt.Fprintf(stdout, "Migration committed: target readings replaced with %d rows.\n", total)
	return nil
}
