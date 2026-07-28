#!/usr/bin/env fish
# backup-db.fish -- PostgreSQL backup via Docker container dump
#
# WHAT IT DOES
#   1. Reads DB credentials from docker-compose.yml (service: postgres).
#   2. Locates the running postgres container through docker compose.
#   3. Runs pg_dump inside the container and pipes the output through gzip.
#   4. Saves the compressed dump to ./backups/<db>_<YYYYMMDD_HHMMSS>.sql.gz
#   5. Deletes backups older than RETENTION_DAYS (default 30).
#
# USAGE
#   ./backup-db.fish
#
# CRON EXAMPLE  (daily at 02:00, logs to backups/cron.log)
#   0 2 * * * /full/path/backup-db.fish >> /full/path/backups/cron.log 2>&1
#
# DEPENDENCIES
#   docker, docker compose, gzip, find
#
# In Fish (unlike Bash), variables use $var without braces, and command substitution is (cmd).
# In Fish (unlike Python), there is no return statement; functions return exit status ($status).
# Global variables need explicit -g flag because Fish defaults to local scope.

# ============================================================
# Configuration -- like Python module-level constants
# ============================================================

# Logging configuration -- like Python's logging.basicConfig(level=logging.INFO).
set -g LOG_LEVEL (string upper (echo $LOG_LEVEL | string trim))
test -z "$LOG_LEVEL"; and set LOG_LEVEL "INFO"

# Numeric log levels -- like Python's logging.DEBUG = 10, logging.INFO = 20, etc.
set -g DEBUG_LEVEL 10
set -g INFO_LEVEL 20
set -g WARN_LEVEL 30
set -g ERROR_LEVEL 40
set -g FATAL_LEVEL 50
set -g CURRENT_LEVEL $INFO_LEVEL

# Convert LOG_LEVEL string to numeric value.
# In Python you'd use logging.getLevelName(level_name).
switch "$LOG_LEVEL"
    case "DEBUG"
        set CURRENT_LEVEL $DEBUG_LEVEL
    case "INFO"
        set CURRENT_LEVEL $INFO_LEVEL
    case "WARN"
        set CURRENT_LEVEL $WARN_LEVEL
    case "ERROR"
        set CURRENT_LEVEL $ERROR_LEVEL
    case "FATAL"
        set CURRENT_LEVEL $FATAL_LEVEL
end

# Log level -> ANSI color mapping.
# In Python: a dict like level_colors = {"INFO": "green", ...}.
set -g COLOR_DEBUG '\033[0;36m'  # Cyan
set -g COLOR_INFO '\033[0;32m'   # Green
set -g COLOR_WARN '\033[1;33m'   # Yellow
set -g COLOR_ERROR '\033[0;31m'  # Red
set -g COLOR_FATAL '\033[1;31m'  # Bold Red
set -g COLOR_RESET '\033[0m'     # No Color

# Database configuration.
# These values match docker-compose.yml. They are hardcoded here so the script
# can run from cron without depending on an .env file being present.
# If you change docker-compose.yml, update these values too.
set -g POSTGRES_USER "tracker"
set -g POSTGRES_PASSWORD "tracker"
set -g POSTGRES_DB "sensors_temp"
set -g DB_SERVICE "postgres"
set -g RETENTION_DAYS 30

# ============================================================
# Leveled logger -- identical pattern across all *.fish scripts
# ============================================================

