// Command winres 给已经编译好的 exe 补上 Windows 版本信息与应用程序清单（manifest）。
//
// 为什么要在编译之后补：Go 只在主包目录里链接 .syso 资源，仓库里那个
// rsrc_windows_amd64.syso 是给图标用的（rsrc 只做图标和清单，不做版本信息）；
// 再放第二个 .syso 会和它抢同一段 .rsrc，链接器不接受。所以版本信息和清单
// 统一走 Windows 自己的资源 API（BeginUpdateResource/UpdateResource），
// 在 scripts/build.mjs 里编译完之后原地打进去，不引入任何第三方依赖、不需要联网。
//
// 这两样东西不是装饰：没有版本信息、没有 asInvoker 清单的未知 exe，
// 杀毒软件和 SmartScreen 都更容易把它当成可疑样本。
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var (
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	procBeginUpdateRes    = kernel32.NewProc("BeginUpdateResourceW")
	procUpdateResource    = kernel32.NewProc("UpdateResourceW")
	procEndUpdateResource = kernel32.NewProc("EndUpdateResourceW")
)

const (
	rtVersion  = 16
	rtManifest = 24
	langID     = 0x0409 // en-US
)

func main() {
	exe := flag.String("exe", "bin/MarkPic.exe", "要打补丁的 exe 路径")
	version := flag.String("version", "0.0.0", "版本号（日期版），例：2026.11.14 或 2026.11.14.1；会补成四段")
	product := flag.String("product", "MarkPic", "产品名")
	company := flag.String("company", "geekMus", "公司/作者名")
	desc := flag.String("desc", "MarkPic - Markdown image uploader and downloader", "文件说明")
	copyright := flag.String("copyright", "", "版权声明，默认根据年份生成")
	skipManifest := flag.Bool("no-manifest", false, "只写版本信息，不写清单")
	flag.Parse()

	ver := normalizeVersion(*version)
	if *copyright == "" {
		*copyright = "Copyright (C) 2026 " + *company
	}

	h, err := beginUpdate(*exe)
	if err != nil {
		fail(err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _, _ = procEndUpdateResource.Call(h, 1) // 丢弃，别留下半个文件
		}
	}()

	vi := versionInfo(ver, *product, *company, *desc, *copyright)
	if err := update(h, rtVersion, 1, vi); err != nil {
		fail(err)
	}
	if !*skipManifest {
		if err := update(h, rtManifest, 1, []byte(manifestXML(ver))); err != nil {
			fail(err)
		}
	}
	if err := endUpdate(h, false); err != nil {
		fail(err)
	}
	committed = true

	fmt.Printf("已写入版本信息与清单：%s（%s）\n", *exe, ver)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "winres:", err)
	os.Exit(1)
}

func beginUpdate(path string) (uintptr, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	h, _, e := procBeginUpdateRes.Call(uintptr(unsafe.Pointer(p)), 0)
	if h == 0 {
		return 0, fmt.Errorf("BeginUpdateResource(%s) 失败：%v", path, e)
	}
	return h, nil
}

func update(h uintptr, typ, id int, data []byte) error {
	var p *byte
	if len(data) > 0 {
		p = &data[0]
	}
	ok, _, e := procUpdateResource.Call(
		h,
		uintptr(typ),
		uintptr(id),
		langID,
		uintptr(unsafe.Pointer(p)),
		uintptr(len(data)),
	)
	if ok == 0 {
		return fmt.Errorf("UpdateResource(type=%d,id=%d) 失败：%v", typ, id, e)
	}
	return nil
}

func endUpdate(h uintptr, discard bool) error {
	d := uintptr(0)
	if discard {
		d = 1
	}
	ok, _, e := procEndUpdateResource.Call(h, d)
	if ok == 0 {
		return fmt.Errorf("EndUpdateResource 失败：%v", e)
	}
	return nil
}

// normalizeVersion 把 1.0.0 这种补成四段，并拆成 fixedFileInfo 要的两个 DWORD。
func normalizeVersion(v string) string {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.Split(v, ".")
	for len(parts) < 4 {
		parts = append(parts, "0")
	}
	num := func(s string) uint16 {
		var n uint32
		for _, r := range s {
			if r < '0' || r > '9' {
				break
			}
			n = n*10 + uint32(r-'0')
			if n > 0xFFFF {
				n = 0xFFFF
			}
		}
		return uint16(n)
	}
	vals := make([]uint16, 4)
	for i := 0; i < 4; i++ {
		vals[i] = num(parts[i])
	}
	return fmt.Sprintf("%d.%d.%d.%d", vals[0], vals[1], vals[2], vals[3])
}

func versionParts(ver string) (uint32, uint32) {
	var a, b, c, d uint32
	_, _ = fmt.Sscanf(ver, "%d.%d.%d.%d", &a, &b, &c, &d)
	return a<<16 | b, c<<16 | d
}

// --- VS_VERSIONINFO ---
//
// 结构是一棵「块」树，每个块都是：
//   WORD wLength / WORD wValueLength / WORD wType / WCHAR szKey[] / padding / value / padding / children[]
// 所有块都按 4 字节对齐。这里用一个小构造器拼出来，避免手算偏移。

type vsBlock struct {
	key      string
	typ      uint16
	value    []byte // 已编码的字节；wValueLength 由 typ 决定怎么算
	children []*vsBlock
}

