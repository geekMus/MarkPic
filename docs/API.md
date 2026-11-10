# MarkPic — 本地 HTTP API 契约

GUI 模式下，单文件 exe 会在 `127.0.0.1:<port>` 启动一个**仅监听回环地址**的 HTTP 服务，
前端静态资源通过 `go:embed` 打进二进制，API 与静态资源同源。

约定：

- 所有请求/响应均为 JSON（`Content-Type: application/json; charset=utf-8`）。
- 路径参数使用 Windows 绝对路径（形如 `D:\\docs\\a.md`），JSON 字符串中需转义反斜杠。
- 出错时返回非 2xx + `{ "error": { "code": "…", "message": "…" } }`。
- 服务端会校验 `Origin`/`Host`，仅接受回环来源，避免被任意网页调用。

## 类型

```ts
type Settings = {
  endpoint: string          // PicGo 本地服务地址，默认 "http://127.0.0.1:36677"
  concurrency: number       // 1..64，默认 48
  retries: number           // 0..5，默认 2
  output: 'sidecar' | 'in-place' | 'directory'
  suffix: string            // 默认 ".converted"
  outDir: string            // output === 'directory' 时使用
  backup: boolean           // in-place 模式是否留 .bak
  dryRun: boolean           // 只解析、不上传、不写文件
  reuseUploads: boolean     // 复用上次的上传结果（默认 true）
  remote: 'ignore' | 'check' | 'download'  // 文章里的远程图怎么处理（默认 ignore）
  remoteDir: string         // download 模式把图放到文档旁边的哪个子目录（默认 "images"）
}

type ImageStatus =
  | 'uploaded'   // 本次上传成功
  | 'reused'     // 同一张图本轮已上传过，复用 URL（去重命中）
  | 'cached'     // 上一次运行时传过同一张图，沿用当时拿到的地址
  | 'remote'     // 本来就是网络图片，跳过
  | 'missing'    // 本地文件不存在
  | 'failed'     // 上传失败
  | 'cancelled'  // 运行被取消，这张图没传（不算失败）
  | 'pending'    // 仅预检（dryRun）：本地文件存在，尚未上传
  | 'localized'  // download 模式：远程图已下载到本地，url 是写回文档的相对路径
  | 'local-kept' // check / download 模式：本地图片原样保留（这两种模式不上传）
  | 'remote-ok'  // check 模式：地址能打开，拿到的确实是图片
  | 'remote-bad' // check 模式：地址打不开，或者返回的不是图片

// 'remote' 除了「ignore 模式下原样留着」，也是 ftp:// / data: / mailto: 这类
// 碰不了的地址的归宿：状态仍是 'remote'（文档确实没动），error 里写明为什么
// 处理不了，并计进 FileResult.remoteUnsupported。

type ImageItem = {
  target: string        // 文档里写的原始地址
  absolutePath: string  // 解析后的绝对路径（remote 时为空）
  kind: 'markdown' | 'html'
  line: number          // 1-based
  status: ImageStatus
  url?: string          // uploaded/reused 时的新地址
  error?: string        // failed/missing 时的原因
}

type FileResult = {
  id: string
  path: string
  name: string
  dir: string
  status: 'ok' | 'partial' | 'no-images' | 'failed' | 'cancelled' | 'preview' | 'checked'
  // checked = check 模式：只检测了远程地址，没有写任何文件
  images: number        // 文档里解析出的图片引用总数
  uploaded: number      // 已经指向图床的引用数（含 reused/cached），不是发出的上传请求数
  reused?: number       // 其中靠复用（本轮去重或上次缓存）拿到的，按引用处算
uploadRequests?: number // 真的发给图床的次数：内容相同的图只发一次，所以这个数可能小于 uploaded
  localized?: number    // download 模式：这一篇里有几张远程图落到了本地
  dead?: number         // check 模式：这一篇里有几个远程地址打不开（或不是图片）
  checked?: number      // check 模式：这一篇里一共检测了几个远程地址
  deadUnique?: number   // 失效地址里去重后的个数：同一个死链被引用 3 次时 dead 是 3、
                        // deadUnique 是 1。命令行与界面的「失效 N 个地址（M 处引用）」
                        // 靠这两个数说清，不然会和结尾那份按地址去重的清单打架
  remoteUnsupported?: number
                        // 这一篇里有几处地址根本碰不了（ftp:// / data: / mailto:），
                        // 既不算 checked 也不算 dead；文档里照原样留着
  remoteFailed?: number // download 模式：几处远程地址没拿到图（连不上、超时）。
                        // 同时也算在 failed 里；界面的「复制清单」靠它把远程失败
                        // 和本地图片读不出来分开
  localMissing?: number // check/download 模式：这一篇里有几张本地图根本不在
                        // （不碰本地图，不等于不用看它）
  remoteKept?: number   // ignore 模式：这一篇里有几处远程图原样留着
                        // （同时算在 skipped 里，只用来把「没有可处理的图片」说清楚）
  remoteFailedUnique?: number
                        // 这些失败地址去重后的个数（remoteFailed 按引用处算）；
                        // 日志里「没下载回来 1 个地址（3 处引用）」用的是它
  failed: number
  skipped: number
  outputPath?: string
  error?: string
  warnings?: string[]   // 扫描阶段的提示，例如「引用式图片的标签没有定义」「图片路径里有空格」
                        // 以及「这处引用是附件（.ps/.eps/.pdf…），已跳过」；界面和命令行都会显示，
                        // 命令行缩进成「提示  …」
  items: ImageItem[]    // 逐图明细，任何时候都会返回（失败/预检也带），界面展开靠它
}

type RunSummary = {
  runId: string
  files: number
  ok: number
  partial: number
  failed: number
  noImages: number
  images: number
  uploaded: number
  failedImages: number
  localized?: number      // download 模式：这一轮下载了几张远程图
  remoteChecked?: number  // check 模式：检测了几处远程引用（同一张图引用多次按处数算）
  remoteCheckedUnique?: number // 去重后真正探活过的地址数（引用 10 次也只算 1）
  remoteDead?: number     // check 模式：其中几个打不开
  remoteDeadUnique?: number
                          // 失效地址去重后的个数（remoteDead 按引用处算）
  remoteFailed?: number   // download 模式：没拿回来的引用数（下载失败、超时、连不上）
  remoteFailedUnique?: number
                          // 上面那些地址去重后的个数：「没下载回来 2 个地址（3 处引用）」
  filesChecked?: number   // check 模式：几个文件只是被检测（没写输出）
  remoteReused?: number   // 上次已下过、这次没再下载的远程图张数
  localMissing?: number   // check/download 模式：这一轮里几张本地图找不到
  remoteKept?: number     // ignore 模式：这一轮保留了多少处远程图
  cancelled: boolean
  // 这一轮被提前掐掉的原因（目前只有「图床成片失败」一种）。
  // 取消事件也带 summary，所以客户端能把「为什么停了」说清楚。
  abortReason?: string
  durationMs: number
}
```

