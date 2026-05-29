#!/usr/bin/env python3
"""
SQLite Backup Tool

A standalone, reusable SQLite backup utility using the official SQLite Online Backup API.
Performs atomic, verified backups with configurable retention policies.

Usage:
    python backup.py --config config.yaml
    python backup.py --config config.yaml --dry-run
    python backup.py --source /path/to/db --backup-dir /path/to/backups --keep 7

Exit Codes:
    0 - Success
    1 - Configuration error
    2 - Source database error
    3 - Backup directory error
    4 - Backup operation failed
    5 - Verification failed
    6 - Cleanup error (non-fatal)
"""

import argparse
import fnmatch
import glob
import logging
import os
import sqlite3
import sys
import tempfile
from datetime import datetime
from pathlib import Path

import yaml

__version__ = "1.0.0"

# Exit codes
EXIT_SUCCESS = 0
EXIT_CONFIG_ERROR = 1
EXIT_SOURCE_ERROR = 2
EXIT_TARGET_ERROR = 3
EXIT_BACKUP_ERROR = 4
EXIT_VERIFY_ERROR = 5
EXIT_CLEANUP_ERROR = 6


def get_file_size(path: str) -> str:
    """Return human-readable file size."""
    size = os.path.getsize(path)
    for unit in ["B", "KB", "MB", "GB"]:
        if size < 1024.0:
            return f"{size:.1f} {unit}"
        size /= 1024.0
    return f"{size:.1f} TB"


def create_backup_filename(format_str: str) -> str:
    """Create a timestamped backup filename using strftime."""
    return datetime.now().strftime(format_str)


def setup_logging(log_file: str = None, level: str = "INFO", log_to_stdout: bool = True):
    """
    Configure logging to both file and stdout.
    
    In Python, logging is configured with handlers. This is like Python's built-in
    logging module setup, which is more flexible than Go's single log output.
    """
    logger = logging.getLogger()
    logger.setLevel(getattr(logging, level.upper(), logging.INFO))
    
    # Clear any existing handlers to prevent duplicates
    logger.handlers.clear()
    
    formatter = logging.Formatter(
        "%(asctime)s [%(levelname)s] %(message)s",
        datefmt="%Y-%m-%d %H:%M:%S"
    )
    
    # Add file handler if log_file is specified
    if log_file:
        try:
            # Ensure log directory exists
            log_dir = os.path.dirname(log_file)
            if log_dir and not os.path.exists(log_dir):
                os.makedirs(log_dir, exist_ok=True)
            
            file_handler = logging.FileHandler(log_file)
            file_handler.setFormatter(formatter)
            logger.addHandler(file_handler)
        except OSError as e:
            logging.warning(f"Failed to open log file {log_file}: {e}")
    
    # Add stdout handler if requested
    if log_to_stdout:
        stdout_handler = logging.StreamHandler(sys.stdout)
        stdout_handler.setFormatter(formatter)
        logger.addHandler(stdout_handler)


def load_config(config_path: str) -> dict:
    """
    Load configuration from YAML file.
    
    Uses PyYAML, similar to how you'd use json.load() but for YAML.
    In Go, you'd use yaml.Unmarshal with a struct, but Python's dynamic
    typing lets us load into dicts directly.
    """
    try:
        with open(config_path, "r") as f:
            config = yaml.safe_load(f)
        if config is None:
            config = {}
        return config
    except FileNotFoundError:
        print(f"ERROR: Configuration file not found: {config_path}")
        return None
    except yaml.YAMLError as e:
        print(f"ERROR: Invalid YAML in configuration file: {e}")
        return None


def validate_config(config: dict) -> bool:
    """
    Validate configuration has required fields and valid values.
    
    Returns True if configuration is valid, False otherwise.
    Logs specific errors for each validation failure.
    """
    if not config:
        logging.error("Configuration is empty")
        return False
    
    backup_config = config.get("backup", {})
    
    # Check required fields
    source_db = backup_config.get("source_db")
    if not source_db:
        logging.error("Configuration error: backup.source_db is required")
        return False
    
    backup_dir = backup_config.get("backup_dir")
    if not backup_dir:
        logging.error("Configuration error: backup.backup_dir is required")
        return False
    
    # Validate retention count
    retention_count = backup_config.get("retention_count", 7)
    if not isinstance(retention_count, int) or retention_count < 1:
        logging.error(f"Configuration error: retention_count must be a positive integer, got {retention_count}")
        return False
    
    return True


