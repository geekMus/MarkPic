// Package i18n 提供界面文案的双语目录。
//
// 界面语言由前端每次请求带上的 Accept-Language 决定（见 web/src/api.ts），
// 服务端所有会显示给用户看的文字 —— 逐图失败原因、PicGo 提示、更新错误、
// 接口错误的 message —— 都从这里取。
//
// 每条文案中英写在同一个 entry 里，这样漏译在代码里就是一眼能看出来的事，
// 不需要额外的测试去兜。
package i18n

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Lang 是界面语言。
type Lang string

const (
	// ZH 是默认语言。
	ZH Lang = "zh"
	// EN 是英文。
	EN Lang = "en"
)

// Key 是文案的键名。
type Key string

const (
	KeyPicgoConnect      Key = "picgo.connect"
	KeyPicgoRequest      Key = "picgo.request"
	KeyPicgoStatus       Key = "picgo.status"
	KeyPicgoParse        Key = "picgo.parse"
	KeyPicgoNoMessage    Key = "picgo.noMessage"
	KeyPicgoNoURL        Key = "picgo.noURL"
	KeyPicgoBadEndpoint  Key = "picgo.badEndpoint"
	KeyPicgoNoResponse   Key = "picgo.noResponse"
	KeyPicgoEmptySnippet Key = "picgo.emptySnippet"

	KeyConvExpandFailed       Key = "conv.expandFailed"
	KeyConvPathMissing        Key = "conv.pathMissing"
	KeyConvReadFailed         Key = "conv.readFailed"
	KeyConvNotUTF8            Key = "conv.notUTF8"
	KeyConvEncodeFailed       Key = "conv.encodeFailed"
	KeyConvEmptyTarget        Key = "conv.emptyTarget"
	KeyConvUnresolvedTarget   Key = "conv.unresolvedTarget"
	KeyConvFileMissing        Key = "conv.fileMissing"
	KeyConvPathIsDir          Key = "conv.pathIsDir"
	KeyConvCancelled          Key = "conv.cancelled"
	KeyConvAllFailed          Key = "conv.allFailed"
	KeyConvRewriteFailed      Key = "conv.rewriteFailed"
	KeyConvBackupFailed       Key = "conv.backupFailed"
	KeyConvWriteFailed        Key = "conv.writeFailed"
	KeyConvOutDirRequired     Key = "conv.outDirRequired"
	KeyConvOutDirCreateFailed Key = "conv.outDirCreateFailed"
	KeyConvReferenceStyle     Key = "conv.referenceStyle"
	KeyConvLoosePath          Key = "conv.loosePath"
	KeyConvAttachmentSkipped  Key = "conv.attachmentSkipped"
	KeyConvEndpointDead       Key = "conv.endpointDead"
	KeyConvEndpointBusy       Key = "conv.endpointBusy"
	KeyConvWriteReadOnly      Key = "conv.writeReadOnly"
	KeyConvWriteLocked        Key = "conv.writeLocked"
	KeyConvTargetIsDir        Key = "conv.targetIsDir"
	KeyConvReadLocked         Key = "conv.readLocked"
	KeyConvOutDirIsFile       Key = "conv.outDirIsFile"
	KeyConvOutDirIsSourceDir  Key = "conv.outDirIsSourceDir"
	KeyConvRemoteDirIsFile    Key = "conv.remoteDirIsFile"

	KeyConvRemoteDead         Key = "conv.remoteDead"
	KeyConvRemoteNotImage     Key = "conv.remoteNotImage"
	KeyConvRemoteUnreachable  Key = "conv.remoteUnreachable"
	KeyConvRemoteTooBusy      Key = "conv.remoteTooBusy"
	KeyConvRemoteUnsupported  Key = "conv.remoteUnsupported"
	KeyConvRemoteBadURL       Key = "conv.remoteBadURL"
	KeyConvRemoteTooLarge     Key = "conv.remoteTooLarge"
	KeyConvRemoteEmpty        Key = "conv.remoteEmpty"
	KeyConvRemoteDownloadFail Key = "conv.remoteDownloadFailed"
	KeyConvRemoteNamingFailed Key = "conv.remoteNamingFailed"
	KeyConvRemoteRedirectLoop Key = "conv.remoteRedirectLoop"
	KeyConvRemotePartial      Key = "conv.remotePartial"

	KeyUpdateBadURL           Key = "update.badURL"
	KeyUpdateConnectFailed    Key = "update.connectFailed"
	KeyUpdateManifestMissing  Key = "update.manifestMissing"
	KeyUpdateManifestStatus   Key = "update.manifestStatus"
	KeyUpdateManifestInvalid  Key = "update.manifestInvalid"
	KeyUpdateManifestNoVer    Key = "update.manifestNoVersion"
	KeyUpdateNoAssetForArch   Key = "update.noAssetForArch"
	KeyUpdateNoSHA            Key = "update.noSHA"
	KeyUpdateBadDownloadURL   Key = "update.badDownloadURL"
	KeyUpdateDownloadFailed   Key = "update.downloadFailed"
	KeyUpdateDownloadStatus   Key = "update.downloadStatus"
	KeyUpdateDestDirFailed    Key = "update.destDirFailed"
	KeyUpdateDestFileFailed   Key = "update.destFileFailed"
	KeyUpdateWriteFailed      Key = "update.writeFailed"
	KeyUpdateInterrupted      Key = "update.interrupted"
	KeyUpdateCloseFailed      Key = "update.closeFailed"
	KeyUpdateChecksumMismatch Key = "update.checksumMismatch"
	KeyUpdateSaveFailed       Key = "update.saveFailed"
	KeyUpdateReplaceBusy      Key = "update.replaceBusy"
	KeyUpdateReplaceFailed    Key = "update.replaceFailed"
	KeyUpdateRestartFailed    Key = "update.restartFailed"

	KeyAppdataEnvUnusable Key = "appdata.envUnusable"
	KeyAppdataExeUnusable Key = "appdata.exeUnusable"

	KeySettingsUnknownOutput Key = "settings.unknownOutput"
	KeySettingsBadSuffix     Key = "settings.badSuffix"
	KeySettingsOutDirNeeded  Key = "settings.outDirNeeded"
	KeySettingsBrokenJSON    Key = "settings.brokenJSON"
	KeySettingsBrokenValues  Key = "settings.brokenValues"
	KeySettingsBrokenKept    Key = "settings.brokenKept"

	KeySettingsUnknownRemote     Key = "settings.unknownRemote"
	KeySettingsRemoteDirAbsolute Key = "settings.remoteDirAbsolute"

	KeyPlatformNoDialog     Key = "platform.noDialog"
	KeyPlatformNoFolderPath Key = "platform.noFolderPath"
	KeyPlatformCannotOpen   Key = "platform.cannotOpen"
	KeyPlatformPickFiles    Key = "platform.pickFiles"
	KeyPlatformPickFolder   Key = "platform.pickFolder"
	KeyPlatformFilterMd     Key = "platform.filterMarkdown"
	KeyPlatformFilterAll    Key = "platform.filterAll"

	KeyServerNotFound          Key = "server.notFound"
	KeyServerPicgoOK           Key = "server.picgoOK"
	KeyServerMethodNotAllowed  Key = "server.methodNotAllowed"
	KeyServerLocalOnly         Key = "server.localOnly"
	KeyServerCrossOrigin       Key = "server.crossOrigin"
	KeyServerSaveFailed        Key = "server.saveFailed"
	KeyServerNoDialog          Key = "server.noDialog"
	KeyServerNeedPaths         Key = "server.needPaths"
	KeyServerBusy              Key = "server.busy"
	KeyServerMissingFilePath   Key = "server.missingFilePath"
	KeyServerMissingPath       Key = "server.missingPath"
	KeyServerBadJSON           Key = "server.badJSON"
	KeyServerEmptyBody         Key = "server.emptyBody"
	KeyServerBodyTooLarge      Key = "server.bodyTooLarge"
	KeyServerNoEventStream     Key = "server.noEventStream"
	KeyServerUpdateUnsupported Key = "server.updateUnsupported"
	KeyServerCheckFirst        Key = "server.checkFirst"
	KeyServerUpdateBusy        Key = "server.updateBusy"
)

