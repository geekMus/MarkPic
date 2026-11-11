"""把品牌标记的两套 SVG 光栅化成各尺寸 PNG：<variant>/mark-16.png … mark-256.png。

几何来源是 `assets/mark-dark.svg` 与 `assets/mark-light.svg`（形状逐字相同，只有配色不同）。
这个脚本不画图，只负责「渲染」：
用一个本机已有的 Chromium（Edge 或 Chrome，Windows 上一定有 Edge）按 4 倍尺寸
无头截一张透明背景的 PNG，再按非透明区域裁掉四周留白、用预乘 alpha 的 LANCZOS
缩到目标尺寸。这样做出来的 16px 一档边缘才立得住，直接画会把圆头笔画糊成一块。

为什么不用 cairosvg / rsvg：仓库不想为了几个图标引入原生依赖；浏览器反正装在
每台 Windows 上，WebView2 也是 Chromium，渲染结果和界面里看到的一致。

用法：
    python scripts/make-mark.py [输出目录]
默认写 assets/build/<dark|light>/。之后用 scripts/make-icon.py 打包成两个 ico
（assets/icon-dark.ico、assets/icon-light.ico），再用 rsrc 一起生成
cmd/markpic/rsrc_windows_amd64.syso（见 README「图标」）。注意 ico 的顺序：
亮色放前面，它既是 exe 自己的图标，也是运行时的默认窗口图标。

可以用环境变量 MARKPIC_BROWSER 指定浏览器可执行文件。
"""

from __future__ import annotations

import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

from PIL import Image

ROOT = Path(__file__).resolve().parent.parent
# 顺序即 ico 里的顺序：亮色在前（exe 图标与默认窗口图标），暗色在后。
VARIANTS = [
    ("light", ROOT / "assets" / "mark-light.svg"),
    ("dark", ROOT / "assets" / "mark-dark.svg"),
]
SIZES = [16, 20, 24, 32, 40, 48, 64, 128, 256]

# 渲染画布至少这么大：Chromium 无头模式的窗口有最小尺寸，给 256 会被撑到
# 512 再裁边，出来的图就是残的。512 是实测能完整装下整块方片的保守值。
MIN_CANVAS = 512
SUPERSAMPLE = 4

BROWSERS = [
    r"C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe",
    r"C:\Program Files\Microsoft\Edge\Application\msedge.exe",
    r"C:\Program Files\Google\Chrome\Application\chrome.exe",
    r"C:\Program Files (x86)\Google\Chrome\Application\chrome.exe",
]

WRAP = """<!doctype html><meta charset="utf-8">
<style>
  html, body {{ margin: 0; padding: 0; width: {canvas}px; height: {canvas}px; overflow: hidden; background: transparent; }}
  svg.mark {{ display: block; width: {canvas}px; height: {canvas}px; }}
</style>
{svg}
"""

# 必须把 SVG 内联进页面，不能用 <img src="mark.svg">：无头 Chromium 会把
# file:// 页面里的跨目录图片当成「不允许加载的本地资源」，静默画一张破图，
# 截出来就是全透明 —— 看起来像「渲染成功」。
CLASS_ATTR = 'class="mark"'


def inline_svg(svg: Path) -> str:
    """把 svg 文件内容塞进页面，顺带补上 class 让 CSS 能定尺寸。"""
    text = svg.read_text(encoding="utf-8")
    for tag in ("<svg", ):
        text = text.replace(tag, f'{tag} {CLASS_ATTR}', 1)
    return text


def find_browser() -> str | None:
    env = os.environ.get("MARKPIC_BROWSER")
    if env and Path(env).exists():
        return env
    for path in BROWSERS:
        if Path(path).exists():
            return path
    for name in ("msedge", "chrome", "chromium"):
        found = shutil.which(name)
        if found:
            return found
    return None


def render(browser: str, canvas: int, work: Path, svg: Path) -> Image.Image:
    """让浏览器把一份 SVG 渲染成 canvas×canvas 的透明 PNG。"""
    wrap = work / "wrap.html"
    wrap.write_text(WRAP.format(canvas=canvas, svg=inline_svg(svg)), encoding="utf-8")
    out = work / f"raw-{canvas}.png"
    cmd = [
        browser,
        "--headless=new",
        "--disable-gpu",
        "--hide-scrollbars",
        "--allow-file-access-from-files",
        "--default-background-color=00000000",
        f"--window-size={canvas},{canvas}",
        f"--screenshot={out}",
        wrap.as_uri(),
    ]
    proc = subprocess.run(cmd, capture_output=True, text=True)
    if not out.exists():
        raise RuntimeError(
            f"浏览器没有产出截图：{proc.stderr.strip() or proc.stdout.strip()}"
        )
    with Image.open(out) as img:
        return img.convert("RGBA")


def trim(img: Image.Image) -> Image.Image:
    """裁掉透明留白，只留方片本身——这样 ico 里方片是满格的。"""
    box = img.getchannel("A").getbbox()
    if box is None:
        raise RuntimeError("渲染结果是全透明的，SVG 路径或浏览器参数不对")
    if box[2] - box[0] != box[3] - box[1]:
        raise RuntimeError(f"方片不是正方形：{box}")
    return img.crop(box)


def main() -> int:
    out_root = Path(sys.argv[1]) if len(sys.argv) > 1 else ROOT / "assets" / "build"
    missing = [str(svg) for _, svg in VARIANTS if not svg.exists()]
    if missing:
        print(f"missing {', '.join(missing)}")
        return 1
    browser = find_browser()
    if not browser:
        print("找不到 Edge 或 Chrome；用 MARKPIC_BROWSER 指定一个可执行文件")
        return 1
    print(f"browser: {browser}")

    with tempfile.TemporaryDirectory(prefix="mark-") as tmp:
        work = Path(tmp)
        for variant, svg in VARIANTS:
            out_dir = out_root / variant
            out_dir.mkdir(parents=True, exist_ok=True)
            print(f"{variant}: {svg.relative_to(ROOT)} -> {out_dir.relative_to(ROOT)}")

            # 每种目标尺寸单独一次渲染，尺寸 = max(512, 目标×4)，再缩下来。
            for size in SIZES:
                canvas = max(MIN_CANVAS, size * SUPERSAMPLE)
                canvas = (canvas + 1) // 2 * 2
                raw = render(browser, canvas, work, svg)
                tile = trim(raw)
                # 预乘 alpha 再缩，否则透明边上会渗出灰边
                small = tile.convert("RGBa").resize((size, size), Image.Resampling.LANCZOS)
                small = small.convert("RGBA")
                small.save(out_dir / f"mark-{size}.png", optimize=True)
                print(f"  mark-{size}.png  ({tile.width}px tile -> {size}px)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())