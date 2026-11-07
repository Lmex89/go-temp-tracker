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
// In Python: if format == "table": write_table(...) elif ...
func writeReport(w io.Writer, report Report, format string) error {
	logger.debug("writing report in %s format (%d rows)", format, len(report.Rows))
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
func writeTable(w io.Writer, report Report) error {
	fmt.Fprintf(w, "Temperature Spike Report\n")
	fmt.Fprintf(w, "========================\n")
	fmt.Fprintf(w, "Period:        last %d days\n", report.Days)
	fmt.Fprintf(w, "Baseline:      %d-day average per sensor\n", report.BaselineDays)
	fmt.Fprintf(w, "Threshold:     > baseline + %.1fC\n", report.Deviation)
	fmt.Fprintf(w, "Generated:     %s\n", report.GeneratedAt)
	fmt.Fprintf(w, "Spikes:        %d across %d sensor(s)\n", report.SpikeCount, report.SensorCount)
	fmt.Fprintf(w, "Max spike:     %.1fC (deviation +%.1fC)\n", report.MaxSpikeTemp, report.MaxDeviation)
	fmt.Fprintf(w, "Avg deviation: +%.1fC\n", report.AvgDeviation)
	fmt.Fprintf(w, "Top sensor:    %s (%d spikes)\n", report.TopSensor, report.SeverityCounts["mild"]+report.SeverityCounts["moderate"]+report.SeverityCounts["high"]+report.SeverityCounts["severe"])
	fmt.Fprintf(w, "Severity:      mild=%d moderate=%d high=%d severe=%d\n\n",
		report.SeverityCounts["mild"], report.SeverityCounts["moderate"],
		report.SeverityCounts["high"], report.SeverityCounts["severe"])

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	fmt.Fprintf(tw, "Sensor\tTime\tTemp\t+Avg\tSev\tBase Avg\tBase Min\tBase Max\tCPU%%\tMem%%\tSwap%%\tDisk%%\tLoad1\tLoad5\tLoad15\n")
	fmt.Fprintf(tw, "------\t----\t----\t----\t---\t--------\t--------\t--------\t----\t-----\t------\t------\t-----\t-----\t------\n")

	for _, row := range report.Rows {
		fmt.Fprintf(tw, "%s\t%s\t%.1f\t+%.1f\t%s\t%.1f\t%.1f\t%.1f\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			row.Sensor,
			row.Timestamp,
			row.TempC,
			row.AboveMean,
			row.Severity,
			row.BaselineMean,
			row.BaselineMin,
			row.BaselineMax,
			formatFloatOrDash(row.CPUPercent),
			formatFloatOrDash(row.MemPercent),
			formatFloatOrDash(row.SwapPercent),
			formatFloatOrDash(row.DiskPercent),
			formatFloatOrDash(row.Load1Min),
			formatFloatOrDash(row.Load5Min),
			formatFloatOrDash(row.Load15Min),
		)
	}
	tw.Flush()

	if len(report.Summaries) > 0 {
		fmt.Fprintf(w, "\nPer-Sensor Summary\n")
		fmt.Fprintf(w, "------------------\n")
		sw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintf(sw, "Sensor\tSpikes\tMax Temp\tMax Dev\tAvg Dev\tFirst Spike\tLast Spike\n")
		fmt.Fprintf(sw, "------\t------\t--------\t-------\t-------\t-----------\t----------\n")
		for _, s := range report.Summaries {
			fmt.Fprintf(sw, "%s\t%d\t%.1fC\t+%.1fC\t+%.1fC\t%s\t%s\n",
				s.Sensor, s.SpikeCount, s.MaxTempC, s.MaxDeviation, s.AvgDeviation, s.FirstSpike, s.LastSpike)
		}
		sw.Flush()
	}

	return nil
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
func writeCSV(w io.Writer, report Report) error {
	cw := csv.NewWriter(w)

	header := []string{"sensor", "timestamp", "temp_c", "above_mean", "severity",
		"baseline_mean", "baseline_min", "baseline_max", "baseline_std",
		"cpu_percent", "mem_percent", "swap_percent", "disk_percent",
		"load_1min", "load_5min", "load_15min"}
	if err := cw.Write(header); err != nil {
		return err
	}

	for _, row := range report.Rows {
		record := []string{
			row.Sensor,
			row.Timestamp,
			fmt.Sprintf("%.1f", row.TempC),
			fmt.Sprintf("%.1f", row.AboveMean),
			row.Severity,
			fmt.Sprintf("%.1f", row.BaselineMean),
			fmt.Sprintf("%.1f", row.BaselineMin),
			fmt.Sprintf("%.1f", row.BaselineMax),
			fmt.Sprintf("%.1f", row.BaselineStd),
			formatFloatOrDash(row.CPUPercent),
			formatFloatOrDash(row.MemPercent),
			formatFloatOrDash(row.SwapPercent),
			formatFloatOrDash(row.DiskPercent),
			formatFloatOrDash(row.Load1Min),
			formatFloatOrDash(row.Load5Min),
			formatFloatOrDash(row.Load15Min),
		}
		if err := cw.Write(record); err != nil {
			return err
		}
	}

	cw.Flush()
	return cw.Error()
}

// formatFloatOrDash returns a formatted float string, or "-" for the sentinel -1.
func formatFloatOrDash(v float64) string {
	if v < 0 { // -1 is our sentinel for "no matching reading".
		return "-"
	}
	return strconv.FormatFloat(v, 'f', 1, 64) // 'f' = fixed point, 1 decimal place.
}
