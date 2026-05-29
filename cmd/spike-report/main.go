package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"database/sql"
	_ "modernc.org/sqlite"
)

// main is the entry point — like Python's `if __name__ == "__main__": main()`.
// Go's main() takes no args; use the flag package for CLI arguments.
func main() {
	// Define CLI flags — like Python's argparse or click.
	// flag.String("name", "default", "help text") creates a string flag.
	days := flag.Int("days", 7, "Report window: look for spikes in the last N days")
	baselineDays := flag.Int("baseline-days", 30, "Baseline window: calculate per-sensor average over the last N days")
	deviation := flag.Float64("deviation", 15.0, "Spike threshold: degrees Celsius above the sensor's baseline average")
	dbPath := flag.String("db", "temps.db", "Path to SQLite database (temps.db)")
	format := flag.String("format", "table", "Output format: table, json, csv")
	outputPath := flag.String("output", "-", "Output file path (- for stdout)")

	flag.Parse()

	// Open the SQLite database — like Python's sqlite3.connect(db_path).
	// sql.Open returns a *sql.DB which is a connection pool, not a single connection.
	db, err := sql.Open("sqlite", *dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close() // Ensure cleanup when main() returns — like Python's `with db:`.

	if err := db.Ping(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to database: %v\n", err)
		os.Exit(1)
	}

	// Load the America/Merida timezone for display — like pytz.timezone("America/Merida").
	// If the system doesn't have this timezone, fall back to UTC.
	loc, err := time.LoadLocation("America/Merida")
	if err != nil {
		loc = time.UTC
	}

	// Calculate the time range boundaries.
	// time.Now().UTC() gets current UTC time — like Python's datetime.utcnow().
	now := time.Now().UTC()
	reportSince := now.AddDate(0, 0, -*days)
	baselineSince := now.AddDate(0, 0, -*baselineDays)

	// Step 1: Detect temperature spikes.
	// This calls spike.go logic — calculates per-sensor baselines and filters readings.
	spikes, err := detectSpikes(db, baselineSince, reportSince, now, *deviation)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Spike detection failed: %v\n", err)
		os.Exit(1)
	}

	if len(spikes) == 0 {
		fmt.Println("No temperature spikes found in the specified period.")
		os.Exit(0)
	}

	// Step 2: Correlate with CPU and system load for each spike.
	// This calls correlate.go logic — finds the nearest CPU/load reading.
	correlated, err := correlateMetrics(db, spikes, loc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Correlation failed: %v\n", err)
		os.Exit(1)
	}

	// Step 3: Format and output the report.
	// This calls format.go logic — table, JSON, or CSV.
	var out *os.File
	if *outputPath == "-" {
		out = os.Stdout
	} else {
		f, err := os.Create(*outputPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create output file: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		out = f
	}

	report := Report{
		Days:          *days,
		BaselineDays:  *baselineDays,
		Deviation:     *deviation,
		GeneratedAt:   time.Now().In(loc).Format("2006-01-02 15:04:05"),
		SpikeCount:    len(correlated),
		SensorCount:   countUniqueSensors(correlated),
		Rows:          correlated,
	}

	if err := writeReport(out, report, *format); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write report: %v\n", err)
		os.Exit(1)
	}
}

// countUniqueSensors returns the number of distinct sensor names in the report rows.
// In Python: len(set(r.Sensor for r in rows))
func countUniqueSensors(rows []ReportRow) int {
	seen := make(map[string]bool) // map is like a Python dict; make() creates it.
	for _, r := range rows {
		seen[r.Sensor] = true
	}
	return len(seen) // len() is like Python's len().
}
