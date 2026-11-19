//go:build windows

package main

// 原生拖放：WebView2 不给本地路径。
//
// Chromium 的 DataTransfer 里只有 File 对象，没有 path（那个非标准字段早被删掉），
// 从资源管理器拖文件进来时 text/uri-list 也是空的 —— 所以「把文件拖进窗口」这件事
// 在网页层根本拿不到路径。
//
// 做法是接管窗口的 OLE 放置目标：先对 WebView2 的子窗口 RevokeDragDrop（它自己注册了
// 一个，会把拖放当成导航处理），再注册我们的 IDropTarget。DragEnter/DragOver/Drop
// 都在窗口线程上回调，Drop 时从 CF_HDROP 里直接用 DragQueryFileW 读路径，
// 再交给前端加进队列。这条路不抢焦点、不打开任何窗口。

import (
	"encoding/json"
	"log"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
)

// OLE / shell 的常量与接口号。
const (
	dropCFHDrop         = 15 // CF_HDROP
	dropDVAspectContent = 1  // DVASPECT_CONTENT
	dropTYMEDHGLOBAL    = 1  // TYMED_HGLOBAL
	dropEffectNone      = 0
	dropEffectCopy      = 1
	dropSOK             = 0
	dropENoInterface    = 0x80004002
	dropEPointer        = 0x80004003
	dropEPending        = 0x8000000A
)

var (
	ole32Drop   = syscall.NewLazyDLL("ole32.dll")
	shell32Drop = syscall.NewLazyDLL("shell32.dll")

	procOleInitialize    = ole32Drop.NewProc("OleInitialize")
	procRegisterDragDrop = ole32Drop.NewProc("RegisterDragDrop")
	procRevokeDragDrop   = ole32Drop.NewProc("RevokeDragDrop")
	procReleaseStgMedium = ole32Drop.NewProc("ReleaseStgMedium")
	procDragQueryFileW   = shell32Drop.NewProc("DragQueryFileW")
	procEnumChildWindows = user32.NewProc("EnumChildWindows")
)

// formatEtc / stgMedium 是 GetData 的参数与结果。
type formatEtc struct {
	cfFormat uint16
	target   uintptr
	aspect   uint32
	index    int32
	tymed    uint32
}

type stgMedium struct {
	tymed      uint32
	hGlobal    uintptr
	releaseUnk uintptr
}

// dispatch / drop 是 IDataObject 虚表里的两个方法下标（IUnknown 之后：GetData=3，QueryGetData=5）。
const (
	vtblGetData      = 3
	vtblQueryGetData = 5
)

// IDropTarget 的 IID：{00000122-0000-0000-C000-000000000046}
var iidDropTarget = [16]byte{
	0x22, 0x01, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00,
	0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46,
}

// iidUnknown 是 IUnknown：COM 有时会拿它来问。
var iidUnknown = [16]byte{
	0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00,
	0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46,
}

// fileDropTarget 是一个最小可用的 COM 对象：第一个字段必须是虚表指针，
// 因为 OLE 拿到的是「指向对象的指针」，而对象的前 8 个字节被当作 vtable。
type fileDropTarget struct {
	vtbl uintptr
	refs int32
}

var (
	dropObject fileDropTarget
	dropVtbl   [7]uintptr
	dropReady  atomic.Bool
	// dropHasFiles 记着这次拖的是不是文件：DragEnter 问一次，DragOver 只管用。
	dropHasFiles atomic.Bool

	// dropWebView 是投递路径用的窗口；Drop 回调在窗口线程上，Dispatch 会把它排队。
	dropWebView webview2.WebView
)

func init() {
	dropVtbl[0] = syscall.NewCallback(dropQueryInterface)
	dropVtbl[1] = syscall.NewCallback(dropAddRef)
	dropVtbl[2] = syscall.NewCallback(dropRelease)
	dropVtbl[3] = syscall.NewCallback(dropDragEnter)
	dropVtbl[4] = syscall.NewCallback(dropDragOver)
	dropVtbl[5] = syscall.NewCallback(dropDragLeave)
	dropVtbl[6] = syscall.NewCallback(dropDrop)
	dropObject.vtbl = uintptr(unsafe.Pointer(&dropVtbl[0]))
}

