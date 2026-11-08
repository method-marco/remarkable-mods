#!/bin/sh
# Build sleepscreen-library (static aarch64 Linux binary for the reMarkable) from
# library/. Go standard library only; builds in Docker, nothing else to install.
#   ./build.sh           build ./sleepscreen-library
#   ./build.sh darwin    also build ./sleepscreen-library-darwin (testing on a Mac)
set -e
cd "$(dirname "$0")"
GO_IMAGE="${GO_IMAGE:-golang:1.25-bookworm}"
run() { docker run --rm -v "$PWD/library:/src" -w /src -e GOTOOLCHAIN=local -e GOPROXY=off \
        -e GOFLAGS=-buildvcs=false -e GOCACHE=/tmp/gocache -e CGO_ENABLED=0 "$@"; }
[ -f library/go.mod ] || printf 'module sleepscreen-library\n\ngo 1.25\n' > library/go.mod
run -e GOOS=linux -e GOARCH=arm64 "$GO_IMAGE" \
    sh -c 'go vet . && go build -trimpath -ldflags="-s -w" -o sleepscreen-library.bin .'
mv library/sleepscreen-library.bin sleepscreen-library
chmod +x sleepscreen-library
echo "Built $(pwd)/sleepscreen-library"
if [ "$1" = darwin ]; then
    run -e GOOS=darwin -e GOARCH=arm64 "$GO_IMAGE" go build -o sleepscreen-library-darwin.bin .
    mv library/sleepscreen-library-darwin.bin sleepscreen-library-darwin
    echo "Built $(pwd)/sleepscreen-library-darwin"
fi
