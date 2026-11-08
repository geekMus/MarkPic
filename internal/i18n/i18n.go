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
	KeyConvEmptyTarget        Key = "conv.emptyTarget"
	KeyConvUnresolvedTarget   Key = "conv.unresolvedTarget"
	KeyConvFileMissing        Key = "conv.fileMissing"
	KeyConvCancelled          Key = "conv.cancelled"
	KeyConvAllFailed          Key = "conv.allFailed"
	KeyConvRewriteFailed      Key = "conv.rewriteFailed"
	KeyConvBackupFailed       Key = "conv.backupFailed"
	KeyConvWriteFailed        Key = "conv.writeFailed"
	KeyConvOutDirRequired     Key = "conv.outDirRequired"
	KeyConvOutDirCreateFailed Key = "conv.outDirCreateFailed"
	KeyConvReferenceStyle     Key = "conv.referenceStyle"
	KeyConvLoosePath          Key = "conv.loosePath"

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
	KeyServerMissingFilePath   Key = "server.missingFilePath"
	KeyServerMissingPath       Key = "server.missingPath"
	KeyServerBadJSON           Key = "server.badJSON"
	KeyServerEmptyBody         Key = "server.emptyBody"
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
		"文件不是 UTF-8 编码，暂不支持（请转存为 UTF-8 后重试）",
		"The file is not UTF-8 encoded. Save it as UTF-8 and try again.",
	},
	KeyConvEmptyTarget: {
		"图片地址为空",
		"Empty image address",
	},
	KeyConvUnresolvedTarget: {
		"无法解析地址",
		"Could not resolve the address",
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
		"无法创建输出目录：%v",
		"Could not create the output directory: %v",
	},
	KeyConvReferenceStyle: {
		"第 %d 行使用了引用式图片语法 ![alt][id]，本工具暂不处理",
		"Line %d uses reference-style image syntax (![alt][id]), which this tool does not handle yet",
	},
	KeyConvLoosePath: {
		"第 %d 行：图片路径里有空格，规范写法是 <路径> 或把空格写成 %%20（已按原样处理）",
		"Line %d: the image path contains a space; the standard form is <path> or %%20 (handled as written)",
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
