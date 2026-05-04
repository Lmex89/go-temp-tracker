# Sensor Temperature Tracker

A lightweight Go application that monitors CPU temperatures on a Linux server, stores readings in SQLite, and displays them in a real-time web dashboard.

## Features

- Reads temperatures from all available `/sys/class/thermal/thermal_zone*` sensors
- Stores historical data in a local SQLite database (`temps.db`)
- Automatic pruning of old records to prevent database bloat
- REST API for querying temperature data
- Web dashboard with interactive Chart.js line graph
- Configurable polling interval and data retention

## Requirements

- Linux with thermal zones at `/sys/class/thermal/thermal_zone*`
- Go 1.21+ (to build from source)

## Installation

```bash
git clone <repo-url> sensors-temp
cd sensors-temp
go build -o temp-tracker .
```

## Usage

```bash
# Run with defaults (port 8080, interval 30s, retain 8760h / ~12 months)
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
| `-interval` | `30`    | Sensor polling interval in seconds     |
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

### `GET /api/temps?hours=N`

Returns all readings from the last N hours (default 1).

```json
[
  {"sensor": "x86_pkg_temp", "temp_c": 45.2, "created_at": "2026-05-02 20:00:00"},
  {"sensor": "coretemp",      "temp_c": 42.8, "created_at": "2026-05-02 20:00:00"}
]
```

### `GET /api/current`

Returns the latest reading from each sensor.

```json
[
  {"sensor": "x86_pkg_temp", "temp_c": 45.2, "created_at": "2026-05-02 20:00:00"},
  {"sensor": "coretemp",      "temp_c": 42.8, "created_at": "2026-05-02 20:00:00"}
]
```

### `GET /`

Serves the web dashboard (`static/index.html`).

## Project structure

```
sensors-temp/
├── logger.go       Leveled logger (DEBUG/INFO/WARN/ERROR/FATAL)
├── main.go         Entry point — wires SensorReader, Store, Poller, HTTP server
├── sensor.go       SensorReader interface + LinuxThermalSensor (reads /sys/class/thermal/)
├── poller.go       Poller struct — periodic read + insert + prune loop
├── db.go           Store interface + SQLiteStore (schema, insert, query, prune)
├── timeutil.go     TimeConverter interface + MeridaTimeConverter (UTC → America/Merida)
├── handler.go      REST API endpoints
├── static/
│   └── index.html  Chart.js web dashboard
├── AGENTS.md       IDE agent instructions
├── README.md       This file
├── go.mod / go.sum Go module files
├── temps.db        SQLite database (created at runtime)
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

The web dashboard (`static/index.html`) uses Chart.js loaded from CDN. It auto-refreshes every 30s and shows a selectable time range (1h, 6h, 24h). Current temperatures update every 10s.

![screenshot](screenshot.png) *Coming soon*
