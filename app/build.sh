#!/bin/bash
# Build AriadneMonitor.app from main.swift and install to ~/Applications.
set -euo pipefail
cd "$(dirname "$0")"

APP="AriadneMonitor"
DEST="$HOME/Applications/$APP.app"
BUILD="./build"
rm -rf "$BUILD"; mkdir -p "$BUILD"

echo "==> compiling"
swiftc -O -parse-as-library -o "$BUILD/$APP" main.swift

echo "==> bundling"
C="$DEST/Contents"
rm -rf "$DEST"; mkdir -p "$C/MacOS"
cp "$BUILD/$APP" "$C/MacOS/$APP"
cat > "$C/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleName</key><string>$APP</string>
  <key>CFBundleDisplayName</key><string>Ariadne Monitor</string>
  <key>CFBundleIdentifier</key><string>com.ariadne.monitor</string>
  <key>CFBundleExecutable</key><string>$APP</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>1.0</string>
  <key>LSMinimumSystemVersion</key><string>13.0</string>
  <key>LSUIElement</key><true/>
</dict></plist>
PLIST

echo "==> codesign (ad-hoc)"
codesign --force --sign - "$DEST"

# notch-overflow fix: seat the item in the right-most visible slot (see guardian lesson)
defaults write com.ariadne.monitor "NSStatusItem Preferred Position AriadneMonitor" -float 0 2>/dev/null || true

echo "==> installed: $DEST"