## 端点

所有接口都看 `Accept-Language` 请求头（`zh*` / `en*`，按 `q=` 排序取第一个支持的，其它一律回中文）：错误 `message`、`picgo.detail`、`items[].error`、更新相关的报错都会用这个语言返回。前端在 `web/src/i18n/index.ts` 里统一加这个头。

| 方法 | 路径 | 请求体 | 响应 |
| --- | --- | --- | --- |
| GET | `/api/health` | — | `{ app, version, endpoint, picgo: { online: boolean, detail: string, needed: boolean }, dataDir: string, dataNote?: string, settingsNote?: string }` |
| GET | `/api/settings` | — | `Settings` |
| PUT | `/api/settings` | `Partial<Settings>` | `Settings`（校验失败 400） |
| POST | `/api/dialog/files` | — | `{ paths: string[] }`；不支持原生对话框时 `501` |
| POST | `/api/dialog/folder` | — | `{ paths: string[] }`；不支持时 `501` |
| POST | `/api/preview` | `{ paths: string[], settings?: Partial<Settings> }` | `{ files: FileResult[], summary: RunSummary }` |
| POST | `/api/run` | `{ paths: string[], settings?: Partial<Settings> }` | `{ runId: string }`；已经有一次转换在跑时返回 `409`（`error.code = "conflict"`）；请求体超过 4 MB 返回 `413`（`error.code = "too_large"`） |
| GET | `/api/run/current` | — | `{ running, done, runId?, files?, results?, summary? }`；服务端记着「这一轮」的现状，刷新页面或断线重连后前端靠它把界面接回来（`done=true` 的那份只回一次，再问就清掉） |
| POST | `/api/run/cancel` | `{ runId: string }` | `{ ok: boolean }` |
| POST | `/api/reveal` | `{ path: string }` | `{ ok: boolean }`（在资源管理器中定位） |
| POST | `/api/open` | `{ target: string }` | `{ ok: boolean }`（打开文件/目录/URL） |
| GET | `/api/events` | — | `text/event-stream` |
| GET | `/api/update/status` | — | `UpdateStatus` |
| POST | `/api/update/check` | — | `UpdateStatus`（检查失败也返回 200，原因在 `error` 里） |
| POST | `/api/update/install` | — | `UpdateStatus`；`501` 表示这个运行形态不支持自更新 |

`/api/health` 里的 `picgo.needed` 说明**这次运行到底要不要 PicGo**：`remote` 为 `check` / `download`
时不上传本地图片，PicGo 没开也不算故障（界面据此把状态点显示成中性的「本次不需要 PicGo」）。

`/api/health` 里的 `settingsNote` 只在设置文件读不出来时出现（此时进程用的是默认设置）。
出现它说明用户的 `settings.json` 有问题，原文件已被另存为 `settings.json.broken`。

`/api/health` 里的 `dataDir` 是设置与 WebView 缓存所在的位置（默认 exe 旁边的 `data` 目录，
见 `internal/appdata`）；只有没能放到那里时才带 `dataNote` 说明原因（例如 exe 装在 Program Files
里写不进去）。`/api/reveal` 传这个目录就能在资源管理器里定位过去。

