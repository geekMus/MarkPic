package app

import (
	"encoding/json"

	"github.com/geekMus/markpic/internal/i18n"
)

// handler 是一次调用背后的实现：只管参数与结果，成功与失败都交给 Dispatch 包装。
type handler func(json.RawMessage) (any, error)

// noParams 把「不看参数」的实现包成 handler：原生对话框、更新检查都不需要入参。
func noParams(f func() (any, error)) handler {
	return func(json.RawMessage) (any, error) { return f() }
}

// okCall 把「只成功、不会失败」的读取包成 handler：省掉每处都写一遍 "return x, nil"。
func okCall[T any](f func() T) handler {
	return func(json.RawMessage) (any, error) { return f(), nil }
}

// handlers 是 bridge 的方法表：方法名 → 实现。
//
// 做成表而不是 20 个 case 的 switch，是因为「有哪些调用」这件事本身要能一眼看
// 完 —— 以前它散在两百行的 switch 里，前端加了什么、后端还缺什么，只能靠翻。
// 加一个调用只要在这里加一行（实现本身放在对应主题的文件里）。
func (a *App) handlers() map[string]handler {
	return map[string]handler{
		"i18n.set":             a.setLangCall,
		"health":               okCall(a.health),
		"settings.get":         okCall(a.store.Load),
		"settings.put":         a.putSettings,
		"settings.closeAction": a.setCloseAction,
		"dialog.files":         noParams(a.pickFiles),
		"dialog.folder":        noParams(a.pickFolder),
		"scan.start":           a.startScan,
		"scan.cancel":          a.cancelScan,
		"run.start":            a.startRun,
		"run.cancel":           a.cancelRun,
		"reveal":               a.reveal,
		"open":                 a.open,
		"update.status":        okCall(a.upd.snapshot),
		"update.check":         noParams(a.checkUpdate),
		"update.install":       noParams(a.installUpdate),
	}
}

// Dispatch 是 bridge 的唯一入口：前端 window.markpic(method, params)。
func (a *App) Dispatch(method string, params json.RawMessage) Reply {
	h, ok := a.handlers()[method]
	if !ok {
		return Reply{OK: false, Code: "not_found", Message: "未知的调用：" + method}
	}
	return a.result(h(params))
}

// setLangCall 换界面语言。返回一个占位的 ok：前端只关心有没有报错。
func (a *App) setLangCall(params json.RawMessage) (any, error) {
	var p struct {
		Lang string `json:"lang"`
	}
	_ = json.Unmarshal(params, &p)
	a.setLang(i18n.Parse(p.Lang))
	return map[string]bool{"ok": true}, nil
}
