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

set -g SERVICE_NAME "temp-tracker.service"
set -g SYSTEM_MODE 0
set -g COMMAND ""
set -g FOLLOW 0

for arg in $argv
    switch "$arg"
        case "--system"
            set SYSTEM_MODE 1
        case "status" "start" "stop" "restart" "logs"
            if test -z "$COMMAND"
                set COMMAND "$arg"
            else
                echo "Error: only one command allowed (got $COMMAND and $arg)"
                exit 1
            end
        case "-f" "--follow"
            set FOLLOW 1
        case "*"
            echo "Unknown option/command: $arg"
            echo ""
            echo "Usage: $argv[0] [--system] [status|start|stop|restart|logs] [-f]"
            echo ""
            echo "Examples:"
            echo "  $argv[0] status"
            echo "  $argv[0] stop"
            echo "  $argv[0] restart"
            echo "  $argv[0] logs"
            echo "  $argv[0] logs -f"
            echo "  $argv[0] --system status"
            exit 1
    end
end

if test -z "$COMMAND"
    echo "Usage: $argv[0] [--system] [status|start|stop|restart|logs] [-f]"
    exit 1
end

function run_systemctl
    # In Fish, a variable holding a command AND its flags cannot be used as the command.
    # systemctl --user as one string would look for a binary literally named "systemctl --user".
    # Use a function instead -- like a Bash wrapper that forwards arguments.
    if test "$SYSTEM_MODE" -eq 1
        sudo systemctl $argv
    else
        systemctl --user $argv
    end
end

switch "$COMMAND"
    case "status"
        run_systemctl status "$SERVICE_NAME" --no-pager
    case "start"
        echo "Starting $SERVICE_NAME..."
        run_systemctl start "$SERVICE_NAME"
        run_systemctl status "$SERVICE_NAME" --no-pager
    case "stop"
        echo "Stopping $SERVICE_NAME..."
        run_systemctl stop "$SERVICE_NAME"
        echo "Stopped."
    case "restart"
        echo "Restarting $SERVICE_NAME..."
        run_systemctl restart "$SERVICE_NAME"
        run_systemctl status "$SERVICE_NAME" --no-pager
    case "logs"
        if test "$FOLLOW" -eq 1
            if test "$SYSTEM_MODE" -eq 1
                echo "Following system service logs (Ctrl+C to exit)..."
                sudo journalctl -u "$SERVICE_NAME" -f
            else
                echo "Following user service logs (Ctrl+C to exit)..."
                journalctl --user -u "$SERVICE_NAME" -f
            end
        else
            if test "$SYSTEM_MODE" -eq 1
                echo "Last 50 lines of system service logs:"
                sudo journalctl -u "$SERVICE_NAME" -n 50 --no-pager
            else
                echo "Last 50 lines of user service logs:"
                journalctl --user -u "$SERVICE_NAME" -n 50 --no-pager
            end
        end
end
