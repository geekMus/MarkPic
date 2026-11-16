# MarkPic

MarkPic 是一个 Windows 桌面工具：把 Markdown 文档里的本地图片上传到 PicGo 图床，并把文档里的图片地址替换成图床链接。它也可以反向处理文章里已有的网络图片：只做检测，或者下载到本地。

程序是单个 exe，界面内嵌，不需要 Node 或本地服务。数据放在 exe 旁边的 `data` 目录。

## 功能

- **上传本地图片**：扫描 Markdown / HTML 里的本地图片，经 PicGo 上传，替换成图床地址。
- **检测远程图片**：逐个检查文章里的网络图片，报告打不开、不是图片、只返回半截等状况；只读，不改文档。
- **下载远程图片**：把网络图片下载到文档旁边的 `images/`（目录可改），链接改成相对路径。
- **队列**：拖入文件或文件夹，支持并发、取消、失败重试、只看问题；每张图的明细可以展开。
- **去重与缓存**：同一张图只上传一次；已经上传/下载过且内容未变的图直接复用。
- **三种输出**：sidecar、就地改写（带备份）、输出到指定目录。
- **桌面集成**：亮色/暗色/跟随系统、中文/英文、关闭到托盘、单实例。

## 使用

1. 安装并启动 [PicGo](https://github.com/Molunerfinn/PicGo)，配置好图床。
2. 在 PicGo 的 **设置 → 设置 Server** 里启用服务（默认 `http://127.0.0.1:36677`）。
3. 打开 MarkPic，把 `.md` 文件或文件夹拖进窗口。
4. 在左侧选择 **上传**、**检测** 或 **下载**，然后开始。

上传只走本机的 PicGo HTTP 接口，MarkPic 不接触图床密钥。检测和下载不上传图片，不需要 PicGo 开着。

### 三种作业

| 作业 | 处理什么 | 会改文档吗 | 需要 PicGo |
| --- | --- | --- | --- |
| 上传 | 本地图片 → 图床 | 会 | 是 |
| 检测 | 检查远程图片是否可用 | 不会 | 否 |
| 下载 | 远程图片 → 本地 `images/` | 会 | 否 |

上传时，文章里原有的远程图片原样保留。检测只读远程地址，但也会指出“文档引用了、磁盘上却不存在”的本地图。下载只处理远程图，本地图保留。

### 输出方式

| 模式 | 结果 |
| --- | --- |
| sidecar（默认） | 生成 `note.converted.md`，原文件不动 |
| in-place | 改写原文件，先备份成 `note.md.bak` |
| directory | 写到指定目录；同名文件自动加序号，不覆盖已有文件 |

## 支持的写法

```markdown
![图片](images/a.png)
![图片](images/a.png "标题")
![图片](<images/带 空格.png>)
![](images/有(括号).png)
<img src="images/a.png" width="60">
<img srcset="images/a.png 1x, images/a@2x.png 2x">
<source srcset="images/wide.png 640w">
![完整][ref1]  ![折叠][]  ![简写]
[ref1]: images/a.png
```

- 支持引用式图片、HTML `src` / `srcset` / `<source>`、一行多图、带空格或括号的路径。
- 跳过远程图、`data:`、缺失的本地文件，以及 `.ps` / `.eps` / `.pdf` / `.ai` / `.psd` / `.sketch` / `.fig` / `.drawio` 这类附件。
- 代码块、行内代码、HTML 注释、`<pre>` / `<code>` 里的图片不处理。
- 读写 UTF-8（含 BOM）、带 BOM 的 UTF-16 和本机 ANSI 代码页；输出保持原编码。

## 设置

设置面板里可以改：

- PicGo 地址、并发数、重试次数。
- 是否复用上次的上传/下载结果。
- 输出方式、输出目录、sidecar 后缀、远程图存放目录。
- 主题（亮色 / 暗色 / 跟随系统）、语言（中文 / 英文）。
- 关闭窗口时询问、最小化到托盘，还是直接退出；是否开机/启动时进入托盘。

## 数据目录

默认在 exe 旁边：

```text
MarkPic.exe
data/
  settings.json
  upload-cache.json
  remote-cache.json
  webview2/
```

如果 exe 所在目录不可写，会退回 `%APPDATA%\markpic`。也可以用环境变量 `MARKPIC_DATA_DIR` 指定别的目录。

## 开发

需要 Go 1.24+。构建或修改前端需要 Node 20.19+ 和 pnpm；构建脚本会自动执行 `pnpm --dir web install`。

```bash
npm run build        # 构建前端 + bin/MarkPic.exe
npm run build:go     # 只编 Go（需要已有 internal/ui/dist）
npm run start        # Vite 5173 + 窗口热更新
npm run start:mock   # 带假图床，不连 PicGo
npm run test
npm run typecheck
```

`internal/ui/dist` 不提交到版本库，它是 `go:embed` 的输入。`build`、`start`、`test` 发现产物缺失时会自动构建前端。直接 `go run ./cmd/markpic` 也可以，但同样需要先有 `internal/ui/dist`。

### 目录结构

| 路径 | 内容 |
| --- | --- |
| `cmd/markpic` | 入口、单实例、原生窗口、bridge、标题栏 |
| `internal/app` | bridge 方法表与事件 |
| `internal/mdscan` | Markdown 扫描与按字节替换 |
| `internal/imgpath` | 图片地址分类与解析 |
| `internal/picgo` | PicGo 上传客户端 |
| `internal/conv` | 转换流水线 |
| `internal/remoteimg` | 远程图片检测与下载 |
| `internal/remotecache` | 下载缓存 |
| `internal/uploadcache` | 上传缓存 |
| `internal/settings` | 设置读写 |
| `internal/platform` | 原生对话框 |
| `internal/update` | 更新检查与安装 |
| `internal/ui` | 前端产物（`go:embed`） |
| `web` | Vue 3 + TypeScript + Vite 前端 |
| `scripts` | 构建、启动、测试脚本 |
| `tools/bench` | 基准测试 |
| `tools/mockpicgo` | 假图床 |

图标资源在 `assets/`；常规构建直接使用已提交的 `.ico` 和 `.syso`，不需要重新生成。

## 文档

- [Bridge API](docs/API.md)
