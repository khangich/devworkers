#!/usr/bin/env bash
set -euo pipefail

if [[ "$(uname)" != "Darwin" ]]; then
  echo "DevAgent installer only supports macOS" >&2
  exit 1
fi

REPO_URL="${DEVAGENT_REPO:-https://github.com/<me>/devagent}"
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
SRC_DIR="$(find "$TMP_DIR" -mindepth 1 -maxdepth 1 -type d | head -n1)"

if ! command -v go >/dev/null 2>&1; then
  echo "Go toolchain is required to build DevAgent" >&2
  exit 1
fi

echo "Building binary..."
(cd "$SRC_DIR" && GOOS=darwin GOARCH=arm64 go build -o "$TMP_DIR/devagent-arm64" ./cmd/devagent)
(cd "$SRC_DIR" && GOOS=darwin GOARCH=amd64 go build -o "$TMP_DIR/devagent-amd64" ./cmd/devagent)

# Prefer native architecture when possible
TARGET="$TMP_DIR/devagent"
ARCH="$(uname -m)"
if [[ "$ARCH" == "arm64" ]]; then
  cp "$TMP_DIR/devagent-arm64" "$TARGET"
else
  cp "$TMP_DIR/devagent-amd64" "$TARGET"
fi
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
