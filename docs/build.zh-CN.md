# 构建与开发

[English](build.md) | [简体中文](build.zh-CN.md) · 返回 [README](../README.zh-CN.md)

## 环境

编二进制需要 Go 1.24+。改界面还要 Node 20.19+ 和 pnpm——构建脚本会自动跑
`pnpm --dir web install`。

## 常用命令

在仓库根目录执行：

| 命令 | 作用 |
| --- | --- |
| `npm run build` | 构建前端，再编出 `bin/MarkPic.exe` |
| `npm run build:go` | 只编 Go（要求已有 `internal/ui/dist`） |
| `npm run start` | 起 Vite 5173 + 带热更新的窗口 |
| `npm run start:mock` | 同上，但连假图床，不碰 PicGo |
| `npm run test` | `go test ./... -count=1` |
| `npm run typecheck` | 只跑 `vue-tsc --noEmit` |

`internal/ui/dist` 不进版本库，它是 `go:embed` 的输入。`build`、`start`、`test` 发现产物
缺失时会自动构建前端。直接 `go run ./cmd/markpic` 也可以，但同样要先有 `internal/ui/dist`。

## 目录结构

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
| `internal/platform` | 原生对话框、ANSI 代码页 |
| `internal/update` | 更新检查与安装 |
| `internal/ui` | 前端产物（`go:embed`） |
| `web` | Vue 3 + TypeScript + Vite 前端 |
| `scripts` | 构建、启动、测试脚本 |
| `tools/bench` | 基准测试 |
| `tools/mockpicgo` | 假图床 |

图标资源在 `assets/`；常规构建直接使用已提交的 `.ico` 和 `.syso`，不需要重新生成。

## 分支与发版

- 开发在 `dev` 上进行，`main` 只接受来自 `dev` 的合并，由分支规则强制。
- 合并到 `main` 会触发「构建并发布」。只有改动落在 `cmd/`、`internal/`、`web/`、
  `scripts/`、`assets/`、`tools/`、`go.mod`、`go.sum`、`package.json` 时才会发版——
  纯文档的合并不发版。
- 版本号就是发布当天的日期（CalVer，`Asia/Shanghai`）；同一天第二次发布顺延成
  `2026.11.17.1`，靠 tag 探测，不落状态。
- 提交标题里带 `[no-release]` 时照常构建和跑测试，但不发布。
- 发布产物是 exe 加 `version.json`——客户端靠这份清单发现更新。整个流程不往仓库里推东西。
  清单里的 `notesBody` 是整篇更新说明，程序的「更新说明」对话框直接读它。
- 更新说明由 `scripts/release-notes.mjs` 从上一次 tag 到现在的提交生成。本地预览：

```bash
npm run notes -- --version 2026.11.17 --dry-run
```

「同步 main 到 dev」会在每次合并后把 `main` 合回 `dev`；「主从同步检查」会拦住
来源分支未包含最新 `main` 的 PR。
