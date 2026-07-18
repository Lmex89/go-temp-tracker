#!/usr/bin/env fish
# setup-systemd-service.fish -- Install temp-tracker as a systemd service.
#
# Usage:
#   ./setup-systemd-service.fish          # user service (starts on login, no sudo)
#   ./setup-systemd-service.fish --system # system service (starts at boot, uses sudo)
#
# Like a Python script that:
#   - writes a systemd unit file
#   - runs systemctl daemon-reload
#   - enables the service so it starts automatically
#   - starts the service now
#
# In Fish (unlike Bash), variables use $var without braces, and command substitution is (cmd)
# not $(cmd) or `cmd`.

# ============================================================
# Configuration -- like Python module-level constants
# ============================================================

# LOG_LEVEL env var controls verbosity. Like Python's logging.getLogger(__name__).
set -g LOG_LEVEL (string upper (echo $LOG_LEVEL | string trim))
test -z "$LOG_LEVEL"; and set LOG_LEVEL "INFO"

# Numeric log levels (like Python's logging.DEBUG = 10, logging.INFO = 20, etc.)
set -g DEBUG_LEVEL 10
set -g INFO_LEVEL 20
set -g WARN_LEVEL 30
set -g ERROR_LEVEL 40
set -g FATAL_LEVEL 50

set -g CURRENT_LEVEL $INFO_LEVEL
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

# ANSI colors for console output.
set -g COLOR_DEBUG '\033[0;36m'
set -g COLOR_INFO '\033[0;32m'
set -g COLOR_WARN '\033[1;33m'
set -g COLOR_ERROR '\033[0;31m'
set -g COLOR_FATAL '\033[1;31m'
set -g COLOR_RESET '\033[0m'

# Globals populated by parse_args and init_paths.
set -g SYSTEM_MODE 0
set -g SERVICE_NAME "temp-tracker.service"
set -g PROJECT_DIR ""
set -g SERVICE_DIR ""
set -g SERVICE_PATH ""
set -g TARGET ""
set -g USER_LINE ""
set -g PORT 9091
set -g INTERVAL 60

# ============================================================
# Leveled logger -- identical pattern across all *.fish scripts
# ============================================================

# log -- write a timestamped, leveled message to console and log file.
#
# Usage: log INFO "message"
# Logs to setup-systemd-service.log in the current directory.
#
# In Python:
#   def log(level_name, message):
#       if current_level > LEVELS[level_name]: return
#       with open("setup-systemd-service.log", "a") as f:
#           f.write(f"[{ts}] [{level_name}] {message}\n")
#       print(colored(f"[{level_name}] {message}", COLORS[level_name]))
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

    if test $CURRENT_LEVEL -gt $numeric_level
        return
    end

    set -l log_line "[$timestamp] [$level_name] $message"
    echo "$log_line" >> setup-systemd-service.log

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
# Functions
# ============================================================

# run_systemctl -- execute systemctl with the correct user/system flags.
#
# In Fish you cannot store a command with flags in one variable and run it
# because the whole string becomes the command name. Use a function instead.
# In Python:
#   def run_systemctl(*args):
#       if system_mode:
#           subprocess.run(["sudo", "systemctl", *args])
#       else:
#           subprocess.run(["systemctl", "--user", *args])
function run_systemctl
    if test "$SYSTEM_MODE" -eq 1
        log DEBUG "Running: sudo systemctl $argv"
        sudo systemctl $argv
    else
        log DEBUG "Running: systemctl --user $argv"
        systemctl --user $argv
    end
end

# parse_args -- detect --system flag.
#
# In Python: argparse with store_true for --system.
function parse_args
    for arg in $argv
        if test "$arg" = "--system"
            set SYSTEM_MODE 1
            log INFO "System mode enabled (requires sudo, starts at boot)"
            return
        end
    end
    log INFO "User mode (no sudo required, starts on login)"
end