# log -- write a timestamped, leveled message to console and log file.
#
# Usage: log INFO "message"
#
# In Python:
#   def log(level_name, message):
#       if current_level > LEVELS[level_name]:
#           return
#       line = f"[{timestamp}] [{level_name}] {message}"
#       with open("backup-db.log", "a") as f:
#           f.write(line + "\n")
#       print(colored(line, COLORS[level_name]))
function log
    set -l level_name (string upper $argv[1])
    set -l message $argv[2]
    set -l timestamp (date '+%Y-%m-%d %H:%M:%S')
    set -l numeric_level $INFO_LEVEL

    switch "$level_name"
        case "DEBUG"
            set numeric_level $DEBUG_LEVEL
        case "INFO"
            set numeric_level $INFO_LEVEL
        case "WARN"
            set numeric_level $WARN_LEVEL
        case "ERROR"
            set numeric_level $ERROR_LEVEL
        case "FATAL"
            set numeric_level $FATAL_LEVEL
    end

    # Skip messages below the configured level.
    # Like Python's if logger.level > record.levelno: return
    if test $CURRENT_LEVEL -gt $numeric_level
        return
    end

    set -l log_line "[$timestamp] [$level_name] $message"

    # Append to log file (plain text, no colors).
    # Like Python's with open('backup-db.log', 'a') as f: f.write(...)
    echo "$log_line" >> backup-db.log

    # Print to console with ANSI colors.
    # printf %b interprets backslash escapes (like \033 for colors).
    set -l color
    switch "$level_name"
        case "DEBUG"
            set color $COLOR_DEBUG
        case "INFO"
            set color $COLOR_INFO
        case "WARN"
            set color $COLOR_WARN
        case "ERROR"
            set color $COLOR_ERROR
        case "FATAL"
            set color $COLOR_FATAL
    end

    printf "%b[%s]%b %s\n" "$color" "$level_name" "$COLOR_RESET" "$message"
end

# ============================================================
# Functions (named instead of a monolithic script -- like Python modules)
# ============================================================

# resolve_project_root -- determine the directory where this script lives.
#
# In Python:
#   SCRIPT_DIR = Path(__file__).resolve().parent
function resolve_project_root
    # status filename returns the path of the currently running script
    # (like __file__ in Python). dirname strips the filename, and realpath -s
    # returns the canonical directory.
    set -l script_path (status filename)
    set -l script_dir (dirname "$script_path")
    realpath -s "$script_dir"
end

# locate_container -- find the running postgres container ID.
# Sets the global CONTAINER_ID variable (like a Python function setting a module-level global).
#
# We use a global variable instead of echo because the log function writes to stdout.
# If we used echo to return the container ID, the log output would be captured too
# when called as: set -l id (locate_container ...).
#
# In Python:
#   def locate_container(script_dir):
#       global container_id
#       result = subprocess.run(
#           ["docker", "compose", "-f", "docker-compose.yml", "ps", "-q", "postgres"],
#           capture_output=True, text=True
#       )
#       container_id = result.stdout.strip()
function locate_container
    set -l script_dir $argv[1]
    set -l compose_file "$script_dir/docker-compose.yml"

    # docker compose ps -q <service> returns the container ID.
    # We capture stdout with command substitution, like subprocess.check_output().
    set -l container_name (docker compose -f "$compose_file" ps -q "$DB_SERVICE" 2>/dev/null)

    if test -z "$container_name"
        log ERROR "The '$DB_SERVICE' container is not running."
        log ERROR "Start it with: docker compose up -d"
        return 1
    end

    # Set the global variable so the caller can read it without command substitution.
    # In Python: global container_id; container_id = name
    set -g CONTAINER_ID "$container_name"
    log INFO "Target container: $container_name"
end

# create_backup -- run pg_dump inside the container and compress the output.
#
# In Python:
#   with open(output_path, "wb") as f:
#       dump = subprocess.Popen(["docker", "exec", "-e", f"PGPASSWORD={...}", ...],
#                               stdout=subprocess.PIPE)
#       subprocess.run(["gzip"], stdin=dump.stdout, stdout=f)
function create_backup
    set -l script_dir $argv[1]
    set -l container_name $argv[2]
    set -l output_path $argv[3]

    mkdir -p "$script_dir/backups"
    log INFO "Starting backup -> $output_path"

    # pg_dump flags:
    #   -U <user>      -> connect as this user
    #   -d <dbname>    -> database to dump
    #   --no-owner     -> skip ownership commands (makes restore more portable)
    #   --no-privileges -> skip ACL/privilege commands
    #
    # We use plain text format (-Fp, the default) piped through gzip so the
    # output is a standard SQL dump that you can inspect with zcat.
    #
    # PGPASSWORD is set as an env var for the docker exec command so the
    # password is not visible in the process list.
    #
    # In Fish, never store a command with flags in a variable and run it.
    # We run the pipeline directly so each word is expanded properly.
    #
    # Fish has no "set -o pipefail" like Bash. After a pipeline, $status holds
    # only the last command's exit code. $pipestatus is a list of ALL stages'
    # exit codes (like Python's [p.returncode for p in processes]).
    # We must check every element so a silent pg_dump failure is not missed.
    docker exec -e PGPASSWORD="$POSTGRES_PASSWORD" "$container_name" \
        pg_dump -U"$POSTGRES_USER" -d "$POSTGRES_DB" --no-owner --no-privileges \
        | gzip > "$output_path"

    set -l pipe_exit $pipestatus
    for code in $pipe_exit
        if test "$code" -ne 0
            log ERROR "Pipeline stage failed with exit code $code (pipestatus: $pipe_exit)"
            return 1
        end
    end
