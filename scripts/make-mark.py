"""把应用标记（不是字标）画成各尺寸 PNG：深蓝圆角方块 + 青色「画框里有山有太阳」。

画法刻意保持和界面里的 SVG 完全同一套几何（24×24 视图坐标），先按 8 倍超采样画，
再 LANCZOS 缩小 —— 16px 那一档直接画会糊成一块，超采样后能保住框线。

用法：
  python scripts/make-mark.py <输出目录>
输出 mark-16.png … mark-256.png，再用 scripts/make-icon.py 打包成 assets/icon.ico。
"""

from __future__ import annotations

import sys
from pathlib import Path

from PIL import Image, ImageDraw

SIZES = [16, 20, 24, 32, 40, 48, 64, 128, 256]
SUPERSAMPLE = 8

CHIP = (16, 27, 44, 255)      # #101b2c
GLYPH = (79, 209, 224, 255)   # #4fd1e0

# 24×24 视图里的几何，和 AppHeader.vue / index.html 里的 SVG 一一对应
CHIP_RADIUS = 6.5
FRAME = (5.4, 6.8, 18.6, 17.2)  # x0, y0, x1, y1
FRAME_RADIUS = 2.6
FRAME_WIDTH = 1.55
SUN = (9.0, 10.2, 1.25)         # cx, cy, r
MOUNTAIN = [(7.0, 15.7), (10.1, 12.5), (11.9, 14.2), (13.3, 12.8), (16.8, 15.8)]
MOUNTAIN_WIDTH = 1.55


def render(size: int) -> Image.Image:
    s = size * SUPERSAMPLE
    img = Image.new("RGBA", (s, s), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    k = s / 24.0

    d.rounded_rectangle(
        [1.5 * k, 1.5 * k, 22.5 * k, 22.5 * k],
        radius=CHIP_RADIUS * k,
        fill=CHIP,
    )

    x0, y0, x1, y1 = FRAME
    d.rounded_rectangle(
        [x0 * k, y0 * k, x1 * k, y1 * k],
        radius=FRAME_RADIUS * k,
        outline=GLYPH,
        width=max(1, round(FRAME_WIDTH * k)),
    )

    cx, cy, r = SUN
    d.ellipse([(cx - r) * k, (cy - r) * k, (cx + r) * k, (cy + r) * k], fill=GLYPH)

    d.line([(x * k, y * k) for x, y in MOUNTAIN], fill=GLYPH,
           width=max(1, round(MOUNTAIN_WIDTH * k)), joint="curve")
    # 线端补圆点，纯 line 在超采样缩小后会显得尖
    for x, y in (MOUNTAIN[0], MOUNTAIN[-1]):
        rad = MOUNTAIN_WIDTH * k / 2
        d.ellipse([x * k - rad, y * k - rad, x * k + rad, y * k + rad], fill=GLYPH)

    return img.resize((size, size), Image.LANCZOS)


def main() -> int:
    out = Path(sys.argv[1]) if len(sys.argv) > 1 else Path("assets")
    out.mkdir(parents=True, exist_ok=True)
    for size in SIZES:
        render(size).save(out / f"mark-{size}.png")
    print(f"wrote {len(SIZES)} png into {out}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())