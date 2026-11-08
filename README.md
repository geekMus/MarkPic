# convert-md-picture

把 Markdown 文档里的本地图片上传到 [PicGo](https://github.com/Molunerfinn/PicGo) 图床，并把文档里的图片链接替换成图床地址。

单个 exe，免安装，不需要 Node、Electron 或任何运行时环境。前端界面已经编译进二进制，双击即可使用。

## 这次重写解决了什么

旧版本是 Electron 应用，核心的替换逻辑按「行号」建立映射，一旦某个文件里出现一行多张图、或者部分图片上传失败，替换结果就会串位——同一张图被反复替换成别人的地址。新版本换了实现方式：

- **按字节偏移替换，不再按行**：扫描阶段记录每个图片引用（含 URL）在文件中的精确字节区间，替换时一次性写回。一行多图、同一张图被引用多次、部分失败，都不会串位。
- **同一张图一轮只上传一次**：并发上传去重，重复引用直接复用已得到的 URL。
- **失败不写坏文件**：某张图上传失败时保留原文，只有全部本地图片都失败才不生成文件；绝不产生「上传了一半」的文档。
- **原地替换前自动备份** `.bak`。
- **保留原始字节**：CRLF 换行、UTF-8 BOM、缩进格式逐字节保留，只动图片地址。
- 支持 `![alt](a.png "标题")`、`![](<带 空格.png>)`、`<img src="a.png" width="60">`、一行多图；代码块与行内代码里的 `![]()` 不会误伤。
- 上传失败自动重试（可配置次数、指数退避），可随时取消。
- 扫描器、替换器、PicGo 客户端、HTTP 服务都有测试覆盖。

## 使用

### 图形界面

直接运行 `convert-md-picture.exe`，程序会在 `127.0.0.1:17860`（被占用时自动后移）启动本地服务并打开浏览器。界面里可以选择文件或文件夹、预览每张图片的处理结果、开始转换、取消、在资源管理器中定位结果文件。

关闭控制台窗口即退出程序。

### 命令行

```powershell
# 转换单个文件，生成 note.converted.md
convert-md-picture note.md

# 原地替换（先备份为 note.md.bak）
convert-md-picture note.md --in-place

# 递归处理整个目录，输出到指定目录
convert-md-picture .\docs --output directory --out-dir .\out

# 只检查不写文件
convert-md-picture .\docs --dry-run
```

| 参数 | 说明 |
| --- | --- |
| `-p, --port <int>` | 图形界面端口，默认 17860，被占用时自动后移 |
| `--no-open` | 启动后不自动打开浏览器 |
| `-o, --output <sidecar\|in-place\|directory>` | 输出方式，默认 `sidecar` |
| `--out-dir <dir>` | `directory` 模式的输出目录 |
| `-s, --suffix <str>` | 旁路输出后缀，默认 `.converted` |
| `--in-place` | 等价于 `--output in-place`，会先备份 `.bak` |
| `--backup` | 原地替换前备份，默认开启；用 `--backup=false` 关闭 |
| `--dry-run` | 只检查，不上传、不写文件 |
| `-e, --endpoint <url>` | PicGo 服务地址，默认 `http://127.0.0.1:36677` |
| `-c, --concurrency <int>` | 同时处理的文件数，1-16，默认 4 |
| `--retries <int>` | 上传失败重试次数，0-5，默认 2 |
| `-q, --quiet` | 只输出汇总 |
| `-v, --version` | 显示版本 |

命令行参数只覆盖你显式写出来的项，其余沿用界面里保存的设置。
退出码：有文件失败或图片失败时为 `1`，否则为 `0`。

### 输出方式

| 模式 | 结果 |
| --- | --- |
| `sidecar`（默认） | 生成 `note.converted.md`，原文件不动 |
| `in-place` | 直接改写原文件，先备份为 `note.md.bak` |
| `directory` | 全部结果写到 `--out-dir` 指定目录，同名文件自动加序号 |

## 前置条件

1. 安装并运行 [PicGo](https://github.com/Molunerfinn/PicGo)。
2. 在 PicGo 里配置好图床。
3. 打开 **PicGo 设置 → 设置 Server**，启用服务（默认端口 `36677`）。

程序只通过 `http://127.0.0.1:36677/upload` 这个本地接口上传，不接触你的图床密钥。

## 支持的图片写法

```markdown
![图片](images/a.png)
![图片](images/a.png "标题")
![图片](<images/带 空格.png>)
![](images/有(括号).png)
<img src="images/a.png" width="60">
![x](images/a.png) 和 ![y](images/a.png)   <!-- 一行多张，只上传一次 -->
```

跳过：网络图片（`http://`、`https://`、`data:` 等）、不存在的本地文件。
不处理：引用式图片 `![alt][id]`（扫描时会在结果里给出提示）。
代码块（``` 或 ~~~ 围栏）、行内代码和 HTML 注释里的内容不会被当作图片处理。
用 4 个空格缩进写的代码块不在此列：缩进同时是列表项的续行语法，两者无法可靠区分，
所以这类文档请改用围栏代码块。

文档必须是 UTF-8 编码。

## 设置存储位置

界面里修改的设置保存在 `%APPDATA%\convert-md-picture\settings.json`（原子写入，损坏时自动回退默认值）。

## 从源码构建

需要 Go 1.24 或更新版本；只有改动前端界面时才需要 Node 与 pnpm。
后端只依赖两个第三方库：`github.com/go-chi/chi/v5`（HTTP 路由）和 `github.com/spf13/cobra`（命令行），
其余全部是标准库，没有 CGO，仍然是单个 exe。

```powershell
# 只构建 Go 部分（前端产物已提交在仓库里）
go build -o bin\convert-md-picture.exe .\cmd\convert-md-picture

# 改了前端：先构建前端，再构建 exe
cd web; pnpm install; pnpm build; cd ..
go build -o bin\convert-md-picture.exe .\cmd\convert-md-picture

# 跑测试
go test ./...
```

或者直接用脚本：

```powershell
.\scripts\build.ps1            # 构建 exe（不重建前端）
.\scripts\build.ps1 -Web       # 先重建前端再构建
.\scripts\build.ps1 -Web -Test # 再顺手跑一遍测试
```

exe 的图标来自 `assets/icon.ico`，由 `cmd/convert-md-picture/rsrc_windows_amd64.syso` 打进去（已提交，构建时不需要联网）。
Go 只链接**主包目录**里的 `.syso`，所以这个文件必须和 `main.go` 放在一起。
删掉它再构建会尝试用 `go run github.com/akavel/rsrc@latest` 重新生成；失败也不影响构建，只是图标变成默认的。

交叉编译到其他平台：

```powershell
$env:GOOS="linux"; $env:GOARCH="amd64"; go build -o bin/convert-md-picture-linux ./cmd/convert-md-picture
```

交叉编译不会带上 Windows 图标（`*.syso` 只在对应平台参与链接）。

非 Windows 平台没有原生文件对话框（界面里对应的按钮会提示手动输入路径），其余功能一致。

## 目录结构

```
cmd/convert-md-picture/   入口：命令行与图形界面两种模式
internal/mdscan/          Markdown/HTML 图片引用扫描与按字节替换
internal/imgpath/         图片地址分类（本地/远程）与相对路径解析
internal/picgo/           PicGo 上传客户端（重试、去重、错误分类）
internal/conv/            转换流水线：并发、去重、输出、备份
internal/server/          本地 HTTP API（chi 路由）+ SSE 事件流 + 静态资源
internal/webui/           前端构建产物（go:embed 打进二进制）
internal/platform/        系统原生对话框、打开文件、定位文件
internal/settings/        设置读写
web/                      Vue 3 + TypeScript 前端源码
docs/API.md               前后端接口契约
```

## 常见问题

**提示无法连接 PicGo**
PicGo 没运行，或「设置 → 设置 Server」里的服务没开启。开启后地址默认是 `http://127.0.0.1:36677`，可以在界面的设置里改。

**部分图片显示「文件不存在」**
文档里的相对路径是相对 Markdown 文件所在目录解析的，确认图片确实在那里。

**端口被占用**
图形界面会自动往后找可用端口；也可以用 `--port` 指定。

**转换后文档里还有没替换的图片**
看一下界面里该文件的展开明细，每张图都有具体原因（远程图片、文件缺失、上传失败）。
