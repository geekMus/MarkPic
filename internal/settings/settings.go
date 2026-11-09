// Package settings 负责 GUI/CLI 的运行参数及其持久化。
package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/geekMus/convert-md-picture/internal/appdata"
	"github.com/geekMus/convert-md-picture/internal/i18n"
)

// 输出方式。
const (
	OutputSidecar   = "sidecar"   // 原文件旁边生成 xxx.converted.md
	OutputInPlace   = "in-place"  // 覆盖原文件（可留 .bak）
	OutputDirectory = "directory" // 全部输出到指定目录
)

// 拿「文档里已经写着的网络图片地址」怎么办。
const (
	// RemoteIgnore 是默认：远程图片原样留着，一个字节都不碰。
	RemoteIgnore = "ignore"
	// RemoteCheck 只做检测：逐个看这些地址还能不能打开，报告哪些已经失效，
	// 但绝不改写文档。
	RemoteCheck = "check"
	// RemoteDownload 把远程图片下载回本地（默认存到文档旁边的 images 目录），
	// 并把文档里的地址改成相对路径。
	RemoteDownload = "download"
)

// DefaultRemoteDir 是下载回本地时的默认子目录名。
const DefaultRemoteDir = "images"

// CurrentSchemaVersion 是设置文件的结构版本。加载到比它旧的设置时会做一次性迁移，
// 迁移完写回，用户之后再改回旧值就不会再被动。
const CurrentSchemaVersion = 3

// Settings 是全部可配置项。JSON 字段名即 HTTP API 字段名。
type Settings struct {
	Endpoint    string `json:"endpoint"`
	Concurrency int    `json:"concurrency"`
	Retries     int    `json:"retries"`
	Output      string `json:"output"`
	Suffix      string `json:"suffix"`
	OutDir      string `json:"outDir"`
	Backup      bool   `json:"backup"`
	DryRun      bool   `json:"dryRun"`

	// ReuseUploads 打开跨运行的上传结果缓存：同一张图（大小与修改时间都没变）
	// 上次已经传过的话，这一轮直接用上次的地址，不再重传。
	// 新增字段的零值是 false，所以 Default() 里必须显式写 true；
	// 旧设置文件里没有这个字段时，反序列化到 Default() 上会保住 true。
	ReuseUploads bool `json:"reuseUploads"`

	// Remote 决定怎么对待「文档里已经写着的网络图片地址」：
	// ignore（默认，原样留着）/ check（只检测能不能打开）/ download（下载回本地并改链接）。
	Remote string `json:"remote"`
	// RemoteDir 是下载回本地时相对文档所在目录的子目录名，默认 images。
	// 允许写成 ../images 这种（图片放在上一级的公用目录里很常见）。
	RemoteDir string `json:"remoteDir"`

	// SchemaVersion 只在设置文件里出现，界面不显示。
	SchemaVersion int `json:"schemaVersion,omitempty"`
}

// Default 返回出厂设置。
func Default() Settings {
	return Settings{
		Endpoint:      "http://127.0.0.1:36677",
		Concurrency:   DefaultConcurrency,
		Retries:       2,
		Output:        OutputSidecar,
		Suffix:        ".converted",
		Backup:        true,
		ReuseUploads:  true,
		Remote:        RemoteIgnore,
		RemoteDir:     DefaultRemoteDir,
		SchemaVersion: CurrentSchemaVersion,
	}
}

// MaxConcurrency 是并发上传数的上限。再往上实测就没有收益了（图床反而更不稳）。
const MaxConcurrency = 32

// DefaultConcurrency 是默认并发上传数。上传是纯等网络，并发太低最吃亏：
// 真 PicGo + 150 KB 的图、59 张，跑 6 篇文档（每轮配置重复 3 次取最好）：
// 并发 8 → 8.8~10.0 s，并发 12 → 5.9~8.9 s，并发 16 → 5.6~6.2 s，
// 并发 20 → 5.3~6.1 s，并发 32 → 不稳（出现过 30 s 的轮次）。
// 16 是收益拐点：再往上只多压图床、不多拿速度。
const DefaultConcurrency = 16