def perform_online_backup(source_db: str, target_db: str, dry_run: bool = False) -> bool:
    """
    Perform SQLite online backup using the official SQLite Backup API.
    
    The online backup API copies pages incrementally (5 at a time), allowing
    concurrent reads and writes on the source database during backup.
    
    This is the recommended way to backup SQLite databases because:
    - Source database remains accessible during backup
    - Creates a consistent snapshot even if writes occur during backup
    - More reliable than simple file copy
    
    Args:
        source_db: Path to source database
        target_db: Path for backup file (created)
        dry_run: If True, simulate without creating backup
    
    Returns:
        True if backup successful, False otherwise
    """
    if dry_run:
        logging.info(f"[DRY-RUN] Would create backup: {target_db}")
        return True
    
    try:
        # Open source database
        # Note: We don't use read-only mode here because the backup API
        # needs to manage locks on the source database. The backup process
        # itself never writes to the source.
        source_conn = sqlite3.connect(source_db)
        
        # Create target database
        target_conn = sqlite3.connect(target_db)
        
        # Progress callback for large databases
        def progress_callback(status, remaining, total):
            if total > 100 and (total - remaining) % 50 == 0:
                progress_pct = (total - remaining) / total * 100
                logging.debug(f"Backup progress: {progress_pct:.1f}% ({total - remaining}/{total} pages)")
        
        # Perform backup using Python's sqlite3 backup API
        # connection.backup(target, pages=5, progress=callback)
        # pages=5 copies 5 pages at a time, allowing concurrent access
        # This is the Python equivalent of SQLite's online backup API
        source_conn.backup(
            target_conn,
            pages=5,
            progress=progress_callback
        )
        
        target_conn.close()
        source_conn.close()
        
        return True
        
    except sqlite3.Error as e:
        logging.error(f"SQLite backup error: {e}")
        # Clean up partial backup file if it exists
        if os.path.exists(target_db):
            try:
                os.remove(target_db)
                logging.info(f"Cleaned up partial backup file: {target_db}")
            except OSError:
                pass
        return False
    except Exception as e:
        logging.error(f"Unexpected error during backup: {e}")
        return False


def verify_backup(backup_path: str) -> bool:
    """
    Verify backup integrity using SQLite PRAGMA integrity_check.
    
    Runs a full integrity check which validates:
    - Database file structure
    - B-tree integrity
    - Correctness of meta-data
    - No overlapping pages
    - No missing pages
    
    Returns True if backup passes all checks, False otherwise.
    """
    try:
        # Open backup in read-only mode
        conn = sqlite3.connect(f"file:{backup_path}?mode=ro", uri=True)
        cursor = conn.cursor()
        
        # Run integrity check - returns 'ok' or error message
        cursor.execute("PRAGMA integrity_check")
        result = cursor.fetchone()
        
        conn.close()
        
        if result and result[0] == "ok":
            return True
        else:
            error_msg = result[0] if result else "unknown error"
            logging.error(f"Integrity check failed: {error_msg}")
            return False
            
    except sqlite3.Error as e:
        logging.error(f"Verification error: {e}")
        return False


def get_existing_backups(backup_dir: str, pattern: str) -> list:
    """
    Get list of existing backup files sorted by modification time (oldest first).
    
    Args:
        backup_dir: Directory containing backups
        pattern: Filename pattern to match (e.g., "backup_*.db")
    
    Returns:
        List of file paths sorted by modification time
    """
    # Convert strftime format to glob pattern
    # Replace strftime placeholders with wildcards
    glob_pattern = pattern
    replacements = [
        ("%Y", "*"), ("%m", "*"), ("%d", "*"),
        ("%H", "*"), ("%M", "*"), ("%S", "*"),
    ]
    for old, new in replacements:
        glob_pattern = glob_pattern.replace(old, new)
    
    search_pattern = os.path.join(backup_dir, glob_pattern)
    backups = glob.glob(search_pattern)
    
    # Sort by modification time (oldest first)
    backups.sort(key=os.path.getmtime)
    
    return backups


