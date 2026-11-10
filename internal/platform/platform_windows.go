//go:build windows

// Package platform 封装少量平台相关的系统调用（原生文件对话框、打开资源管理器）。
package platform

import (
	"errors"
	"io/fs"
	"os/exec"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/geekMus/markpic/internal/i18n"
)

// Windows 的两个「文件被别的程序占着」错误码。syscall 包只定义了 Errno 类型，
// 没给这两个常量（golang.org/x/sys/windows 里有，但本包不引第三方库）。
const (
	errSharingViolation = syscall.Errno(32) // ERROR_SHARING_VIOLATION
	errLockViolation    = syscall.Errno(33) // ERROR_LOCK_VIOLATION
)

// IsBusy 判断一个文件错误是不是「被别的程序占用，或者没有权限」。
//
// 这两种情况在 Windows 上都只表现为一句系统原话（「Access is denied」/
// 「being used by another process」），用户分不出区别，调用方按同一种说法
// 解释就行：关掉占用它的程序再试。注意 Go 的 errors.Is(err, fs.ErrPermission)
// 只认 ERROR_ACCESS_DENIED(5)，共享冲突(32)/锁冲突(33) 得自己判。
func IsBusy(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, fs.ErrPermission) {
		return true
	}
	return errors.Is(err, errSharingViolation) || errors.Is(err, errLockViolation)
}

var (
	user32           = syscall.NewLazyDLL("user32.dll")
	comdlg32         = syscall.NewLazyDLL("comdlg32.dll")
	shell32          = syscall.NewLazyDLL("shell32.dll")
	ole32            = syscall.NewLazyDLL("ole32.dll")
	procGetOpenFile  = comdlg32.NewProc("GetOpenFileNameW")
	procBrowseFolder = shell32.NewProc("SHBrowseForFolderW")
	procSHGetPath    = shell32.NewProc("SHGetPathFromIDListW")
	procShellExecute = shell32.NewProc("ShellExecuteW")
	procCoInit       = ole32.NewProc("CoInitializeEx")
	procCoUninit     = ole32.NewProc("CoUninitialize")
	procCoTaskFree   = ole32.NewProc("CoTaskMemFree")
	procMessageBox   = user32.NewProc("MessageBoxW")
)

const (
	ofnAllowMultiSelect = 0x00000200
	ofnPathMustExist    = 0x00000800
	ofnFileMustExist    = 0x00001000
	ofnExplorer         = 0x00080000
	ofnNoChangeDir      = 0x00000008

	bifReturnOnlyFSDirs = 0x0001
	bifEditBox          = 0x0010
	bifNewDialogStyle   = 0x0040

	swShowNormal = 1
)

type openFileNameW struct {
	lStructSize       uint32
	hwndOwner         uintptr
	hInstance         uintptr
	lpstrFilter       *uint16
	lpstrCustomFilter *uint16
	nMaxCustFilter    uint32
	nFilterIndex      uint32
	lpstrFile         *uint16
	nMaxFile          uint32
	lpstrFileTitle    *uint16
	nMaxFileTitle     uint32
	lpstrInitialDir   *uint16
	lpstrTitle        *uint16
	flags             uint32
	nFileOffset       uint16
	nFileExtension    uint16
	lpstrDefExt       *uint16
	lCustData         uintptr
	lpfnHook          uintptr
	lpTemplateName    *uint16
	pvReserved        uintptr
	dwReserved        uint32
	flagsEx           uint32
}

type browseInfoW struct {
	hwndOwner      uintptr
	pidlRoot       uintptr
	pszDisplayName *uint16
	lpszTitle      *uint16
	ulFlags        uint32
	lpfn           uintptr
	lParam         uintptr
	iImage         int32
}