// 旧版本的默认并发数：1.0 是 4，2.0（含）之前是 8。
const (
	legacyConcurrency  = 4
	legacyConcurrency2 = 8
)

// migrate 把旧版本的设置补成当前版本，返回是否改动过。
func (s *Settings) migrate() bool {
	if s.SchemaVersion >= CurrentSchemaVersion {
		return false
	}
	// 只在用户用的还是「当时那个版本的默认值」时顺手提上去；迁移写回之后再
	// 改回旧值就不会被覆盖。
	if s.Concurrency == legacyConcurrency || s.Concurrency == legacyConcurrency2 {
		s.Concurrency = DefaultConcurrency
	}
	s.SchemaVersion = CurrentSchemaVersion
	return true
}

// Normalize 校验并规整设置，返回用户可读的错误。
func (s *Settings) Normalize() error {
	return s.NormalizeLang(i18n.ZH)
}

// NormalizeLang 与 Normalize 相同，但错误文案按 lang 本地化。
func (s *Settings) NormalizeLang(lang i18n.Lang) error {
	s.Endpoint = strings.TrimSpace(s.Endpoint)
	if s.Endpoint == "" {
		s.Endpoint = Default().Endpoint
	}
	if !strings.Contains(s.Endpoint, "://") {
		s.Endpoint = "http://" + s.Endpoint
	}
	s.Endpoint = strings.TrimRight(s.Endpoint, "/")

	switch s.Output {
	case OutputSidecar, OutputInPlace, OutputDirectory:
	case "":
		s.Output = OutputSidecar
	default:
		return fmt.Errorf(i18n.T(lang, i18n.KeySettingsUnknownOutput), s.Output)
	}

	if s.Concurrency < 1 {
		s.Concurrency = 1
	}
	if s.Concurrency > MaxConcurrency {
		s.Concurrency = MaxConcurrency
	}
	if s.Retries < 0 {
		s.Retries = 0
	}
	if s.Retries > 5 {
		s.Retries = 5
	}

	s.Suffix = strings.TrimSpace(s.Suffix)
	if s.Suffix == "" {
		s.Suffix = Default().Suffix
	}
	if strings.ContainsAny(s.Suffix, `/\:*?"<>|`) {
		return errors.New(i18n.T(lang, i18n.KeySettingsBadSuffix))
	}

	s.OutDir = strings.TrimSpace(s.OutDir)
	if s.Output == OutputDirectory && s.OutDir == "" {
		return errors.New(i18n.T(lang, i18n.KeySettingsOutDirNeeded))
	}

	switch s.Remote {
	case RemoteIgnore, RemoteCheck, RemoteDownload:
	case "":
		s.Remote = RemoteIgnore
	default:
		return fmt.Errorf(i18n.T(lang, i18n.KeySettingsUnknownRemote), s.Remote)
	}
	s.RemoteDir = filepath.ToSlash(strings.TrimSpace(s.RemoteDir))
	// 绝对路径必须在修剪之前拦住。写成绝对路径会很别扭：文档里要写的是
	// 「相对这个 Markdown 文件」的路径，绝对路径既不可移植、又会把整台机器的
	// 目录结构泄进文档里。更要紧的是顺序：`/abs/path` 如果把开头的斜杠修剪掉，
	// 就成了 `abs/path`，用户的绝对路径会被悄悄当成相对路径，图片落到文档旁边的
	// `abs/path/` 里 —— 他多半还以为写对了。
	if strings.HasPrefix(s.RemoteDir, "/") || (len(s.RemoteDir) > 1 && s.RemoteDir[1] == ':') {
		return errors.New(i18n.T(lang, i18n.KeySettingsRemoteDirAbsolute))
	}
	s.RemoteDir = strings.Trim(s.RemoteDir, "/")
	if s.RemoteDir == "" || s.RemoteDir == "." {
		s.RemoteDir = DefaultRemoteDir
	}
	return nil
}