def cleanup_old_backups(backup_dir: str, pattern: str, keep_count: int, dry_run: bool = False) -> list:
    """
    Remove old backups keeping only the N most recent.
    
    Args:
        backup_dir: Directory containing backups
        pattern: Filename pattern to match
        keep_count: Number of backups to retain
        dry_run: If True, only log what would be deleted
    
    Returns:
        List of deleted backup files
    """
    existing_backups = get_existing_backups(backup_dir, pattern)
    
    if len(existing_backups) <= keep_count:
        logging.info(f"Retention: {len(existing_backups)} total backups, keeping {keep_count}")
        return []
    
    to_delete = existing_backups[:-keep_count]
    deleted = []
    
    logging.info(f"Retention: {len(existing_backups)} total backups, keeping {keep_count}, deleting {len(to_delete)}")
    
    for backup_file in to_delete:
        if dry_run:
            logging.info(f"[DRY-RUN] Would delete old backup: {backup_file}")
            deleted.append(backup_file)
        else:
            try:
                os.remove(backup_file)
                logging.info(f"Deleted old backup: {os.path.basename(backup_file)}")
                deleted.append(backup_file)
            except OSError as e:
                logging.warning(f"Failed to delete old backup {backup_file}: {e}")
    
    return deleted


def main():
    """
    Main entry point for the SQLite Backup Tool.
    
    Parses command-line arguments, loads configuration, validates settings,
    and orchestrates the backup process.
    """
    parser = argparse.ArgumentParser(
        description="SQLite Backup Tool - Create verified SQLite backups with retention management",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  python backup.py --config config.yaml
  python backup.py --config config.yaml --dry-run
  python backup.py --source app.db --backup-dir ./backups --keep 7
        """
    )
    
    parser.add_argument(
        "--config", "-c",
        default="config.yaml",
        help="Path to configuration file (default: config.yaml)"
    )
    parser.add_argument(
        "--dry-run", "-n",
        action="store_true",
        help="Simulate backup without creating files"
    )
    parser.add_argument(
        "--verbose", "-v",
        action="store_true",
        help="Enable verbose (DEBUG) logging"
    )
    parser.add_argument(
        "--source", "-s",
        help="Override source database path"
    )
    parser.add_argument(
        "--backup-dir", "-b",
        help="Override backup directory"
    )
    parser.add_argument(
        "--keep", "-k",
        type=int,
        help="Override retention count"
    )
    parser.add_argument(
        "--version",
        action="version",
        version=f"SQLite Backup Tool v{__version__}"
    )
    
    args = parser.parse_args()
    
    # Load configuration
    config = load_config(args.config)
    
    # If no config file and CLI overrides provided, create minimal config
    if config is None:
        if args.source and args.backup_dir:
            config = {
                "backup": {},
                "logging": {
                    "log_file": None,
                    "level": "INFO",
                    "log_to_stdout": True
                }
            }
        else:
            print("ERROR: Configuration file not found. Please provide a config file or use --source and --backup-dir")
            return EXIT_CONFIG_ERROR
    
    # Merge CLI overrides into config so validation can see them
    if args.source:
        config.setdefault("backup", {})["source_db"] = args.source
    if args.backup_dir:
        config.setdefault("backup", {})["backup_dir"] = args.backup_dir
    if args.keep:
        config.setdefault("backup", {})["retention_count"] = args.keep
    
    # Setup logging early to capture validation errors
    log_config = config.get("logging", {})
    log_level = "DEBUG" if args.verbose else log_config.get("level", "INFO")
    log_file = log_config.get("log_file")
    log_to_stdout = log_config.get("log_to_stdout", True)
    
    setup_logging(log_file, log_level, log_to_stdout)
    
    logging.info(f"SQLite Backup Tool v{__version__}")
    if config:
        logging.info(f"Configuration loaded: {args.config}")
    
    # Validate configuration
    if not validate_config(config):
        return EXIT_CONFIG_ERROR
    
    # Get backup configuration (now includes CLI overrides)
    backup_config = config.get("backup", {})
    
    source_db = backup_config.get("source_db")
    backup_dir = backup_config.get("backup_dir")
    retention_count = backup_config.get("retention_count", 7)
    filename_format = backup_config.get("filename_format", "backup_%Y%m%d_%H%M%S.db")
    verify_backup_enabled = backup_config.get("verify_backup", True)
    
    # Resolve source database path
    source_db = os.path.abspath(os.path.expanduser(source_db))
    
    # Validate source database exists
    if not os.path.exists(source_db):
        logging.error(f"Source database not found: {source_db}")
        return EXIT_SOURCE_ERROR
    
    if not os.access(source_db, os.R_OK):
        logging.error(f"Source database not readable: {source_db}")
        return EXIT_SOURCE_ERROR
    
    # Validate source is actually a database (try to open it)
    try:
        conn = sqlite3.connect(source_db)
        conn.execute("SELECT 1")
        conn.close()
    except sqlite3.Error as e:
        logging.error(f"Source database is not a valid SQLite database: {e}")
        return EXIT_SOURCE_ERROR
    
    # Setup backup directory
    backup_dir = os.path.abspath(os.path.expanduser(backup_dir))
    
    if not os.path.exists(backup_dir):
        if args.dry_run:
            logging.info(f"[DRY-RUN] Would create backup directory: {backup_dir}")
        else:
            try:
                os.makedirs(backup_dir, exist_ok=True)
                logging.info(f"Created backup directory: {backup_dir}")
            except OSError as e:
                logging.error(f"Failed to create backup directory: {e}")
                return EXIT_TARGET_ERROR
    
    if os.path.exists(backup_dir):
        if not os.access(backup_dir, os.W_OK):
            logging.error(f"Backup directory not writable: {backup_dir}")
            return EXIT_TARGET_ERROR
    else:
        # Directory doesn't exist - in dry-run, we already logged it would be created
        if not args.dry_run:
            logging.error(f"Backup directory does not exist and could not be created: {backup_dir}")
            return EXIT_TARGET_ERROR
    
    # Display backup info
    source_size = get_file_size(source_db)
    logging.info(f"Source database: {source_db} ({source_size})")
    logging.info(f"Backup directory: {backup_dir}")
    logging.info(f"Retention: keeping {retention_count} backup(s)")
    
    # Generate backup filename
    backup_filename = create_backup_filename(filename_format)
    backup_path = os.path.join(backup_dir, backup_filename)
    
    # Check if backup already exists
    if os.path.exists(backup_path) and not args.dry_run:
        logging.error(f"Backup file already exists: {backup_path}")
        return EXIT_BACKUP_ERROR
    
    if args.dry_run:
        logging.info(f"[DRY-RUN] Would create backup: {backup_path}")
    else:
        logging.info(f"Starting online backup to: {backup_filename}")
    
    # Perform backup to temporary file first (atomic operation)
    # Using tempfile to create backup in temp location, then move
    if not args.dry_run:
        fd, temp_path = tempfile.mkstemp(
            suffix=".tmp",
            dir=backup_dir,
            prefix="backup_"
        )
        os.close(fd)
        
        success = perform_online_backup(source_db, temp_path, dry_run=False)
        
        if not success:
            # Clean up temp file
            try:
                os.remove(temp_path)
            except OSError:
                pass
            logging.error("Backup operation failed")
            return EXIT_BACKUP_ERROR
        
        # Move temp file to final location (atomic operation)
        try:
            os.rename(temp_path, backup_path)
            backup_size = get_file_size(backup_path)
            logging.info(f"Backup created: {backup_filename} ({backup_size})")
        except OSError as e:
            logging.error(f"Failed to finalize backup: {e}")
            try:
                os.remove(temp_path)
            except OSError:
                pass
            return EXIT_BACKUP_ERROR
    else:
        logging.info("[DRY-RUN] Skipping actual backup creation")
    
    # Verify backup integrity
    if verify_backup_enabled:
        if args.dry_run:
            logging.info("[DRY-RUN] Would verify backup integrity")
        else:
            logging.info("Verifying backup integrity...")
            if verify_backup(backup_path):
                logging.info("Verification: PASSED")
            else:
                logging.error("Verification: FAILED - backup may be corrupt")
                # Remove failed backup
                try:
                    os.remove(backup_path)
                    logging.info("Removed failed backup file")
                except OSError:
                    pass
                return EXIT_VERIFY_ERROR
    
    # Cleanup old backups
    deleted = cleanup_old_backups(
        backup_dir,
        filename_format,
        retention_count,
        dry_run=args.dry_run
    )
    
    if deleted:
        if args.dry_run:
            logging.info(f"[DRY-RUN] Would delete {len(deleted)} old backup(s)")
        else:
            logging.info(f"Retention cleanup: removed {len(deleted)} old backup(s)")
    else:
        logging.info("Retention cleanup: no old backups to remove")
    
    logging.info("Backup process completed successfully")
    return EXIT_SUCCESS


if __name__ == "__main__":
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        logging.warning("Backup interrupted by user")
        sys.exit(EXIT_BACKUP_ERROR)
    except Exception as e:
        logging.error(f"Unexpected error: {e}")
        sys.exit(EXIT_BACKUP_ERROR)
