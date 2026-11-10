"""把应用标记（不是字标）画成各尺寸 PNG：圆角深蓝方块 + 青色「M 形山」+ 暖色太阳。

这套几何和界面标题栏、`web/index.html` 的 favicon 是同一份（24×24 视图坐标），
改一个就要同步另外两个 —— 否则任务栏、标签页、窗口里会出现三个不同的标志。

画法是先在 8 倍尺寸上画，再 LANCZOS 缩小：16px 那一档直接画会把山脊糊成一块，
超采样之后边缘才立得住。

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

# 24×24 视图里的几何，和 AppHeader.vue / index.html 里的 SVG 一一对应
CHIP_BOX = (1.25, 1.25, 22.75, 22.75)
CHIP_RADIUS = 6.2

BG_TOP = (34, 48, 77)        # #22304d
BG_MID = (19, 28, 48)        # #131c30
BG_BOTTOM = (10, 15, 28)     # #0a0f1c

RIDGE_LEFT = (27, 142, 166)   # #1b8ea6
RIDGE_MID = (63, 212, 232)    # #3fd4e8
RIDGE_RIGHT = (200, 248, 255)  # #c8f8ff

SUN = (17.15, 6.95, 1.62)     # cx, cy, r
SUN_COLOR = (255, 217, 168)   # #ffd9a8

# M 形山：两个峰、一个深谷，底下贴住方块的下沿
MOUNTAIN = [(3.4, 19.3), (7.6, 11.0), (12.0, 15.4), (16.6, 10.6), (20.6, 19.3)]
HORIZON_Y = 19.3
HORIZON_COLOR = (255, 255, 255, 40)
HORIZON_WIDTH = 1.15

INNER_STROKE = (255, 255, 255, 24)
INNER_STROKE_WIDTH = 0.9


def lerp(a: tuple[int, int, int], b: tuple[int, int, int], t: float) -> tuple[int, int, int, int]:
    return (
        round(a[0] + (b[0] - a[0]) * t),
        round(a[1] + (b[1] - a[1]) * t),
        round(a[2] + (b[2] - a[2]) * t),
        255,
    )


def gradient(size: int, stops: list[tuple[float, tuple[int, int, int]]],
             p0: tuple[float, float], p1: tuple[float, float]) -> Image.Image:
    """沿 p0→p1 的线性渐变（24×24 坐标），stops 是 (位置, 颜色)。"""
    img = Image.new("RGBA", (size, size))
    px = img.load()
    x0, y0 = p0[0] / 24.0 * size, p0[1] / 24.0 * size
    x1, y1 = p1[0] / 24.0 * size, p1[1] / 24.0 * size
    dx, dy = x1 - x0, y1 - y0
    denom = dx * dx + dy * dy or 1.0
    for y in range(size):
        for x in range(size):
            t = ((x - x0) * dx + (y - y0) * dy) / denom
            t = min(1.0, max(0.0, t))
            # 在 stops 之间插值
            for i in range(len(stops) - 1):
                a, ca = stops[i]
                b, cb = stops[i + 1]
                if a <= t <= b or i == len(stops) - 2:
                    local = 0.0 if b <= a else (t - a) / (b - a)
                    px[x, y] = lerp(ca, cb, min(1.0, max(0.0, local)))
                    break
    return img


def radial_glow(size: int, center: tuple[float, float], radius: float,
                color: tuple[int, int, int], alpha: int) -> Image.Image:
    img = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    steps = 48
    cx, cy = center[0] / 24.0 * size, center[1] / 24.0 * size
    r = radius / 24.0 * size
    for i in range(steps, 0, -1):
        t = i / steps
        a = round(alpha * (1.0 - t) ** 2)
        rr = r * t
        d.ellipse([cx - rr, cy - rr, cx + rr, cy + rr], fill=(*color, a))
    return img


def render(size: int) -> Image.Image:
    s = size * SUPERSAMPLE
    k = s / 24.0

    # 方块：渐变底 + 圆角遮罩
    bg = gradient(
        s,
        [(0.0, BG_TOP), (0.58, BG_MID), (1.0, BG_BOTTOM)],
        (3.0, 1.0), (21.0, 23.0),
    )
    mask = Image.new("L", (s, s), 0)
    ImageDraw.Draw(mask).rounded_rectangle(
        [CHIP_BOX[0] * k, CHIP_BOX[1] * k, CHIP_BOX[2] * k, CHIP_BOX[3] * k],
        radius=CHIP_RADIUS * k,
        fill=255,
    )
    img = Image.new("RGBA", (s, s), (0, 0, 0, 0))
    img.paste(bg, (0, 0), mask)

    # 山背后的一层青光，让它不像贴纸
    img.alpha_composite(radial_glow(s, (11.6, 16.4), 9.0, (47, 208, 228), 80).convert("RGBA"))
    img.putalpha(Image.composite(img.getchannel("A"), Image.new("L", (s, s), 0), mask))

    # 山：填充渐变，用多边形做遮罩
    ridge = gradient(
        s,
        [(0.0, RIDGE_LEFT), (0.55, RIDGE_MID), (1.0, RIDGE_RIGHT)],
        (4.0, 19.0), (20.0, 9.0),
    )
    ridge_mask = Image.new("L", (s, s), 0)
    ImageDraw.Draw(ridge_mask).polygon(
        [(x * k, y * k) for x, y in MOUNTAIN], fill=235
    )
    img.alpha_composite(Image.composite(ridge, Image.new("RGBA", (s, s), (0, 0, 0, 0)), ridge_mask))

    # 半透明的线和描边要画在独立图层上再 alpha_composite：
    # ImageDraw 是「写入」不是「混合」，直接画 RGBA 会把底色打成半透明。
    overlay = Image.new("RGBA", (s, s), (0, 0, 0, 0))
    od = ImageDraw.Draw(overlay)

    # 地平线：一条很淡的横线，把山脚压住
    od.line(
        [(3.4 * k, HORIZON_Y * k), (20.6 * k, HORIZON_Y * k)],
        fill=HORIZON_COLOR,
        width=max(1, round(HORIZON_WIDTH * k)),
    )

    # 太阳
    cx, cy, r = SUN
    od.ellipse([(cx - r) * k, (cy - r) * k, (cx + r) * k, (cy + r) * k], fill=SUN_COLOR)


    # 内描边：极淡的一圈，给方块一点玻璃感（不是边线）
    od.rounded_rectangle(
        [CHIP_BOX[0] * k, CHIP_BOX[1] * k, CHIP_BOX[2] * k, CHIP_BOX[3] * k],
        radius=CHIP_RADIUS * k,
        outline=INNER_STROKE,
        width=max(1, round(INNER_STROKE_WIDTH * k)),
    )

    img.alpha_composite(overlay)
    return img.resize((size, size), Image.LANCZOS)


def main() -> int:
    out = Path(sys.argv[1]) if len(sys.argv) > 1 else Path("assets")
    out.mkdir(parents=True, exist_ok=True)
    for size in SIZES:
        path = out / f"mark-{size}.png"
        render(size).save(path)
        print(f"  {path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())