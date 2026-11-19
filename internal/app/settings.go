package app

import (
	"encoding/json"

	"github.com/geekMus/markpic/internal/i18n"
	"github.com/geekMus/markpic/internal/settings"
	"github.com/geekMus/markpic/internal/version"
)

// ---------- 设置 ----------

// putSettings 把前端交上来的设置整份写下去。
//
// 前端持有完整的一份设置（草稿 + 已保存值），保存时交上来的一定是完整的：
// 以前这里按「只覆盖发上来的字段」做增量，是因为要兼容「某次调用顺带改一个字段」
// 的用法（把页面名当设置传上来）。现在操作类型走 start 的 op 参数，设置只有
// 「读一份、改一改、整份存回去」这一条路，增量补丁那套（12 个指针字段 + 逐字段
// 判空）就没有存在的理由了。
func (a *App) putSettings(params json.RawMessage) (any, error) {
	var incoming settings.Settings
	if len(params) > 0 {
		if err := json.Unmarshal(params, &incoming); err != nil {
			return nil, badRequest(i18n.T(a.Lang(), i18n.KeyServerBadJSON))
		}
	}
	cfg, err := a.configOf(&incoming)
	if err != nil {
		return nil, badRequest(err.Error())
	}
	if err := a.store.Save(cfg); err != nil {
		return nil, internal(i18n.T(a.Lang(), i18n.KeyServerSaveFailed, err))
	}
	return a.store.Load(), nil
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

// configOf 把交上来的一份设置规整成可用的运行参数。
//
// 校验只有这一处：前端那份「保存前先拦一下」的检查只是为了让用户马上看到问题，
// 真正说了算的是这里（settings.Normalize）。
func (a *App) configOf(incoming *settings.Settings) (settings.Settings, error) {
	cfg := settings.Default()
	if incoming != nil {
		cfg = *incoming
	}
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
