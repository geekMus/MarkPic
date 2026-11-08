// Package appdata 决定程序把数据放在哪里。
//
// 默认放在 exe 旁边的 data 目录：green 版、可以整个文件夹拷走、不用了删目录就干净，
// 而且升级时只换 exe，data 目录原样留着。只有当那里写不进去（典型情况是装到了
// Program Files，或者放在只读介质上）时才退回系统目录，并把原因记下来给界面显示。
//
// 目录可以用环境变量 CMP_DATA_DIR 覆盖，便于测试、多开或把数据放到别处。
package appdata

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// EnvDir 可用来强制指定数据目录。
const EnvDir = "CMP_DATA_DIR"

// appName 是退回系统目录时用的目录名，也是旧版本用过的名字。
const appName = "convert-md-picture"

// dirName 是 exe 旁边那个数据目录的名字。
const dirName = "data"

// Source 说明数据目录是怎么定下来的。
type Source string

const (
	// SourcePortable 表示数据放在 exe 旁边的 data 目录。
	SourcePortable Source = "portable"
	// SourceEnv 表示数据目录来自 CMP_DATA_DIR。
	SourceEnv Source = "env"
	// SourceSystem 表示退回了系统目录。
	SourceSystem Source = "system"
)

var (
	once   sync.Once
	dir    string
	source Source
	note   string
)

// Dir 返回数据目录（调用后会确保它存在）。
func Dir() string {
	once.Do(resolve)
	return dir
}

// Where 返回数据目录与它的来源说明，供界面显示。
func Where() (string, Source) {
	once.Do(resolve)
	return dir, source
}

// Note 在没能放到 exe 旁边时说明原因；一切正常时返回空串。
func Note() string {
	once.Do(resolve)
	return note
}

// SettingsPath 返回设置文件的位置。
func SettingsPath() string {
	return filepath.Join(Dir(), "settings.json")
}

// WebViewDir 返回 WebView2 的用户数据目录。这里是缓存，删掉会自动重建；
// 建不出来时返回空串，交给运行时自己决定放哪。
func WebViewDir() string {
	path := filepath.Join(Dir(), "webview2")
	if err := os.MkdirAll(path, 0o755); err != nil {
		return ""
	}
	return path
}

func resolve() {
	if v := strings.TrimSpace(os.Getenv(EnvDir)); v != "" {
		if usable(v) {
			dir, source = v, SourceEnv
			return
		}
		note = fmt.Sprintf("环境变量 %s 指定的目录写不进去，改用别处", EnvDir)
	}

	if exe, err := os.Executable(); err == nil {
		if base := filepath.Join(filepath.Dir(exe), dirName); usable(base) {
			dir, source = base, SourcePortable
			return
		}
		if note == "" {
			note = "程序所在目录写不进去（可能装在 Program Files），数据放到了系统目录"
		}
	}

	if cfg, err := os.UserConfigDir(); err == nil && cfg != "" {
		dir = filepath.Join(cfg, appName)
	} else {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		dir = "."
	}
	source = SourceSystem
}

// usable 判断目录能不能真的写：先建出来，再实打实地建一个临时文件删掉。
// 只看 MkdirAll 不够——目录可能已存在但是只读的。
func usable(dir string) bool {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false
	}
	f, err := os.CreateTemp(dir, ".writetest-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// LegacySettingsPath 返回旧版本写设置的地方（%APPDATA%\convert-md-picture\settings.json）。
func LegacySettingsPath() string {
	cfg, err := os.UserConfigDir()
	if err != nil || cfg == "" {
		return ""
	}
	return filepath.Join(cfg, appName, "settings.json")
}

// AdoptLegacySettings 在新位置还没有设置、旧位置有设置时把它搬过来，这样老用户升级
// 之后配置不会「丢」。返回搬过来的旧路径；没搬则返回空串。
func AdoptLegacySettings(dest string) string {
	if dest == "" {
		return ""
	}
	if _, err := os.Stat(dest); err == nil {
		return "" // 新位置已经有设置，不动它
	}
	old := LegacySettingsPath()
	if old == "" || strings.EqualFold(old, dest) {
		return ""
	}
	raw, err := os.ReadFile(old)
	if err != nil {
		return ""
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return ""
	}
	if err := os.WriteFile(dest, raw, 0o644); err != nil {
		return ""
	}
	return old
}
