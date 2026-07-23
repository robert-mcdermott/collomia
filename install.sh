#!/bin/sh
set -eu
umask 022

REPOSITORY=${COLLO_REPOSITORY:-robert-mcdermott/collomia}
VERSION=${COLLO_VERSION:-latest}
INSTALL_DIR=${COLLO_INSTALL_DIR:-}
TMP_DIR=""
DEST_TMP=""

info() {
  printf '==> %s\n' "$*"
}

warn() {
  printf 'Warning: %s\n' "$*" >&2
}

error() {
  printf 'Error: %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<'USAGE'
Install the latest stable Collomia release for macOS or Linux.

Usage:
  install.sh [--version VERSION] [--install-dir DIRECTORY]

Options:
  --version VERSION       Release to install, such as v0.1.3. Defaults to latest.
  --install-dir DIRECTORY Installation directory. Defaults to $HOME/.local/bin.
  -h, --help              Show this help.

Environment:
  COLLO_VERSION           Same as --version.
  COLLO_INSTALL_DIR       Same as --install-dir.
  COLLO_REPOSITORY        GitHub owner/repository. Defaults to the official repo.

The installer downloads the matching raw binary and checksums.txt from GitHub,
verifies SHA-256, and atomically installs the binary as collo. It does not use
sudo, modify PATH, create Collomia data, or start Collomia.
USAGE
}

has_command() {
  command -v "$1" >/dev/null 2>&1
}

cleanup() {
  if [ -n "${DEST_TMP:-}" ] && [ -e "$DEST_TMP" ]; then
    rm -f "$DEST_TMP"
  fi
  if [ -n "${TMP_DIR:-}" ] && [ -d "$TMP_DIR" ]; then
    rm -rf "$TMP_DIR"
  fi
}

trap cleanup 0
trap 'exit 1' HUP INT TERM

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version)
      [ "$#" -ge 2 ] || error "--version requires a value"
      VERSION=$2
      shift 2
      ;;
    --install-dir)
      [ "$#" -ge 2 ] || error "--install-dir requires a value"
      INSTALL_DIR=$2
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      error "unknown argument: $1"
      ;;
  esac
done

if [ -z "$INSTALL_DIR" ]; then
  [ -n "${HOME:-}" ] || error "HOME is not set; provide --install-dir"
  INSTALL_DIR="$HOME/.local/bin"
fi

