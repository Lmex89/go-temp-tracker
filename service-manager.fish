#!/usr/bin/env fish
# service-manager.fish -- Manage the temp-tracker systemd service.
#
# Usage:
#   ./service-manager.fish status              # user service status
#   ./service-manager.fish start               # user service start
#   ./service-manager.fish stop                # user service stop
#   ./service-manager.fish restart             # user service restart
#   ./service-manager.fish logs                # user service logs (last 50 lines)
#   ./service-manager.fish logs -f             # user service logs (follow)
#
# Add --system anywhere for system service (requires sudo):
#   ./service-manager.fish --system status
#   ./service-manager.fish --system stop
#   ./service-manager.fish --system restart
#   ./service-manager.fish --system logs -f
#
# In Fish (unlike Bash), variables use $var without braces, and command substitution is (cmd).
# In Fish (unlike Python), there is no return statement; functions return exit status ($status).
# Global variables need explicit -g flag because Fish defaults to local scope.

# SCRIPT_NAME stores the original program name for usage messages.
# In Python: sys.argv[0]. Fish arrays start at 1, so $argv[0] is invalid.
# Use status filename instead -- it returns the path of the currently running script.
set -g SCRIPT_NAME (status filename)

# Service configuration -- constants at the top like Python module-level globals.
set -g SERVICE_NAME "temp-tracker.service"
set -g SYSTEM_MODE 0
set -g COMMAND ""
set -g FOLLOW 0

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

# log -- write a timestamped, leveled message to console and log file.
#
# Usage: log INFO "message"
#
# In Python this would be:
#   def log(level_name, message):
#       if current_level > LEVELS[level_name]:
#           return
#       line = f"[{timestamp}] [{level_name}] {message}"
#       with open("service-manager.log", "a") as f:
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
    # Like Python's with open('service-manager.log', 'a') as f: f.write(...)
    echo "$log_line" >> service-manager.log

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

# show_usage -- print usage instructions.
#
# In Python:
#   def show_usage():
#       print("Usage: ...", file=sys.stderr)
function show_usage
    echo "Usage: $SCRIPT_NAME [--system] [status|start|stop|restart|logs] [-f]"
    echo ""
    echo "Examples:"
    echo "  $SCRIPT_NAME status"
    echo "  $SCRIPT_NAME stop"
    echo "  $SCRIPT_NAME restart"
    echo "  $SCRIPT_NAME logs"
    echo "  $SCRIPT_NAME logs -f"
    echo "  $SCRIPT_NAME --system status"
end

# parse_args -- parse command-line arguments.
#
# In Python this would be argparse:
#   parser = argparse.ArgumentParser()
#   parser.add_argument("--system", action="store_true")
#   parser.add_argument("command", choices=["status","start","stop","restart","logs"])
#   parser.add_argument("-f", "--follow", action="store_true")
#   args = parser.parse_args()
#
# Fish uses a for loop over $argv and a switch statement instead.
function parse_args
    for arg in $argv
        switch "$arg"
            case "--system"
                set SYSTEM_MODE 1
                log DEBUG "System mode enabled (will use sudo)"
            case "status" "start" "stop" "restart" "logs"
                if test -z "$COMMAND"
                    set COMMAND "$arg"
                    log DEBUG "Command set to: $arg"
                else
                    log ERROR "only one command allowed (got $COMMAND and $arg)"
                    show_usage
                    exit 1
                end
            case "-f" "--follow"
                set FOLLOW 1
                log DEBUG "Follow mode enabled"
            case "*"
                log ERROR "Unknown option/command: $arg"
                show_usage
                exit 1
        end
    end
end

# run_systemctl -- execute systemctl with the correct user/system flags.
#
# In Fish you cannot store a command with flags in one variable and run it
# because the whole string becomes the command name. We use a function
# instead, like a Python wrapper:
#
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

# show_status -- display the current state of the service.
function show_status
    log INFO "Showing status for $SERVICE_NAME"
    run_systemctl status "$SERVICE_NAME" --no-pager
end

# start_service -- start the service and confirm it is running.
function start_service
    log INFO "Starting $SERVICE_NAME..."
    run_systemctl start "$SERVICE_NAME"
    log INFO "Service started; showing status..."
    run_systemctl status "$SERVICE_NAME" --no-pager
end

# stop_service -- stop the service gracefully.
function stop_service
    log INFO "Stopping $SERVICE_NAME..."
    run_systemctl stop "$SERVICE_NAME"
    log INFO "Stopped."
end

# restart_service -- restart the service and confirm it is running.
function restart_service
    log INFO "Restarting $SERVICE_NAME..."
    run_systemctl restart "$SERVICE_NAME"
    log INFO "Service restarted; showing status..."
    run_systemctl status "$SERVICE_NAME" --no-pager
end

# show_logs -- display or follow systemd journal logs for the service.
function show_logs
    if test "$FOLLOW" -eq 1
        if test "$SYSTEM_MODE" -eq 1
            log INFO "Following system service logs (Ctrl+C to exit)..."
            sudo journalctl -u "$SERVICE_NAME" -f
        else
            log INFO "Following user service logs (Ctrl+C to exit)..."
            journalctl --user -u "$SERVICE_NAME" -f
        end
    else
        if test "$SYSTEM_MODE" -eq 1
            log INFO "Last 50 lines of system service logs:"
            sudo journalctl -u "$SERVICE_NAME" -n 50 --no-pager
        else
            log INFO "Last 50 lines of user service logs:"
            journalctl --user -u "$SERVICE_NAME" -n 50 --no-pager
        end
    end
end

# dispatch -- route the parsed command to the appropriate handler.
#
# In Python this would be a dict of functions:
#   commands = {
#       "status": show_status,
#       "start": start_service,
#       "stop": stop_service,
#       "restart": restart_service,
#       "logs": show_logs,
#   }
#   commands[command]()
function dispatch
    switch "$COMMAND"
        case "status"
            show_status
        case "start"
            start_service
        case "stop"
            stop_service
        case "restart"
            restart_service
        case "logs"
            show_logs
        case "*"
            log ERROR "Internal error: unknown command '$COMMAND'"
            show_usage
            exit 1
    end
end

# Main entry point -- like Python's if __name__ == "__main__": main().
parse_args $argv

if test -z "$COMMAND"
    log ERROR "No command provided"
    show_usage
    exit 1
end

log INFO "service-manager started (command=$COMMAND, system_mode=$SYSTEM_MODE, follow=$FOLLOW)"
dispatch
