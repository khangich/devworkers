#!/usr/bin/env bash
set -euo pipefail

if [[ "$(uname)" != "Darwin" ]]; then
  echo "DevAgent installer only supports macOS" >&2
  exit 1
fi

REPO_URL="${DEVAGENT_REPO:-https://github.com/khangich/devworkers}"
INSTALL_DIR="/usr/local/bin"
if [[ ! -w "$INSTALL_DIR" ]]; then
  INSTALL_DIR="/opt/homebrew/bin"
fi
mkdir -p "$INSTALL_DIR"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

echo "Downloading DevAgent sources..."
curl -fsSL "$REPO_URL/archive/refs/heads/main.tar.gz" -o "$TMP_DIR/devagent.tar.gz"
tar -xzf "$TMP_DIR/devagent.tar.gz" -C "$TMP_DIR"

GO_MOD_PATH="$(find "$TMP_DIR" -type f -name go.mod -print -quit || true)"
if [[ -z "$GO_MOD_PATH" ]]; then
  echo "Failed to locate go.mod in downloaded archive" >&2
  exit 1
fi
SRC_DIR="$(dirname "$GO_MOD_PATH")"

if ! command -v go >/dev/null 2>&1; then
  echo "Go toolchain is required to build DevAgent" >&2
  exit 1
fi

ARCH="$(uname -m)"
case "$ARCH" in
  arm64|aarch64)
    GOARCH_TARGET="arm64"
    ;;
  x86_64|amd64)
    GOARCH_TARGET="amd64"
    ;;
  *)
    echo "Unsupported architecture: $ARCH" >&2
    exit 1
    ;;
esac

TARGET="$TMP_DIR/devagent"
echo "Building $GOARCH_TARGET binary..."
(cd "$SRC_DIR" && GOOS=darwin GOARCH="$GOARCH_TARGET" go build -o "$TARGET" ./cmd/devagent)
chmod +x "$TARGET"

BIN_PATH="$INSTALL_DIR/devagent"
cp "$TARGET" "$BIN_PATH"
chmod +x "$BIN_PATH"

echo "Installing LaunchAgent..."
PLIST="$HOME/Library/LaunchAgents/com.llmlab.devagent.plist"
mkdir -p "$(dirname "$PLIST")"
cat >"$PLIST" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.llmlab.devagent</string>
  <key>ProgramArguments</key>
  <array>
    <string>$BIN_PATH</string>
    <string>daemon</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>$HOME/Library/Logs/devagent.log</string>
  <key>StandardErrorPath</key>
  <string>$HOME/Library/Logs/devagent.log</string>
</dict>
</plist>
PLIST

launchctl unload "$PLIST" >/dev/null 2>&1 || true
launchctl load "$PLIST"

echo "DevAgent installed to $BIN_PATH"
echo "Logs available via: log stream --predicate 'process == \"devagent\"'"
