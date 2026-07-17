#!/bin/sh
set -eu

REPOSITORY=${COLLO_REPOSITORY:-robert-mcdermott/collomia}
VERSION=${COLLO_VERSION:-latest}
INSTALL_DIR=${COLLO_INSTALL_DIR:-"$HOME/.local/bin"}

case $(uname -s) in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) printf 'Unsupported operating system: %s\n' "$(uname -s)" >&2; exit 1 ;;
esac

case $(uname -m) in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) printf 'Unsupported architecture: %s\n' "$(uname -m)" >&2; exit 1 ;;
esac

asset="collo-$os-$arch"
if [ "$VERSION" = latest ]; then
  base="https://github.com/$REPOSITORY/releases/latest/download"
else
  base="https://github.com/$REPOSITORY/releases/download/$VERSION"
fi

temporary=$(mktemp -d "${TMPDIR:-/tmp}/collo-install.XXXXXX")
trap 'rm -rf "$temporary"' EXIT HUP INT TERM

curl --proto '=https' --tlsv1.2 -fsSL "$base/$asset" -o "$temporary/$asset"
curl --proto '=https' --tlsv1.2 -fsSL "$base/checksums.txt" -o "$temporary/checksums.txt"

expected=$(awk -v name="$asset" '$2 == name { print $1 }' "$temporary/checksums.txt")
if [ -z "$expected" ]; then
  printf 'No checksum found for %s\n' "$asset" >&2
  exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$temporary/$asset" | awk '{ print $1 }')
else
  actual=$(shasum -a 256 "$temporary/$asset" | awk '{ print $1 }')
fi

if [ "$actual" != "$expected" ]; then
  printf 'Checksum verification failed for %s\n' "$asset" >&2
  exit 1
fi

mkdir -p "$INSTALL_DIR"
chmod 0755 "$temporary/$asset"
mv "$temporary/$asset" "$INSTALL_DIR/collo"
printf 'Installed collo to %s/collo\n' "$INSTALL_DIR"
