#!/bin/bash
# /opt/leeforest/scripts/reload-gateway.sh
#
# Trigger a config reload on the gateway without restarting.
# Usage: sudo /opt/leeforest/scripts/reload-gateway.sh
#
# The gateway catches SIGHUP, reloads config.json, and spawns/stops child apps
# as needed. Existing apps continue running uninterrupted.

set -euo pipefail

SCRIPT_NAME="$(basename "$0")"
PID_FILE="/opt/leeforest/leeforest.pid"
LOG_PREFIX="[gateway-reload]"

log() {
    echo "$LOG_PREFIX $*"
}

# Check PID file exists
if [ ! -f "$PID_FILE" ]; then
    log "ERROR: PID file not found at $PID_FILE"
    log "Is the gateway running?"
    exit 1
fi

# Read PID
PID=$(cat "$PID_FILE")

# Verify process is alive
if ! kill -0 "$PID" 2>/dev/null; then
    log "ERROR: Gateway not running (PID $PID appears dead)"
    log "Try: sudo systemctl start leeforest"
    exit 1
fi

log "Sending SIGHUP to gateway (PID $PID)..."
kill -SIGHUP "$PID"

# Wait briefly for reload to complete
sleep 0.5

# Verify gateway survived the signal
if ! kill -0 "$PID" 2>/dev/null; then
    log "ERROR: Gateway crashed after signal"
    log "Check logs: sudo journalctl -u leeforest -n 20"
    exit 1
fi

log "SUCCESS: Gateway reloaded config."
log "Check child apps: sudo journalctl -u leeforest -n 5 --no-pager | grep 'Started app'"
exit 0
