#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
TEST_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/collo-installer-test.XXXXXX")
MOCK_BIN="$TEST_ROOT/bin"
ASSETS="$TEST_ROOT/assets"
INSTALL_DIR="$TEST_ROOT/install"
DOWNLOAD_LOG="$TEST_ROOT/downloads.log"

cleanup() {
  rm -rf "$TEST_ROOT"
}
trap cleanup 0
trap 'exit 1' HUP INT TERM

mkdir -p "$MOCK_BIN" "$ASSETS" "$INSTALL_DIR"

cat > "$MOCK_BIN/uname" <<'EOF'
#!/bin/sh
case "${1:-}" in
  -s) printf '%s\n' "${COLLO_INSTALL_TEST_OS:-Linux}" ;;
  -m) printf '%s\n' "${COLLO_INSTALL_TEST_ARCH:-x86_64}" ;;
  *) exit 2 ;;
esac
EOF

cat > "$MOCK_BIN/curl" <<'EOF'
#!/bin/sh
set -eu
output=""
url=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --proto|--proto-redir|--retry|--connect-timeout|--output)
      [ "$#" -ge 2 ] || exit 2
      if [ "$1" = --output ]; then output=$2; fi
      shift 2
      ;;
    --tlsv1.2|--fail|--silent|--show-error|--location)
      shift
      ;;
    *)
      url=$1
      shift
      ;;
  esac
done
[ -n "$output" ] && [ -n "$url" ] || exit 2
printf '%s\n' "$url" >> "$COLLO_INSTALL_TEST_LOG"
cp "$COLLO_INSTALL_TEST_ASSETS/${url##*/}" "$output"
EOF

cat > "$ASSETS/collo-linux-amd64" <<'EOF'
#!/bin/sh
if [ "${1:-}" = --version ]; then
  printf '%s\n' 'collo v0.1.3 (fixture, 2026-07-22T00:00:00Z)'
  exit 0
fi
exit 2
EOF

chmod +x "$MOCK_BIN/uname" "$MOCK_BIN/curl" "$ASSETS/collo-linux-amd64"

checksum() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{ print $1 }'
  else
    shasum -a 256 "$1" | awk '{ print $1 }'
  fi
}

GOOD_HASH=$(checksum "$ASSETS/collo-linux-amd64")
printf '%s  %s\n' "$GOOD_HASH" collo-linux-amd64 > "$ASSETS/checksums.txt"

PATH="$MOCK_BIN:$INSTALL_DIR:$PATH" \
COLLO_INSTALL_TEST_ASSETS="$ASSETS" \
COLLO_INSTALL_TEST_LOG="$DOWNLOAD_LOG" \
COLLO_INSTALL_DIR="$INSTALL_DIR" \
COLLO_REPOSITORY=example/collomia \
  "$ROOT/install.sh" >/dev/null

[ -x "$INSTALL_DIR/collo" ] || {
  printf '%s\n' 'installer did not publish an executable' >&2
  exit 1
}
grep -Fq 'https://github.com/example/collomia/releases/latest/download/collo-linux-amd64' "$DOWNLOAD_LOG"

: > "$DOWNLOAD_LOG"
PATH="$MOCK_BIN:$INSTALL_DIR:$PATH" \
COLLO_INSTALL_TEST_ASSETS="$ASSETS" \
COLLO_INSTALL_TEST_LOG="$DOWNLOAD_LOG" \
  "$ROOT/install.sh" --version 0.1.3 --install-dir "$INSTALL_DIR" >/dev/null
grep -Fq 'https://github.com/robert-mcdermott/collomia/releases/download/v0.1.3/collo-linux-amd64' "$DOWNLOAD_LOG"

cat > "$ASSETS/collo-linux-amd64" <<'EOF'
#!/bin/sh
if [ "${1:-}" = --version ]; then
  printf '%s\n' 'collo v0.1.4 (fixture, 2026-07-23T00:00:00Z)'
  exit 0
fi
exit 2
EOF
chmod +x "$ASSETS/collo-linux-amd64"
UPGRADE_HASH=$(checksum "$ASSETS/collo-linux-amd64")
printf '%s  %s\n' "$UPGRADE_HASH" collo-linux-amd64 > "$ASSETS/checksums.txt"
PATH="$MOCK_BIN:$INSTALL_DIR:$PATH" \
COLLO_INSTALL_TEST_ASSETS="$ASSETS" \
COLLO_INSTALL_TEST_LOG="$DOWNLOAD_LOG" \
  "$ROOT/install.sh" --version v0.1.4 --install-dir "$INSTALL_DIR" >/dev/null
"$INSTALL_DIR/collo" --version | grep -Fq 'collo v0.1.4 ('

cat > "$ASSETS/collo-linux-amd64" <<'EOF'
#!/bin/sh
if [ "${1:-}" = --version ]; then
  printf '%s\n' 'collo v0.1.3 (fixture, 2026-07-22T00:00:00Z)'
  exit 0
fi
exit 2
EOF
chmod +x "$ASSETS/collo-linux-amd64"
GOOD_HASH=$(checksum "$ASSETS/collo-linux-amd64")
printf '%s  %s\n' "$GOOD_HASH" collo-linux-amd64 > "$ASSETS/checksums.txt"
PATH="$MOCK_BIN:$INSTALL_DIR:$PATH" \
COLLO_INSTALL_TEST_ASSETS="$ASSETS" \
COLLO_INSTALL_TEST_LOG="$DOWNLOAD_LOG" \
  "$ROOT/install.sh" --version v0.1.3 --install-dir "$INSTALL_DIR" >/dev/null
"$INSTALL_DIR/collo" --version | grep -Fq 'collo v0.1.3 ('

BEFORE=$(checksum "$INSTALL_DIR/collo")
printf '%064d  %s\n' 0 collo-linux-amd64 > "$ASSETS/checksums.txt"
if PATH="$MOCK_BIN:$INSTALL_DIR:$PATH" \
  COLLO_INSTALL_TEST_ASSETS="$ASSETS" \
  COLLO_INSTALL_TEST_LOG="$DOWNLOAD_LOG" \
  COLLO_INSTALL_DIR="$INSTALL_DIR" \
  "$ROOT/install.sh" >/dev/null 2>&1; then
  printf '%s\n' 'installer accepted a bad checksum' >&2
  exit 1
fi
AFTER=$(checksum "$INSTALL_DIR/collo")
[ "$BEFORE" = "$AFTER" ] || {
  printf '%s\n' 'failed upgrade replaced the existing binary' >&2
  exit 1
}

printf '%s  %s\n%s *%s\n' "$GOOD_HASH" collo-linux-amd64 "$GOOD_HASH" collo-linux-amd64 > "$ASSETS/checksums.txt"
if PATH="$MOCK_BIN:$INSTALL_DIR:$PATH" \
  COLLO_INSTALL_TEST_ASSETS="$ASSETS" \
  COLLO_INSTALL_TEST_LOG="$DOWNLOAD_LOG" \
  COLLO_INSTALL_DIR="$INSTALL_DIR" \
  "$ROOT/install.sh" >/dev/null 2>&1; then
  printf '%s\n' 'installer accepted duplicate checksum entries' >&2
  exit 1
fi

printf '%s\n' 'shell installer tests passed'
