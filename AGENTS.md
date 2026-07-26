# Sensor Temperature Tracker - Agent Notes

## Tool preference MANDATORY (non-negotiable)
**ALWAYS use codegraph_* tools FIRST for all code search and navigation.** This includes:
- `codegraph_find_symbol` - Find function/struct definitions
- `codegraph_context_for_task` - Get relevant context for a task
- `codegraph_search_symbols` - Search symbol names and signatures
- `codegraph_find_callers` / `codegraph_find_callees` - Trace call chains
- `codegraph_trace_dependencies` - Find dependency chains
- `codegraph_search_semantic` - Semantic code search

**ONLY fall back to built-in tools (grep, read, glob) when codegraph tools cannot satisfy the query.** Never use grep/read as the first choice for code exploration.

## Non-negotiables
- Do not delete or truncate `temps.db` (or `temps.db-shm` / `temps.db-wal` while app is running). Schema migration is automatic in `db.go`; data loss is irreversible.
- Keep code/comments ASCII only; no emojis in code, logs, strings, or comments.
- For Go edits, keep the existing teaching style comments for Python developers (compare Go behavior to Python where helpful).
- For Fish script edits, every script MUST follow this structure:
  1. **Leveled logger** -- every script needs a `log` function with DEBUG/INFO/WARN/ERROR/FATAL levels, timestamps, console colors, and file output (like `cleanup-and-build.fish` and `service-manager.fish`).
  2. **Function-based layout** -- split logic into named functions with doc comments (e.g. `parse_args`, `run_systemctl`, `dispatch`). No monolithic scripts.
  3. **Python-developer teaching comments** -- explain Fish quirks by comparing to Python (e.g. arrays start at 1, globals need `-g`, variables holding commands+flags cannot be executed directly, `status filename` instead of `$argv[0]`).
  4. **Never store a command with flags in a variable and execute it** -- `$SYSTEMCTL` where SYSTEMCTL="systemctl --user" fails. Use a wrapper function instead.

