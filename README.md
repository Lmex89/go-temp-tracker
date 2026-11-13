# System Monitor

A lightweight Go application that monitors CPU temperatures, system metrics (CPU, memory, swap, disk, load), stores readings in PostgreSQL (or SQLite as a fallback), and displays them in a real-time web dashboard with gauges and interactive charts.

## Features

- **Temperature sensors**: Reads from all available `/sys/class/thermal/thermal_zone*` and `/sys/class/hwmon/hwmon*` sensors
- **System metrics**: CPU usage per core, RAM usage, swap usage, disk usage, and load averages via gopsutil v3
- **Gauges**: Semicircular visual indicators for CPU, RAM, Swap, and Disk at a glance
- **Historical data**: Stored in PostgreSQL (default for the service scripts) or SQLite (binary default; `temps.db` fallback)
- **Auto-pruning**: Uniform retention for all metric types via the `-retain` flag (default 1 year; the `*_RETAIN_HOURS` vars in `.env.example` are not consumed by the code)
- **REST API**: Query historical data by metric type with relative hours or absolute date range
- **Interactive dashboard**: Multiple Chart.js line charts with zoom/pan, stacked vertically

## Requirements

- Linux (reads `/proc` and `/sys` for system metrics)
- Go 1.25+
- Docker (for PostgreSQL backend)
- PostgreSQL 17+ (via `docker compose up -d`)

## Installation

```bash
git clone <repo-url> sensors-temp
cd sensors-temp
mkdir -p bin && go build -o bin/temp-tracker .

# Start PostgreSQL (required when DB_DRIVER=postgres)
docker compose up -d
```

## Usage

Para configurar alertas opcionales de CPU por Telegram, consulte [la guia de alertas](docs/alertas-cpu.md).

```bash
# Run with PostgreSQL (default for helper scripts; the binary defaults to sqlite)
DB_DRIVER=postgres ./bin/temp-tracker -port 9091

# Run with SQLite (fallback, no Docker needed)
DB_DRIVER=sqlite ./bin/temp-tracker -port 9091

# Custom configuration
./bin/temp-tracker -port 9090 -interval 60 -retain 48
```

Open `http://localhost:9091` in your browser.

## Running indefinitely

### Background process (nohup)

```bash
cd /home/lmex89/Documentos/probe/sensors-temp
mkdir -p logs
nohup ./bin/temp-tracker -port 9091 -interval 30 > logs/tracker.log 2>&1 &
```

- Logs go to `logs/tracker.log`
- To stop: `pkill temp-tracker`

### systemd service (survives reboots)

The project includes helper scripts that create, enable, and start the service for you.

#### User service (starts on login, no sudo)

```bash
./scripts/setup-systemd-service.sh
# or, if you prefer Fish:
./scripts/setup-systemd-service.fish
```

This installs `~/.config/systemd/user/temp-tracker.service` and uses the same settings as `cleanup-and-build.fish` (port **9091**, 60s polling intervals).

- Check status: `systemctl --user status temp-tracker`
- Stop: `systemctl --user stop temp-tracker` (or `./scripts/service-manager.sh stop`)
- Restart: `systemctl --user restart temp-tracker` (or `./scripts/service-manager.sh restart`)
- View logs: `journalctl --user -u temp-tracker -f` (or `./scripts/view-logs.sh -f`)

#### System service (starts at boot, requires sudo)

```bash
./scripts/setup-systemd-service.sh --system
# or, if you prefer Fish:
./scripts/setup-systemd-service.fish --system
```

This installs `/etc/systemd/system/temp-tracker.service`, runs as your user, and starts automatically at boot.

- Check status: `sudo systemctl status temp-tracker`
- Stop: `sudo systemctl stop temp-tracker` (or `./scripts/service-manager.sh --system stop`)
- Restart: `sudo systemctl restart temp-tracker` (or `./scripts/service-manager.sh --system restart`)
- View logs: `sudo journalctl -u temp-tracker -f` (or `./scripts/view-logs.sh --system -f`)

#### Manual setup

If you prefer to write the unit file yourself, create `/etc/systemd/system/temp-tracker.service`:

```ini
[Unit]
Description=Sensor Temperature Tracker
After=network.target

[Service]
ExecStart=/home/lmex89/Documentos/probe/sensors-temp/temp-tracker -port 9091 -interval 60
WorkingDirectory=/home/lmex89/Documentos/probe/sensors-temp
Restart=on-failure
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

- Stop: `sudo systemctl stop temp-tracker`

### tmux session

```bash
tmux new -s temp-tracker
./bin/temp-tracker -port 9091 -interval 30
# Detach: Ctrl+B, D
# Reattach: tmux attach -t temp-tracker
```

## Database backup

### Manual backup

```bash
# Create a gzip-compressed backup (saved to backups/)
./scripts/backup-db.sh

# List available backups
./scripts/restore-db.sh

