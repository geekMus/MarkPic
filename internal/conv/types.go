// Package conv 是转换流程的核心：解析文档、上传图片、回写链接。
package conv

// 图片状态。
const (
	StatusUploaded  = "uploaded"   // 本次上传成功
	StatusReused    = "reused"     // 同一张图本轮已上传，复用结果
	StatusCached    = "cached"     // 上一次运行时已经传过，直接复用当时的地址
	StatusRemote    = "remote"     // 本来就是网络图片，原地不动
	StatusRemoteOK  = "remote-ok"  // 检测：这个远程地址还能打开
	StatusRemoteBad = "remote-bad" // 检测：这个远程地址已经失效
	StatusLocalized = "localized"  // 已经下载回本地，链接改成了本地相对路径
	StatusLocalKept = "local-kept" // 「下载回本地」模式下，本来就是本地图片，不动
	StatusMissing   = "missing"    // 本地文件不存在
	StatusFailed    = "failed"     // 上传（或下载）失败
	StatusCancelled = "cancelled"  // 用户中途取消，这张图没传（不算失败）
	StatusPending   = "pending"    // 仅预检模式：本地文件存在，尚未上传
)

// 文件状态。
const (
	FileOK        = "ok"        // 全部图片处理成功
	FilePartial   = "partial"   // 部分图片失败，已尽力替换成功的部分
	FileNoImages  = "no-images" // 文档里没有本地图片
	FileFailed    = "failed"    // 没有可写出的结果
	FileCancelled = "cancelled" // 用户中途取消，这个文件没有写出去
	FilePreview   = "preview"   // 预检，未写入
	FileChecked   = "checked"   // 只做了远程地址检测（不改写文档）
)

// Item 是一张图片的处理结果。
type Item struct {
	Target       string `json:"target"`
	AbsolutePath string `json:"absolutePath"`
	Kind         string `json:"kind"`
	Line         int    `json:"line"`
	Status       string `json:"status"`
	URL          string `json:"url,omitempty"`
	Error        string `json:"error,omitempty"`
}