end

# validate_backup -- ensure the dump file exists and is non-empty.
#
# In Python:
#   if output_path.stat().st_size > 0:
#       print("Backup created successfully")
#   else:
#       output_path.unlink()
#       sys.exit(1)
function validate_backup
    set -l output_path $argv[1]
    set -l filename $argv[2]

    # test -s checks the file exists AND has size > 0
    # (like Path.stat().st_size > 0 in Python).
    if test -s "$output_path"
        set -l file_size (du -h "$output_path" | cut -f1)
        log INFO "Backup created successfully: $filename ($file_size)"
    else
        log ERROR "Backup produced an empty file -- removing it."
        rm -f "$output_path"
        return 1
    end
end

# cleanup_old_backups -- remove backups older than RETENTION_DAYS.
#
# In Python:
#   for old_file in backup_dir.glob(f"{db}_*.sql.gz"):
#       if (now - old_file.stat().st_mtime).days > retention_days:
#           old_file.unlink()
function cleanup_old_backups
    set -l backup_dir $argv[1]
    set -l deleted_count 0

    # find ... -mtime +N -delete removes files modified more than N days ago.
    # We iterate over the deleted files to count them.
    #
    # The -name pattern must be fully quoted so Fish does not try to glob-expand it
    # before passing to find. We concatenate the quoted variable with a quoted literal.
    # In Python: f"{db}_*.sql.gz" passed as a single string argument.
    set -l pattern "$POSTGRES_DB""_*.sql.gz"
    for old_file in (find "$backup_dir" -name "$pattern" -mtime +"$RETENTION_DAYS" -print -delete 2>/dev/null)
        log DEBUG "Removing expired backup: "(basename "$old_file")
        set deleted_count (math $deleted_count + 1)
    end

    if test "$deleted_count" -gt 0
        log INFO "Cleaned $deleted_count backup(s) older than $RETENTION_DAYS days"
    else
        log DEBUG "No expired backups to clean"
    end
end

# build_output_path -- construct the backup file path from the current timestamp.
#
# In Python:
#   timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
#   output_path = backup_dir / f"{db}_{timestamp}.sql.gz"
function build_output_path
    set -l backup_dir $argv[1]
    set -l timestamp (date +"%Y%m%d_%H%M%S")
    set -l filename "$POSTGRES_DB"_"$timestamp".sql.gz
    echo "$backup_dir/$filename"
end

# dispatch -- run all backup steps in sequence.
#
# In Python:
#   def dispatch():
#       script_dir = resolve_project_root()
#       container = locate_container(script_dir)
#       output_path = build_output_path(script_dir / "backups")
#       create_backup(script_dir, container, output_path)
#       validate_backup(output_path)
#       cleanup_old_backups(script_dir / "backups")
function dispatch
    log INFO "========================================"
    log INFO "Starting PostgreSQL backup job"
    log INFO "LOG_LEVEL=$LOG_LEVEL"
    log INFO "========================================"

    set -l script_dir (resolve_project_root)
    log DEBUG "Script directory: $script_dir"

    # locate_container sets the global CONTAINER_ID (it cannot return via echo
    # because log writes to stdout). Like Python calling a function that sets a global.
    locate_container "$script_dir"
    if test $status -ne 0
        return 1
    end

    set -l backup_dir "$script_dir/backups"
    set -l output_path (build_output_path "$backup_dir")
    set -l filename (basename "$output_path")

    create_backup "$script_dir" "$CONTAINER_ID" "$output_path"
    if test $status -ne 0
        log ERROR "pg_dump/gzip pipeline failed"
        return 1
    end

    validate_backup "$output_path" "$filename"
    if test $status -ne 0
        return 1
    end

    cleanup_old_backups "$backup_dir"

    log INFO "Backup job finished"
end

# ============================================================
# Main entry point -- like Python's if __name__ == "__main__": main()
# ============================================================
dispatch
