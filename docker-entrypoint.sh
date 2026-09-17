#!/usr/bin/env bash
set -euo pipefail

# docker-entrypoint.sh
# Ensures a secrets store exists (encrypted with MASTER_KEY) before starting
# the firmware-updater server. The store and the server share the same
# MASTER_KEY so credentials can be decrypted at runtime.

SECRETS_FILE="${FIRMWARE_UPDATER_SECRETS_FILE:-/home/firmware/secrets.json}"
SECRET_ID="${SECRET_ID:-default-bmc}"
SECRET_USERNAME="${SECRET_USERNAME:-root}"
SECRET_PASSWORD="${SECRET_PASSWORD:-initial0}"

# Generate a MASTER_KEY if one was not supplied.
if [ -z "${MASTER_KEY:-}" ]; then
  MASTER_KEY="$(openssl rand -hex 32)"
  export MASTER_KEY
  echo "MASTER_KEY not provided; generated an ephemeral key for this container."
fi

# Create the encrypted secrets store with a default admin/password entry.
if [ ! -f "$SECRETS_FILE" ]; then
  echo "Creating secrets store at ${SECRETS_FILE} (secret-id: ${SECRET_ID})..."
  secret-cli \
    --secret-id "$SECRET_ID" \
    --username "$SECRET_USERNAME" \
    --password "$SECRET_PASSWORD" \
    --store-path "$SECRETS_FILE"
else
  echo "Secrets store already exists at ${SECRETS_FILE}; skipping creation."
fi

exec firmware-updater "$@"
