package app

import (
	"context"
	"encoding/json"

	"github.com/geekMus/markpic/internal/conv"
	"github.com/geekMus/markpic/internal/i18n"
)

// ---------- 预览 ----------

type previewResponse struct {
	Files   []conv.FileResult `json:"files"`
	Summary conv.Summary      `json:"summary"`
}

func (a *App) preview(params json.RawMessage) (any, error) {
	var req runRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, badRequest(i18n.T(a.Lang(), i18n.KeyServerBadJSON))
	}
	if len(req.Paths) == 0 {
		return nil, badRequest(i18n.T(a.Lang(), i18n.KeyServerNeedPaths))
	}
	cfg, err := a.configOf(req.Settings)
	if err != nil {
		return nil, badRequest(err.Error())
	}
	cfg.DryRun = true

	runner := conv.NewRunner(cfg, a.Lang(), "preview", nil).UsePreview()
	files, sum := runner.RunCollect(context.Background(), req.Paths)
	if files == nil {
		files = []conv.FileResult{}
	}
	return previewResponse{Files: files, Summary: sum}, nil
}