// installFileDrop 把窗口的放置目标换成我们自己的。
//
// WebView2 的子窗口是它出现之后才有的，所以这里排几次重试：第一次通常在
// 页面导航完成时就成立，后两次兜住慢启动。
func installFileDrop(w webview2.WebView, hwnd uintptr) {
	if w == nil || hwnd == 0 {
		return
	}
	dropWebView = w
	// RegisterDragDrop 要求当前线程初始化过 OLE。WebView2 一般已经初始化了，
	// 返回 S_FALSE / RPC_E_CHANGED_MODE 都无所谓。
	procOleInitialize.Call(0)

	for _, delay := range []time.Duration{0, 400 * time.Millisecond, 1200 * time.Millisecond} {
		time.AfterFunc(delay, func() {
			w.Dispatch(func() { grabDropTargets(hwnd) })
		})
	}
}

// grabDropTargets 给窗口自己与它下面所有子窗口注册放置目标。
//
// WebView2 的渲染窗口是顶层的子孙窗口，真正的命中在那一层，所以顶层与子窗口
// 都要注册：拖放进哪个窗口都归我们处理。已经注册过的（重试时）先撤再注册，
// RevokeDragDrop 对没有放置目标的窗口返回错误，忽略即可。
func grabDropTargets(hwnd uintptr) {
	target := uintptr(unsafe.Pointer(&dropObject))
	procRevokeDragDrop.Call(hwnd)
	if r, _, _ := procRegisterDragDrop.Call(hwnd, target); r == dropSOK && !dropReady.Swap(true) {
		log.Printf("原生拖放已接管")
	}
	procEnumChildWindows.Call(hwnd, dropEnumProc, 0)
}

var dropEnumProc = syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
	target := uintptr(unsafe.Pointer(&dropObject))
	// WebView2 自己在子窗口上注册过放置目标（会把拖放当导航），先撤掉。
	procRevokeDragDrop.Call(hwnd)
	procRegisterDragDrop.Call(hwnd, target)
	return 1 // 继续枚举
})

func dropQueryInterface(self, riid, ppv uintptr) uintptr {
	if ppv == 0 {
		return dropEPointer
	}
	guid := (*[16]byte)(unsafe.Pointer(riid))
	if *guid == iidDropTarget || *guid == iidUnknown {
		*(*uintptr)(unsafe.Pointer(ppv)) = self
		dropAddRef(self)
		return dropSOK
	}
	*(*uintptr)(unsafe.Pointer(ppv)) = 0
	return dropENoInterface
}

func dropAddRef(self uintptr) uintptr {
	atomic.AddInt32(&dropObject.refs, 1)
	return 1
}

func dropRelease(self uintptr) uintptr {
	n := atomic.AddInt32(&dropObject.refs, -1)
	if n < 0 {
		return 0
	}
	return uintptr(n)
}

func dropDragEnter(self, dataObj, keyState, point, effect uintptr) uintptr {
	has := dataObjectHasHDrop(dataObj)
	dropHasFiles.Store(has)
	// 只有真的拖来文件才算「可以放」：拖文字进来时不该亮起整个窗口的放置态。
	setDropEffect(effect, has)
	if has {
		emitDropOverlay(true)
	}
	return dropSOK
}

func dropDragOver(self, keyState, point, effect uintptr) uintptr {
	// OLE 每过一会儿就问一次效果；保持 COPY（用户看到的是「复制到此处」）。
	// 拖的不是文件时给 NONE，否则拖一段文字进来也会显示「可以放下」。
	setDropEffect(effect, dropHasFiles.Load())
	return dropSOK
}

func dropDragLeave(self uintptr) uintptr {
	dropHasFiles.Store(false)
	emitDropOverlay(false)
	return dropSOK
}

