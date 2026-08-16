#!/bin/sh
set -eu

# Wait for bootstrap file to be generated
for i in 1 2 3 4 5; do
    if [ -f "/var/lib/grok2api-quality-guard/bootstrap.json" ]; then
        echo "QualityGuard bootstrap found, starting..."
        break
    fi
    echo "Waiting for QualityGuard bootstrap ($i/5)..."
    sleep 5
done

if [ ! -f "/var/lib/grok2api-quality-guard/bootstrap.json" ]; then
    echo "QualityGuard bootstrap not found, exiting..."
    exit 1
fi

# Start quality guard with localhost (same container)
exec /usr/local/bin/grok2api-egress-quality-guard --base-url "http://127.0.0.1:8000"
