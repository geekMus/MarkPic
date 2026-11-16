package app

import (
	"context"
	"time"

	"github.com/geekMus/markpic/internal/appdata"
	"github.com/geekMus/markpic/internal/brand"
	"github.com/geekMus/markpic/internal/i18n"
	"github.com/geekMus/markpic/internal/picgo"
	"github.com/geekMus/markpic/internal/settings"
	"github.com/geekMus/markpic/internal/version"
)

// ---------- 健康检查 ----------

type picgoHealth struct {
	Online bool   `json:"online"`
	Detail string `json:"detail"`
	// Needed 说明这次运行到底要不要 PicGo：只检测 / 下载回本地这两种模式不上传本地图片。
	Needed bool `json:"needed"`
}

type healthResponse struct {
	App      string      `json:"app"`
	Version  string      `json:"version"`
	Endpoint string      `json:"endpoint"`
	Picgo    picgoHealth `json:"picgo"`
	// DataDir 是设置与 WebView 缓存所在的位置（默认 exe 旁边的 data）。
	DataDir string `json:"dataDir"`
	// DataNote 只在没能放到 exe 旁边时说明原因。
	DataNote string `json:"dataNote,omitempty"`
	// SettingsNote 在设置文件读不出来时说明「现在跑的是默认值」。
	SettingsNote string `json:"settingsNote,omitempty"`
	// Debug 说明当前是不是调试构建（scripts/start.mjs）：界面据它在标题栏挂标记。
	Debug bool `json:"debug"`
}

// needsPicGo 与转换流程同一套判断：只碰远程图的两种模式不需要图床。
func needsPicGo(cfg settings.Settings) bool {
	if cfg.DryRun {
		return false
	}
	return cfg.Remote != settings.RemoteCheck && cfg.Remote != settings.RemoteDownload
}

func (a *App) health() healthResponse {
	lang := a.Lang()
	cfg := a.store.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 1600*time.Millisecond)
	defer cancel()

	pc := picgo.New(cfg.Endpoint, 0, 2*time.Second, lang)
	online, detail := true, i18n.T(lang, i18n.KeyServerPicgoOK)
	if err := pc.Ping(ctx); err != nil {
		online, detail = false, err.Error()
	}

	return healthResponse{
		App:      brand.Slug,
		Version:  version.String(),
		Endpoint: cfg.Endpoint,
		Picgo:    picgoHealth{Online: online, Detail: detail, Needed: needsPicGo(cfg)},
		DataDir:  appdata.Dir(),
		DataNote: appdata.Note(lang),
		// Load 已经把「设置文件读不出来」这件事咽下去了（回退默认值继续跑），
		// 这里把它说给用户听，免得他改过的地址、并发数凭空变回默认值。
		SettingsNote: a.store.Problem(lang),
		Debug:        version.DebugMode(),
	}
}
