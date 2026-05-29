# Sensor Temperature Tracker - Agent Notes

## Non-negotiables
- Do not delete or truncate `temps.db` (or `temps.db-shm` / `temps.db-wal` while app is running). Schema migration is automatic in `db.go`; data loss is irreversible.
- Keep code/comments ASCII only; no emojis in code, logs, strings, or comments.
- For Go edits, keep the existing teaching style comments for Python developers (compare Go behavior to Python where helpful).

## Verified Commands
- Build: `go build -o temp-tracker .`
- Run default: `./temp-tracker` (actual defaults from `main.go`: `-port 8080 -interval 60 -retain 8760`)
- Run custom: `./temp-tracker -port 9090 -interval 60 -retain 48`
- Debug logs: `LOG_LEVEL=DEBUG ./temp-tracker`
- Quick verification: `go test ./...` (currently prints `no test files`)
- Local helper script: `cleanup-and-build.fish` kills running `temp-tracker` processes, rebuilds, and logs to `cleanup-and-build.log`.
- Spike report (on-demand analysis): `go build -o spike-report ./cmd/spike-report/ && ./spike-report` (defaults: last 7 days, 30-day baseline, +15C threshold; flags: `-days`, `-baseline-days`, `-deviation`, `-format table|json|csv`, `-output`)

## Config Quirks That Cause Mistakes
- `main.go` reads only these env vars at runtime: `LOG_LEVEL`, `CPU_POLL_INTERVAL`, `MEMORY_POLL_INTERVAL`, `SWAP_POLL_INTERVAL`, `DISK_POLL_INTERVAL`, `LOAD_POLL_INTERVAL`.
- Polling intervals come from flags (`-interval` for temperature, `*_POLL_INTERVAL` env vars for system metrics).
- **Retention is unified**: the `-retain` flag controls deletion for ALL metric types (temperature, CPU, memory, swap, disk, load). Default is 8760h (~1 year).
- `.env.example` includes `PORT`, `TEMP_POLL_INTERVAL`, `TEMP_RETAIN_HOURS`, and `*_RETAIN_HOURS`, but those are not consumed by current Go code.

## Architecture Snapshot
- Entrypoint is `main.go`: creates sensor + metrics readers, opens SQLite, forces single DB connection (`SetMaxOpenConns(1)`), starts one goroutine per poller, registers HTTP handlers, serves `static/`.
- Data model is one table (`readings`) for all metrics with legacy value field `temp_c`; metric identity is in `metric_type` + `unit`.
- DB migration in `SQLiteStore.InitDB()` auto-adds `metric_type` and `unit` columns when missing; no manual migration files exist.
- Time handling: DB stores UTC strings; API parsing accepts RFC3339 plus frontend datetime formats; responses are converted to `America/Merida` in `timeutil.go`.

## API/Dashboard Notes
- Historical endpoints exist for `temps`, `cpu`, `memory`, `swap`, `disk`, `load`; current-value endpoints exist under `/api/current/*`.
- `static/index.html` loads `/config.json` with in-code fallback defaults if fetch/parse fails.
- Active dashboard settings live in `static/config.json` (not `static/config.default.json`). If sensors appear "missing", check `sensorFilter` first.
