#!/bin/sh
set -eu

# The guard directory is pinned by the main program (GROK2API_QUALITY_GUARD_DIR)
# and mirrored by the sidecar's own hard-coded path, so keep the same default.
guard_dir="${GROK2API_QUALITY_GUARD_DIR:-/var/lib/grok2api-quality-guard}"
bootstrap="${guard_dir}/bootstrap.json"

# Wait for the main program to publish the bootstrap file.
for i in 1 2 3 4 5 6 7 8 9 10; do
    if [ -f "${bootstrap}" ]; then
        echo "QualityGuard bootstrap found, starting..." >&2
        break
    fi
    echo "Waiting for QualityGuard bootstrap ($i/10)..." >&2
    sleep 3
done

if [ ! -f "${bootstrap}" ]; then
    echo "QualityGuard bootstrap not found, exiting..." >&2
    exit 0
fi

# Wait until the main program is actually serving requests. The guard discovers
# managed egress nodes through the internal API, so starting before the main
# program has assembled them yields an empty node set and an immediate failure
# (guard_started node_count=0 followed by active_cycle_failed), after which the
# guard would not retry for a full activeInterval.
ready=0
for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
    if wget -q -O /dev/null "http://127.0.0.1:8000/healthz" 2>/dev/null; then
        ready=1
        echo "QualityGuard: main program is ready, starting guard..." >&2
        break
    fi
    echo "Waiting for main program to become ready ($i/15)..." >&2
    sleep 2
done

if [ "${ready}" -ne 1 ]; then
    echo "QualityGuard: main program did not report ready, starting guard anyway" >&2
fi

# Patch the base_url in bootstrap file to use localhost
sed -i "s|http://grok2api:8000|http://127.0.0.1:8000|g" "${bootstrap}"

# Start quality guard
exec /usr/local/bin/grok2api-egress-quality-guard
