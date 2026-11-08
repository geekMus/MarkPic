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
  concurrency: number       // 1..16，默认 4
  retries: number           // 0..5，默认 2
  output: 'sidecar' | 'in-place' | 'directory'
  suffix: string            // 默认 ".converted"
  outDir: string            // output === 'directory' 时使用
  backup: boolean           // in-place 模式是否留 .bak
  dryRun: boolean           // 只解析、不上传、不写文件
}

type ImageStatus =
  | 'uploaded'   // 本次上传成功
  | 'reused'     // 同一张图本轮已上传过，复用 URL（去重命中）
  | 'remote'     // 本来就是网络图片，跳过
  | 'missing'    // 本地文件不存在
  | 'failed'     // 上传失败
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
  status: 'ok' | 'partial' | 'no-images' | 'failed' | 'preview'
  images: number        // 文档里解析出的图片引用总数
  uploaded: number      // 已经指向图床的引用数（含 reused），不是发出的上传请求数
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

| 方法 | 路径 | 请求体 | 响应 |
| --- | --- | --- | --- |
| GET | `/api/health` | — | `{ app, version, endpoint, picgo: { online: boolean, detail: string } }` |
| GET | `/api/settings` | — | `Settings` |
| PUT | `/api/settings` | `Settings` | `Settings`（校验失败 400） |
| POST | `/api/dialog/files` | — | `{ paths: string[] }`；不支持原生对话框时 `501` |
| POST | `/api/dialog/folder` | — | `{ paths: string[] }`；不支持时 `501` |
| POST | `/api/preview` | `{ paths: string[], settings?: Partial<Settings> }` | `{ files: FileResult[], summary: RunSummary }` |
| POST | `/api/run` | `{ paths: string[], settings?: Partial<Settings> }` | `{ runId: string }` |
| POST | `/api/run/cancel` | `{ runId: string }` | `{ ok: boolean }` |
| POST | `/api/reveal` | `{ path: string }` | `{ ok: boolean }`（在资源管理器中定位） |
| POST | `/api/open` | `{ target: string }` | `{ ok: boolean }`（打开文件/目录/URL） |
| GET | `/api/events` | — | `text/event-stream` |

### `/api/preview` 与 `/api/run` 的 paths

`paths` 可以是 `.md` 文件，也可以是目录（递归查找 `*.md`）。
去重规则：同一绝对路径只处理一次；同一张图片（按绝对路径）在一轮里只上传一次。

`settings` 可以只给需要覆盖的字段，服务端会把它们叠加到已保存的设置上再校验；
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

## 安全

- 只监听 `127.0.0.1`。
- 校验 `Host` 必须是 `127.0.0.1:<port>` 或 `localhost:<port>`。
- 校验 `Origin`（若存在）必须是本服务自身来源，否则 403。
