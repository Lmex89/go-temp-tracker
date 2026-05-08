# System Monitor

A lightweight Go application that monitors CPU temperatures, system metrics (CPU, memory, swap, load), stores readings in SQLite, and displays them in a real-time web dashboard with gauges and interactive charts.

## Features

- **Temperature sensors**: Reads from all available `/sys/class/thermal/thermal_zone*` and `/sys/class/hwmon/hwmon*` sensors
- **System metrics**: CPU usage per core, RAM usage, swap usage, disk usage, and load averages via gopsutil v3
- **Gauges**: Semicircular visual indicators for CPU, RAM, Swap, and Disk at a glance
- **Historical data**: Stored in local SQLite database (`temps.db`)
- **Auto-pruning**: Configurable retention per metric type (temperature 1y, CPU 24h, memory 24h, etc.)
- **REST API**: Query historical data by metric type with relative hours or absolute date range
- **Interactive dashboard**: Multiple Chart.js line charts with zoom/pan, stacked vertically

## Requirements

- Linux (reads `/proc` and `/sys` for system metrics)
- Go 1.21+

## Installation

```bash
git clone <repo-url> sensors-temp
cd sensors-temp
go build -o temp-tracker .
```

## Usage

```bash
# Run with defaults (port 8080)
./temp-tracker

# Custom configuration
./temp-tracker -port 9090 -interval 60 -retain 48
```

Open `http://localhost:8080` in your browser.

## Running indefinitely

### Background process (nohup)

```bash
cd /home/lmex89/Documentos/probe/sensors-temp
nohup ./temp-tracker -port 9091 -interval 30 > tracker.log 2>&1 &
```

- Logs go to `tracker.log`
- To stop: `pkill temp-tracker`

### systemd service (survives reboots)

Create `/etc/systemd/system/temp-tracker.service`:

```ini
[Unit]
Description=Sensor Temperature Tracker
After=network.target

[Service]
ExecStart=/home/lmex89/Documentos/probe/sensors-temp/temp-tracker -port 9091 -interval 30
WorkingDirectory=/home/lmex89/Documentos/probe/sensors-temp
Restart=always
RestartSec=5
User=lmex89

[Install]
WantedBy=multi-user.target
```