func (b *vsBlock) encode() []byte {
	var buf []byte
	buf = append(buf, 0, 0, 0, 0, 0, 0) // wLength/wValueLength/wType 先占位
	buf = append(buf, str(b.key)...)
	buf = pad4(buf)
	buf = append(buf, b.value...)
	buf = pad4(buf)
	for _, c := range b.children {
		buf = append(buf, c.encode()...)
	}
	valueLen := len(b.value)
	if b.typ == 1 { // 文本块：wValueLength 数的是 WCHAR 个数
		valueLen = len(b.value) / 2
	}
	binary.LittleEndian.PutUint16(buf[0:], uint16(len(buf)))
	binary.LittleEndian.PutUint16(buf[2:], uint16(valueLen))
	binary.LittleEndian.PutUint16(buf[4:], b.typ)
	return buf
}

func utf16Bytes(s string) []byte {
	u := utf16.Encode([]rune(s))
	out := make([]byte, 0, (len(u)+1)*2)
	for _, c := range u {
		out = binary.LittleEndian.AppendUint16(out, c)
	}
	return out
}

// str 是键或值里的以 0 结尾的 UTF-16 字符串。
func str(s string) []byte {
	out := utf16Bytes(s)
	return append(out, 0, 0)
}

func pad4(b []byte) []byte {
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

func fixedFileInfo(ms, ls uint32) []byte {
	b := make([]byte, 0, 52)
	for _, v := range []uint32{
		0xFEEF04BD, // dwSignature
		0x00010000, // dwStrucVersion
		ms, ls,     // dwFileVersionMS/LS
		ms, ls, // dwProductVersionMS/LS
		0x3F,    // dwFileFlagsMask
		0,       // dwFileFlags
		0x40004, // dwFileOS = VOS_NT_WINDOWS32
		1,       // dwFileType = VFT_APP
		0,       // dwFileSubtype
		0, 0,    // dwFileDateMS/LS
	} {
		b = binary.LittleEndian.AppendUint32(b, v)
	}
	return b
}

func versionInfo(ver, product, company, desc, copyright string) []byte {
	ms, ls := versionParts(ver)
	strings := []struct{ k, v string }{
		{"CompanyName", company},
		{"FileDescription", desc},
		{"FileVersion", ver},
		{"InternalName", "MarkPic"},
		{"LegalCopyright", copyright},
		{"OriginalFilename", "MarkPic.exe"},
		{"ProductName", product},
		{"ProductVersion", ver},
	}
	var entries []*vsBlock
	for _, s := range strings {
		entries = append(entries, &vsBlock{key: s.k, typ: 1, value: str(s.v)})
	}
	table := &vsBlock{key: "040904B0", typ: 1, children: entries}
	sfi := &vsBlock{key: "StringFileInfo", typ: 1, children: []*vsBlock{table}}
	vfi := &vsBlock{
		key:      "VarFileInfo",
		typ:      1,
		children: []*vsBlock{{key: "Translation", typ: 0, value: []byte{0x09, 0x04, 0xB0, 0x04}}},
	}
	root := &vsBlock{
		key:      "VS_VERSION_INFO",
		typ:      0,
		value:    fixedFileInfo(ms, ls),
		children: []*vsBlock{sfi, vfi},
	}
	return root.encode()
}

// manifestXML 是最小可用的一组：以当前用户权限运行（不要管理员提权提示）、
// 声明支持到 Win11（拿正经的版本 API 行为）、支持超长路径、UTF-8 代码页。
// 不写 DPI 相关设置——DPI 感知是程序启动时自己调的（cmd/markpic/dpi_windows.go），
// 清单里再写一遍会和它打架。
func manifestXML(ver string) string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
  <assemblyIdentity type="win32" name="MarkPic" version="` + ver + `" processorArchitecture="amd64"/>
  <trustInfo xmlns="urn:schemas-microsoft-com:asm.v3">
    <security>
      <requestedPrivileges>
        <requestedExecutionLevel level="asInvoker" uiAccess="false"/>
      </requestedPrivileges>
    </security>
  </trustInfo>
  <dependency>
    <dependentAssembly>
      <assemblyIdentity type="win32" name="Microsoft.Windows.Common-Controls" version="6.0.0.0" processorArchitecture="*" publicKeyToken="6595b64144ccf1df" language="*"/>
    </dependentAssembly>
  </dependency>
  <application xmlns="urn:schemas-microsoft-com:asm.v3">
    <windowsSettings>
      <longPathAware xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">true</longPathAware>
      <activeCodePage xmlns="http://schemas.microsoft.com/SMI/2019/WindowsSettings">UTF-8</activeCodePage>
    </windowsSettings>
  </application>
  <compatibility xmlns="urn:schemas-microsoft-com:compatibility.v1">
    <application>
      <supportedOS Id="{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}"/>
      <supportedOS Id="{1f676c76-80e1-4239-95bb-83d0f6d0da78}"/>
      <supportedOS Id="{4a2f28e3-53b9-4441-ba9c-d69d4a4a6e38}"/>
      <supportedOS Id="{35138b9a-5d96-4fbd-8e2d-a2440225f93a}"/>
    </application>
  </compatibility>
</assembly>
`
}