case "$INSTALL_DIR" in
  /*) ;;
  *) error "installation directory must be an absolute path: $INSTALL_DIR" ;;
esac

case "$REPOSITORY" in
  */*/*|/*|*/|'') error "COLLO_REPOSITORY must use owner/repository syntax" ;;
  */*) ;;
  *) error "COLLO_REPOSITORY must use owner/repository syntax" ;;
esac
case "$REPOSITORY" in
  *[!0-9A-Za-z._/-]*) error "invalid COLLO_REPOSITORY: $REPOSITORY" ;;
esac

has_command grep || error "grep is required"
if [ "$VERSION" != latest ]; then
  case "$VERSION" in
    [0-9]*) VERSION="v$VERSION" ;;
  esac
  printf '%s\n' "$VERSION" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?(\+[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$' ||
    error "invalid release version: $VERSION"
fi

has_command curl || error "curl is required"
has_command uname || error "uname is required"
has_command awk || error "awk is required"
has_command mktemp || error "mktemp is required"

OS_NAME=$(uname -s)
ARCH_NAME=$(uname -m)

case "$OS_NAME" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) error "unsupported operating system: $OS_NAME" ;;
esac

case "$ARCH_NAME" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) error "unsupported architecture: $ARCH_NAME" ;;
esac

ASSET="collo-$os-$arch"
if [ "$VERSION" = latest ]; then
  DOWNLOAD_BASE="https://github.com/$REPOSITORY/releases/latest/download"
  VERSION_LABEL="latest stable release"
else
  DOWNLOAD_BASE="https://github.com/$REPOSITORY/releases/download/$VERSION"
  VERSION_LABEL=$VERSION
fi

TMP_DIR=$(mktemp -d 2>/dev/null || mktemp -d -t collo-install) ||
  error "could not create a temporary directory"
BINARY_PATH="$TMP_DIR/$ASSET"
CHECKSUM_PATH="$TMP_DIR/checksums.txt"

download() {
  source_url=$1
  destination=$2
  curl \
    --proto '=https' \
    --proto-redir '=https' \
    --tlsv1.2 \
    --fail \
    --silent \
    --show-error \
    --location \
    --retry 3 \
    --connect-timeout 15 \
    --output "$destination" \
    "$source_url"
}

info "Installing Collomia $VERSION_LABEL for $OS_NAME/$ARCH_NAME"
info "Downloading $ASSET"
download "$DOWNLOAD_BASE/$ASSET" "$BINARY_PATH" || error "failed to download $ASSET"

info "Downloading checksums.txt"
download "$DOWNLOAD_BASE/checksums.txt" "$CHECKSUM_PATH" || error "failed to download checksums.txt"

EXPECTED=$(
  awk -v filename="$ASSET" '
    $2 == filename || $2 == "*" filename {
      count++
      checksum = $1
    }
    END {
      if (count != 1) exit 1
      print checksum
    }
  ' "$CHECKSUM_PATH"
) || error "checksums.txt does not contain exactly one entry for $ASSET"

[ "${#EXPECTED}" -eq 64 ] || error "invalid SHA-256 value for $ASSET"
case "$EXPECTED" in
  *[!0-9A-Fa-f]*) error "invalid SHA-256 value for $ASSET" ;;
esac

if has_command sha256sum; then
  ACTUAL=$(sha256sum "$BINARY_PATH" | awk '{ print $1 }')
elif has_command shasum; then
  ACTUAL=$(shasum -a 256 "$BINARY_PATH" | awk '{ print $1 }')
else
  error "sha256sum or shasum is required"
fi

EXPECTED=$(printf '%s' "$EXPECTED" | tr '[:upper:]' '[:lower:]')
ACTUAL=$(printf '%s' "$ACTUAL" | tr '[:upper:]' '[:lower:]')
[ "$EXPECTED" = "$ACTUAL" ] || error "checksum verification failed for $ASSET (expected $EXPECTED, got $ACTUAL)"
info "Checksum verified"

if [ ! -d "$INSTALL_DIR" ]; then
  mkdir -p "$INSTALL_DIR" 2>/dev/null ||
    error "cannot create $INSTALL_DIR; choose a writable --install-dir"
fi
[ -w "$INSTALL_DIR" ] || error "cannot write to $INSTALL_DIR; choose a writable --install-dir"

DESTINATION="$INSTALL_DIR/collo"
DEST_TMP=$(mktemp "$INSTALL_DIR/.collo.install.XXXXXX") ||
  error "failed to create a temporary installation file in $INSTALL_DIR"
cp "$BINARY_PATH" "$DEST_TMP" || error "failed to copy binary into $INSTALL_DIR"
chmod 0755 "$DEST_TMP" || error "failed to make the installed binary executable"

INSTALLED_VERSION=$("$DEST_TMP" --version 2>/dev/null) ||
  error "the downloaded binary did not pass its version check"
if [ "$VERSION" != latest ]; then
  case "$INSTALLED_VERSION" in
    "collo $VERSION ("*) ;;
    *) error "downloaded binary reports an unexpected version: $INSTALLED_VERSION" ;;
  esac
fi

mv -f "$DEST_TMP" "$DESTINATION" || error "failed to install $DESTINATION"
DEST_TMP=""

info "$INSTALLED_VERSION"
info "Installed at $DESTINATION"

case ":${PATH:-}:" in
  *:"$INSTALL_DIR":*) RUN_COMMAND=collo ;;
  *)
    warn "$INSTALL_DIR is not currently in PATH"
    warn "Add it to PATH or run $DESTINATION directly"
    RUN_COMMAND=$DESTINATION
    ;;
esac

cat <<EOF

Next steps:
  $RUN_COMMAND init --global --with-reference
  $RUN_COMMAND doctor

Documentation: https://github.com/$REPOSITORY/blob/main/docs/USER_GUIDE.md
EOF
