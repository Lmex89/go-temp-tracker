package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"text/tabwriter"
)

// writeReport dispatches to the correct formatter based on the format string.
// Supported: "table", "json", "csv".
func writeReport(w io.Writer, report Report, format string) error {
	switch format {
	case "table":
		return writeTable(w, report)
	case "json":
		return writeJSON(w, report)
	case "csv":
		return writeCSV(w, report)
	default:
		return fmt.Errorf("unknown format: %q (use table, json, or csv)", format)
	}
}

// writeTable outputs a human-readable ASCII table using text/tabwriter.
// text/tabwriter is like Python's tabulate or prettytable, but from Go's stdlib.
func writeTable(w io.Writer, report Report) error {
	// tabwriter.NewWriter is like Python's csv.writer but for aligned columns.
	// \t separates columns; the writer pads them to equal width.
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	// Header block
	fmt.Fprintf(w, "Temperature Spike Report\n")
	fmt.Fprintf(w, "========================\n")
	fmt.Fprintf(w, "Period:      last %d days\n", report.Days)
	fmt.Fprintf(w, "Baseline:    %d-day average per sensor\n", report.BaselineDays)
	fmt.Fprintf(w, "Threshold:   > baseline + %.1fC\n", report.Deviation)
	fmt.Fprintf(w, "Generated:   %s\n", report.GeneratedAt)
	fmt.Fprintf(w, "Spikes:      %d across %d sensor(s)\n\n", report.SpikeCount, report.SensorCount)

	// Table header — tab-separated so tabwriter aligns columns.
	fmt.Fprintf(tw, "Sensor\tTime\tTemp C\t+Avg\tCPU %%\tLoad 1m\tLoad 5m\n")
	fmt.Fprintf(tw, "------\t----\t------\t----\t-----\t-------\t-------\n")

	for _, row := range report.Rows {
		cpuStr := formatFloatOrDash(row.CPUPercent)
		load1mStr := formatFloatOrDash(row.Load1Min)
		load5mStr := formatFloatOrDash(row.Load5Min)

		fmt.Fprintf(tw, "%s\t%s\t%.1f\t+%.1f\t%s\t%s\t%s\n",
			row.Sensor,
			row.Timestamp,
			row.TempC,
			row.AboveMean,
			cpuStr,
			load1mStr,
			load5mStr,
		)
	}

	return tw.Flush() // Flush writes the aligned output — like closing a file buffer.
}

// writeJSON outputs the report as pretty-printed JSON.
// In Python: json.dump(report, fp, indent=2)
func writeJSON(w io.Writer, report Report) error {
	// json.MarshalIndent is like Python's json.dumps(..., indent=2).
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ") // 2-space indentation
	return enc.Encode(report)
}

// writeCSV outputs the report as RFC 4180 CSV.
// In Python: csv.writer(fp).writerows(rows)
func writeCSV(w io.Writer, report Report) error {
	cw := csv.NewWriter(w)

	// Write header row
	header := []string{"sensor", "timestamp", "temp_c", "above_mean", "cpu_percent", "load_1min", "load_5min"}
	if err := cw.Write(header); err != nil {
		return err
	}

	for _, row := range report.Rows {
		record := []string{
			row.Sensor,
			row.Timestamp,
			fmt.Sprintf("%.1f", row.TempC),
			fmt.Sprintf("%.1f", row.AboveMean),
			formatFloatOrDash(row.CPUPercent),
			formatFloatOrDash(row.Load1Min),
			formatFloatOrDash(row.Load5Min),
		}
		if err := cw.Write(record); err != nil {
			return err
		}
	}

	cw.Flush() // Ensure all buffered data is written.
	return cw.Error()
}

// formatFloatOrDash returns a formatted float string, or "-" for the sentinel -1.
func formatFloatOrDash(v float64) string {
	if v < 0 { // -1 is our sentinel for "no matching reading".
		return "-"
	}
	return strconv.FormatFloat(v, 'f', 1, 64) // 'f' = fixed point, 1 decimal place.
}
