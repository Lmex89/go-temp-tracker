package main

import (
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"sensors-temp/internal/store"
)

// testPostgresEnv is the ONLY way these tests reach PostgreSQL. It is
// deliberately not DATABASE_URL, so a production DSN in the environment can
// never be used by accident. Python analogy: a pytest fixture that skips when
// an env var is missing.
const testPostgresEnv = "MIGRATE_TEST_POSTGRES_URL"

// reading is one row as the tests compare it: created_at is the UTC string
// "2006-01-02 15:04:05" on both engines.
type reading struct {
	Sensor, MetricType, Unit, CreatedAt string
	TempC                               float64
}

// sampleReadings returns n distinct readings, one per minute.
func sampleReadings(n int) []reading {
	base := time.Date(2024, 3, 1, 10, 0, 0, 0, time.UTC)
	out := make([]reading, n)
	for i := range out {
		out[i] = reading{
			Sensor:     fmt.Sprintf("sensor-%d", i%3),
			TempC:      40 + float64(i)/4,
			MetricType: []string{"temperature", "cpu", "load"}[i%3],
			Unit:       []string{"C", "%", ""}[i%3],
			CreatedAt:  base.Add(time.Duration(i) * time.Minute).Format("2006-01-02 15:04:05"),
		}
	}
	return out
}

// newSQLiteSource creates a temporary SQLite file with the given readings.
func newSQLiteSource(t *testing.T, rows []reading) string {
	t.Helper()
	path := t.TempDir() + "/source.db"
	db := store.NewSQLiteStore(path).InitDB()
	if db == nil {
		t.Fatal("cannot create temporary SQLite database")
	}
	t.Cleanup(func() { db.Close() })
	addSQLiteRows(t, path, rows)
	return path
}

// addSQLiteRows appends rows to a SQLite file (also used for concurrent writes).
func addSQLiteRows(t *testing.T, path string, rows []reading) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, r := range rows {
		if _, err := db.Exec(
			"INSERT INTO readings (sensor, temp_c, metric_type, unit, created_at) VALUES (?, ?, ?, ?, ?)",
			r.Sensor, r.TempC, r.MetricType, r.Unit, r.CreatedAt); err != nil {
			t.Fatal(err)
		}
	}
}

// pgTarget is an isolated PostgreSQL schema plus a handle bound to it.
type pgTarget struct {
	DSN string
	DB  *sql.DB
}

// newPGTarget creates a throwaway schema and returns a DSN whose search_path
// points at it, so "readings" resolves inside that schema only. The test is
// skipped (never silently passed as verified) when PostgreSQL is unavailable.
func newPGTarget(t *testing.T) *pgTarget {
	t.Helper()
	base := os.Getenv(testPostgresEnv)
	if base == "" {
		t.Skipf("%s not set: PostgreSQL tests skipped, atomicity NOT verified", testPostgresEnv)
	}
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Skipf("PostgreSQL unavailable (%v): atomicity NOT verified", err)
	}
	if err := admin.Ping(); err != nil {
		admin.Close()
		t.Skipf("PostgreSQL unavailable (%v): atomicity NOT verified", err)
	}
	schema := fmt.Sprintf("mig_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
		admin.Close()
		t.Skipf("cannot create test schema (%v): atomicity NOT verified", err)
	}
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	dsn := base + sep + "search_path=" + schema
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		admin.Exec("DROP SCHEMA " + schema + " CASCADE")
		admin.Close()
	})
	return &pgTarget{DSN: dsn, DB: db}
}

// seed prepares the target table (same schema the command uses) with rows.
func (p *pgTarget) seed(t *testing.T, rows []reading) {
	t.Helper()
	if _, err := p.DB.Exec(store.PostgresSchema); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if _, err := p.DB.Exec(
			"INSERT INTO readings (sensor, temp_c, metric_type, unit, created_at) VALUES ($1, $2, $3, $4, $5)",
			r.Sensor, r.TempC, r.MetricType, r.Unit, r.CreatedAt+"+00"); err != nil {
			t.Fatal(err)
		}
	}
}

