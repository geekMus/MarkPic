# Bridge API

[English](API.md) | [简体中文](API.zh-CN.md) · back to [README](../README.md)

The UI and the Go process talk over the bridge that ships with WebView2. There is no
HTTP service, no command line and no public interface — this method table only serves
the embedded UI. It lives in `internal/app/dispatch.go`.

## Calling

```ts
const reply = await window.markpic(
  'run.start',
  JSON.stringify({ paths: ['D:/notes/a.md'], lang: 'zh' }),
)
```

`params` must be a JSON string: WebView2 deserializes each argument on its own. The
frontend always sends `lang` (`zh` / `en`) inside the params, and the Go side uses it to
pick the wording.

## Replies

```json
{ "ok": true, "data": { } }
{ "ok": false, "code": "conflict", "message": "已经有一轮在跑了" }
```

Failures never reject the promise; the UI branches on `code` and shows `message`.

| code | meaning |
| --- | --- |
| `bad_request` | invalid params |
| `conflict` | a run is already going on this page, or the state conflicts |
| `unsupported` | not available on this platform (native dialogs, for example) |
| `not_found` | unknown method |
| `internal` | internal server error |

## Methods

| method | params | returns |
| --- | --- | --- |
| `i18n.set` | `{lang}` | `{lang}` |
| `health` | — | PicGo reachability and similar status |
| `settings.get` | — | current settings |
| `settings.put` | settings fields | the saved settings |
| `settings.closeAction` | `{action}` | `{closeAction}`; only the close behaviour, leaving an unsaved draft alone |
| `dialog.files` | — | `{paths}` |
| `dialog.folder` | — | `{paths}` |
| `scan.start` | `{scanId, paths}` | accepted; results arrive as `scan.file` events, one per requested path |
| `scan.cancel` | `{scanId, path?}` | `{cancelled}`; with `path` only that file is cancelled |
| `run.start` | `{op, paths}` | starts a run; `op` is `upload` / `check` / `download` (not a setting) |
| `run.cancel` | `{runId}` | `{ok}` |
| `reveal` | `{path}` | `{ok}`; reveal in the file manager |
| `open` | `{target}` | `{ok}`; open a file or folder |
| `update.status` | — | current update state (offline) |
| `update.check` | — | check for updates; the reason for a failure lands in `status.error` |
| `update.install` | — | download, verify, replace and restart |

## Events

Go pushes through `window.__markpicEvents(payload)`. `payload` is either a single event
or an array of events; terminal events (`run.finished`, `run.cancelled`, `scan.done`) are
never batched or delayed.

| type | key fields |
| --- | --- |
| `run.started` | `runId`, `mode`, `files` |
| `file.started` | `runId`, `id` |
| `file.progress` | `runId`, `id`, `uploaded`, `done`, `total`, `items`, `summary` |
| `file.finished` | `runId`, `id`, `result`, `summary` |
| `run.cancelled` | `runId` |
| `run.finished` | `runId`, `summary` |
| `log` | `level`, `message` |
| `scan.file` | `runId` (`scanId` when scanning), `path`, `scanned`, `cancelled`, `timedOut` |
| `scan.done` | `runId` (`scanId` when scanning) |

`mode` in `run.started` carries the operation name (`upload` / `check` / `download`); each
page claims the run that belongs to it. `summary` on `file.progress` / `file.finished` is the
run's running total, so the UI never recomputes counters of its own.
