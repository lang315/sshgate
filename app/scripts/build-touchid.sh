#!/bin/sh
# Builds the Touch ID helper (spec 2026-10-12-touchid-unlock-design.md) into
# <repo root>/sshgate-touchid, ad-hoc signed: the app finds it there when
# unpackaged, and package-mac.sh copies it into the bundle. macOS only.
set -eu
cd "$(dirname "$0")/../.."
swiftc -O app/native/touchid/main.swift -o sshgate-touchid
codesign --force --sign - sshgate-touchid
echo "built sshgate-touchid"
