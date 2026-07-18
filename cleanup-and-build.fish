#!/usr/bin/env fish
# cleanup-and-build.fish -- Clean up temp-tracker binary, kill running instances, and rebuild.
#
# Like a Python script that:
#   - Uses logging module with timestamps and levels
#   - os.remove('temp-tracker') to delete the binary
#   - subprocess.run(['pgrep', '-f', 'temp-tracker']) to find PIDs
#   - os.kill(pid, signal.SIGTERM) to stop processes
#   - subprocess.run(['go', 'build', ...]) to recompile
#
# In Fish (unlike Bash), variables use $var without braces, and command substitution is (cmd)
# not $(cmd) or `cmd`.

# ============================================================
# Configuration -- like Python module-level constants
# ============================================================

# LOG_LEVEL env var controls verbosity. Like Python's logging.getLogger(__name__).
set -g LOG_LEVEL (string upper (echo $LOG_LEVEL | string trim))
set -g LOG_FILE "cleanup-and-build.log"
# Shared poll interval default (seconds).
# Like a Python module-level constant: DEFAULT_INTERVAL = 60.
set -g DEFAULT_INTERVAL 60
test -z "$LOG_LEVEL"; and set LOG_LEVEL "INFO"

# Numeric log levels (like Python's logging.DEBUG = 10, logging.INFO = 20, etc.)
# Must be global so the log() function can access them.
set -g DEBUG_LEVEL 10
set -g INFO_LEVEL 20
set -g WARN_LEVEL 30
set -g ERROR_LEVEL 40
set -g FATAL_LEVEL 50

# Map LOG_LEVEL string to numeric value.
# In Python: level = logging.getLevelName(log_level).
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

# ANSI colors for console output (like Python's colorama or rich library).
# Must be global so the log() function can use them.
set -g COLOR_DEBUG '\033[0;36m'  # Cyan
set -g COLOR_INFO '\033[0;32m'   # Green
set -g COLOR_WARN '\033[1;33m'   # Yellow
set -g COLOR_ERROR '\033[0;31m'  # Red
set -g COLOR_FATAL '\033[1;31m'  # Bold Red
set -g COLOR_RESET '\033[0m'     # No Color

# ============================================================
# Leveled logger -- identical pattern across all *.fish scripts
# ============================================================

# log -- write a timestamped, leveled message to console and log file.
#
# Usage: log INFO "message"
#
# In Python:
#   def log(level_name, message):
#       if current_level > LEVELS[level_name]: return
#       with open("cleanup-and-build.log", "a") as f:
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

    # Skip messages below the configured level.
    # Like Python's if logger.level > record.levelno: return.
    if test $CURRENT_LEVEL -gt $numeric_level
        return
    end

    set -l log_line "[$timestamp] [$level_name] $message"

    # Append to log file (plain text, no colors).
    # Like Python's with open('cleanup-and-build.log', 'a') as f: f.write(...).
    echo "$log_line" >> $LOG_FILE

    # Print to console with ANSI colors.
    # In Fish, printf %b interprets backslash escapes (like \033 for colors).
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

# remove_binary -- delete the old temp-tracker binary if it exists.
#
# In Python:
#   import os, pathlib
#   if pathlib.Path("temp-tracker").exists():
#       os.remove("temp-tracker")
function remove_binary
    log INFO "Step 1: Removing binary"
    # test -f is like Python's os.path.isfile() or pathlib.Path.exists().
    if test -f ./temp-tracker
        rm ./temp-tracker
        log INFO "Binary removed successfully"
    else
        log WARN "No binary found to remove"
    end
end

