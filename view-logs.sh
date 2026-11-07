#!/usr/bin/env bash
# view-logs.sh -- Show temp-tracker service logs.
#
# Usage:
#   ./view-logs.sh          # user service logs (last 50 lines)
#   ./view-logs.sh -f       # user service logs (follow)
#   ./view-logs.sh --system # system service logs (last 50 lines)
#   ./view-logs.sh --system -f # system service logs (follow)
#
# In Bash (unlike Fish), variable substitution uses ${var} or $var,
# and command substitution is $(cmd).

set -euo pipefail

SERVICE_NAME="temp-tracker.service"
SYSTEM_MODE=0
FOLLOW=0

for arg in "$@"; do
    case "$arg" in
        --system)
            SYSTEM_MODE=1
            ;;
        -f|--follow)
            FOLLOW=1
            ;;
        *)
            echo "Unknown option: $arg"
            echo "Usage: $0 [--system] [-f|--follow]"
            exit 1
            ;;
    esac
done

if [[ "$SYSTEM_MODE" -eq 1 ]]; then
    if [[ "$FOLLOW" -eq 1 ]]; then
        echo "Following system service logs (Ctrl+C to exit)..."
        sudo journalctl -u "${SERVICE_NAME}" -f
    else
        echo "Last 50 lines of system service logs:"
        sudo journalctl -u "${SERVICE_NAME}" -n 50 --no-pager
    fi
else
    if [[ "$FOLLOW" -eq 1 ]]; then
        echo "Following user service logs (Ctrl+C to exit)..."
        journalctl --user -u "${SERVICE_NAME}" -f
    else
        echo "Last 50 lines of user service logs:"
        journalctl --user -u "${SERVICE_NAME}" -n 50 --no-pager
    fi
fi
