# convert-md-picture — 本地 HTTP API 契约

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
  concurrency: number       // 1..32，默认 16
  retries: number           // 0..5，默认 2
  output: 'sidecar' | 'in-place' | 'directory'
  suffix: string            // 默认 ".converted"
  outDir: string            // output === 'directory' 时使用
  backup: boolean           // in-place 模式是否留 .bak
  dryRun: boolean           // 只解析、不上传、不写文件
  reuseUploads: boolean     // 复用上次的上传结果（默认 true）
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
  status: 'ok' | 'partial' | 'no-images' | 'failed' | 'cancelled' | 'preview'
  images: number        // 文档里解析出的图片引用总数
  uploaded: number      // 已经指向图床的引用数（含 reused/cached），不是发出的上传请求数
  reused?: number       // 其中靠复用（本轮去重或上次缓存）拿到的，按引用处算
  failed: number
  skipped: number
  outputPath?: string
  error?: string
  warnings?: string[]   // 例如「检测到引用式图片 ![]\[id\]，暂不支持」
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
  cancelled: boolean
  durationMs: number
}
```

## 端点

所有接口都看 `Accept-Language` 请求头（`zh*` / `en*`，按 `q=` 排序取第一个支持的，其它一律回中文）：错误 `message`、`picgo.detail`、`items[].error`、更新相关的报错都会用这个语言返回。前端在 `web/src/i18n/index.ts` 里统一加这个头。

| 方法 | 路径 | 请求体 | 响应 |
| --- | --- | --- | --- |
| GET | `/api/health` | — | `{ app, version, endpoint, picgo: { online: boolean, detail: string }, dataDir: string, dataNote?: string }` |
| GET | `/api/settings` | — | `Settings` |
| PUT | `/api/settings` | `Partial<Settings>` | `Settings`（校验失败 400） |
| POST | `/api/dialog/files` | — | `{ paths: string[] }`；不支持原生对话框时 `501` |
| POST | `/api/dialog/folder` | — | `{ paths: string[] }`；不支持时 `501` |
| POST | `/api/preview` | `{ paths: string[], settings?: Partial<Settings> }` | `{ files: FileResult[], summary: RunSummary }` |
| POST | `/api/run` | `{ paths: string[], settings?: Partial<Settings> }` | `{ runId: string }` |
| POST | `/api/run/cancel` | `{ runId: string }` | `{ ok: boolean }` |
| POST | `/api/reveal` | `{ path: string }` | `{ ok: boolean }`（在资源管理器中定位） |
| POST | `/api/open` | `{ target: string }` | `{ ok: boolean }`（打开文件/目录/URL） |
| GET | `/api/events` | — | `text/event-stream` |
| GET | `/api/update/status` | — | `UpdateStatus` |
| POST | `/api/update/check` | — | `UpdateStatus`（检查失败也返回 200，原因在 `error` 里） |
| POST | `/api/update/install` | — | `UpdateStatus`；`501` 表示这个运行形态不支持自更新 |

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
（`https://raw.githubusercontent.com/geekMus/convert-md-picture/main/version.json`），
可用环境变量 `CMP_UPDATE_MANIFEST` 指向别处（自建镜像、本地调试）。

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
  | { type: 'run.cancelled'; runId: string }
  | { type: 'log'; level: 'info' | 'warn' | 'error'; message: string }
```

前端只需在启动时连接一次 `/api/events`，服务端广播全部事件；用 `runId` 过滤。

**背压语义（重要）**：进度事件是「每张图片一条」，大文档量下会有上万条。服务端不会把它们
原样堆给客户端，而是：

- `file.progress` **按文件合并**——同一个文件只保留最新的那一条，因此收到的进度一定
  是递增的、但**不保证每张图片都有一条**（想拿精确的逐图结果请看 `file.finished` 里的
  `result`）；
- 一批事件合并成一次写出、一次 flush，客户端可能一次收到多条；
- `run.finished` 与 `run.cancelled` **保证送达**，即使客户端读得很慢（队列满时先丢旧的
  普通事件，但绝不丢终止事件）。

因此客户端应当把事件当作「状态的增量提示」而不是「完整日志」：逐图明细以
`file.finished` 的 `FileResult` 为准，整体结果以 `run.finished` 的 `summary` 为准。

## 安全

- 只监听 `127.0.0.1`。
- 校验 `Host` 必须是 `127.0.0.1:<port>` 或 `localhost:<port>`。
- 校验 `Origin`（若存在）必须是本服务自身来源，否则 403。