// FileResult 是单个 Markdown 文件的处理结果。
type FileResult struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Name     string `json:"name"`
	Dir      string `json:"dir"`
	Status   string `json:"status"`
	Images   int    `json:"images"`
	Uploaded int    `json:"uploaded"`
	Reused   int    `json:"reused"`
	// Localized 是「下载回本地」的张数，Dead 是检测出来失效的远程图张数，
	// Checked 是检测过的远程图张数（含失效的）。都按引用处算，和 Items 对得上。
	Localized int `json:"localized"`
	// LocalizedUnique 是同一篇文档里「去重之后下载了几张」。
	//
	// 一篇文档里同一张图被引用 20 次是常事，Localized 会数成 20，可 images/ 里
	// 只有一个文件 —— 只报 20 会让人以为另外 19 张失败了。两个数一起给，
	// 界面上就能写成「下载 1 张（20 处引用）」。
	LocalizedUnique int `json:"localizedUnique"`
	Dead            int `json:"dead"`
	// DeadUnique 是失效地址里去重后的个数：同一个死链被引用 3 次时 Dead 是 3，
	// 可真正打不开的只有 1 个地址。报告里两个数都得说清楚，否则「失效 3 张」
	// 和结尾那份「失效的远程地址（1 个）」会当场打架。
	DeadUnique int `json:"deadUnique"`
	// CheckedUnique 是去重之后真正探活了几次：同一张图被引用 10 次时 Checked 是 10，
	// 实际只发了一个请求。和 LocalizedUnique 一个道理，只报 10 会显得我们敲了十次门。
	CheckedUnique int `json:"checkedUnique"`
	Checked       int `json:"checked"`
	// RemoteUnsupported 是「是远程地址，但我们碰不了」的引用数（ftp://、data:）。
	// 它们既不算检测过、也不算失效，得单独记一笔，否则一篇里 4 处远程引用
	// 只报「远程图 1 张」，另外 3 处等于从报告里消失了。
	RemoteUnsupported int `json:"remoteUnsupported"`
	// RemoteFailed 是「远程地址这次没拿到图」的引用数（download 模式下下载失败、
	// 超时、连不上）。它同时算在 Failed 里——文件确实是「部分完成」——但界面要按
	// 地址去重地列一份「没下载回来」的清单，光看 Failed 会混进本地图片的失败。
	RemoteFailed int `json:"remoteFailed"`
	// RemoteFailedUnique 是这些失败地址去重后的个数：同一个地址被引用 3 次、
	// 三次都失败时 RemoteFailed 是 3，可真正没拿回来的只有 1 个地址。
	RemoteFailedUnique int `json:"remoteFailedUnique"`
	// LocalMissing 是「只做检测/下载」那一档里引用了、但磁盘上找不到的本地图片数。
	// 这两个模式不碰本地图（用户在意的正是反方向），可缺图照样是文档坏了：
	// 以前这类引用直接从报告里消失，一篇缺图的文档读起来跟完全正常一样。
	LocalMissing int      `json:"localMissing"`
	Failed       int      `json:"failed"`
	Skipped      int      `json:"skipped"`
	OutputPath   string   `json:"outputPath,omitempty"`
	Error        string   `json:"error,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
	Items        []Item   `json:"items"`
}

// FileTask 是排队中的文件（run.started 时下发）。
type FileTask struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Name   string `json:"name"`
	Dir    string `json:"dir"`
	Images int    `json:"images"`
}

// Summary 是一次运行的汇总。
type Summary struct {
	RunID        string `json:"runId"`
	Files        int    `json:"files"`
	OK           int    `json:"ok"`
	Partial      int    `json:"partial"`
	Failed       int    `json:"failed"`
	NoImages     int    `json:"noImages"`
	Images       int    `json:"images"`
	Uploaded     int    `json:"uploaded"`
	Reused       int    `json:"reused"`
	FailedImages int    `json:"failedImages"`
	// 远程图片相关的三个数：本地化（下载回本地）张数、检测过的张数、其中失效的张数。
	Localized int `json:"localized"`
	// LocalizedUnique 是去重之后真正下载了几张（按文件算，不按引用处算）。
	LocalizedUnique int `json:"localizedUnique"`
	RemoteChecked   int `json:"remoteChecked"`
	// RemoteCheckedUnique 是去重后真正探活过的地址数（同一张图引用 10 次只算 1）。
	RemoteCheckedUnique int `json:"remoteCheckedUnique"`
	RemoteDead          int `json:"remoteDead"`
	// RemoteDeadUnique 是失效地址去重后的个数（同一个死链引用 3 次只算 1 个）。
	RemoteDeadUnique int `json:"remoteDeadUnique"`
	// RemoteUnsupported 是碰不了的远程地址数（ftp://、data: 之类）。
	RemoteUnsupported int `json:"remoteUnsupported"`
	// RemoteFailed 是下载模式下没拿回来的引用数（下载失败、超时、连不上）。
	RemoteFailed int `json:"remoteFailed"`
	// RemoteFailedUnique 是没拿回来的地址去重后的个数（按地址算，不按引用处算）。
	RemoteFailedUnique int `json:"remoteFailedUnique"`
	// LocalMissing 是「只检测/下载」那一档里引用了、但磁盘上找不到的本地图片数。
	LocalMissing int `json:"localMissing"`
	// FilesChecked 是「只做检测、没改写文档」的文件数。
	FilesChecked int  `json:"filesChecked"`
	Cancelled    bool `json:"cancelled"`
	// RemoteReused 是「这次少下了几张远程图」：上次已经下过、本地文件还在且大小一致。
	// 和 Reused 一样按去重后的图片算，不是按引用处算。
	RemoteReused int `json:"remoteReused"`
	// AbortReason 非空表示这一轮是「被提前掐掉的」而不是跑完的：连续太多张图
	// 上传失败、期间一张都没成功，基本就是图床连不上，与其让用户一张张等超时，
	// 不如停在这里并说清原因。
	AbortReason string `json:"abortReason,omitempty"`
	DurationMs  int64  `json:"durationMs"`
}

// Event 是推送给前端的事件（SSE）。
type Event struct {
	Type     string      `json:"type"`
	RunID    string      `json:"runId,omitempty"`
	ID       string      `json:"id,omitempty"`
	Files    []FileTask  `json:"files,omitempty"`
	Uploaded int         `json:"uploaded"`
	Total    int         `json:"total"`
	Result   *FileResult `json:"result,omitempty"`
	Summary  *Summary    `json:"summary,omitempty"`
	Level    string      `json:"level,omitempty"`
	Message  string      `json:"message,omitempty"`
}

// 事件类型。
const (
	EventRunStarted   = "run.started"
	EventFileStarted  = "file.started"
	EventFileProgress = "file.progress"
	EventFileFinished = "file.finished"
	EventRunFinished  = "run.finished"
	EventRunCancelled = "run.cancelled"
	EventLog          = "log"
)
