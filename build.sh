#!/bin/sh
# Cross-compile uz801d for the targets used by MSM8916 / OpenStick.
set -eu

cd "$(dirname "$0")"
VERSION=${1:-0.1.0}
COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ)
GO=${GO:-go}
LDFLAGS="-s -w -X uz801d/internal/buildinfo.Version=${VERSION} -X uz801d/internal/buildinfo.Commit=${COMMIT} -X uz801d/internal/buildinfo.BuildTime=${BUILD_TIME}"

mkdir -p dist
export CGO_ENABLED=0

echo "==> linux/arm64"
GOOS=linux GOARCH=arm64 $GO build -trimpath -ldflags "$LDFLAGS" -o dist/uz801d-linux-arm64 ./cmd/uz801d

echo "==> linux/arm (armv7)"
GOOS=linux GOARCH=arm GOARM=7 $GO build -trimpath -ldflags "$LDFLAGS" -o dist/uz801d-linux-armv7 ./cmd/uz801d

echo "==> linux/amd64"
GOOS=linux GOARCH=amd64 $GO build -trimpath -ldflags "$LDFLAGS" -o dist/uz801d-linux-amd64 ./cmd/uz801d

echo "==> windows/amd64 (smoke test only)"
GOOS=windows GOARCH=amd64 $GO build -trimpath -ldflags "$LDFLAGS" -o dist/uz801d-windows-amd64.exe ./cmd/uz801d

echo "==> 完成"
ls -lh dist
