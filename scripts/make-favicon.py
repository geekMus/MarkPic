"""把两套品牌标记写成 web/index.html 里的 favicon data URI。

标签页图标必须离线可用（窗口用 file:// 加载，不额外请求任何文件），所以只能内联
成 data URI。两套配色各写一个 <link rel="icon">，暗色那条带
media="(prefers-color-scheme: dark)"：支持的浏览器会跟着系统明暗换，不支持的
就固定用前一条（亮色，和安装图标一致）。

生成的段落夹在 web/index.html 的 favicon:begin / favicon:end 两行注释之间，
每次整段重写 —— 手改那两行之间的内容会被覆盖。

用法：
    python scripts/make-favicon.py
"""

from __future__ import annotations

import re
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
INDEX = ROOT / "web" / "index.html"
VARIANTS = [("light", ROOT / "assets" / "mark-light.svg", None), ("dark", ROOT / "assets" / "mark-dark.svg", "dark")]
BEGIN = "<!-- favicon:begin 由 scripts/make-favicon.py 生成，别手改 -->"
END = "<!-- favicon:end -->"

COMMENT = re.compile(r"<!--.*?-->", re.S)
BETWEEN_TAGS = re.compile(r">\s+<")


def to_data_uri(svg: Path) -> str:
    """压成一行 data URI：去掉注释与标签间隙、双引号换单引号、# 转义成 %23。"""
    text = COMMENT.sub("", svg.read_text(encoding="utf-8"))
    text = BETWEEN_TAGS.sub("><", text)
    text = " ".join(text.split())
    text = text.replace('"', "'").replace("#", "%23")
    return "data:image/svg+xml," + text


def main() -> int:
    html = INDEX.read_text(encoding="utf-8")
    start = html.index(BEGIN)
    stop = html.index(END) + len(END)

    lines = [BEGIN]
    for name, svg, media in VARIANTS:
        media_attr = f' media="(prefers-color-scheme: {media})"' if media else ""
        lines.append(f'    <link rel="icon" type="image/svg+xml"{media_attr} href="{to_data_uri(svg)}" />')
        lines.append(f"    <!-- 上面这条是 {name} 配色：{svg.relative_to(ROOT).as_posix()} -->")
    lines.append("    " + END)
    body = "\n".join(lines)

    INDEX.write_text(html[:start] + body + html[stop:], encoding="utf-8")
    print(f"wrote {INDEX.relative_to(ROOT)} ({stop - start} -> {len(body)} chars)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())