// Store 以 JSON 文件持久化设置。文件不存在时返回默认值。
type Store struct {
	path string
	mu   sync.RWMutex
	// 下面三个字段记录「上一次 Load 为什么没能用上文件里的设置」。
	// 读设置失败只在内部回退到默认值，但用户得能知道这件事——否则他改过的
	// 图床地址、并发数会凭空变回默认值，而界面上一点提示都没有。
	problemKey  i18n.Key
	quarantined string
}

// NewStore 创建存储。path 为空时放在程序数据目录（默认是 exe 旁边的 data 目录，
// 详见 internal/appdata）。旧版本把设置写在 %APPDATA%，这里会顺手搬过来。
func NewStore(path string) *Store {
	if path == "" {
		path = appdata.SettingsPath()
		appdata.AdoptLegacySettings(path)
	}
	return &Store{path: path}
}

// Path 返回设置文件位置。
func (s *Store) Path() string { return s.path }

// Load 读取设置；任何读取/解析失败都回退到默认值（不打断主流程）。
// 读到旧版本的设置会做一次性迁移并写回（写回失败不影响本次使用）。
func (s *Store) Load() Settings {
	s.mu.RLock()
	raw, err := os.ReadFile(s.path)
	s.mu.RUnlock()
	if err != nil {
		s.setProblem("") // 文件不存在是正常的（还没改过设置）
		return Default()
	}
	raw = trimBOM(raw)

	cfg := Default()
	if err := json.Unmarshal(raw, &cfg); err != nil {
		s.quarantine(raw)
		s.setProblem(i18n.KeySettingsBrokenJSON)
		return Default()
	}
	if err := cfg.Normalize(); err != nil {
		s.quarantine(raw)
		s.setProblem(i18n.KeySettingsBrokenValues)
		return Default()
	}
	s.setProblem("")
	// Default() 里已经带上当前版本号，所以光看 cfg.SchemaVersion 分不清
	// 「刚迁移过」和「文件里根本没这个字段」，得回到原始 JSON 里确认。
	if !hasSchemaVersion(raw) {
		cfg.SchemaVersion = 0
	}
	if cfg.migrate() {
		_ = s.Save(cfg)
	}
	return cfg
}

// trimBOM 去掉 UTF-8 BOM。记事本存 UTF-8 会带上 BOM，而 encoding/json
// 见到 BOM 会直接报错——不能因为用户用记事本编辑过设置就把设置全丢成默认值。
func trimBOM(raw []byte) []byte {
	return bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})
}

// Problem 返回上一次 Load 遇到的问题（按 lang 本地化）；没有问题时空串。
// 界面与命令行用它告诉用户「你的设置没被用上，现在跑的是默认值」。
func (s *Store) Problem(lang i18n.Lang) string {
	s.mu.RLock()
	key, path := s.problemKey, s.quarantined
	s.mu.RUnlock()
	if key == "" {
		return ""
	}
	msg := i18n.T(lang, key)
	if path != "" {
		msg += " " + i18n.T(lang, i18n.KeySettingsBrokenKept, path)
	}
	return msg
}

func (s *Store) setProblem(key i18n.Key) {
	s.mu.Lock()
	s.problemKey = key
	s.mu.Unlock()
}

// quarantine 把读不出来的设置文件另存一份（xxx.broken），这样用户还有机会把它
// 手工改回来；返回另存的位置。已经存在同名备份时不覆盖：第一份才是最接近
// 「设置本来长什么样」的那一份。
func (s *Store) quarantine(raw []byte) string {
	backup := s.path + ".broken"
	if _, err := os.Stat(backup); err == nil {
		s.mu.Lock()
		s.quarantined = backup
		s.mu.Unlock()
		return backup
	}
	if err := os.WriteFile(backup, raw, 0o644); err != nil {
		return ""
	}
	s.mu.Lock()
	s.quarantined = backup
	s.mu.Unlock()
	return backup
}

// hasSchemaVersion 判断设置文件里是否写了 schemaVersion。
func hasSchemaVersion(raw []byte) bool {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return true // 不是 JSON 对象，交给上面的回退逻辑，别在这里反复改文件
	}
	_, ok := probe["schemaVersion"]
	return ok
}

// Save 原子写入设置。
func (s *Store) Save(cfg Settings) error {
	if err := cfg.Normalize(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