# init_paths -- set service paths based on system/user mode.
#
# Like Python:
#   if system_mode:
#       service_dir = "/etc/systemd/system"
#       target = "multi-user.target"
#   else:
#       service_dir = os.path.expanduser("~/.config/systemd/user")
#       target = "default.target"
function init_paths
    # Resolve project directory -- like os.path.dirname(os.path.abspath(__file__)) in Python.
    set PROJECT_DIR (cd (dirname (status -f)); and pwd)
    log DEBUG "Project directory: $PROJECT_DIR"

    if test "$SYSTEM_MODE" -eq 1
        set SERVICE_DIR "/etc/systemd/system"
        set TARGET "multi-user.target"
        set USER_LINE "User=$USER"
    else
        set SERVICE_DIR "$HOME/.config/systemd/user"
        set TARGET "default.target"
        set USER_LINE ""
    end
    set SERVICE_PATH "$SERVICE_DIR/$SERVICE_NAME"
    log DEBUG "Service path: $SERVICE_PATH"
end

# stop_disable_old -- stop and disable any previous version of the service.
#
# In Python:
#   try:
#       subprocess.run(["systemctl", "--user", "disable", "--now", name])
#   except subprocess.CalledProcessError:
#       pass  # service may not exist yet
function stop_disable_old
    log INFO "Checking for existing service..."

    # In Fish, 'or true' suppresses errors -- like Python's except: pass.
    # The command substitution $status is set by the last command.
    # We use a direct call instead of run_systemctl here because we
    # need is-enabled check + conditional disable in one flow.
    if test "$SYSTEM_MODE" -eq 1
        if sudo systemctl is-enabled --quiet "$SERVICE_NAME" 2>/dev/null
            log WARN "Existing service found, disabling and stopping..."
            sudo systemctl disable --now "$SERVICE_NAME" 2>/dev/null; or true
            log INFO "Existing service disabled"
        else
            log DEBUG "No existing service found"
        end
    else
        if systemctl --user is-enabled --quiet "$SERVICE_NAME" 2>/dev/null
            log WARN "Existing service found, disabling and stopping..."
            systemctl --user disable --now "$SERVICE_NAME" 2>/dev/null; or true
            log INFO "Existing service disabled"
        else
            log DEBUG "No existing service found"
        end
    end
end

# build_unit_file -- assemble the systemd unit file content.
#
# IMPORTANT: This function prints to stdout so callers capture its output
# with (build_unit_file). Do NOT call log() here -- log() prints to stdout
# and would be captured into the unit file, corrupting it.
#
# Like Python writing to a text file:
#   lines = [
#       "[Unit]",
#       "Description=Sensor Temperature Tracker",
#       ...
#   ]
#   with open(path, "w") as f:
#       f.write("\n".join(lines))
function build_unit_file
    set -l lines
    set -a lines "[Unit]"
    set -a lines "Description=Sensor Temperature Tracker"
    set -a lines "After=network.target"
    set -a lines ""
    set -a lines "[Service]"
    set -a lines "Type=simple"
    set -a lines "WorkingDirectory=$PROJECT_DIR"
    if test -n "$USER_LINE"
        set -a lines "$USER_LINE"
    end
    set -a lines "Environment=CPU_POLL_INTERVAL=$INTERVAL"
    set -a lines "Environment=MEMORY_POLL_INTERVAL=$INTERVAL"
    set -a lines "Environment=SWAP_POLL_INTERVAL=$INTERVAL"
    set -a lines "Environment=DISK_POLL_INTERVAL=$INTERVAL"
    set -a lines "Environment=LOAD_POLL_INTERVAL=$INTERVAL"
    set -a lines "ExecStart=$PROJECT_DIR/temp-tracker -port $PORT -interval $INTERVAL"
    set -a lines "Restart=on-failure"
    set -a lines "RestartSec=5"
    set -a lines ""
    set -a lines "[Install]"
    set -a lines "WantedBy=$TARGET"

    printf "%s\n" $lines
end

