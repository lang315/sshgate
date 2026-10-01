#!/bin/sh
# Rebuilds build/icon.png (1024) and build/icon.icns from
# build/icon-source.png. The outputs are committed; run this only after
# changing the source image. Needs macOS (iconutil) and python3 with Pillow.
# The source is a square image of a shield, centred, on a flat background
# (the shield about 82% of the height). Everything outside the shield is
# painted with the background colour (the source carries a generator
# watermark in one corner), then the image is placed on the macOS icon grid:
# an 824 px rounded square on a transparent 1024 px canvas.
set -eu
cd "$(dirname "$0")/../build"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir "$tmp/icon.iconset"

python3 - "$tmp" <<'PY'
import statistics
import sys

from PIL import Image, ImageChops, ImageDraw

tmp = sys.argv[1]
src = Image.open("icon-source.png").convert("RGB")
w, h = src.size
if w != h:
    sys.exit(f"icon-source.png must be square, got {w}x{h}")

# The background is noisy, so take the median of a corner patch, not one pixel.
patch = list(src.crop((0, 0, w // 16, h // 16)).getdata())
bg = tuple(int(statistics.median(p[c] for p in patch)) for c in range(3))
flat = Image.new("RGB", src.size, bg)

# The shield is the only thing far from the background colour. A box that is
# off centre means something else got in (the watermark, a new background).
box = ImageChops.difference(src, flat).convert("L").point(lambda v: 255 if v > 90 else 0).getbbox()
if box is None:
    sys.exit("icon-source.png: nothing stands out from the background")
if abs(box[0] + box[2] - w) > w * 0.04 or abs(box[1] + box[3] - h) > h * 0.04:
    sys.exit(f"icon-source.png: the shield is not centred (found {box} in {w}x{h})")
pad = w // 100
box = (max(box[0] - pad, 0), max(box[1] - pad, 0), min(box[2] + pad, w), min(box[3] + pad, h))
flat.paste(src.crop(box), box)

body = flat.resize((824, 824), Image.LANCZOS).convert("RGBA")
# Drawn at 4x and scaled down, since Pillow's rounded_rectangle is not antialiased.
mask = Image.new("L", (824 * 4, 824 * 4), 0)
ImageDraw.Draw(mask).rounded_rectangle((0, 0, 824 * 4 - 1, 824 * 4 - 1), radius=185 * 4, fill=255)
body.putalpha(mask.resize((824, 824), Image.LANCZOS))

icon = Image.new("RGBA", (1024, 1024), (0, 0, 0, 0))
icon.paste(body, (100, 100))
icon.save(f"{tmp}/icon.png")
for s in (16, 32, 128, 256, 512):
    icon.resize((s, s), Image.LANCZOS).save(f"{tmp}/icon.iconset/icon_{s}x{s}.png")
    icon.resize((s * 2, s * 2), Image.LANCZOS).save(f"{tmp}/icon.iconset/icon_{s}x{s}@2x.png")
PY

iconutil -c icns "$tmp/icon.iconset" -o "$tmp/icon.icns"
mv "$tmp/icon.png" "$tmp/icon.icns" .
echo "built build/icon.png, build/icon.icns"
