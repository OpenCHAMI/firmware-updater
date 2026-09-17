#!/usr/bin/env bash
set -euo pipefail

IMAGE_NAME="${1:-firmware-updater}"
TEST_TAG="${2:-release-test}"
KEEP_DIST="${KEEP_DIST:-false}"

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$PROJECT_ROOT"

require_command() {
    if ! command -v "$1" >/dev/null 2>&1; then
        echo "error: $1 was not found on PATH" >&2
        exit 1
    fi
}

require_command goreleaser
require_command docker

echo "Checking GoReleaser configuration..."
goreleaser check

echo "Building local snapshot release..."
goreleaser release --snapshot --clean

if [[ ! -f dist/artifacts.json ]]; then
    echo "error: GoReleaser did not produce dist/artifacts.json" >&2
    exit 1
fi

SOURCE_IMAGE="ghcr.io/openchami/firmware-updater:latest-amd64"
if ! docker image inspect "$SOURCE_IMAGE" >/dev/null 2>&1; then
    echo "error: local AMD64 image was not found: $SOURCE_IMAGE" >&2
    echo "Check the Docker artifacts in dist/artifacts.json." >&2
    exit 1
fi

TEST_IMAGE="${IMAGE_NAME}:${TEST_TAG}"
echo "Tagging $SOURCE_IMAGE as $TEST_IMAGE"
docker tag "$SOURCE_IMAGE" "$TEST_IMAGE"

echo "Testing server binary..."
docker run --rm --entrypoint /usr/local/bin/firmware-updater "$TEST_IMAGE" --help >/dev/null

echo "Testing release files..."
docker run --rm --entrypoint /bin/bash "$TEST_IMAGE" -c '
    set -eu
    test -x /usr/local/bin/secret-cli
    test -x /usr/local/bin/docker-entrypoint.sh
    test -s /home/firmware/device-profiles/crayex.yaml
    test -s /home/firmware/device-profiles/ilo.yaml
'

echo "Release test passed: $TEST_IMAGE"
echo "Run it with: docker run --rm -p 8080:8080 $TEST_IMAGE"

if [[ "$KEEP_DIST" != "true" ]]; then
    rm -rf dist
fi