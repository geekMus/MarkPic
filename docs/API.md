# MarkPic 前后端契约（窗口 bridge）

这不是 HTTP 接口。MarkPic 是一个纯桌面程序：窗口里跑的是前端，窗口进程里跑的是 Go，
两边通过 WebView2 自带的 bridge 直接说话，不经过 localhost，也没有任何端口可以连。

**这个文件描述的是"界面和 Go 之间怎么说话"，不是"外部程序怎么调 MarkPic"。**
MarkPic 不提供外部调用接口；需要脚本化时，等专门那套接口做出来再说（见 README「只有一个窗口」）。

## 调用形状

前端调 Go：

```ts
const reply = await window.markpic('settings.get', JSON.stringify({ lang: 'zh-CN' }))
```

- 方法名是第一个参数，第二个参数永远是**一段 JSON 文本**（不是对象）。
  go-webview2 的 `Bind` 按字符串解参数，直接传对象会被当成 `[object Object]`。
- 返回的是一个 Promise。**它不是"发出去就算"，必须 await**；
  以前按同步调用写，`reply.ok` 永远是 `undefined`，界面会一直卡在"没能对上话"。
- 每次调用都带上当前语言（前端自动塞 `lang`），Go 侧按它生成本地化提示。

Go 回前端：

```json
{ "ok": true, "data": { } }
{ "ok": false, "code": "conflict", "message": "已经有一次转换在跑了" }
```

错误码：

| `code` | 什么时候出现 |
| --- | --- |
| `bad_request` | 参数解不开、少了必填项、设置值越界 |
| `conflict` | 已经有一轮在跑（`run.start`） |
| `unsupported` | 这个运行形态做不到（例如没有原生文件对话框的平台） |
| `not_found` | 未知的方法名、要定位的路径不存在 |
| `internal` | 其他（读写失败、PicGo 连不上等） |

## 方法表

| 方法 | 参数 | 返回 `data` |
| --- | --- | --- |
| `app.hello` | `{ lang? }` | `{ app, name, version, lang, dataDir, dataNote? }` |
| `i18n.set` | `{ lang }` | `{ ok: true }`（让后续提示换语言） |
| `health` | — | `{ app, version, endpoint, picgo: { online, detail, needed }, dataDir, dataNote?, settingsNote? }` |
| `settings.get` | — | `Settings` |
| `settings.put` | `Partial<Settings>` | 合并后的 `Settings`（只改发出来的字段，没发的保持原值） |
| `dialog.files` | — | `{ paths: string[] }` |
| `dialog.folder` | — | `{ paths: string[] }` |
| `preview` | `{ paths: string[], settings?: Partial<Settings> }` | `{ files: FileResult[], summary: Summary }`（强制 `dryRun`） |
| `run.start` | `{ paths: string[], settings?: Partial<Settings> }` | `{ runId: string }` |
| `run.current` | — | `{ running, done, runId?, files?, results?, summary? }` |
| `run.cancel` | `{ runId: string }` | `{ ok: boolean }` |
| `reveal` | `{ path: string }` | `{ ok: boolean }`（在资源管理器里定位） |
| `open` | `{ target: string }` | `{ ok: boolean }`（打开文件/目录/URL） |
| `update.status` | — | `UpdateStatus` |
| `update.check` | — | `UpdateStatus`（检查失败也回 `ok`，原因在 `error` 里） |
| `update.install` | — | `UpdateStatus` |

`Settings`、`FileResult`、`Summary` 的字段与 `internal/settings`、`internal/conv` 里的结构体一一对应
（JSON 标签就是字段名），这里不重复抄一遍——两边共用同一份 Go 结构体，抄一遍只会抄歪。

## 事件

Go 主动推给前端，走 `window.__markpicEvents(payload)`：

- `payload` 可能是**一个事件**，也可能是**一个数组**（批量）。前端必须两种都认。
- 事件对象就是 `conv.Event`：`type`、`runId`、`id`、`files`、`uploaded`、`done`、`total`、
  `result`、`summary`、`level`、`message`。
- `type` 的取值：`run.started`、`file.started`、`file.progress`、`file.finished`、
  `log`、`run.cancelled`、`run.finished`。
- 终止事件（`run.finished` / `run.cancelled`）**不会被批量窗口吞掉**：它们立刻发，不等 40ms 的合批。
  这条是硬要求——以前 SSE 的缓冲区灌满时丢掉过终止事件，界面就永远停在"转换中"。

事件里的 `uploaded` 是"这一篇已经替换成功几处"，`done` 是"这一篇已经处理了几张图"。
上传模式看前者，检测/下载模式看后者；两个数混用会让只检测的一轮冒出"替换 13"。

## 页面刷新之后

`run.current` 是给这个场景准备的：开发时改了前端会热更新、刷新页面，
前端启动后先问一次它，把队列、进度和已完成的结果接回来，然后接着吃增量事件。

窗口关掉就是进程退出——那一轮随之中断，不存在"刷新后接着跑"这回事。