# convert-md-picture

把 Markdown 文档里的本地图片上传到 [PicGo](https://github.com/Molunerfinn/PicGo) 图床，并把文档里的图片链接替换成图床地址。

单个 exe，免安装，不需要 Node、Electron 或任何运行时环境。前端界面已经编译进二进制，双击即可使用。

## 这次重写解决了什么

旧版本是 Electron 应用，核心的替换逻辑按「行号」建立映射，一旦某个文件里出现一行多张图、或者部分图片上传失败，替换结果就会串位——同一张图被反复替换成别人的地址。新版本换了实现方式：

- **按字节偏移替换，不再按行**：扫描阶段记录每个图片引用（含 URL）在文件中的精确字节区间，替换时一次性写回。一行多图、同一张图被引用多次、部分失败，都不会串位。
- **同一张图一轮只上传一次**：并发上传去重，重复引用直接复用已得到的 URL。
- **上次传过的图不再重传**：按「图片路径 + 大小 + 修改时间」记住上次拿到的地址，改一篇文章再跑一次时几乎立刻完成（设置里「复用上次的上传结果」默认开启，命令行用 `--no-reuse` 关掉）。
- **失败不写坏文件**：某张图上传失败时保留原文，只有全部本地图片都失败才不生成文件；绝不产生「上传了一半」的文档。
- **原地替换前自动备份** `.bak`。
- **保留原始字节**：CRLF 换行、UTF-8 BOM、缩进格式逐字节保留，只动图片地址。
- 支持 `![alt](a.png "标题")`、`![](<带 空格.png>)`、`<img src="a.png" width="60">`、一行多图；代码块与行内代码里的 `![]()` 不会误伤。
- 上传失败自动重试（可配置次数、指数退避），可随时取消。
- 扫描器、替换器、PicGo 客户端、HTTP 服务都有测试覆盖。

## 使用

### 图形界面

直接运行 `convert-md-picture.exe`，会弹出一个桌面窗口：程序在 `127.0.0.1:17860`（被占用时自动后移）启动本地服务，窗口里就是完整界面。界面里可以选择文件或文件夹、预览每张图片的处理结果、开始转换、取消、在资源管理器中定位结果文件。

窗口用的是系统自带的 WebView2（Windows 11 与装了新版 Edge 的 Windows 10 都有），所以不需要额外装运行时、也不会调用浏览器；万一机器上确实没有 WebView2，程序会自动改用系统浏览器打开同一个地址，并在终端里说明。

窗口没有系统标题栏：顶部那一条是界面自己画的，左边是程序名与版本，右边是 PicGo 状态、主题与设置，最右三个是最小化 / 最大化 / 关闭。按住这一条可以拖动窗口，拖四条边可以改窗口大小（双击标题栏空白处 = 最大化 / 还原）。窗口有最小尺寸 760×560：再窄的话台账的列和底栏的按钮就没法舒服地排开了，界面在 820 以下会进入窄版（标题栏收掉版本号、台账收掉「图片」列、底栏收掉快捷键提示）。用浏览器打开同一个界面时，这三个窗口按钮和边缘缩放区会自动隐藏。

关闭窗口即退出程序。从终端启动时控制台窗口会保留（能看到日志）；直接双击 exe 时程序会自己把控制台收起来，看起来就是个普通桌面程序。

### 主题与语言

主题有「亮色 / 暗色 / 跟随系统」三态，**默认跟随系统**（也就是跟着 Windows 的浅色/深色设置走，系统切了界面立刻跟着切）。语言有「简体中文 / English」两种，默认中文。两个开关都在「设置 → 外观」里，改完立即生效。

语言不只是界面文字：前端每个请求都会带上 `Accept-Language`，服务端按它返回对应语言的提示，所以「某张图片为什么失败」「连不上 PicGo」「检查更新失败」这类信息也会跟着换语言。

这两项属于界面自己的偏好，存在本机浏览器存储里（`convert-md-picture.lang`、`convert-md-picture.theme`），不写进 `settings.json`——切换语言不需要点「保存设置」。

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
| `--no-open` | 只启动本地服务，不开窗口也不开浏览器 |
| `--browser` | 改用系统浏览器打开界面（不开桌面窗口） |
| `-o, --output <sidecar\|in-place\|directory>` | 输出方式，默认 `sidecar` |
| `--out-dir <dir>` | `directory` 模式的输出目录 |
| `-s, --suffix <str>` | 旁路输出后缀，默认 `.converted` |
| `--in-place` | 等价于 `--output in-place`，会先备份 `.bak` |
| `--backup` | 原地替换前备份，默认开启；用 `--backup=false` 关闭 |
| `--dry-run` | 只检查，不上传、不写文件 |
| `--no-reuse` | 不复用上次的上传结果，每张图都重新上传 |
| `-e, --endpoint <url>` | PicGo 服务地址，默认 `http://127.0.0.1:36677` |
| `-c, --concurrency <int>` | 同时上传的图片数，1-32，默认 16 |
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
| `directory` | 全部结果写到 `--out-dir` 指定目录，同名文件按加入顺序自动加序号（`post.md`、`post-2.md`…），每次跑结果一致 |

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
![完整][ref1]  ![折叠][]  ![简写]           <!-- 引用式，定义写在文档别处 -->
[ref1]: images/a.png
[折叠]: images/a.png "标题"
[简写]: images/有(括号).png
```

跳过：网络图片（`http://`、`https://`、`data:` 等）、不存在的本地文件。
引用式图片（`![alt][label]`、`![alt][]`、`![alt]`）会就地改成等价的 `![alt](地址)`，
文档末尾那几行链接定义原样保留；标签在文档里找不到定义时才提示并跳过。
代码块（``` 或 ~~~ 围栏）、行内代码、HTML 注释里的内容不会被当作图片处理，
`<pre>`、`<code>` 标签之间的内容同样按代码看待。
用 4 个空格缩进写的代码块不在此列：缩进同时是列表项的续行语法，两者无法可靠区分，
所以这类文档请改用围栏代码块。

地址里有空格时规范写法是 `![图片](<images/带 空格.png>)`。若漏了尖括号
（`![图片](images/带 空格.png)`，CommonMark 不允许，但手写 Windows 路径和不少编辑器就是这么生成的），
只要磁盘上真有这个文件，本工具照样会替换它，并在结果里附一条提示；文件不存在时
（例如正文里的 `![说明](见 上文)`）当作普通文字，不报错也不算失败。

地址里的 `%20` 会按 URL 解码：`images/my%20pic.png` 指向 `images/my pic.png`。
磁盘上确实有名字带百分号的文件时，写成 `images/图%20片.png` 也能引用到它本身
（先按解码后的名字找，找不到再按原样找）。`#` 是 URL 的片段分隔符，
要引用文件名里带 `#` 的图片得写成 `%23`。

文档必须是 UTF-8 编码。

## 数据放在哪

程序不往系统目录里塞东西，所有数据都在 **exe 旁边的 `data` 目录**里：

```
convert-md-picture.exe
data/
  settings.json     界面里改的设置（原子写入，损坏时自动回退默认值）
  upload-cache.json 上次上传拿到的图片地址，重复运行就不用再传一遍（删掉只是会重传）
  webview2/         内嵌浏览器的缓存，删掉会自动重建
```

这么做的好处是绿色的：整个文件夹拷到 U 盘或另一台机器就能用，设置跟着走；不想用了删掉
`data` 目录就等于清干净；自动更新只替换 exe，`data` 原样留着。

只有一种情况会放到系统目录：**exe 所在目录写不进去**（比如装进了 `Program Files`，或者放在
只读介质上），这时会退回 `%APPDATA%\convert-md-picture\`，并在「关于」弹窗里说明原因。
从旧版本升级过来时，原来写在 `%APPDATA%` 的设置会自动搬到 `data` 目录，不会丢。

想指定别的位置，设环境变量 `CMP_DATA_DIR` 即可（多开、把数据放移动硬盘上都用得上）：

```powershell
$env:CMP_DATA_DIR = 'D:\cmp-data'; .\bin\convert-md-picture.exe
```

「关于」弹窗里能看到当前的数据目录，点「打开」直接在资源管理器里定位过去。

## 更新

底栏右下角的版本号是个按钮，点开是「关于」：

- 当前版本、最新版本、更新时间、安装包大小
- 「检查更新」主动查一次（启动 1.2 秒后也会悄悄查一次，可以在弹窗里关掉）
- 查到新版本时，显示更新说明并提供「立即更新」

更新过程在一个进程里完成：下载到 `<exe>.new` → 核对清单里的 SHA-256 →
把正在运行的 exe 改名成 `<exe>.old` → 新 exe 放到原路径 → 启动新进程 → 旧进程退出。
新进程带着 `--handover-port` 先等端口空出来，所以不会把自己当成「已经在运行的实例」。
仍然被映射的 `.old` 删不掉，留到下次启动时清理。

检查更新读的是仓库根的静态清单 `version.json`（不是 GitHub Releases API）：

```json
{
  "version": "1.0.1",
  "tag": "v1.0.1",
  "published": "2026-11-09",
  "notes": "更新说明，显示在「关于」里",
  "windows": {
    "amd64": { "url": "…releases/download/v1.0.1/…exe", "sha256": "…", "size": 9400000 }
  }
}
```

清单里没有给出 SHA-256 时**不会自动安装**（只提示可以去发布页手动下载）。
需要自建镜像或本地调试时，用 `CMP_UPDATE_MANIFEST` 指向另一份清单：

```powershell
$env:CMP_UPDATE_MANIFEST = 'http://127.0.0.1:8080/version.json'
```

### 发版

```powershell
.\scripts\release.ps1 -Version 1.0.1 -Notes "修了几个对齐问题"
```

脚本会用这个版本号编译 `bin\convert-md-picture-v1.0.1-windows-amd64.exe`，
算出 SHA-256 与大小，写进 `version.json`，并打印后续的 `gh release create` / `git tag` 命令。

顺序很重要：**先把 exe 传到 release，再推 `version.json`**。
反过来的话，老版本会在下载地址还不存在时看到「有新版本」。

## 开发与打包

前端是 Vue 3 + TypeScript + Vite（在 `web/`），后端是 Go（`chi` 路由 + `cobra` 命令行 +
`jchv/go-webview2` 承载桌面窗口）。全部依赖都是纯 Go，没有 CGO，编译出来仍是单个 exe。
开发时不需要配跨域：Vite 把 `/api/*` 代理给本机 Go 服务，浏览器只用一个地址。

两条命令概括全部：

```powershell
pnpm web     # 起开发环境（Go 服务 + Vite），前后端都带热更新
pnpm build   # 打包成单个 exe
```

想直接体验打包后的形态（和最终用户双击 exe 一样）：

```powershell
pnpm start   # 构建 exe 并运行它，起服务 + 打开桌面窗口
```

### 开发环境

需要 Go 1.24 或更新版本；只有改前端时才需要 Node 与 pnpm。

```powershell
pnpm web
```

它会依次：编译后端到 `bin/dev-api.exe`，交给 `scripts/watch-api.ps1` 在 `127.0.0.1:17860` 启动并监视
Go 源码（带 `--no-open`，后端自己不去抢浏览器）→ 等 `/api/health` 就绪后打印横幅（后端地址、版本、
PicGo 状态）→ 在 `127.0.0.1:5173` 启动 Vite 并把 `/api/*` 代理到后端 → 按 Ctrl+C 时把后端和监视
进程一起关掉。前端已有的实例会被复用，不会重复启动。

**默认不会自动打开浏览器**，界面地址只打印在终端里（开发机常常同时在干别的事，弹窗抢焦点很烦）。
想让它自动打开就加 `-Open`：

```powershell
.\scripts\web.ps1 -Open
```

| 地址 | 是什么 |
| --- | --- |
| `http://127.0.0.1:5173` | 带热更新的界面（日常开发用这个） |
| `http://127.0.0.1:17860` | 后端接口本身（可以直接调 `/api/*`，见 `docs/API.md`） |

> 终端里只看到横幅是正常的：本脚本不碰浏览器，自己打开 `http://127.0.0.1:5173` 就行；确认这个
> 终端还在运行（Ctrl+C 或关掉窗口会把前后端一起带走）。`pnpm start` 与双击 `convert-md-picture.exe`
> 是另一种形态：自带界面（桌面窗口），不需要浏览器；想改成浏览器打开就加 `--browser`，只想跑服务
> 就加 `--no-open`。

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
| `pnpm web` | 开发环境：前后端一起启动，都带热更新（不自动开浏览器） |
| `pnpm start` | 构建并运行打包好的 exe（等于双击 exe，会打开桌面窗口） |
| `pnpm api` | 只启动后端（另开终端跑 `pnpm ui` 调前端也行） |
| `pnpm ui` | 只启动 Vite（假定后端已在 17860） |
| `pnpm build` | 构建前端 + 构建 exe |
| `pnpm build:go` | 只构建 exe（前端产物已提交在仓库里） |
| `pnpm test` | `go test ./...` |
| `pnpm typecheck` | 前端类型检查（`vue-tsc --noEmit`） |

端口冲突或工具装在非标准位置时用脚本参数覆盖：

```powershell
.\scripts\web.ps1 -ApiPort 17870 -WebPort 5174
.\scripts\web.ps1 -Go D:\Env\Go\bin\go.exe -Pnpm D:\Env\node\pnpm.cmd
```

脚本会自己找 Go 与 pnpm（PATH、`GOROOT`、几个常见安装目录），找不到才报错。

### 打包

```powershell
pnpm build            # 构建前端 + 构建 exe
pnpm start            # 构建 exe 并运行它（=用户双击 exe，会打开桌面窗口）
```

`pnpm start` 的常用开关（要传给脚本就写在 `--` 后面）：

```powershell
pnpm start -- -NoOpen        # 只起服务，不开窗口（给前端调试用）
pnpm start -- -Web           # 连前端一起重新构建（改了 web/ 之后用）
pnpm start -- -NoBuild       # 不重新编译，直接运行现有的 exe
pnpm start -- -Port 17900    # 换端口
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

图标（exe 文件图标、任务栏图标、浏览器标签页图标）画的是同一个标记：界面左上角那个
深色圆角方块 + 青色折角，唯一来源是 `web/src/components/AppHeader.vue` 里的 `.tb__mark`。
exe 的图标来自 `assets/icon.ico`，由 `cmd/convert-md-picture/rsrc_windows_amd64.syso`
打进去（已提交，构建时不需要联网）；窗口自己的图标在运行时从这份资源里读出来挂上去，
所以任务栏和 Alt-Tab 拿到的也是它，而不是窗口类的默认图标。
Go 只链接**主包目录**里的 `.syso`，所以这个文件必须和 `main.go` 放在一起。
删掉它再构建会尝试用 `go run github.com/akavel/rsrc@latest` 重新生成；失败也不影响构建，只是图标变成默认的。

改了那个标记之后，图标要按三步重做（脚本在 `scripts/make-icon.py`）：

```powershell
# 1. 按各尺寸把 SVG 渲染成 PNG（用浏览器截图即可，尺寸：16 24 32 48 64 128 256）
#    产物放在 %TEMP%\cmp-icon\mark-<size>.png
# 2. 打包成多尺寸 ico
python scripts\make-icon.py "$env:TEMP\cmp-icon" assets\icon.ico
# 3. 重新生成资源文件
go run github.com/akavel/rsrc@latest -ico assets\icon.ico -arch amd64 -o cmd\convert-md-picture\rsrc_windows_amd64.syso
```

网页里的标签页图标（`web/index.html` 的 `<link rel="icon">`）是同一份 SVG 的内联 data URI，
改标记时手工同步一下。

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
internal/uploadcache/     上传结果缓存（同一张图不重复上传）
web/                      Vue 3 + TypeScript 前端源码（Vite 工程）
scripts/                  开发与构建脚本（web.ps1 / start.ps1 / watch-api.ps1 / build.ps1 / test.ps1 / _tools.ps1）
tools/bench/              性能与功能基准（造语料、假图床、逐篇校验；不参与打包）
tools/mockpicgo/          开发用假图床（可控延迟/失败率，用来测界面表现；不参与打包）
package.json              根目录的命令入口（pnpm web、pnpm start、pnpm build 等快捷方式，不是前端工程）
docs/API.md               前后端接口契约
```

## 常见问题

**提示无法连接 PicGo**
PicGo 没运行，或「设置 → 设置 Server」里的服务没开启。开启后地址默认是 `http://127.0.0.1:36677`，可以在界面的设置里改。

**部分图片显示「文件不存在」**
文档里的相对路径是相对 Markdown 文件所在目录解析的，确认图片确实在那里。

**图床挂了，程序会不会一张一张慢慢等？**
不会等到底。**连续 12 张图片上传失败、且这一轮一张都没成功**，程序就认定图床连不上，
立刻停掉整轮并把原因说清楚（界面弹红条、命令行打 `错误：…图床多半连不上：<地址>`），
而不是对着几百张图一张张超时重试。PicGo 本身还活着、只是它背后的存储挂了，这种也算。

**怎么确认到底是图床还是网络的问题？**
看 PicGo 自己的日志：`%APPDATA%\picgo\picgo.log`。如果里面是 `ETIMEDOUT` / `AggregateError`，
就是图床（或它背后的对象存储）连不上，跟本程序无关；程序这边只会报告「上传失败 + 原始错误」。

**端口被占用**
图形界面会自动往后找可用端口；也可以用 `--port` 指定。

**转换后文档里还有没替换的图片**
看一下界面里该文件的展开明细，每张图都有具体原因（远程图片、文件缺失、上传失败）。

**命令行跑完什么提示都没有，文档里却还有没换的图**
扫描阶段的提示（引用式图片、路径里有空格）现在会跟着文件一起打印，形如 `提示  第 3 行：…`；
逐图失败也会逐条列出。两者都在文件那一行的下面，缩进对齐。

**提示「目标文件是只读的，覆盖不了」**
输出文件（原地模式下就是源文件本身）带着只读属性。程序不会硬闯，会跳过这一条并报出具体是哪个文件；
去掉只读属性再跑一次即可（Windows：右键 → 属性 → 取消勾选「只读」）。

**选了「输出到目录」，预检就说「输出目录是一个文件，不是目录」**
那个路径上已经有一个同名文件占着。换个目录，或先把那个文件挪走。
这类问题在预检阶段就会指出来，不会等你按下「开始转换」才失败。

**`pnpm web` 说找不到 go 或 pnpm**
用 `-Go` / `-Pnpm` 指定可执行文件的完整路径，例如 `.\scripts\web.ps1 -Go D:\Env\Go\bin\go.exe`。

**改 `web/vite.config.ts` 之后 Vite 报 `EBUSY: resource busy or locked`**
Vite 重载配置时会在 `web/` 下写一个临时目录再 watch 它，Windows 上偶发占用冲突。停掉 `pnpm web` 重新执行即可；配置本身没有问题。

**界面能打开但接口全是 404 / 连不上**
开发环境下 `http://127.0.0.1:5173/api/health` 应该返回一段 JSON。若返回失败，说明后端没起来或端口不是 17860——用 `.\scripts\web.ps1 -ApiPort <端口>` 换端口，并确认 `pnpm ui` 是在同一个 `CMP_API` 目标下启动的。