Then enable and start:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now temp-tracker
```

- Check status: `sudo systemctl status temp-tracker`
- View logs: `journalctl -u temp-tracker -f`
- Stop: `sudo systemctl stop temp-tracker`

### tmux session

```bash
tmux new -s temp-tracker
./temp-tracker -port 9091 -interval 30
# Detach: Ctrl+B, D
# Reattach: tmux attach -t temp-tracker
```

## Command-line flags

| Flag        | Default | Description                            |
|-------------|---------|----------------------------------------|
| `-port`     | `8080`  | HTTP server port                       |
| `-interval` | `30`    | Temperature polling interval in seconds |
| `-retain`   | `8760`  | Delete readings older than N hours (~12 months) |

## Environment variables

| Variable    | Default | Description                                      |
|-------------|---------|--------------------------------------------------|
| `LOG_LEVEL` | `INFO`  | Log level: `DEBUG`, `INFO`, `WARN`, or `ERROR`   |

Example with debug logging:

```bash
LOG_LEVEL=DEBUG ./temp-tracker
```

## API endpoints

### Historical data

All endpoints support `?hours=N` (relative) or `?from=ISO&to=ISO` (absolute range):

| Endpoint | Returns | Description |
|----------|---------|-------------|
| `GET /api/temps` | `[]Reading` | Historical temperature |
| `GET /api/cpu` | `[]Reading` | CPU usage per core + total |
| `GET /api/memory` | `[]Reading` | Memory usage (used_percent, total/used/free bytes) |
| `GET /api/swap` | `[]Reading` | Swap usage (used_percent, total/used/free bytes) |
| `GET /api/load` | `[]Reading` | Load averages (1m, 5m, 15m) |

### Current values

| Endpoint | Description |
|----------|-------------|
| `GET /api/current` | Latest temperature per sensor |
| `GET /api/current/cpu` | Latest CPU readings |
| `GET /api/current/memory` | Latest memory readings |
| `GET /api/current/swap` | Latest swap readings |
| `GET /api/current/disk` | Latest disk readings (root partition) |
| `GET /api/current/load` | Latest load readings |

### Response format

Unified across all metric types:

```json
[
  {
    "sensor": "cpu/Core 0",
    "temp_c": 45.2,
    "metric_type": "cpu",
    "unit": "%",
    "created_at": "2026-05-07 14:30:00"
  }
]
```

The `temp_c` field name is historic (kept for backward compatibility). It stores the value for any metric type.

### `GET /`

Serves the web dashboard (`static/index.html`).

## Polling intervals and retention

| Metric | Interval | Retention | Source |
|--------|----------|-----------|--------|
| Temperature | 30s | 8760h (1 year) | `/sys/class/thermal` / hwmon |
| CPU | 5s | 24h | gopsutil cpu.Percent |
| Memory | 10s | 24h | gopsutil mem.VirtualMemory |
| Swap | 60s | 168h (7 days) | gopsutil mem.SwapMemory |
| Disk | 60s | 168h (7 days) | gopsutil disk.Usage (gauge only) |
| Load | 10s | 24h | gopsutil load.Avg |

## Project structure

```
sensors-temp/
├── logger.go       Leveled logger (DEBUG/INFO/WARN/ERROR/FATAL)
├── main.go         Entry point — wires sensors, metrics, store, pollers, HTTP server
├── sensor.go       SensorReader + LinuxThermalSensor (reads /sys/class/thermal/ and hwmon)
├── metrics.go      SystemMetrics (CPU, memory, swap, disk, load via gopsutil v3)
├── poller.go       Poller + RunMetricPoller — one goroutine per metric type
├── db.go           Store + SQLiteStore (schema, migration, insert, query, prune)
├── timeutil.go     TimeConverter + MeridaTimeConverter (UTC -> America/Merida)
├── handler.go      REST API endpoints
├── static/
│   ├── index.html           Chart.js dashboard (gauges + line charts)
│   ├── config.json          Active dashboard configuration
│   └── config.default.json  Reference defaults
├── AGENTS.md       IDE agent instructions
├── README.md       This file
├── go.mod / go.sum Go module files
├── temps.db        SQLite database (created at runtime — NEVER DELETE)
└── temp-tracker    Compiled binary
```

## Log levels

| Level | Usage                                            |
|-------|--------------------------------------------------|
| DEBUG | Detailed diagnostic information                   |
| INFO  | Normal operational messages (startup, ticks)     |
| WARN  | Non-critical issues (missing sensors, bad params)|
| ERROR | Failures that don't halt the application          |
| FATAL | Unrecoverable errors (calls os.Exit(1))          |

## Dashboard

The web dashboard (`static/index.html`) uses Chart.js loaded from CDN.

### Gauge row

Semicircular doughnut gauges show current values for CPU, RAM, Swap, and Disk. Color thresholds:
- Green (< 60%)
- Yellow (60-85%)
- Red (> 85%)

### Line charts

Stacked vertically: Temperature, CPU Usage, Memory Usage, System Load.
All share the same time range controls (5m to 24h presets, or custom range).

### Interactive features

- **Hover tooltips**: Full timestamp, sensor name, and value
- **Zoom/pan**: Drag to zoom, mouse wheel, pinch zoom on touch devices
- **Preset ranges**: 5m, 1h, 3h, 6h, 12h, 24h buttons
- **Custom ranges**: Specific from/to datetime input

### Configuration

Edit `static/config.json` to customize colors, refresh intervals, gauge parameters, and sensor filtering without modifying code. See `AGENTS.md` for all configuration options.

## CRITICAL: Never delete temps.db

The SQLite database (`temps.db`) contains all historical metric data across every metric type. Schema migration is automatic and idempotent. **Deleting this file destroys ALL historical data irreversibly.**

## Database schema

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

Migration (`ALTER TABLE ADD COLUMN`) runs automatically on startup — no manual schema changes needed.