// PickFiles 弹出系统原生「打开文件」对话框，支持多选。
// 用户取消时返回空切片。
func PickFiles(title string, lang i18n.Lang) ([]string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	buf := make([]uint16, 32768)
	filter := utf16Ptr(i18n.T(lang, i18n.KeyPlatformFilterMd) + "\x00*.md;*.markdown\x00" +
		i18n.T(lang, i18n.KeyPlatformFilterAll) + "\x00*.*\x00\x00")

	ofn := openFileNameW{
		lStructSize:  uint32(unsafe.Sizeof(openFileNameW{})),
		lpstrFilter:  filter,
		lpstrFile:    &buf[0],
		nMaxFile:     uint32(len(buf)),
		lpstrTitle:   utf16Ptr(title),
		lpstrDefExt:  utf16Ptr("md"),
		flags:        ofnExplorer | ofnFileMustExist | ofnPathMustExist | ofnAllowMultiSelect | ofnNoChangeDir,
		nFilterIndex: 1,
	}
	ret, _, _ := procGetOpenFile.Call(uintptr(unsafe.Pointer(&ofn)))
	if ret == 0 {
		return nil, nil // 用户取消
	}
	return splitMultiSelect(buf), nil
}

// PickFolder 弹出系统原生「选择文件夹」对话框。用户取消时返回空字符串。
func PickFolder(title string, lang i18n.Lang) (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	coInit, _, _ := procCoInit.Call(0, 2 /* COINIT_APARTMENTTHREADED */)
	defer func() {
		if coInit == 0 { // S_OK 或 S_FALSE 才需要 Uninitialize
			procCoUninit.Call()
		}
	}()

	display := make([]uint16, 260)
	bi := browseInfoW{
		pszDisplayName: &display[0],
		lpszTitle:      utf16Ptr(title),
		ulFlags:        bifReturnOnlyFSDirs | bifEditBox | bifNewDialogStyle,
	}
	pidl, _, _ := procBrowseFolder.Call(uintptr(unsafe.Pointer(&bi)))
	if pidl == 0 {
		return "", nil
	}
	defer procCoTaskFree.Call(pidl)

	path := make([]uint16, 260)
	ok, _, _ := procSHGetPath.Call(pidl, uintptr(unsafe.Pointer(&path[0])))
	if ok == 0 {
		return "", errors.New(i18n.T(lang, i18n.KeyPlatformNoFolderPath))
	}
	return syscall.UTF16ToString(path), nil
}

// Open 用系统默认方式打开文件、目录或 URL。
func Open(target string, lang i18n.Lang) error {
	p, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	verb := utf16Ptr("open")
	ret, _, _ := procShellExecute.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(p)),
		0, 0,
		swShowNormal,
	)
	if ret <= 32 {
		return errors.New(i18n.T(lang, i18n.KeyPlatformCannotOpen, target))
	}
	return nil
}

// Reveal 在资源管理器中选中指定文件或目录。
func Reveal(path string, lang i18n.Lang) error {
	cmd := exec.Command("explorer.exe", "/select,"+path)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// MessageBox 弹出系统消息框（GUI 启动失败时兜底）。
func MessageBox(title, text string) {
	t := utf16Ptr(title)
	m := utf16Ptr(text)
	procMessageBox.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), 0x00000040 /* MB_ICONINFORMATION */)
}

func utf16Ptr(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		p, _ = syscall.UTF16PtrFromString("")
	}
	return p
}

// splitMultiSelect 解析 OFN_ALLOWMULTISELECT 返回的缓冲区。
// 单选时是一个完整路径；多选时是「目录\0文件1\0文件2\0\0」。
func splitMultiSelect(buf []uint16) []string {
	parts := make([]string, 0, 4)
	start := 0
	for i, c := range buf {
		if c != 0 {
			continue
		}
		if i == start {
			break // 连续 NUL，结束
		}
		parts = append(parts, syscall.UTF16ToString(buf[start:i]))
		start = i + 1
	}
	if len(parts) == 0 {
		return nil
	}
	if len(parts) == 1 {
		return parts
	}
	dir := parts[0]
	out := make([]string, 0, len(parts)-1)
	for _, name := range parts[1:] {
		if len(dir) > 0 && (dir[len(dir)-1] == '\\' || dir[len(dir)-1] == '/') {
			out = append(out, dir+name)
		} else {
			out = append(out, dir+`\`+name)
		}
	}
	return out
}
