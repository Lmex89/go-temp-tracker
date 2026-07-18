#!/usr/bin/env fish
# view-logs.fish -- Show temp-tracker service logs.
#
# Usage:
#   ./view-logs.fish          # user service logs (last 50 lines)
#   ./view-logs.fish -f       # user service logs (follow)
#   ./view-logs.fish --system # system service logs (last 50 lines)
#   ./view-logs.fish --system -f # system service logs (follow)
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

# Globals populated by parse_args.
set -g SERVICE_NAME "temp-tracker.service"
set -g SYSTEM_MODE 0
set -g FOLLOW 0

# ============================================================
# Leveled logger -- identical pattern across all *.fish scripts
# ============================================================

# log -- write a timestamped, leveled message to console and log file.
#
# Usage: log INFO "message"
# Logs to view-logs.log in the current directory.
#
# In Python:
#   def log(level_name, message):
#       if current_level > LEVELS[level_name]: return
#       with open("view-logs.log", "a") as f:
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
    echo "$log_line" >> view-logs.log

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

# parse_args -- parse command-line arguments (--system, -f/--follow).
#
# In Python this would be argparse:
#   parser.add_argument("--system", action="store_true")
#   parser.add_argument("-f", "--follow", action="store_true")
#   args = parser.parse_args()
function parse_args
    for arg in $argv
        switch "$arg"
            case "--system"
                set SYSTEM_MODE 1
                log DEBUG "System mode enabled"
            case "-f" "--follow"
                set FOLLOW 1
                log DEBUG "Follow mode enabled"
            case "*"
                # In Fish, $argv[0] is invalid (arrays start at 1).
                # Use (status filename) to get the script path.
                log ERROR "Unknown option: $arg"
                echo "Usage: "(status filename)" [--system] [-f|--follow]"
                exit 1
        end
    end
end

# show_logs -- display or follow systemd journal logs for the service.
#
# In Python:
#   import subprocess
#   if system_mode:
#       cmd = ["sudo", "journalctl", "-u", SERVICE_NAME]
#   else:
#       cmd = ["journalctl", "--user", "-u", SERVICE_NAME]
#   if follow:
#       cmd.append("-f")
#   else:
#       cmd.extend(["-n", "50", "--no-pager"])
#   subprocess.run(cmd)
function show_logs
    if test "$FOLLOW" -eq 1
        if test "$SYSTEM_MODE" -eq 1
            log INFO "Following system service logs (Ctrl+C to exit)..."
            # journalctl directly, not through a variable -- this avoids the
            # "command + flags in one variable" trap that Fish cannot handle.
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

# dispatch -- route to the appropriate log viewer based on parsed args.
#
# In Python:
#   def dispatch():
#       logger.info("view-logs started")
#       show_logs()
function dispatch
    log INFO "view-logs started (system_mode=$SYSTEM_MODE, follow=$FOLLOW)"
    show_logs
end

# ============================================================
# Main entry point -- like Python's if __name__ == "__main__": main()
# ============================================================
parse_args $argv
dispatch
