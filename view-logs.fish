#!/usr/bin/env fish
# view-logs.fish — Show temp-tracker service logs.
#
# Usage:
#   ./view-logs.fish          # user service logs (last 50 lines)
#   ./view-logs.fish -f       # user service logs (follow)
#   ./view-logs.fish --system # system service logs (last 50 lines)
#   ./view-logs.fish --system -f # system service logs (follow)
#
# In Fish (unlike Bash), variables use $var without braces, and command substitution is (cmd).

set -g SERVICE_NAME "temp-tracker.service"
set -g SYSTEM_MODE 0
set -g FOLLOW 0

for arg in $argv
    switch "$arg"
        case "--system"
            set SYSTEM_MODE 1
        case "-f" "--follow"
            set FOLLOW 1
        case "*"
            echo "Unknown option: $arg"
            echo "Usage: $argv[0] [--system] [-f|--follow]"
            exit 1
    end
end

if test "$SYSTEM_MODE" -eq 1
    if test "$FOLLOW" -eq 1
        echo "Following system service logs (Ctrl+C to exit)..."
        sudo journalctl -u "$SERVICE_NAME" -f
    else
        echo "Last 50 lines of system service logs:"
        sudo journalctl -u "$SERVICE_NAME" -n 50 --no-pager
    end
else
    if test "$FOLLOW" -eq 1
        echo "Following user service logs (Ctrl+C to exit)..."
        journalctl --user -u "$SERVICE_NAME" -f
    else
        echo "Last 50 lines of user service logs:"
        journalctl --user -u "$SERVICE_NAME" -n 50 --no-pager
    end
end
