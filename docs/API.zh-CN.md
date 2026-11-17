# Bridge API

[English](API.md) | [简体中文](API.zh-CN.md) · 返回 [README](../README.zh-CN.md)

界面和 Go 进程通过 WebView2 自带的 bridge 通信。这个程序没有 HTTP 服务、命令行或对外接口；下面这套方法只服务于内嵌界面。方法表在 `internal/app/dispatch.go`。

## 调用

```ts
const reply = await window.markpic(
  'run.start',
  JSON.stringify({ paths: ['D:/notes/a.md'], lang: 'zh' }),
)
```

`params` 必须是 JSON 字符串：WebView2 会把每个参数单独反序列化。前端统一在参数里带 `lang`（`zh` / `en`），Go 侧据此返回对应语言的文案。

## 响应

```json
{ "ok": true, "data": { } }
{ "ok": false, "code": "conflict", "message": "已经有一轮在跑了" }
```

失败不会 reject Promise；界面按 `code` 分支、把 `message` 展示给用户。

| code | 含义 |
| --- | --- |
| `bad_request` | 参数不合法 |
| `conflict` | 同一页已有一轮在跑，或状态冲突 |
| `unsupported` | 当前平台不支持（例如原生对话框） |
| `not_found` | 未知方法 |
| `internal` | 服务端内部错误 |

## 方法

| 方法 | 参数 | 返回 |
| --- | --- | --- |
| `app.hello` | `{lang}` | 版本、数据目录、语言 |
| `i18n.set` | `{lang}` | `{lang}` |
| `health` | — | PicGo 连通性等状态 |
| `settings.get` | — | 当前设置 |
| `settings.put` | 设置字段 | 保存后的设置 |
| `settings.closeAction` | `{action}` | `{closeAction}`；只改关闭行为，不碰未保存的设置草稿 |
| `dialog.files` | — | `{paths}` |
| `dialog.folder` | — | `{paths}` |
| `preview` | `{paths, settings?}` | 扫描预览 |
| `scan.start` | `{scanId, paths, settings?}` | 已受理；结果通过 `scan.file` 推送 |
| `scan.cancel` | `{scanId, path?}` | `{cancelled}`；带 `path` 只取消那一份 |
| `run.start` | `{paths, settings?}` | 启动一轮转换；`mode` 由设置里的远程模式决定 |
| `run.current` | `{mode?}` | 该页当前那一轮的快照，用于界面重载后接回 |
| `run.cancel` | `{runId}` | `{ok}` |
| `reveal` | `{path}` | `{ok}`；在资源管理器中定位 |
| `open` | `{target}` | `{ok}`；打开文件或目录 |
| `update.status` | — | 当前更新状态（不联网） |
| `update.check` | — | 检查更新；失败原因写在 `status.error` |
| `update.install` | — | 下载、校验、替换并重启 |

## 事件

Go 侧通过 `window.__markpicEvents(payload)` 推送。`payload` 可以是单个事件，也可以是事件数组；终止事件（`run.finished`、`run.cancelled`、`scan.done`）不批量延迟。

| type | 关键字段 |
| --- | --- |
| `run.started` | `runId`、`mode`、`files` |
| `file.started` | `runId`、`id` |
| `file.progress` | `runId`、`id`、`uploaded`、`done`、`total`、`items` |
| `file.finished` | `runId`、`id`、`result` |
| `run.cancelled` | `runId` |
| `run.finished` | `runId`、`summary` |
| `log` | `level`、`message` |
| `scan.file` | `runId`（扫描时放 `scanId`）、`path`、`scanned`、`cancelled`、`timedOut` |
| `scan.done` | `runId`（扫描时放 `scanId`） |

`run.started` 里的 `mode` 区分上传 / 检测 / 下载三页，界面按它认领自己的那一轮。页面刷新后先调 `run.current` 拿回当前快照，再继续接收事件。
