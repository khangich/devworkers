#!/usr/bin/env bash
set -euo pipefail

if [[ "$(uname)" != "Darwin" ]]; then
  echo "DevAgent uninstall only supports macOS" >&2
  exit 1
fi

INSTALL_DIR="/usr/local/bin"
if [[ ! -w "$INSTALL_DIR" && -x /opt/homebrew/bin/devagent ]]; then
  INSTALL_DIR="/opt/homebrew/bin"
fi

PLIST="$HOME/Library/LaunchAgents/com.llmlab.devagent.plist"
if [[ -f "$PLIST" ]]; then
  launchctl unload "$PLIST" >/dev/null 2>&1 || true
  rm -f "$PLIST"
fi

if [[ -f "$INSTALL_DIR/devagent" ]]; then
  rm -f "$INSTALL_DIR/devagent"
fi

STATE_DIR="$HOME/.devagent"
if [[ -d "$STATE_DIR" ]]; then
  echo "State preserved at $STATE_DIR"
fi

echo "DevAgent uninstalled"
