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
| ✅ ✓ | `[OK]`, `[PASS]`, `+` |
| ❌ ✗ | `[FAIL]`, `[ERROR]`, `-` |
| ⚠️ | `[WARN]`, `!` |
| 🚀 | `>>`, `=>` |
| 🔧 🛠️ | `[BUILD]`, `[SETUP]` |
| 🧹 | `[CLEAN]`, `[REMOVE]` |
| 🔍 | `[SEARCH]`, `[FIND]` |

### Enforcement

- **Review all code for emojis before committing**
- If emojis are found, replace with ASCII equivalents
- This applies to all files: Go code, shell scripts, HTML, CSS, etc.

---

## Essential Commands

- **Build**: `go build -o temp-tracker .`
- **Run**: `./temp-tracker` (defaults: port 8080, interval 30s, retain 8760h)
- **Custom run**: `./temp-tracker -port 9090 -interval 60 -retain 48`
- **Debug logging**: `LOG_LEVEL=DEBUG ./temp-tracker`

## Key Notes

- **Linux-only**: Requires `/sys/class/thermal/thermal_zone*` sensors
- **Runtime artifacts**: `temps.db` (SQLite) is created automatically
- **Entrypoint**: `main.go` wires up SensorReader, Store, and Poller, then starts the HTTP server
- **Dashboard**: Served from `static/index.html`
  - Interactive Chart.js line graph with hover tooltips (value, date, sensor origin)
  - Zoom/pan controls (drag, wheel, pinch)
  - Configurable time ranges and refresh intervals via `config.json`
- **Logging**: Uses `logger.go` with leveled logging (DEBUG/INFO/WARN/ERROR/FATAL), controlled by `LOG_LEVEL` env var
- **API endpoints**:
  - `GET /api/temps?hours=N` — Historical readings (default 1h)
  - `GET /api/current` — Latest reading per sensor
  - `GET /` — Serves web dashboard
- **Data flow**: `sensor.go` (SensorReader) → `poller.go` (Poller) → `db.go` (Store) → `handler.go`
- **Time conversion**: `timeutil.go` provides TimeConverter (America/Merida zone)
- **Files**: `logger.go`, `main.go`, `sensor.go`, `poller.go`, `db.go`, `timeutil.go`, `handler.go`, `static/index.html`, `static/config.json`, `static/config.default.json`

---

## Dashboard Configuration

The web dashboard (`static/index.html`) loads user preferences from `static/config.json`. Edit this file to customize default chart behavior without modifying code.

### Configuration Options

| Section | Option | Type | Default | Description |
|---------|--------|------|---------|-------------|
| `defaultTimeRange` | `value` | string | `"6h"` | Initial time range. Options: `"5m"`, `"3h"`, `"6h"`, `"12h"` |
| `refreshIntervals` | `currentTempMs` | number | `10000` | How often to refresh current temps (milliseconds) |
| `refreshIntervals` | `chartDataMs` | number | `30000` | How often to refresh chart data (milliseconds) |
| `colors` | `palette` | array | `["#38bdf8", "#4ade80", ...]` | Hex colors for sensor lines (cycles if more sensors) |
| `chart` | `lineTension` | number | `0.3` | Curve smoothness (0=straight lines, 1=very curved) |
| `chart` | `pointRadius` | number | `2` | Size of data points on lines |
| `chart` | `fillArea` | boolean | `false` | Fill area under lines |
| `chart` | `yAxisMin` | number/null | `null` | Fix Y-axis minimum (null=auto) |
| `chart` | `yAxisMax` | number/null | `null` | Fix Y-axis maximum (null=auto) |
| `chart` | `showGridLines` | boolean | `true` | Show Y-axis grid lines |
| `chart` | `maxTicksLimit` | number | `12` | Maximum X-axis labels to show |
| `display` | `decimalPlaces` | number | `1` | Temperature decimal precision |
| `display` | `showLastUpdated` | boolean | `true` | Show "Updated:" timestamp |
| `display` | `timeFormat24h` | boolean | `true` | Use 24-hour time format |
| `zoom` | `wheelEnabled` | boolean | `true` | Enable mouse wheel zoom |
| `zoom` | `dragEnabled` | boolean | `true` | Enable drag-to-zoom |
| `zoom` | `pinchEnabled` | boolean | `true` | Enable pinch zoom (touch) |
| `sensorFilter` | `enabled` | boolean | `false` | Enable sensor filtering |
| `sensorFilter` | `includePatterns` | array | `[]` | Show only sensors matching these patterns |

### Sensor Filtering

Filter which sensors appear on the chart and in the current temps display:

| Pattern | Matches | Example Sensors Shown |
|---------|---------|----------------------|
| `["Core"]` | CPU core temps only | `coretemp/Core 0`, `coretemp/Core 1` |
| `["Package"]` | Package temperature only | `coretemp/Package id 0` |
| `["Core", "Package"]` | Cores + Package | Both cores and package temp |
| `["coretemp"]` | All coretemp sensors | All Intel/AMD CPU sensors |
| `["acpitz"]` | ACPI thermal zone | `acpitz/temp1_input` |

Patterns are **case-sensitive partial matches**. A sensor is shown if its name contains ANY of the patterns (OR logic).

### Configuration Files

Two configuration files are provided:

| File | Purpose | Sensors Shown |
|------|---------|---------------|
| `config.json` | **Active configuration** - Edit this to customize | Core temps only (filtered) |
| `config.default.json` | **Reference defaults** - Shows all options with defaults | All sensors (no filtering) |

**To restore defaults**: Copy `config.default.json` to `config.json`:
```bash
cp static/config.default.json static/config.json
```

**Current active config** (`config.json`) has filtering enabled to show only CPU cores by default for a cleaner view. The default config shows all available sensors.

### Example: Change Default Range and Colors

```json
{
  "defaultTimeRange": { "value": "12h" },
  "colors": {
    "palette": ["#ff6b6b", "#4ecdc4", "#45b7d1", "#96ceb4", "#ffeaa7"]
  },
  "chart": {
    "lineTension": 0.5,
    "fillArea": true
  }
}
```

### How It Works

1. **Config Loading**: Dashboard fetches `/config.json` on page load
2. **Fallback**: If config.json is missing/invalid, uses built-in defaults
3. **Application**: Values applied to Chart.js initialization and refresh timers
4. **No Server Restart**: Changes take effect on next browser refresh

### Fallback Behavior

If `config.json` fails to load (404, parse error, etc.), the dashboard uses hardcoded defaults identical to the shipped `config.json`. This ensures the dashboard always works even if the config file is deleted.
