#!/usr/bin/env bash
# Builds libghostty-vt from Ghostty's sources at the commit mygo's terminal
# plugin binds, ReleaseSafe (bounds and overflow checks kept), with the Zig
# Ghostty asks for. Writes <outdir>/libghostty-vt.{dylib,so} and its
# SHA-256. The build is not bit-reproducible (absolute paths in the debug
# map; the linker version): the packaging job embeds the hash of the copy
# it ships, and egoist's prebuilt library is never used.
set -euo pipefail

GHOSTTY_COMMIT=befcdfd2c3a1cb24d9ec886e93c95b2b5daa7028
ZIG_VERSION=0.16.0
out=$(mkdir -p "${1:?usage: build-ghostty-vt.sh <outdir>}" && cd "$1" && pwd)
work=${GHOSTTY_WORK:-$out/.work}
mkdir -p "$work"

case "$(uname -s)-$(uname -m)" in
  Darwin-arm64)  zplat=aarch64-macos;  zsum=b23d70deaa879b5c2d486ed3316f7eaa53e84acf6fc9cc747de152450d401489; lib=libghostty-vt.dylib ;;
  Darwin-x86_64) zplat=x86_64-macos;   zsum=0387557ed1877bc6a2e1802c8391953baddba76081876301c522f52977b52ba7; lib=libghostty-vt.dylib ;;
  Linux-x86_64)  zplat=x86_64-linux;   zsum=70e49664a74374b48b51e6f3fdfbf437f6395d42509050588bd49abe52ba3d00; lib=libghostty-vt.so ;;
  Linux-aarch64) zplat=aarch64-linux;  zsum=ea4b09bfb22ec6f6c6ceac57ab63efb6b46e17ab08d21f69f3a48b38e1534f17; lib=libghostty-vt.so ;;
  *) echo "unsupported platform $(uname -s)-$(uname -m)" >&2; exit 1 ;;
esac

zig="$work/zig-$zplat-$ZIG_VERSION/zig"
if [ ! -x "$zig" ]; then
  tarball="zig-$zplat-$ZIG_VERSION.tar.xz"
  curl -fsSL -o "$work/$tarball" "https://ziglang.org/download/$ZIG_VERSION/$tarball"
  echo "$zsum  $work/$tarball" | shasum -a 256 -c -
  tar -xJf "$work/$tarball" -C "$work"
fi

src="$work/ghostty"
if [ "$(git -C "$src" rev-parse HEAD 2>/dev/null || true)" != "$GHOSTTY_COMMIT" ]; then
  rm -rf "$src" && mkdir -p "$src"
  git -C "$src" init -q
  git -C "$src" fetch -q --depth 1 https://github.com/ghostty-org/ghostty "$GHOSTTY_COMMIT"
  git -C "$src" checkout -q FETCH_HEAD
fi

prefix="$work/prefix"
rm -rf "$prefix"
(cd "$src" && ZIG_GLOBAL_CACHE_DIR="$work/zig-cache" "$zig" build -Demit-lib-vt -Doptimize=ReleaseSafe --prefix "$prefix")
cp -L "$prefix/lib/$lib" "$out/$lib"
shasum -a 256 "$out/$lib" | cut -d' ' -f1 > "$out/libghostty-vt.sha256"
echo "MYGO_GHOSTTY_VT=$out/$lib"