### 更新

```ts
type UpdateAsset = { url: string; sha256: string; size: number }
type UpdateState =
  | 'idle' | 'checking' | 'latest' | 'available'
  | 'downloading' | 'installing' | 'restarting' | 'error'
interface UpdateStatus {
  state: UpdateState
  current: string          // 当前版本号
  latest: string           // 清单里的版本号
  tag: string              // 例如 v1.0.1
  notes: string            // 更新说明（Markdown 原文）
  published: string        // 清单里的日期
  asset: UpdateAsset | null
  received: number         // 下载已收到字节
  total: number            // 安装包总字节（未知为 0）
  error: string
}
```

版本清单不调用 GitHub Releases API，而是取仓库根那个静态的 `version.json`
（`https://raw.githubusercontent.com/geekMus/MarkPic/main/version.json`），
可用环境变量 `MARKPIC_UPDATE_MANIFEST` 指向别处（自建镜像、本地调试）。

`POST /api/update/install` 会下载安装包、核对清单里的 SHA-256，然后
把当前 exe 改名成 `.old`、把新 exe 放到原路径、启动新进程、自己退出。
新进程带 `--handover-port <端口>` 先等端口空出来，避免被当成「已经有实例在运行」。
`state` 一路变成 `downloading` → `installing` → `restarting`；客户端轮询
`/api/update/status` 就能看到进度，连不上服务端即说明进程已经退出。

### `/api/preview` 与 `/api/run` 的 paths

`paths` 可以是 `.md` 文件，也可以是目录（递归查找 `*.md`）。
去重规则：同一绝对路径只处理一次；同一张图片（按绝对路径）在一轮里只上传一次。

`settings` 可以只给需要覆盖的字段，服务端会把它们叠加到已保存的设置上再校验；
`PUT /api/settings`、`/api/run` 和 `/api/preview` 都是这个规则，漏发字段不会把已保存的设置重置成零值。
`/api/preview` 会强制 `dryRun = true`（不上传、不写文件）。

### `/api/events`（SSE）

服务端先把事件订阅登记好再返回响应头，因此连接建立后不会漏掉紧接着的 `run.started`。
每个事件都以**具名事件**发送（`event: <type>` + `data: <json>`）。
注意：浏览器对具名事件**不会**触发 `onmessage`，必须按类型监听：

```ts
// 每个类型都要单独注册（事件负载里的 type 字段同样可用于分发）
for (const type of ['run.started', 'file.started', 'file.progress',
                    'file.finished', 'run.finished', 'run.cancelled', 'log']) {
  source.addEventListener(type, (e) => handle(JSON.parse((e as MessageEvent).data)))
}
```

事件类型：

```ts
type Event =
  | { type: 'run.started';  runId: string; files: { id: string; path: string; name: string; dir: string; images: number }[] }
  | { type: 'file.started'; runId: string; id: string }
  | { type: 'file.progress'; runId: string; id: string; uploaded: number; total: number }
  | { type: 'file.finished'; runId: string; id: string; result: FileResult }
  | { type: 'run.finished'; runId: string; summary: RunSummary }
  | { type: 'run.cancelled'; runId: string; summary?: RunSummary }
  | { type: 'log'; level: 'info' | 'warn' | 'error'; message: string }
```

前端只需在启动时连接一次 `/api/events`，服务端广播全部事件；用 `runId` 过滤。

**背压语义（重要）**：进度事件是「每张图片一条」，大文档量下会有上万条。服务端不会把它们
原样堆给客户端，而是：

- **文件级事件按文件合并**——`file.started`、`file.progress`、`file.finished` 对同一个文件
  只占一个槽，留下最新那条。同一个文件的事件顺序仍然是从早到晚（不会出现 finished 之后又
  收到 progress），但**中间状态可能整个看不到**：一个文件跑得快时，客户端可能只收到它的
  `file.finished`；跑得慢时能收到递增的进度，但不保证每张图片都有一条（想拿精确的逐图
  结果请看 `file.finished` 里的 `result`）；
- 一批事件合并成一次写出、一次 flush；没有终止事件时最多再攒 40 ms 再发（同一文件的
  中间状态因此在发出前就互相覆盖掉了）；
- `run.started` 一定最先到，`run.finished` 与 `run.cancelled` **保证送达**，即使客户端读得
  很慢（队列满时先丢最旧的普通事件，但绝不丢终止事件）。

因此客户端应当把事件当作「状态的增量提示」而不是「完整日志」：逐图明细以
`file.finished` 的 `FileResult` 为准，整体结果以 `run.finished` 的 `summary` 为准。
每个文件的**最终结果一定会送到**（5000 篇文档的实测里 `file.finished` 恰好 5000 条），
这一条是界面行状态不撒谎的前提。

## 安全

- 只监听 `127.0.0.1`。
- 校验 `Host` 必须是 `127.0.0.1:<port>` 或 `localhost:<port>`。
- 校验 `Origin`（若存在）必须是本服务自身来源，否则 403。