type entry struct{ zh, en string }

// catalog 是全部文案：一条一个 entry，中英并排。
// 模板里的 %s / %d / %v 由 T 的参数填充。
var catalog = map[Key]entry{
	KeyPicgoConnect: {
		"无法连接 PicGo（%s）：请确认 PicGo 正在运行，并在「PicGo 设置 → 设置 Server」中开启服务",
		"Cannot reach PicGo (%s). Make sure PicGo is running and that its server is enabled under “PicGo Settings → Server settings”.",
	},
	KeyPicgoRequest: {
		"请求 PicGo 失败: %v",
		"PicGo request failed: %v",
	},
	KeyPicgoStatus: {
		"PicGo 返回 %d: %s",
		"PicGo returned %d: %s",
	},
	KeyPicgoParse: {
		"无法解析 PicGo 响应: %s",
		"Cannot parse the PicGo response: %s",
	},
	KeyPicgoNoMessage: {
		"PicGo 报告上传失败（未返回原因）",
		"PicGo reported a failed upload without a reason",
	},
	KeyPicgoNoURL: {
		"PicGo 返回成功但没有 URL",
		"PicGo reported success but returned no URL",
	},
	KeyPicgoBadEndpoint: {
		"地址不合法: %s",
		"Invalid address: %s",
	},
	KeyPicgoNoResponse: {
		"端口未响应（%s）",
		"Port not responding (%s)",
	},
	KeyPicgoEmptySnippet: {
		"(空响应)",
		"(empty response)",
	},

	KeyConvExpandFailed: {
		"展开目录失败: %v",
		"Could not walk the directory: %v",
	},
	KeyConvPathMissing: {
		"路径不存在，已跳过: %s",
		"Path does not exist, skipped: %s",
	},
	KeyConvReadFailed: {
		"无法读取文件：%v",
		"Could not read the file: %v",
	},
	KeyConvNotUTF8: {
		"认不出这个文件的编码：不是 UTF-8，不是带 BOM 的 UTF-16，也不是本机 ANSI 编码。请另存为 UTF-8 后重试",
		"Unrecognized encoding: not UTF-8, not UTF-16 with a BOM, and not this machine's ANSI code page. Save it as UTF-8 and try again.",
	},
	KeyConvEncodeFailed: {
		"替换后的内容没法用原编码（%s）写回去，请另存为 UTF-8 后重试",
		"The result cannot be written back in the original encoding (%s); save the document as UTF-8 and try again.",
	},
	KeyConvEmptyTarget: {
		"图片地址为空",
		"Empty image address",
	},
	KeyConvUnresolvedTarget: {
		"无法解析地址",
		"Could not resolve the address",
	},
	KeyConvPathIsDir: {
		"这是一个目录，不是图片文件",
		"That path is a directory, not an image file",
	},
	KeyConvFileMissing: {
		"本地文件不存在：%s",
		"Local file not found: %s",
	},
	KeyConvCancelled: {
		"已取消",
		"Cancelled",
	},
	KeyConvAllFailed: {
		"所有本地图片都处理失败，未生成文件",
		"Every local image failed, so no file was written",
	},
	KeyConvRewriteFailed: {
		"回写失败：%v",
		"Could not rewrite the document: %v",
	},
	KeyConvBackupFailed: {
		"备份失败：%v",
		"Backup failed: %v",
	},
	KeyConvWriteFailed: {
		"写入失败：%v",
		"Could not write the output: %v",
	},
	KeyConvOutDirRequired: {
		"未设置输出目录",
		"No output directory configured",
	},
	KeyConvOutDirCreateFailed: {
		"无法创建输出目录",
		"Could not create the output directory",
	},
	KeyConvOutDirIsFile: {
		"输出目录是一个文件，不是目录：%s",
		"The output directory is a file, not a directory: %s",
	},
	KeyConvRemoteDirIsFile: {
		"本地目录 %s 已经被一个同名文件占了，放不进图片（换个目录名，或先挪走那个文件）",
		"%s is already taken by a file, so images cannot be saved there (rename it or pick another folder)",
	},
	KeyConvOutDirIsSourceDir: {
		"输出目录就是文档所在的目录，因此这份文档会被原地覆盖",
		"The output directory is the document's own folder, so this document is overwritten in place",
	},
	KeyConvWriteReadOnly: {
		"目标文件是只读的，覆盖不了：%s（去掉只读属性后重试，或换个输出位置）",
		"The target file is read-only and cannot be replaced: %s (clear the read-only attribute or write somewhere else)",
	},
	KeyConvWriteLocked: {
		"写不进去：文件正被别的程序占用，或者没有权限：%s（关掉占用它的程序再试）",
		"Could not write: the file is in use by another program, or you lack permission: %s (close whatever is using it and try again)",
	},
	KeyConvTargetIsDir: {
		"输出位置被一个同名目录占着：%s（挪走那个目录，或换个输出位置）",
		"The output path is taken by a folder of the same name: %s (move that folder away or write somewhere else)",
	},
	KeyConvReadLocked: {
		"读不出来：文件正被别的程序占用，或者没有读取权限：%s（关掉占用它的程序再试）",
		"Could not read: the file is in use by another program, or you lack read permission: %s (close whatever is using it and try again)",
	},
	KeyConvReferenceStyle: {
		"第 %d 行：引用式图片的标签在文档里找不到对应定义，已跳过",
		"Line %d: the reference-style image's label has no definition in this document, so it was skipped",
	},
	KeyConvLoosePath: {
		"第 %d 行：图片路径里有空格，规范写法是 <路径> 或把空格写成 %%20（已按原样处理）",
		"Line %d: the image path contains a space; the standard form is <path> or %%20 (handled as written)",
	},
	KeyConvAttachmentSkipped: {
		"第 %d 行：%s 是附件、不是图片（图床当图片渲染不了），已跳过",
		"Line %d: %s is an attachment, not an image (image hosts cannot render it), so it was skipped",
	},
	KeyConvRemoteDead: {
		"远程图片已经打不开了（HTTP %d）",
		"The remote image is no longer reachable (HTTP %d)",
	},
	KeyConvRemoteNotImage: {
		"这个地址返回的不是图片（%s）",
		"That address returned %s, not an image",
	},
	KeyConvRemoteUnreachable: {
		"连不上这个地址：%v",
		"Could not reach that address: %v",
	},
	// KeyConvRemoteTooBusy 专门给「图床让我们慢一点」用。报成「连不上」会把人引到
	// 网络上去查（防火墙、DNS、代理），可对方明明答话了，只是让我们排队。
	KeyConvRemoteTooBusy: {
		"这个地址的图床一直在让我们慢一点（%v），等了 %d 轮还没轮上——它没坏，过一会儿再跑，或把并发调小",
		"That image host kept asking us to slow down (%v) and never let us through in %d rounds — it is not broken, so try again later or lower the concurrency",
	},
	KeyConvRemoteUnsupported: {
		"只支持 http/https 地址，这种没法检测或下载：%s",
		"Only http/https addresses can be checked or downloaded, so this one was left alone: %s",
	},
	KeyConvRemoteBadURL: {
		"%s 这个地址写坏了，百分号转义、括号或空格可能有错",
		"%s is malformed — check its percent-escapes, brackets or spaces",
	},
	KeyConvRemoteTooLarge: {
		"远程图片太大了（超过 %d MB），没有下载",
		"The remote image is larger than %d MB, so it was not downloaded",
	},
	KeyConvRemoteEmpty: {
		"这个地址能打开，但内容是空的（0 字节），不是一张能用的图片",
		"That address responds, but the body is empty (0 bytes), so it is not a usable image",
	},
	KeyConvRemoteDownloadFail: {
		"下载失败：%v",
		"Download failed: %v",
	},
	KeyConvRemoteRedirectLoop: {
		"这个地址在反复跳转，跳了 8 次也没落到一张图上（图床把自己重定向到自己，或者 http 与 https 互相弹）",
		"That address keeps redirecting: eight hops later it still is not an image (the host redirects to itself, or bounces between http and https)",
	},
	KeyConvRemotePartial: {
		"对方只给了一段内容（HTTP 206），不是整张图——多半是图床或中间代理把它截断了",
		"The server returned only a fragment (HTTP 206) instead of the whole image — the image host or a proxy is truncating it",
	},
	KeyConvRemoteNamingFailed: {
		"没能给这张远程图片起个本地文件名：%v",
		"Could not work out a local file name for this remote image: %v",
	},
	KeyConvEndpointDead: {
		"连续 %d 张图片上传失败（期间没有一张成功），已停止本次运行——图床多半连不上：%s",
		"%d images in a row failed to upload without a single success, so the run was stopped early — the host is probably unreachable: %s",
	},
	KeyConvEndpointBusy: {
		"图床一直回「请求太频繁」（连续 %d 张没成），已停止本次运行——它是通的，过一会儿再试，或把并发调小：%s",
		"The host kept asking us to slow down (%d images in a row failed), so the run was stopped early — it is reachable, so try again in a while or lower the concurrency: %s",
	},

	KeyUpdateBadURL: {
		"版本地址不对：%v",
		"Invalid release manifest address: %v",
	},
	KeyUpdateConnectFailed: {
		"连不上版本地址（%s）：%v",
		"Cannot reach the release manifest (%s): %v",
	},
	KeyUpdateManifestMissing: {
		"版本清单不存在，可能还没有发布过版本",
		"No release manifest found — no version has been published yet",
	},
	KeyUpdateManifestStatus: {
		"版本地址返回 %s",
		"The release manifest returned %s",
	},
	KeyUpdateManifestInvalid: {
		"版本清单格式不对：%v",
		"Malformed release manifest: %v",
	},
	KeyUpdateManifestNoVer: {
		"版本清单里没有版本号",
		"The release manifest has no version number",
	},
	KeyUpdateNoAssetForArch: {
		"清单里没有 %s 平台的下载地址",
		"The manifest has no download for the %s platform",
	},
	KeyUpdateNoSHA: {
		"清单没有给出 SHA-256，出于安全考虑不自动安装",
		"The manifest has no SHA-256, so nothing is installed automatically",
	},
	KeyUpdateBadDownloadURL: {
		"下载地址不对：%v",
		"Invalid download address: %v",
	},
	KeyUpdateDownloadFailed: {
		"下载失败：%v",
		"Download failed: %v",
	},
	KeyUpdateDownloadStatus: {
		"下载失败：服务器返回 %s",
		"Download failed: the server returned %s",
	},
	KeyUpdateDestDirFailed: {
		"无法创建下载目录：%v",
		"Could not create the download directory: %v",
	},
	KeyUpdateDestFileFailed: {
		"无法创建下载文件：%v",
		"Could not create the download file: %v",
	},
	KeyUpdateWriteFailed: {
		"写入下载文件失败：%v",
		"Could not write the download file: %v",
	},
	KeyUpdateInterrupted: {
		"下载中断：%v",
		"Download interrupted: %v",
	},
	KeyUpdateCloseFailed: {
		"关闭下载文件失败：%v",
		"Could not close the download file: %v",
	},
	KeyUpdateChecksumMismatch: {
		"下载内容校验失败（期望 %s，实际 %s），已丢弃",
		"Checksum mismatch (expected %s, got %s) — the download was discarded",
	},
	KeyUpdateSaveFailed: {
		"保存下载文件失败：%v",
		"Could not save the downloaded file: %v",
	},
	KeyUpdateReplaceBusy: {
		"无法替换正在运行的程序（%v）：请关掉占用它的程序后重试",
		"Could not replace the running program (%v). Close whatever is holding it and try again.",
	},
	KeyUpdateReplaceFailed: {
		"无法写入新版本（%v）：新版本已下载，可手动替换",
		"Could not write the new version (%v). It has been downloaded and can be swapped in manually.",
	},
	KeyUpdateRestartFailed: {
		"无法启动新版本（%v）：关掉本程序后重新打开即可",
		"Could not start the new version (%v). Close this program and open it again.",
	},

	KeyAppdataEnvUnusable: {
		"环境变量 %s 指定的目录写不进去，改用别处",
		"The directory in %s is not writable, so another location is used",
	},
	KeyAppdataExeUnusable: {
		"程序所在目录写不进去（可能装在 Program Files），数据放到了系统目录",
		"The program folder is not writable (it may live under Program Files), so data goes to the system folder",
	},

	KeySettingsUnknownOutput: {
		"未知的输出方式: %q",
		"Unknown output mode: %q",
	},
	KeySettingsBadSuffix: {
		`文件名后缀不能包含 \ / : * ? " < > |`,
		`The file name suffix cannot contain \ / : * ? " < > |`,
	},
	KeySettingsOutDirNeeded: {
		"输出到目录时必须指定目录",
		"An output directory is required in directory mode",
	},
	KeySettingsUnknownRemote: {
		"未知的远程图片处理方式: %q",
		"Unknown mode for remote images: %q",
	},
	KeySettingsRemoteDirAbsolute: {
		"本地目录要写相对路径（例如 images 或 ../images）",
		"The local folder must be a relative path (for example images or ../images)",
	}, KeySettingsBrokenJSON: {
		"设置文件读不出来（不是合法的 JSON），已改用默认值。",
		"The settings file could not be read (not valid JSON), so defaults are in use.",
	},
	KeySettingsBrokenValues: {
		"设置文件里有不合法的值，已改用默认值。",
		"The settings file contains invalid values, so defaults are in use.",
	},
	KeySettingsBrokenKept: {
		"原文件另存为 %s，改好它可以恢复设置。",
		"The original was kept as %s — fix it to get your settings back.",
	},

	KeyPlatformNoDialog: {
		"当前平台不支持原生对话框，请手动输入路径",
		"Native file dialogs are not available on this platform — type the path instead",
	},
	KeyPlatformNoFolderPath: {
		"无法获取所选目录路径",
		"Could not read the selected folder path",
	},
	KeyPlatformCannotOpen: {
		"系统无法打开：%s",
		"The system could not open: %s",
	},
	KeyPlatformPickFiles: {
		"选择要转换的 Markdown 文件",
		"Choose the Markdown files to convert",
	},
	KeyPlatformPickFolder: {
		"选择包含 Markdown 文件的文件夹",
		"Choose a folder containing Markdown files",
	},
	KeyPlatformFilterMd: {
		"Markdown 文档 (*.md;*.markdown)",
		"Markdown files (*.md;*.markdown)",
	},
	KeyPlatformFilterAll: {
		"所有文件 (*.*)",
		"All files (*.*)",
	},

	KeyServerPicgoOK: {
		"PicGo 服务正常",
		"PicGo is running",
	},
	KeyServerNotFound: {
		"接口不存在",
		"Unknown endpoint",
	},
	KeyServerMethodNotAllowed: {
		"不支持的请求方法",
		"Method not allowed",
	},
	KeyServerLocalOnly: {
		"仅允许本机访问",
		"Local access only",
	},
	KeyServerCrossOrigin: {
		"拒绝跨站请求",
		"Cross-site request rejected",
	},
	KeyServerSaveFailed: {
		"保存设置失败: %v",
		"Could not save the settings: %v",
	},
	KeyServerNoDialog: {
		"当前系统不支持原生对话框，请手动输入路径",
		"Native file dialogs are not available on this system — type the path instead",
	},
	KeyServerNeedPaths: {
		"请先选择文件或文件夹",
		"Choose a file or folder first",
	},
	KeyServerBusy: {
		"已经有一次转换在跑，等它结束或先取消再试",
		"A conversion is already running — wait for it to finish or cancel it first",
	},
	KeyServerMissingFilePath: {
		"缺少文件路径",
		"Missing file path",
	},
	KeyServerMissingPath: {
		"缺少路径",
		"Missing path",
	},
	KeyServerBadJSON: {
		"请求内容不是合法的 JSON",
		"The request body is not valid JSON",
	},
	KeyServerEmptyBody: {
		"请求内容为空",
		"The request body is empty",
	},
	KeyServerBodyTooLarge: {
		"请求内容太大（超过 %d MB），请分成几次提交",
		"The request body is too large (over %d MB); split it into several requests",
	},
	KeyServerNoEventStream: {
		"当前连接不支持事件流",
		"This connection does not support event streams",
	},
	KeyServerUpdateUnsupported: {
		"当前构建不支持自动更新",
		"This build does not support self-update",
	},
	KeyServerCheckFirst: {
		"请先检查更新",
		"Check for updates first",
	},
	KeyServerUpdateBusy: {
		"正在安装，请稍候",
		"An update is being installed, please wait",
	},
}