## Verified Commands
- Build: `go build -o temp-tracker .`
- Build & lint: `go vet ./...`
- Run default (PostgreSQL): `DB_DRIVER=postgres ./temp-tracker` (or set in `.env`; falls back to docker-compose DSN `postgres://tracker:tracker@localhost:5432/sensors_temp?sslmode=disable`). Requires `docker compose up -d` first.
- Run with SQLite (fallback): `DB_DRIVER=sqlite ./temp-tracker` (or omit the env var; uses `temps.db` as before)
- Run custom: `./temp-tracker -port 9090 -interval 60 -retain 48`
- Start PostgreSQL via Docker Compose: `docker compose up -d` (uses `docker-compose.yml` with image `postgres:17-alpine`, port 5432, user/password/database `tracker`/`tracker`/`sensors_temp`)
- Debug logs: `LOG_LEVEL=DEBUG ./temp-tracker`
- Quick verification: `go test ./...` (currently prints `no test files`)
- Local helper script: `cleanup-and-build.fish` kills running `temp-tracker` processes, rebuilds, logs to `cleanup-and-build.log`. Defaults to PostgreSQL driver.
- Install systemd service: `./setup-systemd-service.fish` (or `./setup-systemd-service.sh`) creates the unit file, enables it, and starts it immediately. Defaults to a user service (`~/.config/systemd/user/temp-tracker.service`) with PostgreSQL. Add `--system` to install a system service (`/etc/systemd/system/temp-tracker.service`) that starts at boot and uses sudo. Add `--sqlite` to use SQLite instead. Uses port 9091, 60s intervals.
- Manage the service: `./service-manager.fish` / `./service-manager.sh` supports `status`, `start`, `stop`, `restart`, and `logs` (with optional `-f` to follow). Add `--system` to manage the system service.
- View logs quickly: `./view-logs.fish` / `./view-logs.sh` shows the last 50 lines; add `-f` to follow and `--system` for the system service.
- Spike report (on-demand analysis): `go build -o spike-report ./cmd/spike-report/ && ./spike-report` (defaults: last 7 days, 30-day baseline, +10C deviation; flags: `-days`, `-baseline-days`, `-deviation`, `-driver`, `-db`, `-format table|json|csv`, `-output`, `-verbose` for debug logs, `LOG_LEVEL=DEBUG` env also works). Reports include per-sensor baseline stats (mean/min/max/stddev), severity classification (mild/moderate/high/severe), correlated system metrics (CPU, memory, swap, disk, load 1m/5m/15m), per-sensor summary table, and aggregate stats (max spike, avg deviation, top sensor). To run against PostgreSQL: add `-driver postgres` or set `DB_DRIVER=postgres`; the `-db` flag accepts a Postgres connection string (defaults to `DATABASE_URL` and the project's docker-compose values).
- SQLite-to-PostgreSQL migration: `go build -o migrate-to-postgres ./cmd/migrate-to-postgres/ && ./migrate-to-postgres -sqlite temps.db` (defaults to `DATABASE_URL`, then docker-compose DSN; truncates target `readings` and bulk-copies rows with `COPY FROM`).
- SQLite backup tool (separate): `cd sqlite-backup-tool && python backup.py` (configurable via `config.yaml`).
- PostgreSQL backup: `./backup-db.sh` (dumps the `sensors_temp` database via `docker exec pg_dump`, gzip-compressed to `backups/<db>_<timestamp>.sql.gz`, auto-deletes backups older than 30 days).
- PostgreSQL restore: `./restore-db.sh` (lists backups if no arg; or pass a `.sql.gz` path to restore -- drops/recreates the database, terminates active connections first).
- Cron example (daily 02:00): `0 2 * * * /full/path/backup-db.sh >> /full/path/backups/cron.log 2>&1`

## Config Quirks That Cause Mistakes
- `main.go` reads these env vars at runtime: `LOG_LEVEL`, `CPU_POLL_INTERVAL`, `MEMORY_POLL_INTERVAL`, `SWAP_POLL_INTERVAL`, `DISK_POLL_INTERVAL`, `LOAD_POLL_INTERVAL`, `DB_DRIVER`, `DATABASE_URL`.
- Polling intervals come from flags (`-interval` for temperature, `*_POLL_INTERVAL` env vars for system metrics).
- **Retention is unified**: the `-retain` flag controls deletion for ALL metric types (temperature, CPU, memory, swap, disk, load). Default is 8760h (~1 year).
- `.env.example` includes `PORT`, `TEMP_POLL_INTERVAL`, `TEMP_RETAIN_HOURS`, and `*_RETAIN_HOURS`, but those are not consumed by current Go code.
- `static/config.json` is the active dashboard config; `static/config.default.json` is the reference. If sensors appear "missing" on the temperature chart, check `sensorFilter.enabled` and `includePatterns` in `config.json` -- the default config has filtering ON with `["Core", "acpitz"]`. Copy `config.default.json` to `config.json` to show all sensors.

## Architecture Snapshot
- Entrypoint `main.go`: creates `SensorReader` (temperature via hwmon/thermal zones) + `SystemMetrics` (CPU/mem/swap/disk/load via gopsutil v3). Database backend is selected by `-db-driver` flag or `DB_DRIVER` env (`sqlite` or `postgres`; defaults to `sqlite` for backward compatibility, but the service and helper scripts use `postgres`). SQLite uses WAL mode + busy timeout + `SetMaxOpenConns(1)`; PostgreSQL uses the default `pgx` connection pool. Starts 6 goroutines (1 `Poller.Run` for temperature, 5 `RunMetricPoller` for system metrics), registers HTTP handlers, serves `static/`.
- Two polling mechanisms: `Poller` struct (temperature) calls `store.Insert` + `store.Prune`; standalone `RunMetricPoller` (system metrics) calls `store.Insert` + `store.PruneByType`. Both read -> insert -> prune -> sleep in an infinite loop.
- `Store` interface + `SQLiteStore` / `PostgresStore` implementations with methods: `Insert`, `Query`/`QueryByRange` (temperature only, backward compat), `QueryByType`/`QueryByRangeAndType` (any metric), `QueryLatestPerSensor`/`QueryLatestByType`, `Prune`/`PruneByType`. **PostgresStore uses recursive CTE "loose index scan"** for `QueryLatestByType`/`QueryLatestPerSensor` to avoid full table scans -- the query hops through the partial index `(sensor, id DESC) WHERE metric_type = '...'` to find the latest row per sensor in ~2-5ms instead of scanning all 250K-480K rows (which took 1-5 seconds with `MAX(id) GROUP BY`). `QueryLatestPerSensor` delegates to `QueryLatestByType(db, "temperature")`.
- Data model: single `readings` table with `sensor`, `temp_c` (legacy value field), `metric_type`, `unit`, `created_at`. Metric identity is in `metric_type` + `unit`. SQLite schema migration auto-adds `metric_type` and `unit` when missing; PostgreSQL schema includes them from the start.
- `MetricPoint` struct in `metrics.go` is the intermediate representation from system metric readers (_sensor_, _value_, _unit_) before insertion.
- Time handling: `TimeConverter` interface + `MeridaTimeConverter`; SQLite stores UTC strings (`2006-01-02 15:04:05`), PostgreSQL stores `TIMESTAMPTZ` and formats it back to the same UTC string for responses; `ParseTimestampInput` tries RFC3339Nano, RFC3339, db format, then frontend formats (assumed Merida local); responses converted to `America/Merida`.

## CodeGraphContext (CGC)
- Binary at `/home/lmex89/go/bin/codegraph` (not on PATH; use full path in scripts/configs).
- Index this repo: `codegraph index .`
- MCP server runs automatically via opencode config (`codegraph` server in `~/.config/opencode/opencode.json`).
- Ignore patterns in `.codegraphignore` at repo root.
- Use `codegraph help` to see all CLI commands (analyze, find, list, etc.).
- Use `codegraph find-callers . --symbol <name>` or `codegraph find-callees . --symbol <name>` for graph queries.

## API/Dashboard Notes
- Historical endpoints exist for `temps`, `cpu`, `memory`, `swap`, `disk`, `load`; current-value endpoints exist under `/api/current/*`.
- All historical endpoints support `?hours=N` (relative, default 1) OR `?from=ISO&to=ISO` (absolute range).
- `static/index.html` loads `/config.json` with in-code fallback defaults if fetch/parse fails.
- Active dashboard settings live in `static/config.json` (not `static/config.default.json`). If sensors appear "missing", check `sensorFilter` first.
