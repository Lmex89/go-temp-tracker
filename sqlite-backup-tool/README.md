# SQLite Backup Tool

A standalone, reusable SQLite backup utility using the official [SQLite Online Backup API](https://www.sqlite.org/backup.html). Performs atomic, verified backups with configurable retention policies.

## Features

- **Online Backup**: Uses SQLite's official online backup API - database remains accessible during backup
- **Atomic Operations**: Creates backup in temp file, verifies integrity, then moves to final location
- **Retention Management**: Automatically deletes old backups, keeping only the configured number
- **Integrity Verification**: Runs `PRAGMA integrity_check` on every backup
- **Cron Compatible**: Designed for single execution per run - perfect for cron jobs
- **Dry Run Mode**: Test configuration and see what would happen without creating files
- **Production Ready**: Comprehensive logging, exit codes, and error handling

## Requirements

- Python 3.7 or higher
- PyYAML (`pip install pyyaml`)

## Installation

### Quick Install

```bash
git clone <repository-url>
cd sqlite-backup-tool
pip install -r requirements.txt
```

### Verify Installation

```bash
python backup.py --version
```

## Configuration

Create a configuration file (YAML format):

```bash
cp examples/config.example.yaml my-config.yaml
```

Edit `my-config.yaml`:

```yaml
backup:
  source_db: "/path/to/your/database.db"
  backup_dir: "/path/to/backup/directory"
  retention_count: 7
  filename_format: "backup_%Y%m%d_%H%M%S.db"
  verify_backup: true

logging:
  log_file: "/var/log/sqlite-backup.log"
  level: "INFO"
  log_to_stdout: true
```

### Configuration Options

| Option | Required | Default | Description |
|--------|----------|---------|-------------|
| `backup.source_db` | Yes | - | Path to SQLite database |
| `backup.backup_dir` | Yes | - | Directory for backups |
| `backup.retention_count` | No | 7 | Number of backups to keep |
| `backup.filename_format` | No | `backup_%Y%m%d_%H%M%S.db` | Backup filename (strftime format) |
| `backup.verify_backup` | No | true | Verify integrity after backup |
| `logging.log_file` | No | null | Log file path (null = stdout only) |
| `logging.level` | No | INFO | Log level (DEBUG, INFO, WARNING, ERROR) |
| `logging.log_to_stdout` | No | true | Also output logs to stdout |

## Usage

### Basic Usage

```bash
python backup.py --config my-config.yaml
```

### Command-Line Options

```bash
python backup.py --help
```

| Option | Short | Description |
|--------|-------|-------------|
| `--config` | `-c` | Path to config file (default: config.yaml) |
| `--dry-run` | `-n` | Simulate without creating files |
| `--verbose` | `-v` | Enable DEBUG level logging |
| `--source` | `-s` | Override source database path |
| `--backup-dir` | `-b` | Override backup directory |
| `--keep` | `-k` | Override retention count |
| `--version` | - | Show version |

### Examples

**Test configuration (dry run):**
```bash
python backup.py --config my-config.yaml --dry-run --verbose
```

**Override source database:**
```bash
python backup.py --config my-config.yaml --source ./another.db
```

**Override retention:**
```bash
python backup.py --config my-config.yaml --keep 14
```

**Full command-line mode (no config file):**
```bash
python backup.py --source ./my.db --backup-dir ./backups --keep 7
```

## Cron Setup

### Daily Backup at 2 AM

```bash
# Edit crontab
crontab -e

# Add this line:
0 2 * * * /usr/bin/python3 /opt/sqlite-backup-tool/backup.py --config /etc/sqlite-backup/config.yaml
```

### With Log Output

```bash
0 2 * * * /usr/bin/python3 /opt/sqlite-backup-tool/backup.py --config /etc/sqlite-backup/config.yaml >> /var/log/sqlite-backup-cron.log 2>&1
```

### Multiple Databases

Use separate config files for each database:

```bash
0 2 * * * /usr/bin/python3 /opt/sqlite-backup-tool/backup.py --config /etc/sqlite-backup/db1.yaml
0 3 * * * /usr/bin/python3 /opt/sqlite-backup-tool/backup.py --config /etc/sqlite-backup/db2.yaml
```

See `examples/crontab.example` for more cron schedule examples.

## Exit Codes

| Code | Meaning | Cron Behavior |
|------|---------|---------------|
| 0 | Success | Normal |
| 1 | Configuration error | Consider alerting |
| 2 | Source database error | Consider alerting |
| 3 | Backup directory error | Consider alerting |
| 4 | Backup operation failed | Alert recommended |
| 5 | Verification failed | Alert recommended |
| 6 | Cleanup warning | Usually okay |

### Cron Email on Failure

```bash
MAILTO=admin@example.com
0 2 * * * /usr/bin/python3 /opt/sqlite-backup-tool/backup.py --config /etc/sqlite-backup/config.yaml || echo "SQLite backup failed!"
```

## Logging Output

**Successful backup:**
```
2026-05-22 14:05:30 [INFO] SQLite Backup Tool v1.0.0
2026-05-22 14:05:30 [INFO] Configuration loaded: config.yaml
2026-05-22 14:05:30 [INFO] Source database: /var/lib/app.db (45.2 MB)
2026-05-22 14:05:30 [INFO] Backup directory: /backups
2026-05-22 14:05:30 [INFO] Retention: keeping 7 backup(s)
2026-05-22 14:05:35 [INFO] Backup created: backup_20260522_140530.db (45.2 MB)
2026-05-22 14:05:36 [INFO] Verification: PASSED
2026-05-22 14:05:36 [INFO] Retention cleanup: removed 1 old backup(s)
2026-05-22 14:05:36 [INFO] Backup process completed successfully
```

## How It Works

### Backup Process

1. **Validate Configuration**: Ensure all required settings are present
2. **Check Source Database**: Verify database exists and is readable
3. **Create Temp Backup**: Use SQLite online backup API to create `.tmp` file
4. **Verify Integrity**: Run `PRAGMA integrity_check` on the backup
5. **Atomic Move**: Rename temp file to final backup name (atomic operation)
6. **Retention Cleanup**: Delete old backups exceeding retention count

### Online Backup API

This tool uses SQLite's official online backup API (`sqlite3_backup_*` functions) which:

- Copies pages incrementally (5 pages at a time)
- Allows concurrent reads and writes on the source database
- Creates a consistent point-in-time snapshot
- Automatically retries if the source database changes during backup

This is **superior** to simple file copy because it:
- Doesn't require exclusive locks
- Handles WAL (Write-Ahead Logging) correctly
- Works safely even when other processes are writing

## Security

### File Permissions

```bash
# Backup directory should be accessible only by owner
chmod 700 /path/to/backup/directory

# Config file should not be world-readable
chmod 600 config.yaml
```

### Database Access

- Source database is opened in **read-only mode** (`?mode=ro` URI)
- Backup process cannot modify source database
- Temporary backup files use secure permissions

## Testing

### Test with Dry Run

```bash
python backup.py --config config.yaml --dry-run --verbose
```

This will show what the tool would do without creating any files.

### Test with Sample Database

```bash
# Create test database
python3 -c "
import sqlite3
db = sqlite3.connect('test.db')
db.execute('CREATE TABLE test (id INTEGER PRIMARY KEY, data TEXT)')
db.executemany('INSERT INTO test (data) VALUES (?)', [('test data',)] * 1000)
db.commit()
db.close()
"

# Test backup
python backup.py --source test.db --backup-dir ./test-backups --keep 3 --verbose

# Verify backup exists
ls -lh ./test-backups/
```

## Troubleshooting

### "Source database not found"
- Check the path in `source_db` configuration
- Use absolute paths to avoid confusion
- Verify file permissions

### "Backup directory not writable"
- Ensure the backup directory exists or the parent directory is writable
- Check disk space with `df -h`
- Verify directory permissions

### "Integrity check failed"
- The source database may be corrupt
- Run `sqlite3 /path/to/db "PRAGMA integrity_check"` manually
- If corruption is confirmed, restore from a previous backup

### Permission Denied
- Ensure you have read access to the source database
- Ensure you have write access to the backup directory
- Check SELinux or AppArmor policies if applicable

## Advanced Usage

### Multiple Databases

Create a config file for each database:

```bash
# temps-config.yaml
backup:
  source_db: "/var/lib/temps.db"
  backup_dir: "/backups/temps"
  retention_count: 7

# users-config.yaml
backup:
  source_db: "/var/lib/users.db"
  backup_dir: "/backups/users"
  retention_count: 14
```

Then run separately:
```bash
python backup.py --config temps-config.yaml
python backup.py --config users-config.yaml
```

### Custom Filename Formats

```yaml
backup:
  # Daily format: backup_20260522.db
  filename_format: "backup_%Y%m%d.db"
  
  # Include database name: myapp_2026-05-22.db
  filename_format: "myapp_%Y-%m-%d.db"
  
  # Detailed timestamp: backup_2026-05-22_14-05-30.db
  filename_format: "backup_%Y-%m-%d_%H-%M-%S.db"
```

## License

MIT License - See LICENSE file for details.

## Contributing

Contributions welcome! Please ensure:
- Code follows Python PEP 8 style
- Tests pass (`python -m pytest` if tests exist)
- Documentation is updated for new features

## Changelog

### v1.0.0
- Initial release
- SQLite online backup API support
- Configurable retention management
- Integrity verification
- Cron-compatible execution
- Dry-run mode
- Comprehensive logging