# Restore from a specific backup file
./scripts/restore-db.sh backups/sensors_temp_20260725_020000.sql.gz
```

The backup script:
- Runs `pg_dump` inside the `sensors-temp-db` Docker container
- Compresses to `backups/<database>_<timestamp>.sql.gz`
- Auto-deletes backups older than 3 days (`RETENTION_DAYS` hardcoded in both scripts)

### Scheduled backup (cron)

To run a daily backup at 02:00:

```bash
# Add to crontab interactively
crontab -e
# Add this line:
0 2 * * * /full/path/to/go-temp-tracker/scripts/backup-db.sh >> /full/path/to/go-temp-tracker/backups/cron.log 2>&1
```

```bash
# Or add it in one command (no editor needed)
(crontab -l 2>/dev/null; echo "0 2 * * * /full/path/to/go-temp-tracker/scripts/backup-db.sh >> /full/path/to/go-temp-tracker/backups/cron.log 2>&1") | crontab -

# Verify it was added
crontab -l
```

Backups are saved in `backups/` (gitignored). Cron logs go to `backups/cron.log`.

## Command-line flags

| Flag        | Default  | Description                            |
|-------------|----------|----------------------------------------|
| `-port`     | `8080`   | HTTP server port                       |
| `-interval` | `60`     | Temperature polling interval in seconds |
| `-retain`   | `8760`   | Delete ALL readings older than N hours (~12 months) |
| `-db-driver`| `sqlite` | Database backend: `sqlite` or `postgres` |

## Environment variables

All polling intervals are overridable via environment variables. See `.env.example` for a complete reference.

| Variable              | Default | Description                                      |
|-----------------------|---------|--------------------------------------------------|
| `LOG_LEVEL`           | `INFO`  | Log level: `DEBUG`, `INFO`, `WARN`, or `ERROR`   |
| `DB_DRIVER`           | `sqlite`| Database backend: `sqlite` or `postgres`         |
| `DATABASE_URL`        | (none)  | PostgreSQL connection string (required for postgres) |
| `CPU_POLL_INTERVAL`   | `60`    | CPU polling interval in seconds                  |
| `MEMORY_POLL_INTERVAL`| `60`    | Memory polling interval in seconds               |
| `SWAP_POLL_INTERVAL`  | `60`    | Swap polling interval in seconds                 |
| `DISK_POLL_INTERVAL`  | `60`    | Disk polling interval in seconds                 |
| `LOAD_POLL_INTERVAL`  | `60`    | Load polling interval in seconds                 |

Examples:

```bash
# Debug logging
LOG_LEVEL=DEBUG ./bin/temp-tracker

# Override all intervals from env file
source .env.example && ./bin/temp-tracker
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
| `GET /api/disk` | `[]Reading` | Disk usage (root partition, gauge only) |
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
| Temperature | 60s | 8760h (1 year) | `/sys/class/thermal` / hwmon |
| CPU | 60s | 8760h (1 year) | gopsutil cpu.Percent |
| Memory | 60s | 8760h (1 year) | gopsutil mem.VirtualMemory |
| Swap | 60s | 8760h (1 year) | gopsutil mem.SwapMemory |
| Disk | 60s | 8760h (1 year) | gopsutil disk.Usage (gauge only) |
| Load | 60s | 8760h (1 year) | gopsutil load.Avg |

## Project structure

```
sensors-temp/
|-- main.go         Entry point -- wiring only (flags, store, pollers, HTTP server)
|-- main_test.go    Tests for the wiring helpers
|-- internal/       Private packages (see docs/adr/0003)
|   |-- logging/    Leveled logger (DEBUG/INFO/WARN/ERROR/FATAL)
|   |-- timeutil/   TimeConverter + MeridaTimeConverter (UTC -> America/Merida)
|   |-- store/      Store interface, SQLiteStore, PostgresStore, Open helpers
|   |-- sensor/     SensorReader + LinuxThermalSensor (/sys/class/thermal/ and hwmon)
|   |-- metrics/    SystemMetrics (CPU, memory, swap, disk, load via gopsutil v3)
|   |-- poller/     Poller + RunMetricPoller -- one goroutine per metric type
|   `-- httpapi/    REST API handlers and Register(mux, store, db)
|-- docker-compose.yml  PostgreSQL 17 Alpine container (port 5432)
|-- scripts/
|   |-- setup-systemd-service.fish/.sh  Create/enable systemd user or system service
|   |-- service-manager.fish/.sh        Start/stop/restart/status/logs for the service
|   |-- view-logs.fish/.sh              Show service logs
|   |-- cleanup-and-build.fish          Kill old process, rebuild into bin/, restart
|   |-- backup-db.fish/.sh              PostgreSQL backup via docker exec pg_dump
|   `-- restore-db.sh                   Restore from a gzip-compressed backup
|-- cmd/
|   |-- spike-report/       On-demand spike analysis tool
|   `-- migrate-to-postgres/ SQLite-to-PostgreSQL bulk migration tool
|-- static/
|   |-- index.html           Chart.js dashboard (gauges + line charts)
|   |-- config.json          Active dashboard configuration
|   `-- config.default.json  Reference defaults
|-- .env.example    Environment variable reference template
|-- AGENTS.md       IDE agent instructions
|-- README.md       This file
|-- go.mod / go.sum Go module files
|-- temps.db        SQLite database (fallback backend, created at runtime)
|-- bin/            Compiled binaries (ignored)
|-- logs/           Script and server logs (ignored)
`-- backups/        Database dumps (ignored)
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

Layout behavior is mobile-first and compact:
- Mobile and small tablets use tighter gauge cards to prioritize chart visibility.
- Desktop keeps a 4-column gauge row with reduced card height.
- Gauge canvas sizing is constrained in CSS to prevent Chart.js inline sizing from overflowing cards.

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
