package app

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/geekMus/markpic/internal/i18n"
	"github.com/geekMus/markpic/internal/platform"
)

// ---------- 原生对话框 ----------

func (a *App) pickFiles() (any, error) {
	lang := a.Lang()
	paths, err := platform.PickFilesIn(a.ownerWindowValue(), i18n.T(lang, i18n.KeyPlatformPickFiles), lang)
	if errors.Is(err, platform.ErrUnsupported) {
		return nil, unsupported(i18n.T(lang, i18n.KeyServerNoDialog))
	}
	if err != nil {
		return nil, internal(err.Error())
	}
	if paths == nil {
		// 用户取消时 PickFiles 返回 nil；给前端统一成空数组，省一层判空。
		paths = []string{}
	}
	return map[string][]string{"paths": paths}, nil
}

func (a *App) pickFolder() (any, error) {
	lang := a.Lang()
	dir, err := platform.PickFolderIn(a.ownerWindowValue(), i18n.T(lang, i18n.KeyPlatformPickFolder), lang)
	if errors.Is(err, platform.ErrUnsupported) {
		return nil, unsupported(i18n.T(lang, i18n.KeyServerNoDialog))
	}
	if err != nil {
		return nil, internal(err.Error())
	}
	out := []string{}
	if dir != "" {
		out = append(out, dir)
	}
	return map[string][]string{"paths": out}, nil
}

// ---------- 打开与定位 ----------

type pathRequest struct {
	Path   string `json:"path"`
	Target string `json:"target"`
}

func (a *App) reveal(params json.RawMessage) (any, error) {
	var req pathRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, badRequest(i18n.T(a.Lang(), i18n.KeyServerBadJSON))
	}
	if strings.TrimSpace(req.Path) == "" {
		return nil, badRequest(i18n.T(a.Lang(), i18n.KeyServerMissingFilePath))
	}
	if err := platform.Reveal(req.Path, a.Lang()); err != nil {
		return nil, internal(err.Error())
	}
	return map[string]bool{"ok": true}, nil
}

func (a *App) open(params json.RawMessage) (any, error) {
	var req pathRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, badRequest(i18n.T(a.Lang(), i18n.KeyServerBadJSON))
	}
	target := strings.TrimSpace(req.Target)
	if target == "" {
		target = strings.TrimSpace(req.Path)
	}
	if target == "" {
		return nil, badRequest(i18n.T(a.Lang(), i18n.KeyServerMissingPath))
	}
	if err := platform.Open(target, a.Lang()); err != nil {
		return nil, internal(err.Error())
	}
	return map[string]bool{"ok": true}, nil
}
