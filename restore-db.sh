#!/usr/bin/env bash
# =============================================================================
# restore-db.sh -- Restore PostgreSQL from a gzip-compressed backup
# =============================================================================
#
# WHAT IT DOES
#   1. Reads DB configuration matching docker-compose.yml.
#   2. Locates the running postgres container through docker compose.
#   3. Lists available backups when called with no arguments.
#   4. Prompts for confirmation before overwriting the database.
#   5. Decompresses the chosen .sql.gz and pipes it into psql inside the
#      container, replacing all existing data in the target database.
#
# USAGE
#   ./restore-db.sh                                           # list available backups
#   ./restore-db.sh backups/sensors_temp_20260725_020000.sql.gz  # restore from file
#
# WARNINGS
#   - This is a DESTRUCTIVE operation. All current data in the database will be
#     replaced by the contents of the backup file.
#   - The script asks for interactive confirmation before proceeding.
#   - When running from cron or CI, pipe `yes` into stdin:
#       yes | ./restore-db.sh backups/sensors_temp_20260725_020000.sql.gz
#
# DEPENDENCIES
#   docker, docker compose, gunzip
# =============================================================================

# -- Bash strict mode ---------------------------------------------------------
set -euo pipefail

# -- Logging helpers -----------------------------------------------------------
_ts()  { date +"%Y-%m-%d %H:%M:%S"; }
log_debug()   { echo "[$(_ts)] [DEBUG]   $*" >&2; }
log_info()    { echo "[$(_ts)] [INFO]    $*"; }
log_warning() { echo "[$(_ts)] [WARNING] $*" >&2; }
log_error()   { echo "[$(_ts)] [ERROR]   $*" >&2; }

# -- Resolve project root ------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
log_debug "Script directory: ${SCRIPT_DIR}"

# -- Database configuration ----------------------------------------------------
# These values match docker-compose.yml.
POSTGRES_USER="tracker"
POSTGRES_PASSWORD="tracker"
POSTGRES_DB="sensors_temp"
DB_SERVICE="postgres"

# -- Locate the DB container ---------------------------------------------------
COMPOSE_FILE="${SCRIPT_DIR}/docker-compose.yml"
CONTAINER_NAME=$(docker compose -f "$COMPOSE_FILE" ps -q "$DB_SERVICE" 2>/dev/null)

if [[ -z "$CONTAINER_NAME" ]]; then
  log_error "The '${DB_SERVICE}' container is not running."
  log_error "Start it with: docker compose up -d"
  exit 1
fi
log_debug "Target container: ${CONTAINER_NAME}"

# -- List mode: no arguments -> show available backups --------------------------
BACKUP_DIR="${SCRIPT_DIR}/backups"

if [[ $# -eq 0 ]]; then
  log_info "Available backups in ${BACKUP_DIR}:"
  echo ""
  # shopt -s nullglob makes globs expand to nothing when no match (like glob.glob)
  shopt -s nullglob
  files=("${BACKUP_DIR}"/${POSTGRES_DB}_*.sql.gz)
  shopt -u nullglob

  if [[ ${#files[@]} -eq 0 ]]; then
    echo "  (no backups found)"
  else
    for f in "${files[@]}"; do
      SIZE=$(du -h "$f" | cut -f1)
      MTIME=$(stat -c '%y' "$f" 2>/dev/null | cut -d. -f1)
      echo "  $(basename "$f")  ${SIZE}  ${MTIME}"
    done
  fi

  echo ""
  echo "Usage: $0 <backup-file>"
  echo "Example: $0 backups/${POSTGRES_DB}_20260725_020000.sql.gz"
  exit 0
fi

# -- Resolve backup file path --------------------------------------------------
# $1 is the first CLI argument (like sys.argv[1] in Python).
BACKUP_FILE="$1"

# If the path is relative, make it absolute relative to the script directory.
# This makes the script safe to run from cron where cwd may differ.
if [[ ! "$BACKUP_FILE" = /* ]]; then
  BACKUP_FILE="${SCRIPT_DIR}/${BACKUP_FILE}"
fi

if [[ ! -f "$BACKUP_FILE" ]]; then
  log_error "Backup file not found: ${BACKUP_FILE}"
  log_error "Run ./restore-db.sh with no arguments to list available backups."
  exit 1
fi

FILE_SIZE=$(du -h "$BACKUP_FILE" | cut -f1)
log_info "Selected backup: $(basename "$BACKUP_FILE") (${FILE_SIZE})"

# -- Confirmation prompt -------------------------------------------------------
log_warning "This will DROP and RECREATE all data in '${POSTGRES_DB}'."
read -rp "Continue? [y/N] " confirm
if [[ "${confirm,,}" != "y" ]]; then
  log_info "Aborted by user."
  exit 0
fi

# -- Restore --------------------------------------------------------------------
# Step 1: Drop and recreate the database so the restore is clean.
# Step 2: Decompress the backup and pipe it into psql.
#
# gunzip -c  -> decompress to stdout (keeps the original .gz intact)
# docker exec -i  -> pass stdin into the container (-i = interactive mode)
#
# We use two separate docker exec calls:
#   - First drops the database and recreates it (using the postgres superuser
#     maintenance database "postgres" as the connection target).
#   - Second loads the SQL dump into the recreated database.
log_info "Dropping and recreating database '${POSTGRES_DB}' ..."

# Terminate active connections, drop, and recreate the database.
# This avoids "database is being accessed by other users" errors if the
# temp-tracker service is currently running.
docker exec -e PGPASSWORD="${POSTGRES_PASSWORD}" "$CONTAINER_NAME" \
  psql -U "$POSTGRES_USER" -d postgres -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '${POSTGRES_DB}' AND pid <> pg_backend_pid();" >/dev/null
docker exec -e PGPASSWORD="${POSTGRES_PASSWORD}" "$CONTAINER_NAME" \
  psql -U "$POSTGRES_USER" -d postgres -c "DROP DATABASE IF EXISTS ${POSTGRES_DB};"
docker exec -e PGPASSWORD="${POSTGRES_PASSWORD}" "$CONTAINER_NAME" \
  psql -U "$POSTGRES_USER" -d postgres -c "CREATE DATABASE ${POSTGRES_DB} OWNER ${POSTGRES_USER};"

log_info "Restoring database from: $(basename "$BACKUP_FILE") ..."

gunzip -c "$BACKUP_FILE" | docker exec -i -e PGPASSWORD="${POSTGRES_PASSWORD}" "$CONTAINER_NAME" \
  psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -v ON_ERROR_STOP=1

log_info "Restore completed successfully"
log_info "Database '${POSTGRES_DB}' now reflects the state from $(basename "$BACKUP_FILE")"

# -- Post-restore check --------------------------------------------------------
ROW_COUNT=$(docker exec -e PGPASSWORD="${POSTGRES_PASSWORD}" "$CONTAINER_NAME" \
  psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -t -c "SELECT COUNT(*) FROM readings;" 2>/dev/null | tr -d '[:space:]')
log_info "readings table row count: ${ROW_COUNT}"