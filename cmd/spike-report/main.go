package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// logger is the package-level logger, initialized in main().
// Similar to Python's logging.getLogger(__name__) -- available to all files in this package.
var logger *spikeLogger

// main is the entry point -- like Python's `if __name__ == "__main__": main()`.
// Go's main() takes no args; use the flag package for CLI arguments.
func main() {
	// Define CLI flags -- like Python's argparse or click.
	// flag.String("name", "default", "help text") creates a string flag.
	days := flag.Int("days", 7, "Report window: look for spikes in the last N days")
	baselineDays := flag.Int("baseline-days", 30, "Baseline window: calculate per-sensor average over the last N days")
	deviation := flag.Float64("deviation", 10.0, "Spike threshold: degrees Celsius above the sensor's baseline average")
	dbDriver := flag.String("driver", os.Getenv("DB_DRIVER"), "Database driver: sqlite or postgres (also env DB_DRIVER)")
	dbPath := flag.String("db", "", "SQLite database path, or Postgres connection string/DATABASE_URL (defaults: temps.db for sqlite, env DATABASE_URL or docker-compose DSN for postgres)")
	format := flag.String("format", "table", "Output format: table, json, csv")
	outputPath := flag.String("output", "-", "Output file path (- for stdout)")
	verbose := flag.Bool("verbose", false, "Enable debug logging (also via LOG_LEVEL=DEBUG)")

	flag.Parse()

	// Initialize the logger -- like Python's logging.basicConfig(level=...).
	// LOG_LEVEL env var takes precedence; -verbose flag sets DEBUG if env is empty.
	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" && *verbose {
		logLevel = "DEBUG"
	}
	logger = newSpikeLogger(logLevel)

	setDBDriver(*dbDriver)

	logger.info("spike-report starting (days=%d, baseline=%d, deviation=%.1f, driver=%s, db=%s, format=%s)",
		*days, *baselineDays, *deviation, *dbDriver, *dbPath, *format)

	// Open the database. sql.Open returns a *sql.DB which is a connection pool,
	// not a single connection -- like Python's connection pool.
	db, err := openReportDB(*dbDriver, *dbPath)
	if err != nil {
		logger.error("failed to open database: %v", err)
		fmt.Fprintf(os.Stderr, "Failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close() // Ensure cleanup when main() returns -- like Python's `with db:`.

	if err := db.Ping(); err != nil {
		logger.error("failed to connect to database: %v", err)
		fmt.Fprintf(os.Stderr, "Failed to connect to database: %v\n", err)
		os.Exit(1)
	}
	logger.debug("database connection established: driver=%s, spec=%s", *dbDriver, *dbPath)

	// Load the America/Merida timezone for display -- like pytz.timezone("America/Merida").
	// If the system doesn't have this timezone, fall back to UTC.
	loc, err := time.LoadLocation("America/Merida")
	if err != nil {
		logger.warn("timezone America/Merida not available, falling back to UTC: %v", err)
		loc = time.UTC
	}

	// Calculate the time range boundaries.
	// time.Now().UTC() gets current UTC time -- like Python's datetime.utcnow().
	now := time.Now().UTC()
	reportSince := now.AddDate(0, 0, -*days)
	baselineSince := now.AddDate(0, 0, -*baselineDays)
	logger.debug("time ranges: report since %s, baseline since %s",
		reportSince.Format("2006-01-02 15:04:05"), baselineSince.Format("2006-01-02 15:04:05"))

	// Step 1: Detect temperature spikes.
	// This calls spike.go logic -- calculates per-sensor baselines and filters readings.
	logger.info("detecting temperature spikes...")
	spikes, err := detectSpikes(db, baselineSince, reportSince, now, *deviation)
	if err != nil {
		logger.error("spike detection failed: %v", err)
		fmt.Fprintf(os.Stderr, "Spike detection failed: %v\n", err)
		os.Exit(1)
	}

	if len(spikes) == 0 {
		logger.info("no temperature spikes found in the specified period")
		fmt.Println("No temperature spikes found in the specified period.")
		os.Exit(0)
	}
	logger.info("found %d spike(s) across %d sensor(s)", len(spikes), countUniqueSensorsFromSpikes(spikes))

	// Step 2: Correlate with system metrics for each spike.
	// This calls correlate.go logic -- finds the nearest CPU/memory/swap/disk/load reading.
	logger.info("correlating spikes with system metrics...")
	correlated, err := correlateMetrics(db, spikes, loc)
	if err != nil {
		logger.error("correlation failed: %v", err)
		fmt.Fprintf(os.Stderr, "Correlation failed: %v\n", err)
		os.Exit(1)
	}
	logger.debug("correlation complete: %d rows", len(correlated))

	// Step 3: Format and output the report.
	// This calls format.go logic -- table, JSON, or CSV.
	var out *os.File
	if *outputPath == "-" {
		out = os.Stdout
	} else {
		f, err := os.Create(*outputPath)
		if err != nil {
			logger.error("failed to create output file: %v", err)
			fmt.Fprintf(os.Stderr, "Failed to create output file: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		out = f
		logger.info("writing report to file: %s", *outputPath)
	}

	report := buildReport(*days, *baselineDays, *deviation, correlated, loc)

	if err := writeReport(out, report, *format); err != nil {
		logger.error("failed to write report: %v", err)
		fmt.Fprintf(os.Stderr, "Failed to write report: %v\n", err)
		os.Exit(1)
	}
	logger.info("report generated successfully")
}

// countUniqueSensorsFromSpikes returns the number of distinct sensor names from Spike objects.
// In Python: len(set(s.Sensor for s in spikes))
func countUniqueSensorsFromSpikes(spikes []Spike) int {
	seen := make(map[string]bool) // map is like a Python dict; make() creates it.
	for _, s := range spikes {
		seen[s.Sensor] = true
	}
	return len(seen)
}

// buildReport constructs the full Report with summary statistics and per-sensor breakdowns.
func buildReport(days, baselineDays int, deviation float64, rows []ReportRow, loc *time.Location) Report {
	sensorCount := countUniqueSensors(rows)

	var maxTemp, maxDev, sumDev float64
	sensorSpikes := make(map[string][]ReportRow)
	severityCounts := map[string]int{"mild": 0, "moderate": 0, "high": 0, "severe": 0}

	for _, r := range rows {
		if r.TempC > maxTemp {
			maxTemp = r.TempC
		}
		if r.AboveMean > maxDev {
			maxDev = r.AboveMean
		}
		sumDev += r.AboveMean
		sensorSpikes[r.Sensor] = append(sensorSpikes[r.Sensor], r)
		severityCounts[r.Severity]++
	}

	var avgDev float64
	if len(rows) > 0 {
		avgDev = sumDev / float64(len(rows))
	}

	topSensor := ""
	topCount := 0
	for sensor, spikes := range sensorSpikes {
		if len(spikes) > topCount {
			topCount = len(spikes)
			topSensor = sensor
		}
	}

	var summaries []SensorSummary
	for sensor, spikes := range sensorSpikes {
		var maxT, maxD, sumD float64
		for _, s := range spikes {
			if s.TempC > maxT {
				maxT = s.TempC
			}
			if s.AboveMean > maxD {
				maxD = s.AboveMean
			}
			sumD += s.AboveMean
		}
		summaries = append(summaries, SensorSummary{
			Sensor:       sensor,
			SpikeCount:   len(spikes),
			MaxTempC:     maxT,
			MaxDeviation: maxD,
			AvgDeviation: round1(sumD / float64(len(spikes))),
			FirstSpike:   spikes[0].Timestamp,
			LastSpike:    spikes[len(spikes)-1].Timestamp,
		})
	}

	logger.debug("report summary: max_temp=%.1f, max_dev=%.1f, avg_dev=%.1f, top_sensor=%s",
		maxTemp, maxDev, avgDev, topSensor)

	return Report{
		Days:           days,
		BaselineDays:   baselineDays,
		Deviation:      deviation,
		GeneratedAt:    time.Now().In(loc).Format("2006-01-02 15:04:05"),
		SpikeCount:     len(rows),
		SensorCount:    sensorCount,
		MaxSpikeTemp:   maxTemp,
		MaxDeviation:   maxDev,
		AvgDeviation:   round1(avgDev),
		TopSensor:      topSensor,
		SeverityCounts: severityCounts,
		Rows:           rows,
		Summaries:      summaries,
	}
}

// countUniqueSensors returns the number of distinct sensor names in the report rows.
// In Python: len(set(r.Sensor for r in rows))
func countUniqueSensors(rows []ReportRow) int {
	seen := make(map[string]bool)
	for _, r := range rows {
		seen[r.Sensor] = true
	}
	return len(seen)
}
