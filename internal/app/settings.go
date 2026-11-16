package app

import (
	"encoding/json"

	"github.com/geekMus/markpic/internal/i18n"
	"github.com/geekMus/markpic/internal/settings"
	"github.com/geekMus/markpic/internal/version"
)

// ---------- 设置 ----------

// settingsPatch 是「只覆盖用户改动过的字段」的参数。
// 不能用整个 Settings：前端漏发一个字段就会把用户已保存的设置悄悄重置成零值。
type settingsPatch struct {
	Endpoint    *string `json:"endpoint"`
	Concurrency *int    `json:"concurrency"`
	Retries     *int    `json:"retries"`
	Output      *string `json:"output"`
	Suffix      *string `json:"suffix"`
	OutDir      *string `json:"outDir"`
	Backup      *bool   `json:"backup"`
	Reuse       *bool   `json:"reuseUploads"`
	Remote      *string `json:"remote"`
	RemoteDir   *string `json:"remoteDir"`
	CloseAction *string `json:"closeAction"`
	StartInTray *bool   `json:"startInTray"`
}

func (p *settingsPatch) apply(cfg *settings.Settings) {
	if p == nil {
		return
	}
	if p.Endpoint != nil {
		cfg.Endpoint = *p.Endpoint
	}
	if p.Concurrency != nil {
		cfg.Concurrency = *p.Concurrency
	}
	if p.Retries != nil {
		cfg.Retries = *p.Retries
	}
	if p.Output != nil {
		cfg.Output = *p.Output
	}
	if p.Suffix != nil {
		cfg.Suffix = *p.Suffix
	}
	if p.OutDir != nil {
		cfg.OutDir = *p.OutDir
	}
	if p.Backup != nil {
		cfg.Backup = *p.Backup
	}
	if p.Reuse != nil {
		cfg.ReuseUploads = *p.Reuse
	}
	if p.Remote != nil {
		cfg.Remote = *p.Remote
	}
	if p.RemoteDir != nil {
		cfg.RemoteDir = *p.RemoteDir
	}
	if p.CloseAction != nil {
		cfg.CloseAction = *p.CloseAction
	}
	if p.StartInTray != nil {
		cfg.StartInTray = *p.StartInTray
	}
}

// setCloseAction 只改关闭行为。关窗时勾了「不再询问」的话，前端把这次选的动作存下来，
// 走这里而不是整份 settings.put，免得把设置页里没保存的草稿一起写进去。
func (a *App) setCloseAction(params json.RawMessage) (any, error) {
	var req struct {
		Action string `json:"action"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, badRequest(i18n.T(a.Lang(), i18n.KeyServerBadJSON))
		}
	}
	cfg := a.store.Load()
	cfg.CloseAction = req.Action
	if err := cfg.NormalizeLang(a.Lang()); err != nil {
		return nil, badRequest(err.Error())
	}
	if err := a.store.Save(cfg); err != nil {
		return nil, internal(i18n.T(a.Lang(), i18n.KeyServerSaveFailed, err))
	}
	return map[string]string{"closeAction": cfg.CloseAction}, nil
}

func (a *App) putSettings(params json.RawMessage) (any, error) {
	var patch settingsPatch
	if len(params) > 0 {
		if err := json.Unmarshal(params, &patch); err != nil {
			return nil, badRequest(i18n.T(a.Lang(), i18n.KeyServerBadJSON))
		}
	}
	cfg, err := a.configOf(&patch)
	if err != nil {
		return nil, badRequest(err.Error())
	}
	if err := a.store.Save(cfg); err != nil {
		return nil, internal(i18n.T(a.Lang(), i18n.KeyServerSaveFailed, err))
	}
	return a.store.Load(), nil
}

func (a *App) configOf(patch *settingsPatch) (settings.Settings, error) {
	cfg := a.store.Load()
	patch.apply(&cfg)
	// 调试构建整轮只扫描、不写文件；发布构建里这一位恒为 false。
	// 它不是用户设置：界面没有开关，也不会从 settings.put 传上来。
	cfg.DryRun = version.DebugMode()
	if cfg.Endpoint == "" {
		cfg.Endpoint = settings.Default().Endpoint
	}
	if err := cfg.Normalize(); err != nil {
		return cfg, err
	}
	return cfg, nil
}
