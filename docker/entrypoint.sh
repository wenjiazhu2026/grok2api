#!/bin/sh
set -eu

umask 077

quality_guard_dir=/var/lib/grok2api-quality-guard
mkdir -p "${quality_guard_dir}"
chown grok2api:grok2api "${quality_guard_dir}"
chmod 0700 "${quality_guard_dir}"

# Check if config file exists
if [ -f "${GROK2API_CONFIG_SOURCE}" ]; then
    # Copy existing config
    cp "${GROK2API_CONFIG_SOURCE}" /app/config.yaml
    chown grok2api:grok2api /app/config.yaml
    chmod 0600 /app/config.yaml
elif [ -n "${GROK2API_SECRETS_JWT_SECRET}" ] && [ -n "${GROK2API_SECRETS_CREDENTIAL_ENCRYPTION_KEY}" ]; then
    # Generate config from environment variables
    echo "Generating config from environment variables..." >&2
    
    # Set defaults
    : "${GROK2API_SERVER_LISTEN:=0.0.0.0:8000}"
    : "${GROK2API_SERVER_MAX_BODY_BYTES:=33554432}"
    : "${GROK2API_SERVER_READ_TIMEOUT:=15m}"
    : "${GROK2API_SERVER_REQUEST_TIMEOUT:=2h}"
    : "${GROK2API_SERVER_SWAGGER_ENABLED:=false}"
    : "${GROK2API_AUTH_ACCESS_TOKEN_TTL:=15m}"
    : "${GROK2API_AUTH_REFRESH_TOKEN_TTL:=720h}"
    : "${GROK2API_AUTH_SECURE_COOKIES:=true}"
    : "${GROK2API_BOOTSTRAP_ADMIN_USERNAME:=admin}"
    : "${GROK2API_BOOTSTRAP_ADMIN_PASSWORD:=ChangeMe123!}"
    : "${GROK2API_FRONTEND_STATIC_PATH:=./frontend/dist}"
    : "${GROK2API_DATABASE_DRIVER:=sqlite}"
    : "${GROK2API_DATABASE_SQLITE_PATH:=/tmp/backend.db}"
    : "${GROK2API_RUNTIME_STORE_DRIVER:=memory}"
    : "${GROK2API_DEPLOYMENT_REPLICAS:=1}"
    : "${GROK2API_DEPLOYMENT_INSTANCE_ID:=}"
    : "${GROK2API_DEPLOYMENT_CLUSTER_ID:=grok2api}"
    : "${GROK2API_MEDIA_DRIVER:=local}"
    : "${GROK2API_MEDIA_LOCAL_PATH:=/app/data/media}"
    : "${GROK2API_ROUTING_REASONING_REPLAY_ENABLED:=true}"

    mkdir -p /app/data/media
    chown -R grok2api:grok2api /app/data

    cat > /app/config.yaml << CONFIG
server:
  listen: "${GROK2API_SERVER_LISTEN}"
  maxBodyBytes: ${GROK2API_SERVER_MAX_BODY_BYTES}
  readTimeout: ${GROK2API_SERVER_READ_TIMEOUT}
  requestTimeout: ${GROK2API_SERVER_REQUEST_TIMEOUT}
  swaggerEnabled: ${GROK2API_SERVER_SWAGGER_ENABLED}

auth:
  accessTokenTTL: ${GROK2API_AUTH_ACCESS_TOKEN_TTL}
  refreshTokenTTL: ${GROK2API_AUTH_REFRESH_TOKEN_TTL}
  secureCookies: ${GROK2API_AUTH_SECURE_COOKIES}

secrets:
  jwtSecret: "${GROK2API_SECRETS_JWT_SECRET}"
  credentialEncryptionKey: "${GROK2API_SECRETS_CREDENTIAL_ENCRYPTION_KEY}"

bootstrapAdmin:
  username: "${GROK2API_BOOTSTRAP_ADMIN_USERNAME}"
  password: "${GROK2API_BOOTSTRAP_ADMIN_PASSWORD}"

frontend:
  staticPath: "${GROK2API_FRONTEND_STATIC_PATH}"

database:
  driver: "${GROK2API_DATABASE_DRIVER}"
  sqlite:
    path: "${GROK2API_DATABASE_SQLITE_PATH}"
  postgres:
    dsn: "${GROK2API_DATABASE_URL:-}"
    maxOpenConns: 50
    maxIdleConns: 10

runtimeStore:
  driver: "${GROK2API_RUNTIME_STORE_DRIVER}"

deployment:
  replicas: ${GROK2API_DEPLOYMENT_REPLICAS}
  instanceID: "${GROK2API_DEPLOYMENT_INSTANCE_ID}"
  clusterID: "${GROK2API_DEPLOYMENT_CLUSTER_ID}"
  sharedMedia: false

media:
  driver: "${GROK2API_MEDIA_DRIVER}"
  local:
    path: "${GROK2API_MEDIA_LOCAL_PATH}"

routing:
  reasoningReplayEnabled: ${GROK2API_ROUTING_REASONING_REPLAY_ENABLED}

qualityGuard:
  enabled: ${GROK2API_QUALITY_GUARD_ENABLED:-false}
  model: "${GROK2API_QUALITY_GUARD_MODEL:-grok-3.5}"
  mode: "${GROK2API_QUALITY_GUARD_MODE:-active}"
  activeInterval: ${GROK2API_QUALITY_GUARD_ACTIVE_INTERVAL:-30m}
  softTPS: ${GROK2API_QUALITY_GUARD_SOFT_TPS:-500}
  hardTPS: ${GROK2API_QUALITY_GUARD_HARD_TPS:-1000}
  failClosed: ${GROK2API_QUALITY_GUARD_FAIL_CLOSED:-false}
  rotationURL: "${GROK2API_QUALITY_GUARD_ROTATION_URL:-}"
  rotationToken: "${GROK2API_QUALITY_GUARD_ROTATION_TOKEN:-}"

audit:
  bufferSize: 16384
  batchSize: 256
  flushInterval: 250ms
  commitDelay: 5ms
  ledgerMode: enforce
  ledgerFailureThreshold: 1
  ledgerUnhealthyGrace: 10s
  ledgerQueueHighWatermarkPercent: 90
CONFIG

    chown grok2api:grok2api /app/config.yaml
    chmod 0600 /app/config.yaml
    echo "Config generated successfully" >&2
else
    echo "missing config: ${GROK2API_CONFIG_SOURCE}" >&2
    echo "Please provide either config.yaml or GROK2API_SECRETS_JWT_SECRET and GROK2API_SECRETS_CREDENTIAL_ENCRYPTION_KEY" >&2
    exit 1
fi

# Start QualityGuard sidecar in background if enabled
if grep -qE '^[[:space:]]*enabled:[[:space:]]+true' /app/config.yaml 2>/dev/null; then
    echo "Starting QualityGuard sidecar in background..." >&2
    su-exec grok2api:grok2api /usr/local/bin/grok2api-quality-guard-start &
    sleep 2
fi

exec su-exec grok2api:grok2api "$@"
