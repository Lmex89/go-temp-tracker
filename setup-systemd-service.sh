#!/usr/bin/env bash
# setup-systemd-service.sh — Install temp-tracker as a systemd service.
#
# Usage:
#   ./setup-systemd-service.sh          # user service (starts on login, no sudo)
#   ./setup-systemd-service.sh --system # system service (starts at boot, uses sudo)
#
# Like a Python script that:
#   - writes a systemd unit file
#   - runs systemctl daemon-reload
#   - enables the service so it starts automatically
#   - starts the service now
#
# In Bash (unlike Fish), variable substitution uses ${var} or $var,
# and command substitution is $(cmd).

set -euo pipefail
# 'set -e' is like Python's sys.exit() on the first subprocess error.
# 'set -u' is like Python raising NameError for unset variables.
# 'set -o pipefail' makes pipeline failures propagate (like Python's subprocess pipes).

# Detect system mode.
SYSTEM_MODE=0
if [[ "${1:-}" == "--system" ]]; then
    SYSTEM_MODE=1
fi

# Resolve the project directory (where this script lives).
# Like Python: os.path.dirname(os.path.abspath(__file__))
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="${SCRIPT_DIR}"

# Service identity.
SERVICE_NAME="temp-tracker.service"

# Runtime settings — keep them in sync with cleanup-and-build.fish.
PORT=9091
INTERVAL=60
CPU_POLL_INTERVAL=60
MEMORY_POLL_INTERVAL=60
SWAP_POLL_INTERVAL=60
DISK_POLL_INTERVAL=60
LOAD_POLL_INTERVAL=60

if [[ "$SYSTEM_MODE" -eq 1 ]]; then
    SERVICE_DIR="/etc/systemd/system"
    SERVICE_PATH="${SERVICE_DIR}/${SERVICE_NAME}"
    SYSTEMCTL="sudo systemctl"
    TARGET="multi-user.target"
    USER_LINE="User=$(whoami)"
else
    SERVICE_DIR="${HOME}/.config/systemd/user"
    SERVICE_PATH="${SERVICE_DIR}/${SERVICE_NAME}"
    SYSTEMCTL="systemctl --user"
    TARGET="default.target"
    USER_LINE=""
fi

echo "Installing ${SERVICE_NAME} as a systemd service..."
if [[ "$SYSTEM_MODE" -eq 1 ]]; then
    echo "Mode: system service (requires sudo, starts at boot)"
else
    echo "Mode: user service (no sudo required, starts on login)"
fi

# Ensure the systemd directory exists.
# For system mode this directory already exists; for user mode we create it.
if [[ "$SYSTEM_MODE" -eq 0 ]]; then
    mkdir -p "${SERVICE_DIR}"
fi

# Stop and disable any previous version of the service.
# We ignore failures here because the service may not exist yet.
if ${SYSTEMCTL} is-enabled --quiet "${SERVICE_NAME}" 2>/dev/null; then
    echo "Disabling existing service..."
    ${SYSTEMCTL} disable --now "${SERVICE_NAME}" || true
fi

# Write the unit file.
# In Python this would be:
#   with open(service_path, 'w') as f: f.write(unit_content)
echo "Writing ${SERVICE_PATH}..."
{
    echo "[Unit]"
    echo "Description=Sensor Temperature Tracker"
    echo "After=network.target"
    echo ""
    echo "[Service]"
    echo "Type=simple"
    echo "WorkingDirectory=${PROJECT_DIR}"
    if [[ -n "${USER_LINE}" ]]; then
        echo "${USER_LINE}"
    fi
    echo "Environment=CPU_POLL_INTERVAL=${CPU_POLL_INTERVAL}"
    echo "Environment=MEMORY_POLL_INTERVAL=${MEMORY_POLL_INTERVAL}"
    echo "Environment=SWAP_POLL_INTERVAL=${SWAP_POLL_INTERVAL}"
    echo "Environment=DISK_POLL_INTERVAL=${DISK_POLL_INTERVAL}"
    echo "Environment=LOAD_POLL_INTERVAL=${LOAD_POLL_INTERVAL}"
    echo "ExecStart=${PROJECT_DIR}/temp-tracker -port ${PORT} -interval ${INTERVAL}"
    echo "Restart=on-failure"
    echo "RestartSec=5"
    echo ""
    echo "[Install]"
    echo "WantedBy=${TARGET}"
} | if [[ "$SYSTEM_MODE" -eq 1 ]]; then
    # System mode: need sudo to write to /etc/systemd/system.
    sudo tee "${SERVICE_PATH}" > /dev/null
else
    cat > "${SERVICE_PATH}"
fi

# Tell systemd to re-read unit files.
${SYSTEMCTL} daemon-reload

# Enable the service so it starts automatically.
${SYSTEMCTL} enable "${SERVICE_NAME}"

# Kill any leftover temp-tracker process on the configured port.
# This prevents "address already in use" when starting the service.
for pid in $(pgrep -f "${PROJECT_DIR}/temp-tracker" 2>/dev/null || true); do
    echo "Stopping leftover temp-tracker PID ${pid}..."
    if [[ "$SYSTEM_MODE" -eq 1 ]]; then
        sudo kill -15 "${pid}" 2>/dev/null || true
    else
        kill -15 "${pid}" 2>/dev/null || true
    fi
    sleep 0.5
    if kill -0 "${pid}" 2>/dev/null; then
        if [[ "$SYSTEM_MODE" -eq 1 ]]; then
            sudo kill -9 "${pid}" 2>/dev/null || true
        else
            kill -9 "${pid}" 2>/dev/null || true
        fi
    fi
done
sleep 1

# Start the service now.
${SYSTEMCTL} start "${SERVICE_NAME}"

echo ""
echo "Service installed and started."
echo "Dashboard: http://localhost:${PORT}"
echo ""
echo "Useful commands:"
echo "  ${SYSTEMCTL} status ${SERVICE_NAME}"
echo "  ${SYSTEMCTL} stop ${SERVICE_NAME}"
echo "  ${SYSTEMCTL} restart ${SERVICE_NAME}"
if [[ "$SYSTEM_MODE" -eq 1 ]]; then
    echo "  sudo journalctl -u ${SERVICE_NAME} -f"
else
    echo "  journalctl --user -u ${SERVICE_NAME} -f"
fi

# Show current status.
echo ""
${SYSTEMCTL} status "${SERVICE_NAME}" --no-pager
