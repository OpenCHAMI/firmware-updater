#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 || $# -gt 2 ]]; then
    echo "Usage: $0 NODE_NAME [true|false] (dry-run defaults to true)" >&2
    exit 1
fi

NODE_NAME="$1"
DRYRUN="${2-true}"
OUTPUT_FILE="${NODE_NAME}.json"

if [[ -z "$NODE_NAME" || "$NODE_NAME" == */* ]]; then
    echo "error: node name must be nonempty and cannot contain /" >&2
    exit 1
fi
if [[ "$DRYRUN" != true && "$DRYRUN" != false ]]; then
    echo "error: dry-run must be true or false" >&2
    exit 1
fi

export PODID=`podman ps | grep openchami/firmware-updater | cut -f 1 -d ' '`
export REGHOST=`podman exec -it $PODID "env" | grep FIRMWARE_UPDATER_REGISTRY_HOST | cut -f2 -d'=' | tr -d '\r' | sed 's#$#/firmware#'`

MISSING_ENV=false
for VARIABLE in FWSERVER REGHOST; do
    if [[ -z "${!VARIABLE:-}" ]]; then
        echo "error: set ${VARIABLE} to a nonempty value" >&2
        MISSING_ENV=true
    fi
done
if [[ "$MISSING_ENV" == true ]]; then
    exit 1
fi
if ! command -v jq >/dev/null 2>&1; then
    echo "error: jq is required" >&2
    exit 1
fi
if [[ ! -f update.json ]]; then
    echo "error: input file not found: update.json" >&2
    exit 1
fi
if [[ "$OUTPUT_FILE" -ef update.json ]]; then
    echo "error: output file cannot overwrite update.json" >&2
    exit 1
fi

TEMP_FILE="$(mktemp ./makeUpdateJson.XXXXXX)"
trap 'rm -f -- "$TEMP_FILE"' EXIT

jq --arg nodename "$NODE_NAME" --argjson dryruntf "$DRYRUN" \
    '.spec.targets[0].targetAddress = $nodename
     | .spec.dryrun = $dryruntf
     | .spec.serverProxyAddress = env.FWSERVER
     | .spec.discovery.repository = env.REGHOST' \
    update.json > "$TEMP_FILE"

mv -- "$TEMP_FILE" "$OUTPUT_FILE"
cat -- "$OUTPUT_FILE"