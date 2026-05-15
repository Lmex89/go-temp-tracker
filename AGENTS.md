# Sensor Temperature Tracker — Agent Instructions

## Mandatory: Python-to-Go Docstring Style

**Every code modification MUST include explanatory comments** written for a Python backend developer learning Go. This is **required**, not optional.

### Comment Style Guidelines

When adding or modifying code, always add comments that:

1. **Compare Go concepts to Python equivalents** — e.g., "struct is like a Python dataclass", "goroutine is like threading.Thread but lighter", "interface is like Python's ABC/Protocol"
2. **Explain Go-specific syntax** — e.g., "the `&` means 'address of' — like getting a reference in Python", "`defer` is like Python's context manager (`with ...`)"
3. **Note key differences** — e.g., "Go returns (value, error) instead of raising exceptions", "maps must be created with `make()` before use", "capitalized = exported/public, lowercase = private"
4. **Use inline comments liberally** — Explain what each block does, similar to Python docstrings but inline (Go doesn't have docstrings on functions like Python)

### Example Comment Patterns

```go
// SensorReader is an *interface* (like a Python ABC or Protocol, but implicit).
// Any type with a Read() method automatically satisfies it — no "implements" keyword.

// Read() returns (map, error) — Go's way of handling errors instead of try/except.
// You MUST check if err != nil before using the result.

// defer db.Close() runs when the function returns — like Python's "with db:" context manager.
// It ensures cleanup (close connections, files, etc.) even if an error occurs.

// make(map[string]float64) creates an empty map — like {} in Python.
// But in Go, you MUST use make() for maps, slices, and channels before using them.

// range over a slice gives (index, value) — like Python's enumerate().
// We use _ for the index because Go doesn't allow unused variables.
```

### Enforcement

- **No pull request or change should be accepted without these comments**
- If you see code without comments, add them before committing
- Comments should be in English (code language) but can reference Spanish terms if helpful
- Target audience: experienced Python developer, Go beginner

---

## Code Style: No Emojis

**Never use emojis in code** — this includes comments, strings, output messages, and variable names.

### Rationale

- Emojis can cause encoding issues across different terminals, editors, and systems
- They reduce readability in logs, diffs, and code reviews
- Many build/CI systems may not handle Unicode properly
- Keep code clean and universally compatible

### Allowed Alternatives

Use plain ASCII instead:

| Instead of | Use |
|------------|-----|
| [OK] | `[OK]`, `[PASS]`, `+` |
| [X] | `[FAIL]`, `[ERROR]`, `-` |
| (warn) | `[WARN]`, `!` |
| >> | `>>`, `=>` |
| [tool] | `[BUILD]`, `[SETUP]` |
| [broom] | `[CLEAN]`, `[REMOVE]` |
| [search] | `[SEARCH]`, `[FIND]` |

### Enforcement

- **Review all code for emojis before committing**
- If emojis are found, replace with ASCII equivalents
- This applies to all files: Go code, shell scripts, HTML, CSS, etc.

---

## Essential Commands

- **Build**: `go build -o temp-tracker .`
- **Run**: `./temp-tracker` (defaults: port 8080, interval 30s, retain 8760h)
- **Custom run**: `./temp-tracker -port 9090 -interval 60 -retain 48`
- **Env vars**: See `.env.example` for all overridable polling intervals
- **Debug logging**: `LOG_LEVEL=DEBUG ./temp-tracker`
- **Load env file**: `source .env.example && ./temp-tracker` (edit first as needed)

## CRITICAL: Never Delete temps.db

**`temps.db` must never be deleted or truncated.** It contains historical metric data across ALL metric types (temperature, CPU, memory, swap, disk, load). The application auto-migrates the schema on startup via `ALTER TABLE ADD COLUMN` — **no manual intervention or file deletion is ever needed.**

- Schema migration is idempotent — runs automatically, safe to restart
- Deleting `temps.db` destroys ALL historical data irreversibly
- The DB is SQLite, stored in the project root directory
- `temps.db-shm` and `temps.db-wal` are SQLite runtime artifacts (shared memory + WAL) — also contain live data, but are properly ignored via `.gitignore` (`*.db-shm`, `*.db-wal`)

## Key Notes

- **Linux-only**: Requires `/sys/class/thermal/thermal_zone*` sensors (temperature) + `/proc` (CPU/memory/disk via gopsutil)
- **Runtime artifacts**: `temps.db` (SQLite) is created automatically — **NEVER DELETE**. `temps.db-shm`/`temps.db-wal` are ignored via `.gitignore`.
- **Entrypoint**: `main.go` wires up temperature sensors + system metrics, DB store, polling goroutines, and HTTP server
- **Dashboard**: Served from `static/index.html`
  - Gauges row at top (semicircular doughnut charts for CPU, RAM, Swap, Disk)
  - Multiple Chart.js line graphs stacked vertically (Temperature, CPU, Memory, Load)
  - Zoom/pan controls (drag, wheel, pinch)
  - Configurable time ranges and refresh intervals via `config.json`
- **Logging**: Uses `logger.go` with leveled logging (DEBUG/INFO/WARN/ERROR/FATAL), controlled by `LOG_LEVEL` env var

## Architecture

### Data Flow

```
Temperature sensors (/sys/class/thermal)   System metrics (gopsutil v3)
         |                                      |
   sensor.go (SensorReader)               metrics.go (SystemMetrics)
         |                                      |
    +----+------+-------------------+-------+---+
    |           |                   |       |
  poller.go  RunMetricPoller()     ...    ...
    |           |                   |       |
    +-----+-----+-------+---------+       |
          |             |                 |
       db.go (SQLiteStore — unified readings table)
          |
     handler.go (HTTP API)
          |
    static/index.html (Chart.js dashboard)
```

### API Endpoints

| Endpoint | Return Type | Purpose |
|----------|-------------|---------|
| `GET /api/temps?hours=N` | `[]Reading` | Historical temperature (backward compat) |
| `GET /api/current` | `[]Reading` | Latest temperature per sensor (backward compat) |
| `GET /api/cpu?hours=N` | `[]Reading` | Historical CPU per-core % |
| `GET /api/memory?hours=N` | `[]Reading` | Historical memory usage % |
| `GET /api/swap?hours=N` | `[]Reading` | Historical swap usage % |
| `GET /api/load?hours=N` | `[]Reading` | Historical load averages (1m, 5m, 15m) |
| `GET /api/current/cpu` | `[]Reading` | Latest CPU readings |
| `GET /api/current/memory` | `[]Reading` | Latest memory readings |
| `GET /api/current/swap` | `[]Reading` | Latest swap readings |
| `GET /api/current/disk` | `[]Reading` | Latest disk readings (gauge only) |
| `GET /api/current/load` | `[]Reading` | Latest load readings |
| `GET /` | HTML | Dashboard |

All metric endpoints support `?hours=N` (relative) or `?from=ISO&to=ISO` (absolute range).

### Polling Intervals

| Metric | Interval | Retention | Source |
|--------|----------|-----------|--------|
| Temperature | 30s | 8760h (1 year) | sensor.go (hwmon/thermal zones) |
| CPU | 30s | 24h | metrics.go (gopsutil cpu.Percent) |
| Memory | 10s | 24h | metrics.go (gopsutil mem.VirtualMemory) |
| Swap | 10s | 168h (7 days) | metrics.go (gopsutil mem.SwapMemory) |
| Disk | 60s | 168h (7 days) | metrics.go (gopsutil disk.Usage) - gauge only |
| Load | 10s | 24h | metrics.go (gopsutil load.Avg) |

### Response Format (all endpoints)

Reading struct is unified across all metric types:
```json
{
  "sensor": "cpu/Core 0",
  "temp_c": 45.2,
  "metric_type": "cpu",
  "unit": "%",
  "created_at": "2026-05-07 14:30:00"
}
```

The `temp_c` field name is historic (kept for backward compatibility). It stores the value for any metric type.

### Files

| File | Purpose |
|------|---------|
| `logger.go` | Leveled logging (DEBUG/INFO/WARN/ERROR/FATAL) |
| `main.go` | Entry point — wires everything |
| `sensor.go` | Temperature sensor reader (hwmon + thermal zones) |
| `metrics.go` | System metrics reader (CPU, mem, swap, disk, load) via gopsutil v3 |
| `poller.go` | Polling loops — one goroutine per metric type |
| `db.go` | SQLite store with schema migration |
| `handler.go` | HTTP API handlers |
| `timeutil.go` | Timezone conversion (America/Merida) |
| `static/index.html` | Dashboard with gauges + line charts |
| `static/config.json` | Active dashboard configuration |
| `static/config.default.json` | Reference defaults |
| `.env.example` | Environment variable reference template |

---

## Dashboard Configuration

The web dashboard (`static/index.html`) loads user preferences from `static/config.json`. Edit this file to customize default chart behavior without modifying code.

### Configuration Options

| Section | Option | Type | Default | Description |
|---------|--------|------|---------|-------------|
| `defaultTimeRange` | `value` | string | `"6h"` | Initial time range: `"5m"`, `"3h"`, `"6h"`, `"12h"` |
| `refreshIntervals` | `currentTempMs` | number | `10000` | Temperature current refresh (ms) |
| `refreshIntervals` | `chartDataMs` | number | `30000` | All chart data refresh (ms) |
| `colors` | `palette` | array | `["#38bdf8", ...]` | Hex colors for sensor lines |
| `chart` | `lineTension` | number | `0.3` | Curve smoothness (0=straight, 1=very curved) |
| `chart` | `pointRadius` | number | `2` | Data point size on lines |
| `chart` | `fillArea` | boolean | `false` | Fill area under lines |
| `chart` | `yAxisMin/Max` | number/null | `null` | Fix Y-axis (null=auto) |
| `chart` | `showGridLines` | boolean | `true` | Y-axis grid lines |
| `chart` | `maxTicksLimit` | number | `12` | Max X-axis labels |
| `display` | `decimalPlaces` | number | `1` | Value decimal precision |
| `display` | `showLastUpdated` | boolean | `true` | Show timestamp |
| `display` | `timeFormat24h` | boolean | `true` | 24h time format |
| `zoom` | `wheelEnabled` | boolean | `true` | Mouse wheel zoom |
| `zoom` | `dragEnabled` | boolean | `true` | Drag-to-zoom |
| `zoom` | `pinchEnabled` | boolean | `true` | Pinch zoom (touch) |
| `gauge` | `cpuMax` | number | `100` | CPU gauge max value |
| `gauge` | `ramMax` | number | `100` | RAM gauge max value |
| `gauge` | `swapMax` | number | `100` | Swap gauge max value |
| `gauge` | `diskMax` | number | `100` | Disk gauge max value (gauge only) |
| `gaugeRefreshMs` | `cpu` | number | `5000` | CPU gauge refresh (ms) |
| `gaugeRefreshMs` | `ram` | number | `10000` | RAM gauge refresh (ms) |
| `gaugeRefreshMs` | `swap` | number | `60000` | Swap gauge refresh (ms) |
| `gaugeRefreshMs` | `disk` | number | `60000` | Disk gauge refresh (ms) |
| `sensorFilter` | `enabled` | boolean | `false` | Enable temp sensor filtering |
| `sensorFilter` | `includePatterns` | array | `[]` | Show only matching sensors |

### Sensor Filtering

Filter which temperature sensors appear on the chart (temperature panel only). System metric charts (CPU, memory, etc.) are not affected by this filter.

### Configuration Files

| File | Purpose |
|------|---------|
| `config.json` | **Active configuration** — Edit this to customize |
| `config.default.json` | **Reference defaults** |

**To restore defaults**: Copy `config.default.json` to `config.json`:
```bash
cp static/config.default.json static/config.json
```

### Fallback Behavior

If `config.json` fails to load (404, parse error, etc.), the dashboard uses hardcoded defaults identical to the shipped `config.json`.

## Database Schema

Single unified table `readings` with auto-migration:

```sql
CREATE TABLE readings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    sensor TEXT NOT NULL,
    temp_c REAL NOT NULL,
    metric_type TEXT NOT NULL DEFAULT 'temperature',
    unit TEXT NOT NULL DEFAULT 'C',
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
```

Migration runs automatically on startup — no manual schema changes needed.
