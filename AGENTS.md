# Sensor Temperature Tracker — Agent Instructions

## Essential Commands

- **Build**: `go build -o temp-tracker .`
- **Run**: `./temp-tracker` (defaults: port 8080, interval 30s, retain 720h)
- **Custom run**: `./temp-tracker -port 9090 -interval 60 -retain 48`
- **Debug logging**: `LOG_LEVEL=DEBUG ./temp-tracker`

## Key Notes

- **Linux-only**: Requires `/sys/class/thermal/thermal_zone*` sensors
- **Runtime artifacts**: `temps.db` (SQLite) is created automatically
- **Entrypoint**: `main.go` handles polling and HTTP server
- **Dashboard**: Served from `static/index.html`
- **Logging**: Uses `logger.go` with leveled logging (DEBUG/INFO/WARN/ERROR), controlled by `LOG_LEVEL` env var
- **API endpoints**:
  - `GET /api/temps?hours=N` — Historical readings (default 1h)
  - `GET /api/current` — Latest reading per sensor
  - `GET /` — Serves web dashboard
- **Data flow**: `sensor.go` → `db.go` → `handler.go`
- **Files**: `logger.go`, `main.go`, `sensor.go`, `db.go`, `handler.go`, `static/index.html`