# write_unit_file -- write the unit file to disk.
#
# system mode: uses sudo tee to write to /etc/systemd/system.
# user mode:   write directly to ~/.config/systemd/user/.
function write_unit_file
    log INFO "Building service unit file..."
    log INFO "Writing unit file to $SERVICE_PATH..."

    # Ensure the systemd directory exists.
    # For system mode this already exists; for user mode we create it.
    if test "$SYSTEM_MODE" -eq 0
        mkdir -p "$SERVICE_DIR"
    end

    # Capture unit file content from build_unit_file.
    # In Fish, command substitution (cmd) captures stdout -- like Python's subprocess.check_output().
    set -l content (build_unit_file)

    if test "$SYSTEM_MODE" -eq 1
        printf "%s\n" $content | sudo tee "$SERVICE_PATH" > /dev/null
    else
        printf "%s\n" $content > "$SERVICE_PATH"
    end

    log INFO "Unit file written successfully"
end

# reload_daemon -- tell systemd to re-read unit files.
function reload_daemon
    log INFO "Reloading systemd daemon..."
    run_systemctl daemon-reload
    log INFO "Daemon reloaded"
end

# enable_service -- enable the service so it starts automatically on boot/login.
function enable_service
    log INFO "Enabling $SERVICE_NAME (auto-start on $TARGET)..."
    run_systemctl enable "$SERVICE_NAME"
    log INFO "Service enabled"
end

# kill_leftover_processes -- stop any leftover temp-tracker on the configured port.
# This prevents "address already in use" when starting the service.
function kill_leftover_processes
    log INFO "Checking for leftover temp-tracker processes..."

    set -l pids (pgrep -f "$PROJECT_DIR/temp-tracker" 2>/dev/null; or echo "")
    if test -z "$pids"
        log DEBUG "No leftover processes found"
        return
    end

    for pid in $pids
        log WARN "Stopping leftover temp-tracker PID $pid..."
        if test "$SYSTEM_MODE" -eq 1
            sudo kill -15 "$pid" 2>/dev/null; or true
        else
            kill -15 "$pid" 2>/dev/null; or true
        end
        sleep 0.5
        if kill -0 "$pid" 2>/dev/null
            if test "$SYSTEM_MODE" -eq 1
                sudo kill -9 "$pid" 2>/dev/null; or true
            else
                kill -9 "$pid" 2>/dev/null; or true
            end
        end
    end
    sleep 1
    log INFO "Leftover processes cleared"
end

# start_service -- start the service now.
function start_service
    log INFO "Starting $SERVICE_NAME..."
    run_systemctl start "$SERVICE_NAME"
    log INFO "Service started"
end

# show_status -- display the final state of the service.
function show_status
    log INFO "Final status:"
    run_systemctl status "$SERVICE_NAME" --no-pager
end

# print_summary -- show useful commands and dashboard URL.
function print_summary
    echo ""
    log INFO "Service installed and started"
    log INFO "Dashboard: http://localhost:$PORT"
    echo ""

    echo "Useful commands:"
    if test "$SYSTEM_MODE" -eq 1
        echo "  sudo systemctl status $SERVICE_NAME"
        echo "  sudo systemctl stop $SERVICE_NAME"
        echo "  sudo systemctl restart $SERVICE_NAME"
        echo "  sudo journalctl -u $SERVICE_NAME -f"
    else
        echo "  systemctl --user status $SERVICE_NAME"
        echo "  systemctl --user stop $SERVICE_NAME"
        echo "  systemctl --user restart $SERVICE_NAME"
        echo "  journalctl --user -u $SERVICE_NAME -f"
    end
end

# dispatch -- run installation steps in order.
#
# In Python:
#   def dispatch():
#       parse_args()
#       init_paths()
#       stop_disable_old()
#       write_unit_file()
#       ...
function dispatch
    log INFO "========================================"
    log INFO "Starting setup-systemd-service"
    log INFO "LOG_LEVEL=$LOG_LEVEL"
    log INFO "========================================"

    init_paths
    stop_disable_old
    write_unit_file
    reload_daemon
    enable_service
    kill_leftover_processes
    start_service
    print_summary
    show_status
end

# ============================================================
# Main entry point -- like Python's if __name__ == "__main__": main()
# ============================================================
parse_args $argv
dispatch
