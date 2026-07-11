# Sensor Temperature Tracker - Agent Notes

## Non-negotiables
- Do not delete or truncate `temps.db` (or `temps.db-shm` / `temps.db-wal` while app is running). Schema migration is automatic in `db.go`; data loss is irreversible.
- Keep code/comments ASCII only; no emojis in code, logs, strings, or comments.
- For Go edits, keep the existing teaching style comments for Python developers (compare Go behavior to Python where helpful).

## Verified Commands
- Build: `go build -o temp-tracker .`
- Build & lint: `go vet ./...`
- Run default: `./temp-tracker` (actual defaults from `main.go`: `-port 8080 -interval 60 -retain 8760`)
- Run custom: `./temp-tracker -port 9090 -interval 60 -retain 48`
- Debug logs: `LOG_LEVEL=DEBUG ./temp-tracker`
- Quick verification: `go test ./...` (currently prints `no test files`)
- Local helper script: `cleanup-and-build.fish` kills running `temp-tracker` processes, rebuilds, and logs to `cleanup-and-build.log`.
- Install systemd service: `./setup-systemd-service.fish` (or `./setup-systemd-service.sh`) creates the unit file, enables it, and starts it immediately. Defaults to a user service (`~/.config/systemd/user/temp-tracker.service`) that starts on login with no sudo. Add `--system` to install a system service (`/etc/systemd/system/temp-tracker.service`) that starts at boot and uses sudo. Uses the same port/env settings as `cleanup-and-build.fish` (port 9091, 60s intervals).
- Manage the service: `./service-manager.fish` / `./service-manager.sh` supports `status`, `start`, `stop`, `restart`, and `logs` (with optional `-f` to follow). Add `--system` to manage the system service.
- View logs quickly: `./view-logs.fish` / `./view-logs.sh` shows the last 50 lines; add `-f` to follow and `--system` for the system service.
- Spike report (on-demand analysis): `go build -o spike-report ./cmd/spike-report/ && ./spike-report` (defaults: last 7 days, 30-day baseline, +15C deviation; flags: `-days`, `-baseline-days`, `-deviation`, `-format table|json|csv`, `-output`).
- SQLite backup tool (separate): `cd sqlite-backup-tool && python backup.py` (configurable via `config.yaml`).

## Config Quirks That Cause Mistakes
- `main.go` reads only these env vars at runtime: `LOG_LEVEL`, `CPU_POLL_INTERVAL`, `MEMORY_POLL_INTERVAL`, `SWAP_POLL_INTERVAL`, `DISK_POLL_INTERVAL`, `LOAD_POLL_INTERVAL`.
- Polling intervals come from flags (`-interval` for temperature, `*_POLL_INTERVAL` env vars for system metrics).
- **Retention is unified**: the `-retain` flag controls deletion for ALL metric types (temperature, CPU, memory, swap, disk, load). Default is 8760h (~1 year).
- `.env.example` includes `PORT`, `TEMP_POLL_INTERVAL`, `TEMP_RETAIN_HOURS`, and `*_RETAIN_HOURS`, but those are not consumed by current Go code.
- `static/config.json` is the active dashboard config; `static/config.default.json` is the reference. If sensors appear "missing" on the temperature chart, check `sensorFilter.enabled` and `includePatterns` in `config.json` -- the default config has filtering ON with `["Core", "acpitz"]`. Copy `config.default.json` to `config.json` to show all sensors.

## Architecture Snapshot
- Entrypoint `main.go`: creates `SensorReader` (temperature via hwmon/thermal zones) + `SystemMetrics` (CPU/mem/swap/disk/load via gopsutil v3), opens SQLite with WAL mode + busy timeout + `SetMaxOpenConns(1)`, starts 6 goroutines (1 `Poller.Run` for temperature, 5 `RunMetricPoller` for system metrics), registers HTTP handlers, serves `static/`.
- Two polling mechanisms: `Poller` struct (temperature) calls `store.Insert` + `store.Prune`; standalone `RunMetricPoller` (system metrics) calls `store.Insert` + `store.PruneByType`. Both read -> insert -> prune -> sleep in an infinite loop.
- `Store` interface + `SQLiteStore` implementation with methods: `Insert`, `Query`/`QueryByRange` (temperature only, backward compat), `QueryByType`/`QueryByRangeAndType` (any metric), `QueryLatestPerSensor`/`QueryLatestByType`, `Prune`/`PruneByType`.
- Data model: single `readings` table with `sensor`, `temp_c` (legacy value field), `metric_type`, `unit`, `created_at`. Metric identity is in `metric_type` + `unit`. Schema migration auto-adds `metric_type` and `unit` when missing.
- `MetricPoint` struct in `metrics.go` is the intermediate representation from system metric readers (_sensor_, _value_, _unit_) before insertion.
- Time handling: `TimeConverter` interface + `MeridaTimeConverter`; DB stores UTC strings (`2006-01-02 15:04:05`); `ParseTimestampInput` tries RFC3339Nano, RFC3339, db format, then frontend formats (assumed Merida local); responses converted to `America/Merida`.

## API/Dashboard Notes
- Historical endpoints exist for `temps`, `cpu`, `memory`, `swap`, `disk`, `load`; current-value endpoints exist under `/api/current/*`.
- All historical endpoints support `?hours=N` (relative, default 1) OR `?from=ISO&to=ISO` (absolute range).
- `static/index.html` loads `/config.json` with in-code fallback defaults if fetch/parse fails.
- Active dashboard settings live in `static/config.json` (not `static/config.default.json`). If sensors appear "missing", check `sensorFilter` first.
