#!/usr/bin/env fish
# cleanup-and-build.fish — Clean up temp-tracker binary, kill running instances, and rebuild
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

# Set defaults
set -l LOG_LEVEL (string upper (echo $LOG_LEVEL | string trim))
set -l LOG_FILE "cleanup-and-build.log"
test -z "$LOG_LEVEL"; and set LOG_LEVEL "INFO"

# Log level numeric values (like Python's logging.DEBUG = 10, logging.INFO = 20, etc.)
set -l DEBUG_LEVEL 10
set -l INFO_LEVEL 20
set -l WARN_LEVEL 30
set -l ERROR_LEVEL 40
set -l FATAL_LEVEL 50

# Map level name to numeric value
set -l CURRENT_LEVEL $INFO_LEVEL
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

# Colors for console output (like Python's colorama or rich library)
set -l COLOR_DEBUG '\033[0;36m'  # Cyan
set -l COLOR_INFO '\033[0;32m'   # Green
set -l COLOR_WARN '\033[1;33m'   # Yellow
set -l COLOR_ERROR '\033[0;31m'  # Red
set -l COLOR_FATAL '\033[1;31m'  # Bold Red
set -l COLOR_RESET '\033[0m'     # No Color

# Function to log messages with timestamp and level
# Like Python's logging.debug(), logging.info(), etc.
# Usage: log INFO "message"
# Writes to both console and log file
function log
    set -l level_name (string upper $argv[1])
    set -l message $argv[2]
    set -l timestamp (date '+%Y-%m-%d %H:%M:%S')
    set -l numeric_level $INFO_LEVEL
    
    # Convert level name to numeric value
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
    
    # Skip if current level is higher than message level
    # Like Python's if logger.level > level: return
    if test $CURRENT_LEVEL -gt $numeric_level
        return
    end
    
    # Format for log file (plain text, no colors)
    set -l log_line "[$timestamp] [$level_name] $message"
    
    # Write to log file (append mode >>)
    # Like Python's with open('log.txt', 'a') as f: f.write(...)
    echo "$log_line" >> $LOG_FILE
    
    # Format for console (with colors)
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
    
    # Print to console
    # In Fish, we use printf instead of echo -e for better control over formatting
    # $variable interpolation inside quotes works differently than Bash
    printf "%s[%s]%s %s\n" "$color" "$level_name" "$COLOR_RESET" "$message"
end

# Log script start
log INFO "========================================"
log INFO "Starting cleanup-and-build"
log INFO "LOG_LEVEL=$LOG_LEVEL"
log INFO "========================================"

# Step 1: Remove the binary if it exists
# test -f is like Python's os.path.isfile() or pathlib.Path.exists()
log INFO "Step 1: Removing binary"
if test -f ./temp-tracker
    rm ./temp-tracker
    log INFO "Binary removed successfully"
else
    log WARN "No binary found to remove"
end

# Step 2: Find and kill running temp-tracker processes
# pgrep -f matches the full command line — like Python's psutil.process_iter() checking cmdline()
# The -f flag is crucial: without it, pgrep only matches the process name, not the full command with args
log INFO "Step 2: Checking for running processes"
set -l pids (pgrep -f "temp-tracker")

# Check if we found any PIDs (non-empty string)
# In Fish: if test -n "$pids" is like Python's if pids: (check if string not empty)
if test -n "$pids"
    log INFO "Found running temp-tracker process(es): $pids"
    
    # Iterate over each PID — like Python's for pid in pids.split():
    for pid in $pids
        log INFO "Killing PID $pid"
        # kill is like Python's os.kill(pid, signal.SIGTERM)
        # The -15 flag means SIGTERM (graceful shutdown), not SIGKILL (-9)
        kill -15 $pid 2>/dev/null
        set -l kill_status $status
        log DEBUG "Sent SIGTERM to PID $pid (exit status: $kill_status)"
        
        # Wait a moment for graceful shutdown — like Python's time.sleep(0.5)
        # Fish's sleep accepts decimals (unlike some shells)
        sleep 0.5
        
        # Check if process still exists — kill -0 tests existence without sending a real signal
        # In Python: try os.kill(pid, 0) except ProcessLookupError: ...
        if kill -0 $pid 2>/dev/null
            log WARN "PID $pid still running after SIGTERM, sending SIGKILL"
            kill -9 $pid 2>/dev/null  # SIGKILL — force terminate, like Python's process.terminate()
            set -l force_status $status
            log DEBUG "Sent SIGKILL to PID $pid (exit status: $force_status)"
            
            # Double-check if force kill worked
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

# Step 3: Rebuild the binary
log INFO "Step 3: Building binary"

# Run go build — like Python's subprocess.run(['go', 'build', '-o', 'temp-tracker', '.'])
# Check exit status with $status — like Python's subprocess.run().returncode
# In Fish, the status variable is set automatically after each command
go build -o temp-tracker .
set -l build_status $status

if test $build_status -eq 0
    log INFO "Build successful"
    log INFO "Ready to run: nohup ./temp-tracker -port 9091 -interval 30 > tracker.log 2>&1 &"
else
    log ERROR "Build failed with exit status $build_status"
    log FATAL "Aborting"
    exit 1  # Like Python's sys.exit(1) — return error code to shell
end

log INFO "========================================"
log INFO "Cleanup and rebuild complete"
log INFO "========================================"