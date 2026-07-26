#!/usr/bin/env bash
# =============================================================================
# backup-db.sh -- PostgreSQL backup via Docker container dump
# =============================================================================
#
# WHAT IT DOES
#   1. Reads DB credentials from docker-compose.yml (service: postgres).
#   2. Locates the running postgres container through docker compose.
#   3. Runs pg_dump inside the container and pipes the output through gzip.
#   4. Saves the compressed dump to ./backups/<db>_<YYYYMMDD_HHMMSS>.sql.gz
#   5. Deletes backups older than RETENTION_DAYS (default 30).
#
# USAGE
#   ./backup-db.sh
#
# CRON EXAMPLE  (daily at 02:00, logs to backups/cron.log)
#   0 2 * * * /full/path/backup-db.sh >> /full/path/backups/cron.log 2>&1
#
# DEPENDENCIES
#   docker, docker compose, gzip, find
# =============================================================================

# -- Bash strict mode ---------------------------------------------------------
# set -e  -> exit on error (like Python's unhandled exception)
# set -u  -> error on undefined variables (like NameError)
# set -o pipefail -> a pipeline fails if ANY command in it fails
set -euo pipefail

# -- Logging helpers -----------------------------------------------------------
# Mimic Python loguru levels: DEBUG, INFO, WARNING, ERROR
# Usage: log_info "message"   ->  [2026-07-25 02:00:00] [INFO] message
_ts()  { date +"%Y-%m-%d %H:%M:%S"; }
log_debug()   { echo "[$(_ts)] [DEBUG]   $*" >&2; }
log_info()    { echo "[$(_ts)] [INFO]    $*"; }
log_warning() { echo "[$(_ts)] [WARNING] $*" >&2; }
log_error()   { echo "[$(_ts)] [ERROR]   $*" >&2; }

# -- Resolve project root ------------------------------------------------------
# SCRIPT_DIR is the directory where this script lives (project root).
# Equivalent to: Path(__file__).resolve().parent in Python.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
log_debug "Script directory: ${SCRIPT_DIR}"

# -- Database configuration ----------------------------------------------------
# These values match docker-compose.yml. They are hardcoded here so the script
# can run from cron without depending on an .env file being present.
# If you change docker-compose.yml, update these values too.
POSTGRES_USER="tracker"
POSTGRES_PASSWORD="tracker"
POSTGRES_DB="sensors_temp"
DB_SERVICE="postgres"

# -- Locate the DB container ---------------------------------------------------
# docker compose ps -q <service> returns the container ID of the service.
COMPOSE_FILE="${SCRIPT_DIR}/docker-compose.yml"
CONTAINER_NAME=$(docker compose -f "$COMPOSE_FILE" ps -q "$DB_SERVICE" 2>/dev/null)

if [[ -z "$CONTAINER_NAME" ]]; then
  log_error "The '${DB_SERVICE}' container is not running."
  log_error "Start it with: docker compose up -d"
  exit 1
fi
log_info "Target container: ${CONTAINER_NAME}"

# -- Build output path ---------------------------------------------------------
BACKUP_DIR="${SCRIPT_DIR}/backups"
TIMESTAMP=$(date +"%Y%m%d_%H%M%S")
FILENAME="${POSTGRES_DB}_${TIMESTAMP}.sql.gz"
OUTPUT_PATH="${BACKUP_DIR}/${FILENAME}"
RETENTION_DAYS=30

# mkdir -p -> create directory (and parents) if it doesn't exist;
#            no error if it already exists.  Like os.makedirs(..., exist_ok=True)
mkdir -p "$BACKUP_DIR"
log_info "Starting backup -> ${OUTPUT_PATH}"

# -- Dump & compress -----------------------------------------------------------
# pg_dump flags:
#   -U <user>      -> connect as this user
#   -d <dbname>    -> database to dump
#   --no-owner     -> skip ownership commands (makes restore more portable)
#   --no-privileges -> skip ACL/privilege commands
#   -Fc           -> custom format (compressed, supports parallel restore)
#
# We use plain text format (-Fp, the default) piped through gzip so the
# output is a standard SQL dump that you can inspect with zcat.
#
# PGPASSWORD is set as an env var for the docker exec command so the
# password is not visible in the process list.
#
# Because of set -o pipefail, if pg_dump fails the whole pipeline fails.
docker exec -e PGPASSWORD="${POSTGRES_PASSWORD}" "$CONTAINER_NAME" \
  pg_dump -U"$POSTGRES_USER" -d "$POSTGRES_DB" --no-owner --no-privileges \
  | gzip > "$OUTPUT_PATH"

# -- Validate dump -------------------------------------------------------------
# -s checks the file exists AND has size > 0 (like Path.stat().st_size > 0).
if [[ -s "$OUTPUT_PATH" ]]; then
  FILE_SIZE=$(du -h "$OUTPUT_PATH" | cut -f1)
  log_info "Backup created successfully: ${FILENAME} (${FILE_SIZE})"
else
  log_error "Backup produced an empty file -- removing it."
  rm -f "$OUTPUT_PATH"
  exit 1
fi

# -- Retention cleanup ---------------------------------------------------------
# find ... -mtime +N -delete removes files modified more than N days ago.
# Think of it as: Path.glob("*.sql.gz") filtered by mtime > now - 30 days.
DELETED_COUNT=0
while IFS= read -r old_file; do
  log_debug "Removing expired backup: $(basename "$old_file")"
  DELETED_COUNT=$((DELETED_COUNT + 1))
done < <(find "$BACKUP_DIR" -name "${POSTGRES_DB}_*.sql.gz" -mtime +"$RETENTION_DAYS" -print -delete)

if [[ "$DELETED_COUNT" -gt 0 ]]; then
  log_info "Cleaned ${DELETED_COUNT} backup(s) older than ${RETENTION_DAYS} days"
else
  log_debug "No expired backups to clean"
fi

log_info "Backup job finished"