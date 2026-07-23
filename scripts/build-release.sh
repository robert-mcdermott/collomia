#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
DIST="$ROOT/dist"
VERSION_FILE="$ROOT/VERSION"
SKIP_TESTS=0
CLEAN=0
STAGE=""

usage() {
  cat <<'USAGE'
Usage: scripts/build-release.sh [--skip-tests] [--clean]

Runs the Go test suite and cross-compiles Collomia for macOS, Linux, and
Windows on AMD64 and ARM64. Artifacts and checksums are published to dist/
only after every target builds successfully.

Options:
  --skip-tests  Skip go test ./... (for an already-qualified CI release job).
  --clean       Remove stale release assets after the new build succeeds.
  -h, --help    Show this help.

Environment:
  COLLO_VERSION     Embedded version. Defaults to VERSION.
  COLLO_COMMIT      Embedded commit. Defaults to the current Git commit.
  COLLO_BUILD_DATE  Embedded RFC 3339 date. Defaults to the commit date.
USAGE
}

error() {
  printf 'Error: %s\n' "$*" >&2
  exit 1
}

cleanup() {
  if [ -n "${STAGE:-}" ] && [ -d "$STAGE" ]; then
    rm -rf "$STAGE"
  fi
}

trap cleanup 0
trap 'exit 1' HUP INT TERM

while [ "$#" -gt 0 ]; do
  case "$1" in
    --skip-tests)
      SKIP_TESTS=1
      ;;
    --clean)
      CLEAN=1
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      error "unknown argument: $1"
      ;;
  esac
  shift
done

command -v go >/dev/null 2>&1 || error "go is required"
command -v grep >/dev/null 2>&1 || error "grep is required"
[ -f "$VERSION_FILE" ] || error "missing version file: $VERSION_FILE"

FILE_VERSION=$(tr -d '[:space:]' < "$VERSION_FILE")
printf '%s\n' "$FILE_VERSION" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?(\+[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$' ||
  error "VERSION must contain a semantic version such as v0.2.0 or v0.2.0-beta.1 (got $FILE_VERSION)"

VERSION=${COLLO_VERSION:-$FILE_VERSION}
case "$VERSION" in
  dev|ci) ;;
  *)
    printf '%s\n' "$VERSION" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?(\+[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$' ||
      error "invalid COLLO_VERSION: $VERSION"
    ;;
esac

if [ -n "${COLLO_COMMIT:-}" ]; then
  COMMIT=$COLLO_COMMIT
else
  COMMIT=$(git -C "$ROOT" rev-parse --short=12 HEAD 2>/dev/null || printf unknown)
  if [ "$COMMIT" != unknown ] && ! git -C "$ROOT" diff --quiet --ignore-submodules HEAD --; then
    COMMIT="$COMMIT-dirty"
  fi
fi

if [ -n "${COLLO_BUILD_DATE:-}" ]; then
  DATE=$COLLO_BUILD_DATE
else
  DATE=$(git -C "$ROOT" show -s --format=%cI HEAD 2>/dev/null || date -u +%Y-%m-%dT%H:%M:%SZ)
fi
case "$VERSION$COMMIT$DATE" in
  *' '*) error "release metadata must not contain spaces" ;;
esac

if [ "$SKIP_TESTS" -eq 0 ]; then
  printf '%s\n' '==> Running tests'
  (cd "$ROOT" && go test -count=1 ./...)
fi

mkdir -p "$DIST"
STAGE=$(mktemp -d "$DIST/.build.XXXXXX") || error "could not create release staging directory"

LDFLAGS="-s -w -X github.com/robert-mcdermott/collomia/internal/version.Version=$VERSION -X github.com/robert-mcdermott/collomia/internal/version.Commit=$COMMIT -X github.com/robert-mcdermott/collomia/internal/version.Date=$DATE"

printf '==> Version: %s\n' "$VERSION"
printf '==> Commit: %s\n' "$COMMIT"
printf '==> Build date: %s\n' "$DATE"

for target in darwin/arm64 darwin/amd64 linux/arm64 linux/amd64 windows/arm64 windows/amd64; do
  os=${target%/*}
  arch=${target#*/}
  suffix=""
  if [ "$os" = windows ]; then suffix=.exe; fi
  output="$STAGE/collo-$os-$arch$suffix"
  printf '==> Building collo-%s-%s%s\n' "$os" "$arch" "$suffix"
  (cd "$ROOT" && CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "$LDFLAGS" -o "$output" ./cmd/collo)
done

(
  cd "$STAGE"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum collo-* > checksums.txt
  else
    shasum -a 256 collo-* > checksums.txt
  fi
)

if [ "$CLEAN" -eq 1 ]; then
  rm -f "$DIST"/collo-* "$DIST/checksums.txt" "$DIST/collomia.cdx.json"
fi
mv "$STAGE"/collo-* "$DIST"/
mv "$STAGE/checksums.txt" "$DIST/checksums.txt"
rm -rf "$STAGE"
STAGE=""

printf '==> Wrote release binaries to %s\n' "$DIST"
printf '==> Checksums: %s/checksums.txt\n' "$DIST"
