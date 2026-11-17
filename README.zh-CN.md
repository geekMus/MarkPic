# MarkPic

[English](README.md) | [简体中文](README.zh-CN.md)

MarkPic 是一个 Windows 桌面工具，处理 Markdown 文档里的图片。三件事互相独立：

- **上传**：找出文档引用的本地图片，交给 [PicGo](https://github.com/Molunerfinn/PicGo)
  图床上传，再把文档里的地址换成图床链接。
- **检测**：逐个检查文章里已有的网络图片，报告打不开、不是图片、只返回半截的状况。只读，不改文档。
- **下载**：把这些网络图片下载到本地的 `images/`，链接改成相对路径。

程序是单个 `.exe`，界面内嵌：不需要 Node.js，不起本地服务，没有命令行。上传只走 PicGo
的本机 HTTP 接口，MarkPic 不接触你的图床密钥。

## 一眼看懂

| 作业 | 处理什么 | 会改文档吗 | 需要 PicGo 开着 |
| --- | --- | --- | --- |
| 上传 | 本地图片 → 图床 | 会 | 是 |
| 检测 | 网络图片地址 | 不会 | 否 |
| 下载 | 网络图片 → `images/` | 会 | 否 |

文档保持原编码：UTF-8（含或不含 BOM）、带 BOM 的 UTF-16、以及本机 ANSI 代码页。
除了被替换掉的图片地址，其余内容一个字节都不变。

## 安装

从 [Releases](https://github.com/geekMus/MarkPic/releases) 下载
`MarkPic-v<版本>-windows-amd64.exe`。没有安装程序：放哪都能跑。已安装的版本会自己更新，
设置与缓存放在 exe 旁边的 `data/` 目录里。

## 快速开始

1. 安装并启动 [PicGo](https://github.com/Molunerfinn/PicGo)，在 **设置 → 设置 Server**
   里启用服务（默认 `http://127.0.0.1:36677`）。
2. 把 `.md` 文件或文件夹拖进 MarkPic 窗口。
3. 在左侧选 **上传**、**检测** 或 **下载**，然后开始。

## 文档

- [使用说明](docs/usage.zh-CN.md)：支持的写法、输出方式、设置、数据目录
- [构建与开发](docs/build.zh-CN.md)：工具链、脚本、目录结构
- [Bridge API](docs/API.md)：内嵌界面的通信接口（中文）
