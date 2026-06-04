#!/bin/bash

# Script to start Chromium in remote debugging mode for QuantClaw browser tools
# This is an alternative to the systemd service that has been failing

echo "Preparing to start Chromium in remote debugging mode..."

# Kill any existing instances
pkill -f "chromium-browser.*--remote-debugging-port=9222" 2>/dev/null || true

# Create a temporary directory for Chromium profile
CHROME_PROFILE_DIR="/tmp/chrome_quantclaw_profile"
mkdir -p $CHROME_PROFILE_DIR

echo "Starting Chromium with remote debugging on port 9222..."
echo "If this fails, please check:"
echo "1. Is chromium-browser properly installed?"
echo "2. Do you have a display available?"
echo "3. Is port 9222 available?"

# Start Chromium with essential flags for remote debugging
/usr/bin/chromium-browser \
    --remote-debugging-port=9222 \
    --user-data-dir="$CHROME_PROFILE_DIR" \
    --headless \
    --disable-gpu \
    --no-first-run \
    --no-default-browser-check \
    --disable-extensions \
    --disable-plugins \
    --no-sandbox \
    --disable-dev-shm-usage \
    --disable-gpu \
    --disable-web-security \
    --disable-features=VizDisplayCompositor \
    --ignore-certificate-errors \
    --ignore-urlfetcher-cert-requests \
    --disable-ipc-flooding-protection \
    --disable-setuid-sandbox \
    --no-zygote \
    --disable-breakpad \
    --disable-crash-reporter \
    "about:blank" &

CHROMIUM_PID=$!

echo "Chromium started with PID: $CHROMIUM_PID"

# Wait a moment for the browser to start
sleep 3

# Check if the process is still running
if ps -p $CHROMIUM_PID > /dev/null; then
    echo "Chromium is running with PID $CHROMIUM_PID"
    echo "Remote debugging should be available at http://localhost:9222/json/version"

    # Test if the debugging endpoint is available
    for i in {1..10}; do
        if curl -s http://localhost:9222/json/version > /dev/null 2>&1; then
            echo "✓ Remote debugging endpoint is accessible!"
            break
        else
            echo "Waiting for debugging endpoint to be ready... ($i/10)"
            sleep 2
        fi
    done

    # Keep the script running and monitor the process
    echo "Monitoring Chromium process. Press Ctrl+C to stop."
    wait $CHROMIUM_PID
else
    echo "✗ Chromium failed to start properly."
    echo "Try running Chromium manually to see the error message:"
    echo "/usr/bin/chromium-browser --no-sandbox --remote-debugging-port=9222"
fi