func dropDrop(self, dataObj, keyState, point, effect uintptr) uintptr {
	dropHasFiles.Store(false)
	emitDropOverlay(false)
	setDropEffect(effect, true)
	paths := pathsFromDataObject(dataObj)
	if len(paths) == 0 {
		return dropSOK
	}
	if w := dropWebView; w != nil {
		data, err := json.Marshal(paths)
		if err != nil {
			return dropSOK
		}
		js := "window.__markpicDrop && window.__markpicDrop(" + string(data) + ")"
		w.Dispatch(func() { w.Eval(js) })
	}
	return dropSOK
}

func setDropEffect(effect uintptr, copy bool) {
	if effect == 0 {
		return
	}
	if copy {
		*(*uint32)(unsafe.Pointer(effect)) = dropEffectCopy
		return
	}
	*(*uint32)(unsafe.Pointer(effect)) = dropEffectNone
}

func emitDropOverlay(on bool) {
	w := dropWebView
	if w == nil {
		return
	}
	value := "false"
	if on {
		value = "true"
	}
	js := "window.__markpicDragOverlay && window.__markpicDragOverlay(" + value + ")"
	w.Dispatch(func() { w.Eval(js) })
}

// dataObjectHasHDrop 问一下这份数据里有没有文件（CF_HDROP）。
func dataObjectHasHDrop(dataObj uintptr) bool {
	if dataObj == 0 {
		return false
	}
	fe := formatEtc{
		cfFormat: dropCFHDrop,
		aspect:   dropDVAspectContent,
		index:    -1,
		tymed:    dropTYMEDHGLOBAL,
	}
	fn := comMethod(dataObj, vtblQueryGetData)
	if fn == 0 {
		return false
	}
	r, _, _ := syscall.Syscall(fn, 2, dataObj, uintptr(unsafe.Pointer(&fe)), 0)
	return r == dropSOK
}

// pathsFromDataObject 取出 CF_HDROP 里的路径列表。
func pathsFromDataObject(dataObj uintptr) []string {
	if dataObj == 0 {
		return nil
	}
	fe := formatEtc{
		cfFormat: dropCFHDrop,
		aspect:   dropDVAspectContent,
		index:    -1,
		tymed:    dropTYMEDHGLOBAL,
	}
	var medium stgMedium
	fn := comMethod(dataObj, vtblGetData)
	if fn == 0 {
		return nil
	}
	r, _, _ := syscall.Syscall6(fn, 3, dataObj, uintptr(unsafe.Pointer(&fe)), uintptr(unsafe.Pointer(&medium)), 0, 0, 0)
	if r != dropSOK {
		return nil
	}
	// 拿到的是 OLE 交给我们的一份引用，读完必须还回去。
	defer procReleaseStgMedium.Call(uintptr(unsafe.Pointer(&medium)))
	if medium.tymed != dropTYMEDHGLOBAL || medium.hGlobal == 0 {
		return nil
	}

	hDrop := medium.hGlobal
	count, _, _ := procDragQueryFileW.Call(hDrop, 0xFFFFFFFF, 0, 0)
	if count == 0 {
		return nil
	}
	paths := make([]string, 0, count)
	buf := make([]uint16, 0x8000) // 长路径上限够用；不够的那条会被截断，用户重拖即可
	for i := uintptr(0); i < count; i++ {
		n, _, _ := procDragQueryFileW.Call(hDrop, i, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if n == 0 || int(n) >= len(buf) {
			continue
		}
		paths = append(paths, syscall.UTF16ToString(buf[:n]))
	}
	return paths
}

// comMethod 从接口指针里取出第 index 个方法的地址。
func comMethod(obj uintptr, index int) uintptr {
	vtbl := *(*uintptr)(unsafe.Pointer(obj))
	if vtbl == 0 {
		return 0
	}
	return *(*uintptr)(unsafe.Pointer(vtbl + uintptr(index)*unsafe.Sizeof(uintptr(0))))
}
