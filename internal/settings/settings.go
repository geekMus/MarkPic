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

// CurrentSchemaVersion 是设置文件的结构版本。加载到比它旧的设置时会做一次性迁移，
// 迁移完写回，用户之后再改回旧值就不会再被动。
const CurrentSchemaVersion = 2

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
		SchemaVersion: CurrentSchemaVersion,
	}
}

// DefaultConcurrency 是默认并发上传数。用真实 PicGo 实测（每张图约 1 KB）：
// 并发 1 → 618 ms/张，并发 4 → 218 ms/张，并发 8 → 198 ms/张，
// 并发 16 且图多时跑到 65 ms/张。上传是纯等网络，并发太小吃亏最大。
const DefaultConcurrency = 8

// legacyConcurrency 是 1.x 版本的默认并发数。
const legacyConcurrency = 4

// migrate 把旧版本的设置补成当前版本，返回是否改动过。
func (s *Settings) migrate() bool {
	if s.SchemaVersion >= CurrentSchemaVersion {
		return false
	}
	// 旧版本的默认并发是 4，实测只有 8 的一半吞吐；只在用户用的还是
	// 旧默认值时顺手提上去，迁移写回之后再改回 4 就不会被覆盖。
	if s.Concurrency == legacyConcurrency {
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
	if s.Concurrency > 16 {
		s.Concurrency = 16
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
	return nil
}

// Store 以 JSON 文件持久化设置。文件不存在时返回默认值。
type Store struct {
	path string
	mu   sync.RWMutex
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
		return Default()
	}
	raw = trimBOM(raw)

	cfg := Default()
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Default()
	}
	if err := cfg.Normalize(); err != nil {
		return Default()
	}
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
