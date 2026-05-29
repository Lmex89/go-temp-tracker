# SQLite Backup Tool - Agent Notes

## Non-negotiables
- Preserve the atomic backup flow: temp file -> `PRAGMA integrity_check` -> atomic rename. Do not write directly to the final backup path.
- The tool opens the source DB via the SQLite online backup API (`sqlite3.Connection.backup`), not a file copy. This handles WAL correctly and avoids exclusive locks.

## Verified Commands
- Install: `pip install -r requirements.txt` (only dependency is `pyyaml>=6.0.1`)
- Run: `python backup.py --config config.yaml`
- Dry run: `python backup.py --config config.yaml --dry-run --verbose`
- No build, no test suite, no CI. `python -m pytest` finds nothing.

## Config Quirks That Cause Mistakes
- Default config path is `config.yaml` in the current working directory. Use `--config` to override.
- CLI flags `--source`, `--backup-dir`, and `--keep` can override config values; if both `--source` and `--backup-dir` are provided, no config file is required at all.
- `filename_format` is used for both naming new backups *and* globbing existing ones for retention cleanup. The code replaces strftime placeholders (`%Y`, `%m`, etc.) with `*` to build the glob pattern. Changing the format to include literal numbers that look like strftime codes will break retention cleanup.
- The committed `config.yaml` defaults to `./temps.db` and `./backups`; treat it as an example rather than production configuration.

## Architecture Snapshot
- Single entrypoint: `backup.py`. No package structure, no generated code.
- Logging is configured via `setup_logging()`; it clears existing handlers to prevent duplicates.
- Exit codes are intentional and consumed by cron jobs (see `examples/crontab.example`):
  - 0 Success
  - 1 Config error
  - 2 Source DB error
  - 3 Backup dir error
  - 4 Backup operation failed
  - 5 Verification failed
  - 6 Cleanup warning (non-fatal)

## Style Notes
- Comments in `backup.py` occasionally compare Python behavior to Go (e.g. logging, YAML loading). Preserve this style when editing.
