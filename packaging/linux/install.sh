#!/bin/sh
# Installs GoProBuddy for the current user (no root needed).
set -e
here=$(cd "$(dirname "$0")" && pwd)
mkdir -p "$HOME/.local/bin" "$HOME/.local/share/applications" "$HOME/.local/share/icons/hicolor/256x256/apps"
install -m 755 "$here/goprobuddy" "$HOME/.local/bin/goprobuddy"
install -m 644 "$here/goprobuddy.png" "$HOME/.local/share/icons/hicolor/256x256/apps/goprobuddy.png"
install -m 644 "$here/goprobuddy.desktop" "$HOME/.local/share/applications/goprobuddy.desktop"
echo "Installed. Start GoProBuddy from your app menu, or run: goprobuddy"
