# Sensor Temperature Tracker - Agent Notes

## Tool preference MANDATORY (non-negotiable)
**ALWAYS use codegraph tools FIRST for all code search and navigation.** They are configured via `CODEGRAPH_MCP_TOOLS` in the repo's `.mcp.json` (enabling: `explore`, `node`, `search`, `callers`, `callees`, `impact`, `files`, `status`):
- `codegraph_explore` — primary tool: verbatim source + call paths + blast radius from a natural-language question or symbol names. ONE call usually answers the whole question.
- `codegraph_callees` — list functions that a symbol calls.
- `codegraph_impact` — list symbols affected by changing a symbol (use before refactors).
- `codegraph_status` — index health check.

**ONLY fall back to built-in tools (grep, read, glob) when codegraph tools cannot satisfy the query.** Never use grep/read as the first choice for code exploration.

## Non-negotiables
- **Language**: All agent responses, reports, and documentation MUST be in Spanish for this repository. This applies to all skills including `improve-codebase-architecture`, `code-review`, `ponytail-review`, and any other agent output. Code comments and identifiers remain in English (ASCII only), but explanations, suggestions, and reports are in Spanish.
- Do not delete or truncate `temps.db` (or `temps.db-shm` / `temps.db-wal` while app is running). Schema migration is automatic in `internal/store/sqlite.go`; data loss is irreversible.
- Keep code/comments ASCII only; no emojis in code, logs, strings, or comments.
- For Go edits, keep the existing teaching style comments for Python developers (compare Go behavior to Python where helpful).
- For Fish script edits, every script MUST follow this structure:
  1. **Leveled logger** -- every script needs a `log` function with DEBUG/INFO/WARN/ERROR/FATAL levels, timestamps, console colors, and file output (like `cleanup-and-build.fish` and `service-manager.fish`).
  2. **Function-based layout** -- split logic into named functions with doc comments (e.g. `parse_args`, `run_systemctl`, `dispatch`). No monolithic scripts.
  3. **Python-developer teaching comments** -- explain Fish quirks by comparing to Python (e.g. arrays start at 1, globals need `-g`, variables holding commands+flags cannot be executed directly, `status filename` instead of `$argv[0]`).
  4. **Never store a command with flags in a variable and execute it** -- `$SYSTEMCTL` where SYSTEMCTL="systemctl --user" fails. Use a wrapper function instead.

