#!/usr/bin/env bash
# service-manager.sh — Manage the temp-tracker systemd service.
#
# Usage:
#   ./service-manager.sh status              # user service status
#   ./service-manager.sh start               # user service start
#   ./service-manager.sh stop                # user service stop
#   ./service-manager.sh restart             # user service restart
#   ./service-manager.sh logs                # user service logs (last 50 lines)
#   ./service-manager.sh logs -f             # user service logs (follow)
#
# Add --system anywhere for system service (requires sudo):
#   ./service-manager.sh --system status
#   ./service-manager.sh --system stop
#   ./service-manager.sh --system restart
#   ./service-manager.sh --system logs -f
#
# In Bash (unlike Fish), variable substitution uses ${var} or $var,
# and command substitution is $(cmd).

set -euo pipefail

SERVICE_NAME="temp-tracker.service"
SYSTEM_MODE=0
COMMAND=""
FOLLOW=0

for arg in "$@"; do
    case "$arg" in
        --system)
            SYSTEM_MODE=1
            ;;
        status|start|stop|restart|logs)
            if [[ -z "$COMMAND" ]]; then
                COMMAND="$arg"
            else
                echo "Error: only one command allowed (got $COMMAND and $arg)"
                exit 1
            fi
            ;;
        -f|--follow)
            FOLLOW=1
            ;;
        *)
            echo "Unknown option/command: $arg"
            echo ""
            echo "Usage: $0 [--system] [status|start|stop|restart|logs] [-f]"
            echo ""
            echo "Examples:"
            echo "  $0 status"
            echo "  $0 stop"
            echo "  $0 restart"
            echo "  $0 logs"
            echo "  $0 logs -f"
            echo "  $0 --system status"
            exit 1
            ;;
    esac
done

if [[ -z "$COMMAND" ]]; then
    echo "Usage: $0 [--system] [status|start|stop|restart|logs] [-f]"
    exit 1
fi

if [[ "$SYSTEM_MODE" -eq 1 ]]; then
    SYSTEMCTL="sudo systemctl"
else
    SYSTEMCTL="systemctl --user"
fi

case "$COMMAND" in
    status)
        ${SYSTEMCTL} status "${SERVICE_NAME}" --no-pager
        ;;
    start)
        echo "Starting ${SERVICE_NAME}..."
        ${SYSTEMCTL} start "${SERVICE_NAME}"
        ${SYSTEMCTL} status "${SERVICE_NAME}" --no-pager
        ;;
    stop)
        echo "Stopping ${SERVICE_NAME}..."
        ${SYSTEMCTL} stop "${SERVICE_NAME}"
        echo "Stopped."
        ;;
    restart)
        echo "Restarting ${SERVICE_NAME}..."
        ${SYSTEMCTL} restart "${SERVICE_NAME}"
        ${SYSTEMCTL} status "${SERVICE_NAME}" --no-pager
        ;;
    logs)
        if [[ "$FOLLOW" -eq 1 ]]; then
            if [[ "$SYSTEM_MODE" -eq 1 ]]; then
                echo "Following system service logs (Ctrl+C to exit)..."
                sudo journalctl -u "${SERVICE_NAME}" -f
            else
                echo "Following user service logs (Ctrl+C to exit)..."
                journalctl --user -u "${SERVICE_NAME}" -f
            fi
        else
            if [[ "$SYSTEM_MODE" -eq 1 ]]; then
                echo "Last 50 lines of system service logs:"
                sudo journalctl -u "${SERVICE_NAME}" -n 50 --no-pager
            else
                echo "Last 50 lines of user service logs:"
                journalctl --user -u "${SERVICE_NAME}" -n 50 --no-pager
            fi
        fi
        ;;
esac
