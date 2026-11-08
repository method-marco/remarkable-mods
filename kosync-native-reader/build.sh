#!/bin/sh
# Build kosync-bridge (static aarch64 Linux binary for the reMarkable) from bridge/.
# Go standard library only; builds in Docker, nothing else to install.
#   ./build.sh            build ./kosync-bridge
#   ./build.sh --if-needed  only if it's missing or older than bridge/ (deploy.sh)
#   ./build.sh verify <epub> <pdf> <groundtruth.json>   development check
set -e
cd "$(dirname "$0")"
GO_IMAGE="${GO_IMAGE:-golang:1.25-bookworm}"
# --if-needed: only build if the program is missing or older than its source
if [ "$1" = "--if-needed" ]; then
    if [ -f "kosync-bridge" ] && [ -z "$(find bridge build.sh -newer "kosync-bridge" -type f ! -name '*.bin' 2>/dev/null)" ]; then
        exit 0
    fi
    echo "Building kosync-bridge (Go in Docker)..."
    shift
fi
if ! command -v docker >/dev/null 2>&1 || ! docker info >/dev/null 2>&1; then
    echo "Building kosync-bridge needs Docker: install Docker Desktop (or another Docker) and start it."
    exit 1
fi
run() { docker run --rm -v "$PWD/bridge:/src" -w /src -e GOTOOLCHAIN=local -e GOPROXY=off \
        -e GOFLAGS=-buildvcs=false -e GOCACHE=/tmp/gocache "$@"; }
run -e CGO_ENABLED=0 -e GOOS=linux -e GOARCH=arm64 "$GO_IMAGE" \
    sh -c 'go vet . && go build -trimpath -ldflags="-s -w" -o kosync-bridge.bin .'
mv bridge/kosync-bridge.bin kosync-bridge
chmod +x kosync-bridge
echo "Built $(pwd)/kosync-bridge"
