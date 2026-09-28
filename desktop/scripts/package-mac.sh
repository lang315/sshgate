#!/bin/sh
# Builds out/sshgate.app for this Mac: Electron's own bundle with the app in
# Resources/app and the sshgate binary next to it (main.ts finds the hub at
# app.getAppPath()/../sshgate; Claude Code can use the same binary as the
# bridge). Ad-hoc signed only: for the author's own machine, per the roadmap
# (no signing or notarization before slice 5).
# ponytail: this Mac's arch only, no icon; electron-builder when slice 5 needs installers.
set -eu
cd "$(dirname "$0")/.."
version=$(node -p "require('./package.json').version")
out=out/sshgate.app

npm run build
rm -rf "$out"
mkdir -p out
cp -R node_modules/electron/dist/Electron.app "$out"
mkdir "$out/Contents/Resources/app"
cp -R dist package.json "$out/Contents/Resources/app/"
(cd .. && CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -X github.com/lang315/sshgate/internal/mcpserver.Version=$version" \
  -o "desktop/$out/Contents/Resources/sshgate" ./cmd/sshgate)

plist="$out/Contents/Info.plist"
/usr/libexec/PlistBuddy \
  -c "Set :CFBundleName sshgate" \
  -c "Set :CFBundleDisplayName sshgate" \
  -c "Set :CFBundleIdentifier io.github.lang315.sshgate" \
  -c "Set :CFBundleShortVersionString $version" \
  -c "Set :CFBundleVersion $version" \
  "$plist"
codesign --force --deep --sign - "$out"
echo "built $out ($version)"