// T 取一条文案并填充参数。未知语言回退中文，未知键回退键名本身
// （宁可界面上出现一个键名，也不要静默变成空字符串）。
func T(lang Lang, key Key, args ...any) string {
	e, ok := catalog[key]
	if !ok {
		return string(key)
	}
	text := e.zh
	if lang == EN {
		text = e.en
	}
	if len(args) == 0 {
		return text
	}
	return fmt.Sprintf(text, args...)
}

// Parse 解析 Accept-Language，按 q 值挑出我们支持的语言；没有匹配就回中文。
//
// 例："en-US,en;q=0.9,zh-CN;q=0.8" → EN；"zh-CN,zh;q=0.9" → ZH。
func Parse(header string) Lang {
	type candidate struct {
		lang Lang
		q    float64
		pos  int
	}
	var found []candidate

	for i, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		tag := part
		q := 1.0
		if idx := strings.Index(part, ";"); idx >= 0 {
			tag = strings.TrimSpace(part[:idx])
			for _, param := range strings.Split(part[idx+1:], ";") {
				param = strings.TrimSpace(param)
				if value, ok := strings.CutPrefix(param, "q="); ok {
					if parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil {
						q = parsed
					}
				}
			}
		}
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag == "" {
			continue
		}

		var lang Lang
		switch {
		case tag == "zh" || strings.HasPrefix(tag, "zh-"):
			lang = ZH
		case tag == "en" || strings.HasPrefix(tag, "en-"):
			lang = EN
		default:
			continue
		}
		found = append(found, candidate{lang: lang, q: q, pos: i})
	}

	if len(found) == 0 {
		return ZH
	}
	sort.SliceStable(found, func(i, j int) bool {
		if found[i].q != found[j].q {
			return found[i].q > found[j].q
		}
		return found[i].pos < found[j].pos
	})
	for _, c := range found {
		if c.q > 0 {
			return c.lang
		}
	}
	return ZH
}
