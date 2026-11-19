# Usage

[English](usage.md) | [简体中文](usage.zh-CN.md) · back to [README](../README.md)

## The three jobs

| Job | What it touches | Rewrites the document | Needs PicGo running |
| --- | --- | --- | --- |
| Upload | local images → image host | yes | yes |
| Check | remote image URLs | no | no |
| Download | remote images → `images/` | yes | no |

- **Upload** leaves the remote images an article already has exactly as they are.
- **Check** is read-only, but it also flags local images the document references and
  the disk no longer has.
- **Download** only touches remote images; local ones are left alone.

Drop a single file, several files, or a whole folder. The path comes from the native
drop target — WebView2 itself never exposes the local path of a dropped file. A folder
is scanned recursively and immediately expanded into one card per document — you never
see a folder card. **Every text file counts**: `.md`, `.txt`, `.log`, extension-less
notes, whether or not it has an image to upload. A document with nothing to upload still
gets a card, so the queue always matches what is in the folder. Only binaries are skipped:
images, archives, executables, PDFs and Office files.

A big folder (a whole drive, a cloud sync root) is scanned while you watch: the folder row
shows how many documents have been found so far, and cards appear as they are read. Scanning
has no overall time limit — only an inactivity watchdog — and you can stop it at any time;
after confirming, everything already found stays in the queue and the row tells you the rest
of that folder was not scanned.

Watch the queue, cancel mid-run, retry failures, filter down to problems only, and expand
any row for per-image details. The same image is never uploaded twice, and an unchanged
image that was already uploaded or downloaded is reused from cache.

## Output modes

| Mode | Result |
| --- | --- |
| sidecar (default) | writes `note.converted.md`; the original is untouched |
| in-place | rewrites the original, after backing it up to `note.md.bak` |
| directory | writes into a folder you choose; duplicate names get a numeric suffix, nothing is overwritten |

## Supported syntax

```markdown
![image](images/a.png)
![image](images/a.png "title")
![image](<images/with space.png>)
![](images/has(parens).png)
<img src="images/a.png" width="60">
<img srcset="images/a.png 1x, images/a@2x.png 2x">
<source srcset="images/wide.png 640w">
![full][ref1]  ![collapsed][]  ![shortcut]
[ref1]: images/a.png
```

- Reference-style images, HTML `src` / `srcset` / `<source>`, several images on one
  line, and paths with spaces or parentheses all work.
- Remote images, `data:` URIs, missing local files, and attachments such as
  `.ps` / `.eps` / `.pdf` / `.ai` / `.psd` / `.sketch` / `.fig` / `.drawio` are left
  alone.
- Images inside fenced code blocks, inline code, HTML comments, `<pre>` and `<code>`
  are never touched.

## Encoding

Documents are read and written in their original encoding: UTF-8 (with or without a
BOM), UTF-16 with a BOM, and the machine's ANSI code page. Inside the program
everything is UTF-8, but a document is only accepted when it converts back to the
exact bytes it came from — so nothing outside the rewritten image addresses can drift.

## Settings

PicGo address, concurrency, retry count; whether to reuse previous upload/download
results; output mode, output folder, sidecar suffix, remote-image folder; theme
(light / dark / follow system); language (Chinese / English); what closing the window
does (ask, minimize to tray, quit) and whether to start minimized to the tray.

## Data directory

Next to the executable by default:

```text
MarkPic.exe
data/
  settings.json
  upload-cache.json
  remote-cache.json
  webview2/
```

On Windows, if that folder is not writable MarkPic falls back to `%APPDATA%\markpic`. Set
`MARKPIC_DATA_DIR` to point somewhere else.

## Updating

An installed copy reads the version manifest published with each release and offers
the update in the About panel. Developer builds (`+dev`) skip the check. If you want a
self-hosted mirror, point `MARKPIC_UPDATE_MANIFEST` at your own `version.json`.
