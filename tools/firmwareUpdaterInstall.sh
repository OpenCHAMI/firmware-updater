#!/usr/bin/env bash
set -euo pipefail

# Podman equivalent of docker-compose.yml, for hosts that have podman but not
# docker-compose or podman-compose. Brings up the same two containers
# (OCI registry + firmware-updater) on a shared network with the same
# ports/env/volumes as the compose file.
#
# Usage:
#   tools/podman-compose.sh up
#   tools/podman-compose.sh down [--volumes]
#   tools/podman-compose.sh logs [registry|firmware-updater]
#   tools/podman-compose.sh status

NETWORK_NAME="firmware-net"
REGISTRY_VOLUME="registry-data"
FIRMWARE_VOLUME="firmware-data"
REGISTRY_CONTAINER="oras-registry"
FIRMWARE_CONTAINER="firmware-updater"
REGISTRY_IMAGE="ghcr.io/oras-project/registry:latest"
FIRMWARE_IMAGE="ghcr.io/openchami/firmware-updater:latest"

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CERTS_DIR="${PROJECT_ROOT}/certs"
SECRETS_FILE="${PROJECT_ROOT}/secrets.json"

usage() {
    echo "Usage: $0 [up|down [--volumes]|logs [registry|firmware-updater]|status]" >&2
    exit 1
}

require_command() {
    if ! command -v "$1" >/dev/null 2>&1; then
        echo "error: $1 was not found on PATH" >&2
        exit 1
    fi
}

wait_for_registry_health() {
    echo "Waiting for registry to become healthy..."
    for _ in $(seq 1 30); do
        status="$(podman inspect --format '{{.State.Health.Status}}' "$REGISTRY_CONTAINER" 2>/dev/null || echo starting)"
        if [[ "$status" == "healthy" ]]; then
            echo "Registry is healthy."
            return 0
        fi
        sleep 2
    done
    echo "error: registry did not become healthy in time" >&2
    podman logs "$REGISTRY_CONTAINER" || true
    exit 1
}

cmd_up() {
    if [[ ! -f "${CERTS_DIR}/registry.crt" || ! -f "${CERTS_DIR}/registry.key" ]]; then
        echo "error: expected TLS cert/key at ${CERTS_DIR}/registry.crt and ${CERTS_DIR}/registry.key" >&2
        exit 1
    fi
    if [[ ! -f "$SECRETS_FILE" ]]; then
        echo "error: expected encrypted secrets store at ${SECRETS_FILE}" >&2
        exit 1
    fi
    if [[ -z "${MASTER_KEY:-}" ]]; then
        echo "error: set MASTER_KEY to the key used to encrypt ${SECRETS_FILE}" >&2
        exit 1
    fi

    podman network exists "$NETWORK_NAME" || podman network create "$NETWORK_NAME"
    podman volume exists "$REGISTRY_VOLUME" || podman volume create "$REGISTRY_VOLUME"
    podman volume exists "$FIRMWARE_VOLUME" || podman volume create "$FIRMWARE_VOLUME"

    echo "Starting $REGISTRY_CONTAINER..."
    podman run -d --replace \
        --name "$REGISTRY_CONTAINER" \
        --hostname registry \
        --network "$NETWORK_NAME" \
        --network-alias registry \
        --restart unless-stopped \
        -p 5000:5000 \
        -e REGISTRY_STORAGE_FILESYSTEM_ROOTDIRECTORY=/var/lib/registry \
        -e REGISTRY_HTTP_ADDR=:5000 \
        -e REGISTRY_HTTP_TLS_CERTIFICATE=/certs/registry.crt \
        -e REGISTRY_HTTP_TLS_KEY=/certs/registry.key \
        -v "${REGISTRY_VOLUME}:/var/lib/registry" \
        -v "${CERTS_DIR}:/certs:ro" \
        --health-cmd "wget --no-check-certificate -qO- https://localhost:5000/v2/ || exit 1" \
        --health-interval 10s \
        --health-timeout 10s \
        --health-retries 10 \
        "$REGISTRY_IMAGE"

    wait_for_registry_health

    echo "Starting $FIRMWARE_CONTAINER..."
    podman run -d --replace \
        --name "$FIRMWARE_CONTAINER" \
        --hostname firmwareupdater \
        --network "$NETWORK_NAME" \
        --network-alias firmwareupdater \
        --restart unless-stopped \
        -p 8080:8080 \
        -e MASTER_KEY="$MASTER_KEY" \
        -e FIRMWARE_UPDATER_PORT=8080 \
        -e FIRMWARE_UPDATER_HOST=0.0.0.0 \
        -e FIRMWARE_UPDATER_REGISTRY_HOST="registry:5000" \
        -e FIRMWARE_UPDATER_REPOSITORY_INSECURE_TLS=true \
        -e FIRMWARE_UPDATER_DEBUG=true \
        -v "${FIRMWARE_VOLUME}:/home/firmware" \
        -v "${SECRETS_FILE}:/home/firmware/secrets.json:ro" \
        "$FIRMWARE_IMAGE"

    echo "Both containers are running."
    podman ps --filter "name=${REGISTRY_CONTAINER}" --filter "name=${FIRMWARE_CONTAINER}"
}

cmd_down() {
    echo "Stopping and removing containers..."
    podman rm -f "$FIRMWARE_CONTAINER" >/dev/null 2>&1 || true
    podman rm -f "$REGISTRY_CONTAINER" >/dev/null 2>&1 || true

    if [[ "${1:-}" == "--volumes" ]]; then
        echo "Removing volumes..."
        podman volume rm "$FIRMWARE_VOLUME" "$REGISTRY_VOLUME" >/dev/null 2>&1 || true
    fi

    echo "Removing network..."
    podman network rm "$NETWORK_NAME" >/dev/null 2>&1 || true
}

cmd_logs() {
    local target="${1:-$FIRMWARE_CONTAINER}"
    case "$target" in
        registry) podman logs -f "$REGISTRY_CONTAINER" ;;
        firmware-updater) podman logs -f "$FIRMWARE_CONTAINER" ;;
        *) echo "error: unknown target '$target' (expected registry or firmware-updater)" >&2; exit 1 ;;
    esac
}

cmd_status() {
    podman ps -a --filter "name=${REGISTRY_CONTAINER}" --filter "name=${FIRMWARE_CONTAINER}"
}

require_command podman

case "${1:-}" in
    up) cmd_up ;;
    down) shift; cmd_down "${1:-}" ;;
    logs) shift; cmd_logs "${1:-}" ;;
    status) cmd_status ;;
    *) usage ;;
esac