// readings returns the target content ordered by id.
func (p *pgTarget) readings(t *testing.T) []reading {
	t.Helper()
	rs, err := p.DB.Query(`SELECT sensor, temp_c, metric_type, unit,
		to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS') FROM readings ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	var out []reading
	for rs.Next() {
		var r reading
		if err := rs.Scan(&r.Sensor, &r.TempC, &r.MetricType, &r.Unit, &r.CreatedAt); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rs.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// nextID inserts one reading and returns the identity PostgreSQL assigned.
func (p *pgTarget) nextID(t *testing.T) int64 {
	t.Helper()
	var id int64
	err := p.DB.QueryRow(`INSERT INTO readings (sensor, temp_c) VALUES ('probe', 1) RETURNING id`).Scan(&id)
	if err != nil {
		t.Fatalf("insert after migration: %v", err)
	}
	return id
}

// runMigration calls the command in-process, like `migrate-to-postgres ...`.
func runMigration(sqlitePath, dsn string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = run([]string{"-sqlite", sqlitePath, "-postgres", dsn}, &out, &errOut)
	return code, out.String(), errOut.String()
}

func assertSameReadings(t *testing.T, got, want []reading) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("row count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestMigrateReplacesTargetAndCanBeRepeated(t *testing.T) {
	src := sampleReadings(25)
	sqlitePath := newSQLiteSource(t, src)
	pg := newPGTarget(t)
	pg.seed(t, []reading{{Sensor: "stale", TempC: 99, MetricType: "temperature", Unit: "C", CreatedAt: "2020-01-01 00:00:00"}})

	for run := 1; run <= 2; run++ {
		code, stdout, stderr := runMigration(sqlitePath, pg.DSN)
		if code != 0 {
			t.Fatalf("run %d: exit %d, stderr=%q", run, code, stderr)
		}
		if !strings.Contains(stdout, "Migration committed") {
			t.Errorf("run %d: success message missing from stdout: %q", run, stdout)
		}
		assertSameReadings(t, pg.readings(t), src)
	}

	// The identity restarted, so the next insert continues after the copied rows.
	if id := pg.nextID(t); id != int64(len(src))+1 {
		t.Errorf("next id = %d, want %d", id, len(src)+1)
	}
}

func TestMigrateEmptySourceEmptiesTarget(t *testing.T) {
	sqlitePath := newSQLiteSource(t, nil)
	pg := newPGTarget(t)
	pg.seed(t, sampleReadings(3))

	code, _, stderr := runMigration(sqlitePath, pg.DSN)
	if code != 0 {
		t.Fatalf("exit %d, stderr=%q", code, stderr)
	}
	if got := pg.readings(t); len(got) != 0 {
		t.Fatalf("target has %d rows, want 0", len(got))
	}
	if id := pg.nextID(t); id != 1 {
		t.Errorf("next id = %d, want 1", id)
	}
}

// assertTargetUntouched checks content AND identity sequence of the target
// are exactly what they were before a failed migration.
func assertTargetUntouched(t *testing.T, pg *pgTarget, before []reading) {
	t.Helper()
	assertSameReadings(t, pg.readings(t), before)
	if id := pg.nextID(t); id != int64(len(before))+1 {
		t.Errorf("next id after failed migration = %d, want %d (sequence was not preserved)", id, len(before)+1)
	}
}

func TestMigrateConversionFailureAfterCopyStartsKeepsTarget(t *testing.T) {
	// The bad timestamp is the last row, so many rows are already streamed
	// into PostgreSQL when the conversion fails.
	src := sampleReadings(40)
	src[39].CreatedAt = "not-a-timestamp"
	sqlitePath := newSQLiteSource(t, src)
	pg := newPGTarget(t)
	before := sampleReadings(3)
	pg.seed(t, before)

	code, stdout, stderr := runMigration(sqlitePath, pg.DSN)
	if code == 0 {
		t.Fatalf("exit 0, want failure; stdout=%q", stdout)
	}
	if !strings.Contains(stderr, "not-a-timestamp") {
		t.Errorf("stderr lacks diagnostic: %q", stderr)
	}
	if strings.Contains(stdout, "Migration committed") {
		t.Errorf("success reported on failure: %q", stdout)
	}
	assertTargetUntouched(t, pg, before)
}

func TestMigrateVerificationFailureKeepsTarget(t *testing.T) {
	sqlitePath := newSQLiteSource(t, sampleReadings(10))
	pg := newPGTarget(t)
	before := sampleReadings(3)
	pg.seed(t, before)
	// A trigger that silently drops one row makes the copied count differ from
	// the source view, which the verification step must refuse to commit.
	if _, err := pg.DB.Exec(`
		CREATE FUNCTION drop_one() RETURNS trigger LANGUAGE plpgsql AS
		$$ BEGIN IF NEW.sensor = 'sensor-1' AND NEW.temp_c = 40.25 THEN RETURN NULL; END IF; RETURN NEW; END $$;
		CREATE TRIGGER drop_one BEFORE INSERT ON readings FOR EACH ROW EXECUTE FUNCTION drop_one();`); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runMigration(sqlitePath, pg.DSN)
	if code == 0 {
		t.Fatalf("exit 0, want failure; stdout=%q", stdout)
	}
	if strings.Contains(stdout, "Migration committed") {
		t.Errorf("success reported on failure: %q", stdout)
	}
	if !strings.Contains(stderr, "mismatch") {
		t.Errorf("stderr should explain the count mismatch: %q", stderr)
	}
	assertTargetUntouched(t, pg, before)
}

func TestMigrateCommitRejectedKeepsTarget(t *testing.T) {
	sqlitePath := newSQLiteSource(t, sampleReadings(5))
	pg := newPGTarget(t)
	before := sampleReadings(3)
	pg.seed(t, before)
	// A deferred constraint trigger only fires at COMMIT time, so the failure
	// is produced by the commit itself.
	if _, err := pg.DB.Exec(`
		CREATE FUNCTION fail_at_commit() RETURNS trigger LANGUAGE plpgsql AS
		$$ BEGIN RAISE EXCEPTION 'commit blocked by test'; END $$;
		CREATE CONSTRAINT TRIGGER fail_at_commit AFTER INSERT ON readings
		DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION fail_at_commit();`); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runMigration(sqlitePath, pg.DSN)
	if code == 0 {
		t.Fatalf("exit 0, want failure; stdout=%q", stdout)
	}
	if strings.Contains(stdout, "Migration committed") {
		t.Errorf("success reported on failed commit: %q", stdout)
	}
	if stderr == "" {
		t.Error("stderr empty on failed commit")
	}
	// Remove the trigger so the assertions' own inserts are not rejected too.
	if _, err := pg.DB.Exec("DROP TRIGGER fail_at_commit ON readings"); err != nil {
		t.Fatal(err)
	}
	assertTargetUntouched(t, pg, before)
}

func TestMigrateConnectionFailure(t *testing.T) {
	sqlitePath := newSQLiteSource(t, sampleReadings(3))
	// Port 1 on loopback is closed; no PostgreSQL needed for this one.
	code, stdout, _ := runMigration(sqlitePath, "postgres://u:p@127.0.0.1:1/db?sslmode=disable&connect_timeout=2")
	if code == 0 {
		t.Fatalf("exit 0, want failure; stdout=%q", stdout)
	}
	if strings.Contains(stdout, "Migration committed") {
		t.Errorf("success reported on connection failure: %q", stdout)
	}
}

// startCommitKillingProxy listens on a loopback port and forwards to the real
// PostgreSQL, but when the client sends COMMIT it drops both sockets without
// forwarding it. The client therefore cannot know whether the commit happened.
func startCommitKillingProxy(t *testing.T, realDSN string) (proxyDSN string) {
	t.Helper()
	u, err := url.Parse(realDSN)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	realAddr := u.Host // captured now: u.Host is overwritten below
	go func() {
		for {
			client, err := ln.Accept()
			if err != nil {
				return
			}
			server, err := net.Dial("tcp", realAddr)
			if err != nil {
				client.Close()
				continue
			}
			go io.Copy(client, server)
			go func() {
				defer client.Close()
				defer server.Close()
				buf := make([]byte, 64*1024)
				for {
					n, err := client.Read(buf)
					if n > 0 {
						if bytes.Contains(bytes.ToLower(buf[:n]), []byte("commit")) {
							return
						}
						if _, werr := server.Write(buf[:n]); werr != nil {
							return
						}
					}
					if err != nil {
						return
					}
				}
			}()
		}
	}()
	u.Host = ln.Addr().String()
	return u.String()
}

func TestMigrateLostConnectionDuringCommitIsReportedAsUncertain(t *testing.T) {
	sqlitePath := newSQLiteSource(t, sampleReadings(5))
	pg := newPGTarget(t)
	pg.seed(t, sampleReadings(3))

	code, stdout, stderr := runMigration(sqlitePath, startCommitKillingProxy(t, pg.DSN))
	if code == 0 {
		t.Fatalf("exit 0, want failure; stdout=%q", stdout)
	}
	if strings.Contains(stdout, "Migration committed") {
		t.Errorf("success reported with unknown commit result: %q", stdout)
	}
	if !strings.Contains(stderr, "UNCERTAIN") || !strings.Contains(stderr, "inspect") {
		t.Errorf("stderr must warn the result is uncertain and to inspect the target: %q", stderr)
	}
	if strings.Contains(strings.ToLower(stderr), "rolled back") || strings.Contains(strings.ToLower(stderr), "unchanged") {
		t.Errorf("stderr must not promise a rollback: %q", stderr)
	}
}

func TestMigrateCopiesOneConsistentSQLiteView(t *testing.T) {
	src := sampleReadings(12)
	sqlitePath := newSQLiteSource(t, src)
	pg := newPGTarget(t)
	pg.seed(t, sampleReadings(2))

	// Hold a table lock on the target so the command blocks AFTER it has taken
	// its SQLite view and BEFORE it copies: a controlled window for writes.
	lockTx, err := pg.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockTx.Exec("LOCK TABLE readings IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}

	type result struct {
		code           int
		stdout, stderr string
	}
	done := make(chan result, 1)
	go func() {
		c, o, e := runMigration(sqlitePath, pg.DSN)
		done <- result{c, o, e}
	}()

	// Wait until the command is blocked on the lock.
	deadline := time.Now().Add(15 * time.Second)
	for {
		var waiting int
		err := pg.DB.QueryRow(`SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting)
		if err == nil && waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			lockTx.Rollback()
			t.Fatal("command never blocked on the target lock")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Concurrent writes after the view was taken must not be copied.
	addSQLiteRows(t, sqlitePath, sampleReadings(5))
	if err := lockTx.Rollback(); err != nil {
		t.Fatal(err)
	}

	res := <-done
	if res.code != 0 {
		t.Fatalf("exit %d, stderr=%q", res.code, res.stderr)
	}
	assertSameReadings(t, pg.readings(t), src)
	if !strings.Contains(res.stdout, "not included") {
		t.Errorf("stdout should explain later writes are not included: %q", res.stdout)
	}
}
