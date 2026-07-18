#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
DIST="$ROOT/dist"
if [ -n "${COLLO_VERSION:-}" ]; then
  VERSION=$COLLO_VERSION
elif [ -f "$ROOT/VERSION" ]; then
  VERSION=$(tr -d '\r\n' < "$ROOT/VERSION")
else
  VERSION=dev
fi
COMMIT=${COLLO_COMMIT:-$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || printf unknown)}
DATE=${COLLO_BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}
LDFLAGS="-s -w -X github.com/robert-mcdermott/collomia/internal/version.Version=$VERSION -X github.com/robert-mcdermott/collomia/internal/version.Commit=$COMMIT -X github.com/robert-mcdermott/collomia/internal/version.Date=$DATE"

mkdir -p "$DIST"

for target in darwin/arm64 darwin/amd64 linux/arm64 linux/amd64 windows/arm64 windows/amd64; do
  os=${target%/*}
  arch=${target#*/}
  suffix=""
  if [ "$os" = windows ]; then suffix=.exe; fi
  output="$DIST/collo-$os-$arch$suffix"
  printf 'building %s/%s\n' "$os" "$arch"
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "$LDFLAGS" -o "$output" ./cmd/collo
done

cd "$DIST"
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum collo-* > checksums.txt
else
  shasum -a 256 collo-* > checksums.txt
fi
