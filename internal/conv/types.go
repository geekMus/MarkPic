// Package conv 是转换流程的核心：解析文档、上传图片、回写链接。
package conv

// 图片状态。
const (
	StatusUploaded = "uploaded" // 本次上传成功
	StatusReused   = "reused"   // 同一张图本轮已上传，复用结果
	StatusCached   = "cached"   // 上一次运行时已经传过，直接复用当时的地址
	StatusRemote   = "remote"   // 本来就是网络图片，跳过
	StatusMissing  = "missing"  // 本地文件不存在
	StatusFailed   = "failed"   // 上传失败
	StatusPending  = "pending"  // 仅预检模式：本地文件存在，尚未上传
)

// 文件状态。
const (
	FileOK       = "ok"        // 全部图片处理成功
	FilePartial  = "partial"   // 部分图片失败，已尽力替换成功的部分
	FileNoImages = "no-images" // 文档里没有本地图片
	FileFailed   = "failed"    // 没有可写出的结果
	FilePreview  = "preview"   // 预检，未写入
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
	ID         string   `json:"id"`
	Path       string   `json:"path"`
	Name       string   `json:"name"`
	Dir        string   `json:"dir"`
	Status     string   `json:"status"`
	Images     int      `json:"images"`
	Uploaded   int      `json:"uploaded"`
	Reused     int      `json:"reused"`
	Failed     int      `json:"failed"`
	Skipped    int      `json:"skipped"`
	OutputPath string   `json:"outputPath,omitempty"`
	Error      string   `json:"error,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
	Items      []Item   `json:"items"`
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
	Cancelled    bool   `json:"cancelled"`
	DurationMs   int64  `json:"durationMs"`
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
