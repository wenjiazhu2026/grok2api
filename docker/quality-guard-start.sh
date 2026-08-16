#!/bin/sh
set -eu

# Wait for bootstrap file to be generated
for i in 1 2 3 4 5 6 7 8 9 10; do
    if [ -f "/var/lib/grok2api-quality-guard/bootstrap.json" ]; then
        echo "QualityGuard bootstrap found, starting..." >&2
        break
    fi
    echo "Waiting for QualityGuard bootstrap ($i/10)..." >&2
    sleep 3
done

if [ ! -f "/var/lib/grok2api-quality-guard/bootstrap.json" ]; then
    echo "QualityGuard bootstrap not found, exiting..." >&2
    exit 0
fi

# Patch the base_url in bootstrap file to use localhost
sed -i 's|http://grok2api:8000|http://127.0.0.1:8000|g' /var/lib/grok2api-quality-guard/bootstrap.json

# Start quality guard
exec /usr/local/bin/grok2api-egress-quality-guard