# kill_processes -- find and gracefully stop any running temp-tracker processes.
#
# In Python:
#   import psutil
#   for proc in psutil.process_iter(["pid", "cmdline"]):
#       if "temp-tracker" in " ".join(proc.info["cmdline"] or []):
#           proc.terminate()
#           proc.wait(timeout=0.5)
function kill_processes
    log INFO "Step 2: Checking for running processes"
    # pgrep -f matches the full command line -- like Python's psutil.process_iter()
    # checking cmdline(). Without -f, pgrep only matches the process name.
    set -l pids (pgrep -f "temp-tracker")

    # In Fish, if test -n "$pids" is like Python's if pids: (check not empty).
    # Unlike Python, an unquoted variable in Fish expands to multiple words
    # when it contains spaces. Quoting "$var" keeps it as one word.
    if test -n "$pids"
        log INFO "Found running temp-tracker process(es): $pids"

        # Iterate over each PID -- like Python's for pid in pid_list:.
        for pid in $pids
            log INFO "Killing PID $pid"
            # kill -15 = SIGTERM (graceful shutdown) -- like Python's os.kill(pid, signal.SIGTERM).
            # -9 would be SIGKILL (force terminate, like process.kill()).
            kill -15 $pid 2>/dev/null
            set -l kill_status $status
            log DEBUG "Sent SIGTERM to PID $pid (exit status: $kill_status)"

            # Wait a moment for graceful shutdown -- like Python's time.sleep(0.5).
            # Fish's sleep accepts decimals (unlike some shells).
            sleep 0.5

            # kill -0 tests existence without sending a real signal.
            # In Python: try os.kill(pid, 0) except ProcessLookupError: ...
            if kill -0 $pid 2>/dev/null
                log WARN "PID $pid still running after SIGTERM, sending SIGKILL"
                kill -9 $pid 2>/dev/null
                set -l force_status $status
                log DEBUG "Sent SIGKILL to PID $pid (exit status: $force_status)"

                sleep 0.5
                if kill -0 $pid 2>/dev/null
                    log ERROR "Failed to kill PID $pid even with SIGKILL"
                else
                    log INFO "PID $pid force-stopped"
                end
            else
                log INFO "PID $pid stopped gracefully"
            end
        end
    else
        log INFO "No running temp-tracker processes found"
    end
end

# build_and_start -- rebuild the binary and launch the server in the background.
#
# In Python:
#   result = subprocess.run(["go", "build", "-o", "temp-tracker", "."])
#   if result.returncode == 0:
#       subprocess.Popen(["./temp-tracker", "-port", "9091", ...],
#                        stdout=open("tracker.log", "w"),
#                        start_new_session=True)
function build_and_start
    log INFO "Step 3: Building binary"

    # go build -- like Python's subprocess.run(['go', 'build', '-o', 'temp-tracker', '.']).
    # Check exit status with $status -- like subprocess.run().returncode.
    go build -o temp-tracker .
    set -l build_status $status

    if test $build_status -eq 0
        log INFO "Build successful"

        # Start the server in the background.
        # nohup keeps it running after this script exits (like subprocess.Popen
        # with start_new_session=True in Python).
        log INFO "Starting temp-tracker in background on port 9091"
        nohup env CPU_POLL_INTERVAL=$DEFAULT_INTERVAL MEMORY_POLL_INTERVAL=$DEFAULT_INTERVAL SWAP_POLL_INTERVAL=$DEFAULT_INTERVAL DISK_POLL_INTERVAL=$DEFAULT_INTERVAL LOAD_POLL_INTERVAL=$DEFAULT_INTERVAL ./temp-tracker -port 9091 -interval $DEFAULT_INTERVAL > tracker.log 2>&1 &
        log INFO "Server PID: $last_pid"
        log INFO "Dashboard: http://localhost:9091"
    else
        log ERROR "Build failed with exit status $build_status"
        log FATAL "Aborting"
        exit 1  # Like Python's sys.exit(1) -- return error code to shell.
    end
end

# dispatch -- run all steps in sequence.
#
# Each step is a named function. This reads like a high-level plan:
#   1. remove the binary, 2. kill running processes, 3. rebuild and start.
# In Python:
#   def dispatch():
#       remove_binary()
#       kill_processes()
#       build_and_start()
function dispatch
    log INFO "========================================"
    log INFO "Starting cleanup-and-build"
    log INFO "LOG_LEVEL=$LOG_LEVEL"
    log INFO "========================================"

    remove_binary
    kill_processes
    build_and_start

    log INFO "========================================"
    log INFO "Cleanup, rebuild, and start complete"
    log INFO "========================================"
end

# ============================================================
# Main entry point -- like Python's if __name__ == "__main__": main()
# ============================================================
dispatch
