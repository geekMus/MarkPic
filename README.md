# MarkPic

[English](README.md) | [简体中文](README.zh-CN.md)

MarkPic is a desktop app for the images inside Markdown documents. It does three
independent jobs:

- **Upload** — find the local images a document references, send them to your
  [PicGo](https://github.com/Molunerfinn/PicGo) image host, and rewrite the links to
  the hosted URLs.
- **Check** — walk the remote images an article already points at and report the ones
  that are dead, are not images, or come back truncated. Read-only.
- **Download** — pull those remote images into a local `images/` folder and rewrite
  the links to relative paths.

It ships as one self-contained app with the UI embedded: no Node.js, no local server,
no command line. Uploads go through PicGo's local HTTP interface, so MarkPic never
sees your image-host credentials.

## At a glance

| Job | What it touches | Rewrites the document | Needs PicGo running |
| --- | --- | --- | --- |
| Upload | local images → image host | yes | yes |
| Check | remote image URLs | no | no |
| Download | remote images → `images/` | yes | no |

Documents keep their original encoding — UTF-8 (with or without BOM), UTF-16 with a
BOM, and the machine's ANSI code page. Apart from the rewritten image addresses,
everything comes back byte-for-byte identical.

## Install

Download `MarkPic-v<version>-windows-amd64.exe` from
[Releases](https://github.com/geekMus/MarkPic/releases). There is no installer: put
it anywhere and run it. An installed copy updates itself; settings and caches live in
the `data/` folder next to the executable.

Windows is the only platform published so far; macOS and Linux are planned, but not
verified yet.

## Quick start

1. Install and start [PicGo](https://github.com/Molunerfinn/PicGo), then enable its
   server under **Settings → Server** (default `http://127.0.0.1:36677`).
2. Drop `.md` files or a folder onto the MarkPic window.
3. Choose **Upload**, **Check** or **Download** on the left and start.

## Documentation

- [Usage](docs/usage.md) — supported syntax, output modes, settings, data directory
- [Build & development](docs/build.md) — toolchain, scripts, repository layout
- [Bridge API](docs/API.md) — internal WebView2 bridge (Chinese)
