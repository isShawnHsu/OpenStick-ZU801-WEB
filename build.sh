#!/bin/sh
# Cross-compile msm8916d for the targets used by MSM8916 / OpenStick.
set -eu

cd "$(dirname "$0")"
VERSION=${1:-0.1.0}
COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ)
GO=${GO:-go}
LDFLAGS="-s -w -X msm8916d/internal/buildinfo.Version=${VERSION} -X msm8916d/internal/buildinfo.Commit=${COMMIT} -X msm8916d/internal/buildinfo.BuildTime=${BUILD_TIME}"

mkdir -p dist
export CGO_ENABLED=0

echo "==> linux/arm64"
GOOS=linux GOARCH=arm64 $GO build -trimpath -ldflags "$LDFLAGS" -o dist/msm8916d-linux-arm64 ./cmd/msm8916d

echo "==> linux/arm (armv7)"
GOOS=linux GOARCH=arm GOARM=7 $GO build -trimpath -ldflags "$LDFLAGS" -o dist/msm8916d-linux-armv7 ./cmd/msm8916d

echo "==> linux/amd64"
GOOS=linux GOARCH=amd64 $GO build -trimpath -ldflags "$LDFLAGS" -o dist/msm8916d-linux-amd64 ./cmd/msm8916d

echo "==> windows/amd64 (smoke test only)"
GOOS=windows GOARCH=amd64 $GO build -trimpath -ldflags "$LDFLAGS" -o dist/msm8916d-windows-amd64.exe ./cmd/msm8916d

echo "==> 完成"
ls -lh dist
