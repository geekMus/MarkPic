"""把品牌标记的各个尺寸 PNG 打包成 assets/icon.ico。

图标形状的唯一来源是 assets/mark.svg。改标记时先跑 scripts/make-mark.py
把 SVG 渲染成 assets/build 里的 PNG，再用这个脚本打包，然后重新生成
cmd/markpic/rsrc_windows_amd64.syso（见 README 的「图标」一节）。

这里自己写 ICO 字节而不是用 Pillow 的 ICO 保存器：Pillow 对大尺寸会用 PNG
压缩存进 ICO，而 PNG 条目 GDI+（System.Drawing.Icon、部分老工具）读不了，
会报「请求的范围扩展超过了数组的结尾」。全部用 32 位 DIB + AND 掩码写，
兼容性最好，也能用 System.Drawing 直接验证。

用法：
    python scripts/make-icon.py [PNG 目录] [输出 ico]
默认从 assets/build 读 mark-<size>.png，写到 assets/icon.ico。
"""

from __future__ import annotations

import struct
import sys
from io import BytesIO
from pathlib import Path

from PIL import Image

SIZES = [16, 24, 32, 48, 64, 128, 256]


def dib_for(img: Image.Image) -> bytes:
    """把一张 RGBA 图变成 ICO 里的 DIB 数据（BITMAPINFOHEADER + XOR + AND）。"""
    width, height = img.size
    px = img.load()

    header = struct.pack(
        "<IiiHHIIiiII",
        40,          # biSize
        width,       # biWidth
        height * 2,  # biHeight：XOR 图 + AND 掩码，所以是两倍高
        1,           # biPlanes
        32,          # biBitCount
        0,           # biCompression = BI_RGB
        0,           # biSizeImage（BI_RGB 可为 0）
        0, 0,        # 分辨率
        0,           # biClrUsed
        0,           # biClrImportant
    )

    # XOR 位图：自下而上，每像素 BGRA。
    rows = bytearray()
    for y in range(height - 1, -1, -1):
        for x in range(width):
            r, g, b, a = px[x, y]
            rows += bytes((b, g, r, a))

    # AND 掩码：32 位图里其实用不到（靠 alpha），但格式要求必须有，填 0。
    mask_row = ((width + 31) // 32) * 4
    mask = bytes(mask_row * height)

    return header + bytes(rows) + mask


def main() -> int:
    src_dir = Path(sys.argv[1]) if len(sys.argv) > 1 else Path("assets/build")
    out = Path(sys.argv[2]) if len(sys.argv) > 2 else Path("assets/icon.ico")

    frames = []
    for size in SIZES:
        path = src_dir / f"mark-{size}.png"
        if not path.exists():
            print(f"missing {path}")
            return 1
        img = Image.open(path).convert("RGBA")
        if img.size != (size, size):
            print(f"{path} is {img.size}, want {(size, size)}")
            return 1
        frames.append(img)

    # 16–128 用 DIB；256 用 PNG —— 这是现代图标的通行做法：256 的 32 位 DIB
    # 单独就有 256 KB，而 PNG 条目（Vista 之后都支持）只要几 KB。
    blobs = []
    for img in frames:
        if img.size[0] >= 256:
            buf = BytesIO()
            img.save(buf, format="PNG", optimize=True)
            blobs.append(buf.getvalue())
        else:
            blobs.append(dib_for(img))

    directory = bytearray(struct.pack("<HHH", 0, 1, len(frames)))
    offset = 6 + 16 * len(frames)
    for img, blob in zip(frames, blobs):
        size = img.size[0]
        directory += struct.pack(
            "<BBBBHHII",
            size if size < 256 else 0,   # 256 用 0 表示
            size if size < 256 else 0,
            0, 0, 1, 32,
            len(blob), offset,
        )
        offset += len(blob)

    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_bytes(bytes(directory) + b"".join(blobs))
    print(f"wrote {out} ({out.stat().st_size} bytes, {len(frames)} sizes)")

    # 回读校验：每个尺寸都要能单独解出来。
    with Image.open(out) as check:
        got = sorted(check.ico.sizes())
        print(f"sizes: {got}")
        if got != sorted((s, s) for s in SIZES):
            print("size list mismatch")
            return 1
        for size, _ in got:
            frame = check.ico.getimage((size, size)).convert("RGBA")
            if frame.size != (size, size):
                print(f"decode {size} gave {frame.size}")
                return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
