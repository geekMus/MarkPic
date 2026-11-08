// Package settings 负责 GUI/CLI 的运行参数及其持久化。
package settings

import (
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
}

// Default 返回出厂设置。
func Default() Settings {
	return Settings{
		Endpoint:    "http://127.0.0.1:36677",
		Concurrency: 4,
		Retries:     2,
		Output:      OutputSidecar,
		Suffix:      ".converted",
		Backup:      true,
	}
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
func (s *Store) Load() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cfg := Default()
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return cfg
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Default()
	}
	if err := cfg.Normalize(); err != nil {
		return Default()
	}
	return cfg
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