## Verified Commands
- Build: `go build -o bin/temp-tracker .`
- Build & lint: `go vet ./...`
- Run default (PostgreSQL): `DB_DRIVER=postgres ./bin/temp-tracker` (or set in `.env`; falls back to docker-compose DSN `postgres://tracker:tracker@localhost:5432/sensors_temp?sslmode=disable`). Requires `docker compose up -d` first.
- Run with SQLite (fallback): `DB_DRIVER=sqlite ./bin/temp-tracker` (or omit the env var; uses `temps.db` as before)
- Run custom: `./bin/temp-tracker -port 9090 -interval 60 -retain 48`
- Start PostgreSQL via Docker Compose: `docker compose up -d` (uses `docker-compose.yml` with image `postgres:17-alpine`, port 5432, user/password/database `tracker`/`tracker`/`sensors_temp`, named volume `pgdata`, resource limits: 1.0 CPU / 2G RAM)
- Debug logs: `LOG_LEVEL=DEBUG ./bin/temp-tracker`
- Quick verification: `go test ./...` (table-driven tests per package under `internal/*/*_test.go` plus `main_test.go`; all use temporary SQLite files, never the real `temps.db`)
- Local helper script: `scripts/cleanup-and-build.fish` kills running `temp-tracker` processes, rebuilds into `bin/temp-tracker`, logs to `logs/cleanup-and-build.log` (server output in `logs/tracker.log`). Defaults to PostgreSQL driver.
- Install systemd service: `./scripts/setup-systemd-service.fish` (or `./scripts/setup-systemd-service.sh`) creates the unit file, enables it, and starts it immediately. Defaults to a user service (`~/.config/systemd/user/temp-tracker.service`) with PostgreSQL. Add `--system` to install a system service (`/etc/systemd/system/temp-tracker.service`) that starts at boot and uses sudo. Add `--sqlite` to use SQLite instead. Uses port 9091, 60s intervals.
- Manage the service: `./scripts/service-manager.fish` / `./scripts/service-manager.sh` supports `status`, `start`, `stop`, `restart`, and `logs` (with optional `-f` to follow). Add `--system` to manage the system service.
- View logs quickly: `./scripts/view-logs.fish` / `./scripts/view-logs.sh` shows the last 50 lines; add `-f` to follow and `--system` for the system service.
- Spike report (on-demand analysis): `go build -o bin/spike-report ./cmd/spike-report/ && ./bin/spike-report` (defaults: last 7 days, 30-day baseline, +10C deviation; flags: `-days`, `-baseline-days`, `-deviation`, `-driver`, `-db`, `-format table|json|csv`, `-output`, `-verbose` for debug logs, `LOG_LEVEL=DEBUG` env also works). Reports include per-sensor baseline stats (mean/min/max/stddev), severity classification (mild/moderate/high/severe), correlated system metrics (CPU, memory, swap, disk, load 1m/5m/15m), per-sensor summary table, and aggregate stats (max spike, avg deviation, top sensor). To run against PostgreSQL: add `-driver postgres` or set `DB_DRIVER=postgres`; the `-db` flag accepts a Postgres connection string (defaults to `DATABASE_URL` and the project's docker-compose values).
- SQLite-to-PostgreSQL migration: `go build -o bin/migrate-to-postgres ./cmd/migrate-to-postgres/ && ./bin/migrate-to-postgres -sqlite temps.db` (flags: `-sqlite` source file, default `temps.db`; `-postgres` target URL; defaults to `DATABASE_URL`, then docker-compose DSN; truncates target `readings` with `TRUNCATE ... RESTART IDENTITY` and bulk-copies rows with `COPY FROM`).
- SQLite backup tool (separate): `cd sqlite-backup-tool && python backup.py` (configurable via `config.yaml`; dry run: `python backup.py --dry-run --verbose`). It has its own `sqlite-backup-tool/AGENTS.md`.
- PostgreSQL backup: `./scripts/backup-db.sh` / `./scripts/backup-db.fish` (dumps the `sensors_temp` database via `docker exec pg_dump`, gzip-compressed to `backups/<db>_<timestamp>.sql.gz`, auto-deletes backups older than `RETENTION_DAYS` (3 days, hardcoded in both scripts).
- PostgreSQL restore: `./scripts/restore-db.sh` (lists backups if no arg; or pass a `.sql.gz` path to restore -- drops/recreates the database, terminates active connections first).
- Cron example (daily 02:00): `0 2 * * * /full/path/scripts/backup-db.sh >> /full/path/backups/cron.log 2>&1`

## Config Quirks That Cause Mistakes
- `main.go` reads these env vars at runtime: `LOG_LEVEL`, `CPU_POLL_INTERVAL`, `MEMORY_POLL_INTERVAL`, `SWAP_POLL_INTERVAL`, `DISK_POLL_INTERVAL`, `LOAD_POLL_INTERVAL`, `DB_DRIVER`, `DATABASE_URL`.
- Polling intervals come from flags (`-interval` for temperature, `*_POLL_INTERVAL` env vars for system metrics).
- **Retention is unified**: the `-retain` flag controls deletion for ALL metric types (temperature, CPU, memory, swap, disk, load). Default is 8760h (~1 year).
- `.env.example` includes `PORT`, `TEMP_POLL_INTERVAL`, `TEMP_RETAIN_HOURS`, `*_RETAIN_HOURS`, `DB_DRIVER`, and `DATABASE_URL`. The `*_RETAIN_HOURS`, `TEMP_*`, and `PORT` vars are not consumed by current Go code (retention is unified via `-retain` flag; the HTTP port comes only from the `-port` flag, default 8080).
- `getEnvInt` (in `main.go`) silently falls back to the default and logs a warning when a `*_POLL_INTERVAL` value is not a valid integer.
- `static/config.json` is the active dashboard config; `static/config.default.json` is the reference. If sensors appear "missing" on the temperature chart, check `sensorFilter.enabled` and `includePatterns` in `config.json` -- the default config has filtering ON with `["Core", "acpitz"]`. Copy `config.default.json` to `config.json` to show all sensors.

## Architecture Snapshot
- **Package layout** (see ADR-0003: `internal/` instead of `/pkg`; module name `sensors-temp`): `main.go` (wiring only) + `internal/logging` (leveled logger), `internal/timeutil` (`TimeConverter`), `internal/store` (`Store`, `SQLiteStore`, `PostgresStore`, `Open`, `ResolveSQLitePath`, `ResolvePostgresDSN`), `internal/sensor` (`SensorReader`), `internal/metrics` (`SystemMetrics`, `MetricPoint`), `internal/poller` (`Poller`, `RunMetricPoller`), `internal/httpapi` (`Register`, REST handlers). Extra binaries in `cmd/` (`spike-report`, `migrate-to-postgres`); operational scripts in `scripts/`; compiled binaries in `bin/` and logs in `logs/` (both gitignored). Tests live next to the code (`internal/*/*_test.go`).
- Entrypoint `main.go`: creates `SensorReader` (temperature via hwmon/thermal zones) + `SystemMetrics` (CPU/mem/swap/disk/load via gopsutil v3). Database backend is selected by `-db-driver` flag or `DB_DRIVER` env (`sqlite` or `postgres`; defaults to `sqlite` for backward compatibility, but the service and helper scripts use `postgres`). SQLite uses WAL mode + busy timeout (`internal/store/sqlite.go`) and forces `SetMaxOpenConns(1)`/`SetMaxIdleConns(1)` in `main.go`; PostgreSQL uses the default `pgx` connection pool. Starts 6 goroutines (1 `Poller.Run` for temperature, 5 `RunMetricPoller` for system metrics), registers HTTP handlers, serves `static/`.
- Two polling mechanisms: `Poller` struct (temperature) calls `store.Insert` + `store.Prune(db, "", hours)` (empty metricType = all types); standalone `RunMetricPoller` (system metrics) calls `store.Insert` + `store.Prune(db, metricType, hours)`. Both read -> insert -> prune -> sleep in an infinite loop. The 6 goroutines are staggered by 3s each (`main.go`) to avoid a thundering herd against SQLite's single writer.
- `Store` interface + `SQLiteStore` / `PostgresStore` implementations with methods: `Insert`, `Query(db, TimeWindow, metricType)` (relative Hours or absolute From/To; empty metricType = all metrics), `QueryLatest(db, metricType)` (newest row per sensor), `Prune(db, metricType, retainHours)` (empty metricType = all). **PostgresStore uses recursive CTE "loose index scan"** for `QueryLatest` to avoid full table scans -- the query hops through the partial index `(sensor, id DESC) WHERE metric_type = '...'` to find the latest row per sensor in ~2-5ms instead of scanning all 250K-480K rows (which took 1-5 seconds with `MAX(id) GROUP BY`).
- Data model: single `readings` table with `sensor`, `temp_c` (legacy value field), `metric_type`, `unit`, `created_at`. Metric identity is in `metric_type` + `unit`. SQLite schema migration auto-adds `metric_type` and `unit` when missing; PostgreSQL schema includes them from the start.
- `MetricPoint` struct in `internal/metrics/metrics.go` is the intermediate representation from system metric readers (_sensor_, _value_, _unit_) before insertion.
- Time handling: `TimeConverter` interface + `MeridaTimeConverter`; SQLite stores UTC strings (`2006-01-02 15:04:05`), PostgreSQL stores `TIMESTAMPTZ` and formats it back to the same UTC string for responses; `ParseTimestampInput` tries RFC3339Nano, RFC3339, db format, then frontend formats (assumed Merida local); responses converted to `America/Merida`.

## CodeGraphContext (CGC)
- Binary at `/home/lmex89/.local/bin/codegraph`, served over MCP by the repo's `.mcp.json` (also registered in `~/.config/opencode/opencode.json`).
- Re-index the repo with `codegraph index .`. Ignore patterns live in `.codegraphignore` at repo root (excludes `data/`, `temps.db*`, `*.log`, and `bin/`, `logs/` and built binaries `temp-tracker`/`spike-report`).
- `codegraph help` lists all CLI commands (analyze, find, list, etc.).

## Skills
- Installed skills live in `.agents/skills/` and are tracked (source repo + hash) in `skills-lock.json`. Load one with the `skill` tool when the task matches.
- Relevant here: `golang-patterns` and `golang-testing` (Go code/tests), `bash-defensive-patterns` (the `*.sh` helpers), `frontend-design` / `accessibility` / `seo` (the `static/` dashboard).
- Tests live next to the code (`internal/httpapi/handler_test.go`, `internal/store/*_test.go`, etc.): table-driven, using `httptest` and `store.NewSQLiteStore(path)` with a path inside `t.TempDir()`, so `go test ./...` no longer touches the real `temps.db`. `main.go` passes `temps.db` explicitly to `NewSQLiteStore`. Use the `golang-testing` skill when adding more tests.

## API/Dashboard Notes
- Historical endpoints exist for `temps`, `cpu`, `memory`, `swap`, `disk`, `load`; current-value endpoints exist under `/api/current/*`.
- All historical endpoints support `?hours=N` (relative, default 1) OR `?from=ISO&to=ISO` (absolute range).
- `static/index.html` loads `/config.json` with in-code fallback defaults if fetch/parse fails.
- Active dashboard settings live in `static/config.json` (not `static/config.default.json`); sensor filtering is covered in the Config Quirks section above.
- **Mobile-first responsive CSS**: base styles target mobile (stacked header, 2-column gauges, full-width inputs, 260px charts). Progressive enhancement breakpoints: 600px (tablet: horizontal header, 300px charts), 768px (desktop: 4-column gauges, 340px charts), 1200px (large: 380px charts). All colors, fonts (Orbitron, IBM Plex Mono), CRT effects, and animations preserved.

## Agent skills

### Issue tracker

Issues are tracked in GitHub Issues at Lmex89/go-temp-tracker. See `docs/agents/issue-tracker.md`.

### Triage labels

Uses the five canonical triage labels: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context layout: one `GLOSSARY.md` and one `docs/adr/` at the repo root. See `docs/agents/domain.md`.
