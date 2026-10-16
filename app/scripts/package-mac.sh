#!/bin/sh
# Builds out/sshgate.app for this Mac: the mygo app, the sshgate binary (the hub
# and the MCP bridge), the Touch ID helper and the libghostty-vt built from
# source, ad-hoc signed (no hardened runtime or notarization, as the Electron
# bundle). `mygo build` is not used: it ships egoist's prebuilt libghostty-vt,
# not ours, and cannot embed the hash of the copy the app must load.
#
# Needs, built first: build/natives/libghostty-vt.dylib and .sha256 (run
# ../scripts/build-ghostty-vt.sh build/natives). The Touch ID helper is built here.
# The version is mygo.json's: it is the app's (CFBundleShortVersionString, so
# mygo.App.Version()) and the one `sshgate --version` prints. Run it on a
# Mac with Go, Xcode's swiftc and codesign; it never launches the app.
# Quit sshgate first if you copy the result over /Applications/sshgate.app.
set -eu
cd "$(dirname "$0")/.."
app=$(pwd)
repo=$(cd .. && pwd)
out=$app/out/sshgate.app

version=$(sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' mygo.json | head -1)
identifier=$(sed -n 's/.*"identifier": *"\([^"]*\)".*/\1/p' mygo.json | head -1)
[ -n "$version" ] && [ -n "$identifier" ] || { echo "mygo.json has no version or identifier" >&2; exit 1; }
lib=build/natives/libghostty-vt.dylib
[ -f "$lib" ] && [ -f build/natives/libghostty-vt.sha256 ] || { echo "build $lib first: ../scripts/build-ghostty-vt.sh build/natives" >&2; exit 1; }
sha=$(tr -d ' \n' < build/natives/libghostty-vt.sha256)
[ "$(shasum -a 256 "$lib" | cut -d' ' -f1)" = "$sha" ] || { echo "$lib does not match build/natives/libghostty-vt.sha256" >&2; exit 1; }
icon=$repo/desktop/build/icon.icns
[ -f "$icon" ] || { echo "no $icon" >&2; exit 1; }

"$repo/desktop/scripts/build-touchid.sh"

rm -rf "$out"
mkdir -p "$out/Contents/MacOS" "$out/Contents/Resources"
# production=1 turns off mygo's dev mode; ghosttySHA binds the app to the library it ships.
CGO_ENABLED=0 go build -trimpath -tags mygo_noinspector \
  -ldflags "-s -w -X github.com/egoist/mygo.production=1 -X main.ghosttySHA=$sha" \
  -o "$out/Contents/MacOS/sshgate" ./cmd/sshgate-app
(cd "$repo" && CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -X github.com/lang315/sshgate/internal/mcpserver.Version=$version" \
  -o "$out/Contents/Resources/sshgate" ./cmd/sshgate)
cp "$repo/sshgate-touchid" "$out/Contents/Resources/sshgate-touchid"
cp "$lib" "$out/Contents/Resources/libghostty-vt.dylib" # untouched: re-signing it would change the hash the app checks
cp "$icon" "$out/Contents/Resources/AppIcon.icns"

plist=$out/Contents/Info.plist
/usr/libexec/PlistBuddy \
  -c "Add :CFBundleName string sshgate" \
  -c "Add :CFBundleDisplayName string sshgate" \
  -c "Add :CFBundleIdentifier string $identifier" \
  -c "Add :CFBundleExecutable string sshgate" \
  -c "Add :CFBundlePackageType string APPL" \
  -c "Add :CFBundleVersion string $version" \
  -c "Add :CFBundleShortVersionString string $version" \
  -c "Add :CFBundleIconFile string AppIcon" \
  -c "Add :LSMinimumSystemVersion string 12.0" \
  -c "Add :NSPrincipalClass string NSApplication" \
  -c "Add :NSHighResolutionCapable bool true" \
  "$plist"
codesign --force --sign - "$out"

# What the bundle must hold, checked here so a bad one is never installed.
codesign --verify --deep --strict "$out"
for f in MacOS/sshgate Resources/sshgate Resources/sshgate-touchid Resources/libghostty-vt.dylib Resources/AppIcon.icns Info.plist; do
  [ -f "$out/Contents/$f" ] || { echo "missing $f" >&2; exit 1; }
done
[ "$(shasum -a 256 "$out/Contents/Resources/libghostty-vt.dylib" | cut -d' ' -f1)" = "$sha" ] || { echo "the shipped libghostty-vt changed" >&2; exit 1; }
[ "$("$out/Contents/Resources/sshgate" --version)" = "sshgate $version" ] || { echo "sshgate --version is not $version" >&2; exit 1; }
[ "$(/usr/libexec/PlistBuddy -c "Print :CFBundleShortVersionString" "$plist")" = "$version" ] || { echo "the plist version is not $version" >&2; exit 1; }
echo "built $out ($version)"
