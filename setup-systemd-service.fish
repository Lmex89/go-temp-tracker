#!/usr/bin/env fish
# setup-systemd-service.fish — Install temp-tracker as a systemd service.
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

# Resolve the project directory (where this script lives).
# Like Python: os.path.dirname(os.path.abspath(__file__))
set -g PROJECT_DIR (cd (dirname (status -f)); and pwd)

# Detect system mode.
set -g SYSTEM_MODE 0
for arg in $argv
    if test "$arg" = "--system"
        set SYSTEM_MODE 1
        break
    end
end

# Service identity.
set -g SERVICE_NAME "temp-tracker.service"

# Runtime settings — keep them in sync with cleanup-and-build.fish.
set -g PORT 9091
set -g INTERVAL 60
set -g CPU_POLL_INTERVAL 60
set -g MEMORY_POLL_INTERVAL 60
set -g SWAP_POLL_INTERVAL 60
set -g DISK_POLL_INTERVAL 60
set -g LOAD_POLL_INTERVAL 60

if test "$SYSTEM_MODE" -eq 1
    set -g SERVICE_DIR "/etc/systemd/system"
    set -g SERVICE_PATH "$SERVICE_DIR/$SERVICE_NAME"
    set -g TARGET "multi-user.target"
    set -g USER_LINE "User=$USER"
else
    set -g SERVICE_DIR "$HOME/.config/systemd/user"
    set -g SERVICE_PATH "$SERVICE_DIR/$SERVICE_NAME"
    set -g TARGET "default.target"
    set -g USER_LINE ""
end

echo "Installing $SERVICE_NAME as a systemd service..."
if test "$SYSTEM_MODE" -eq 1
    echo "Mode: system service (requires sudo, starts at boot)"
else
    echo "Mode: user service (no sudo required, starts on login)"
end

# Ensure the systemd directory exists.
# For system mode this directory already exists; for user mode we create it.
if test "$SYSTEM_MODE" -eq 0
    mkdir -p "$SERVICE_DIR"
end

# Stop and disable any previous version of the service.
# We ignore failures here because the service may not exist yet.
if test "$SYSTEM_MODE" -eq 1
    if sudo systemctl is-enabled --quiet "$SERVICE_NAME" 2>/dev/null
        echo "Disabling existing service..."
        sudo systemctl disable --now "$SERVICE_NAME" 2>/dev/null; or true
    end
else
    if systemctl --user is-enabled --quiet "$SERVICE_NAME" 2>/dev/null
        echo "Disabling existing service..."
        systemctl --user disable --now "$SERVICE_NAME" 2>/dev/null; or true
    end
end

# Write the unit file.
# In Python this would be:
#   with open(service_path, 'w') as f: f.write(unit_content)
echo "Writing $SERVICE_PATH..."
set -l unit_lines
set -a unit_lines "[Unit]"
set -a unit_lines "Description=Sensor Temperature Tracker"
set -a unit_lines "After=network.target"
set -a unit_lines ""
set -a unit_lines "[Service]"
set -a unit_lines "Type=simple"
set -a unit_lines "WorkingDirectory=$PROJECT_DIR"
if test -n "$USER_LINE"
    set -a unit_lines "$USER_LINE"
end
set -a unit_lines "Environment=CPU_POLL_INTERVAL=$CPU_POLL_INTERVAL"
set -a unit_lines "Environment=MEMORY_POLL_INTERVAL=$MEMORY_POLL_INTERVAL"
set -a unit_lines "Environment=SWAP_POLL_INTERVAL=$SWAP_POLL_INTERVAL"
set -a unit_lines "Environment=DISK_POLL_INTERVAL=$DISK_POLL_INTERVAL"
set -a unit_lines "Environment=LOAD_POLL_INTERVAL=$LOAD_POLL_INTERVAL"
set -a unit_lines "ExecStart=$PROJECT_DIR/temp-tracker -port $PORT -interval $INTERVAL"
set -a unit_lines "Restart=on-failure"
set -a unit_lines "RestartSec=5"
set -a unit_lines ""
set -a unit_lines "[Install]"
set -a unit_lines "WantedBy=$TARGET"

if test "$SYSTEM_MODE" -eq 1
    # System mode: need sudo to write to /etc/systemd/system.
    printf "%s\n" $unit_lines | sudo tee "$SERVICE_PATH" > /dev/null
else
    printf "%s\n" $unit_lines > "$SERVICE_PATH"
end

# Tell systemd to re-read unit files.
if test "$SYSTEM_MODE" -eq 1
    sudo systemctl daemon-reload
else
    systemctl --user daemon-reload
end

# Enable the service so it starts automatically.
if test "$SYSTEM_MODE" -eq 1
    sudo systemctl enable "$SERVICE_NAME"
else
    systemctl --user enable "$SERVICE_NAME"
end

# Kill any leftover temp-tracker process on the configured port.
# This prevents "address already in use" when starting the service.
set -l pids (pgrep -f "$PROJECT_DIR/temp-tracker" 2>/dev/null; or echo "")
for pid in $pids
    echo "Stopping leftover temp-tracker PID $pid..."
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

# Start the service now.
if test "$SYSTEM_MODE" -eq 1
    sudo systemctl start "$SERVICE_NAME"
else
    systemctl --user start "$SERVICE_NAME"
end

echo ""
echo "Service installed and started."
echo "Dashboard: http://localhost:$PORT"
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

# Show current status.
echo ""
if test "$SYSTEM_MODE" -eq 1
    sudo systemctl status "$SERVICE_NAME" --no-pager
else
    systemctl --user status "$SERVICE_NAME" --no-pager
end
