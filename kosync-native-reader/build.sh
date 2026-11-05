#!/bin/sh
# Build kosync-bridge (static aarch64 Linux binary for the reMarkable) from bridge/.
# Go standard library only; builds in Docker, nothing else to install.
#   ./build.sh            build ./kosync-bridge
#   ./build.sh verify <epub> <pdf> <groundtruth.json>   development check
set -e
cd "$(dirname "$0")"
GO_IMAGE="${GO_IMAGE:-golang:1.25-bookworm}"
run() { docker run --rm -v "$PWD/bridge:/src" -w /src -e GOTOOLCHAIN=local -e GOPROXY=off \
        -e GOFLAGS=-buildvcs=false -e GOCACHE=/tmp/gocache "$@"; }
run -e CGO_ENABLED=0 -e GOOS=linux -e GOARCH=arm64 "$GO_IMAGE" \
    sh -c 'go vet . && go build -trimpath -ldflags="-s -w" -o kosync-bridge.bin .'
mv bridge/kosync-bridge.bin kosync-bridge
chmod +x kosync-bridge
echo "Built $(pwd)/kosync-bridge"
