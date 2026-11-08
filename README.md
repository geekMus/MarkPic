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

## 开发与打包

前端是 Vue 3 + TypeScript + Vite（在 `web/`），后端是 Go（`chi` 路由 + `cobra` 命令行）。
开发时不需要配跨域：Vite 把 `/api/*` 代理给本机 Go 服务，浏览器只用一个地址。

两条命令概括全部：

```powershell
pnpm dev     # 起开发环境（Go 服务 + Vite），前后端都带热更新
pnpm build   # 打包成单个 exe
```

### 开发环境

需要 Go 1.24 或更新版本；只有改前端时才需要 Node 与 pnpm。

```powershell
pnpm dev
```

它会依次：编译后端到 `bin/dev-api.exe`，交给 `scripts/watch-api.ps1` 在 `127.0.0.1:17860` 启动并监视
Go 源码（带 `--no-open`，后端自己不去抢浏览器）→ 等 `/api/health` 就绪后打印横幅（后端地址、版本、
PicGo 状态）→ 在 `127.0.0.1:5173` 启动 Vite 并把 `/api/*` 代理到后端 → 按 Ctrl+C 时把后端和监视
进程一起关掉。前端已有的实例会被复用，不会重复启动。

**默认不会自动打开浏览器**，界面地址只打印在终端里（开发机常常同时在干别的事，弹窗抢焦点很烦）。
想让它自动打开就加 `-Open`：

```powershell
.\scripts\dev.ps1 -Open
```

| 地址 | 是什么 |
| --- | --- |
| `http://127.0.0.1:5173` | 带热更新的界面（日常开发用这个） |
| `http://127.0.0.1:17860` | 后端接口本身（可以直接调 `/api/*`，见 `docs/API.md`） |

> 终端里只看到横幅是正常的：本脚本不碰浏览器，自己打开 `http://127.0.0.1:5173` 就行；确认这个
> 终端还在运行（Ctrl+C 或关掉窗口会把前后端一起带走）。直接双击 `convert-md-picture.exe` 是另一种
> 形态：它自带界面并会打开浏览器，不想它打开就加 `--no-open`。

### 热更新

前后端都不用手动重启：

| 改了什么 | 谁负责 | 大概多久生效 |
| --- | --- | --- |
| `.vue` / `.ts` / `.css` | Vite 热更新（HMR），页面不刷新，组件状态还在 | 一眨眼（< 300ms） |
| `internal/**`、`cmd/**` 下的 `.go`、`go.mod`、`go.sum` | `scripts/watch-api.ps1` 重新编译并重启后端 | 1–3 秒 |

后端重启时：

* **编译失败不会把环境弄挂**：终端会打印 Go 的报错，旧进程继续服务，改好保存后会再自动重启一次。
* 页面上的事件流（SSE）会断开一下，前端自己带指数退避重连，不用手动刷新；Vite 终端里那些
  `http proxy error: /api/events / ECONNRESET` 已经被我们降噪（属于预期内的断连），不会再刷一屏红字。
* 不想让后端跟着重启（比如只想调前端、或后端正停在某个断点上），用 `-NoWatch` 启动。
* 只想单独跑这个监视器（后端已在别处跑时不要重复启动）：

```powershell
.\scripts\watch-api.ps1 -Port 17860
```

> 改 `web/vite.config.ts` 时 Vite 会自己重启一次，这是正常的。如果它反而崩了并报
> `EBUSY: resource busy or locked, watch ...tmpdir`，说明这个 Vite/Node 版本在你的机器上对
> 「临时文件 + 改名」式的保存比较敏感——仓库里的 `server.watch.ignored` 已经排除了这类临时目录，
> 拉最新代码即可。

其他命令：

| 命令 | 作用 |
| --- | --- |
| `pnpm dev` | 前后端一起启动 |
| `pnpm dev:api` | 只启动后端（另开终端跑 `pnpm dev:web` 调前端也行） |
| `pnpm dev:web` | 只启动 Vite（假定后端已在 17860） |
| `pnpm test` | `go test ./...` |
| `pnpm typecheck` | 前端类型检查（`vue-tsc --noEmit`） |

端口冲突或工具装在非标准位置时用脚本参数覆盖：

```powershell
.\scripts\dev.ps1 -ApiPort 17870 -WebPort 5174
.\scripts\dev.ps1 -Go D:\Env\Go\bin\go.exe -Pnpm D:\Env\node\pnpm.cmd
```

脚本会自己找 Go 与 pnpm（PATH、`GOROOT`、几个常见安装目录），找不到才报错。

### 打包

```powershell
pnpm build            # 构建前端 + 构建 exe
```

等价的脚本调用与更多开关：

```powershell
.\scripts\build.ps1            # 只构建 exe（前端产物已提交在仓库里）
.\scripts\build.ps1 -Web       # 先重建前端再构建 exe
.\scripts\build.ps1 -Web -Test # 再顺手跑一遍测试
```

产物是 `bin\convert-md-picture.exe`（约 9 MB），双击即可运行。
版本号从 git 自动注入，`--version` 可以看到（例如 `1.0.0 (5494b24)`）。

### 手动构建（不用脚本）

```powershell
# 只构建 Go 部分（前端产物已提交在仓库里）
go build -o bin\convert-md-picture.exe .\cmd\convert-md-picture

# 改了前端：先构建前端，再构建 exe
cd web; pnpm install; pnpm build; cd ..
go build -o bin\convert-md-picture.exe .\cmd\convert-md-picture

# 跑测试
go test ./...
```

前端产物 `internal/webui/dist` 是提交进仓库的，这样 clone 之后只装 Go 就能构建，不必先装 Node。

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
web/                      Vue 3 + TypeScript 前端源码（Vite 工程）
scripts/                  开发与构建脚本（dev.ps1 / build.ps1 / test.ps1 / _tools.ps1）
package.json              根目录的命令入口（pnpm dev、pnpm build 等快捷方式，不是前端工程）
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

**`pnpm dev` 说找不到 go 或 pnpm**
用 `-Go` / `-Pnpm` 指定可执行文件的完整路径，例如 `.\scripts\dev.ps1 -Go D:\Env\Go\bin\go.exe`。

**改 `web/vite.config.ts` 之后 Vite 报 `EBUSY: resource busy or locked`**
Vite 重载配置时会在 `web/` 下写一个临时目录再 watch 它，Windows 上偶发占用冲突。停掉 `pnpm dev` 重新执行即可；配置本身没有问题。

**界面能打开但接口全是 404 / 连不上**
开发环境下 `http://127.0.0.1:5173/api/health` 应该返回一段 JSON。若返回失败，说明后端没起来或端口不是 17860——用 `.\scripts\dev.ps1 -ApiPort <端口>` 换端口，并确认 `pnpm dev:web` 是在同一个 `CMP_API` 目标下启动的。
