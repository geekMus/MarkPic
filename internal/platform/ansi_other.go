//go:build !windows

package platform

// 非 Windows 平台没有「本机 ANSI 代码页」这个概念（也拿不到 GBK 转换表，
// 不引第三方库的前提下无从实现），所以一律不支持：调用方会把这类文档
// 判为「编码不支持」并给出提示。
const ansiName = "本机 ANSI"

// ANSIName 返回本机 ANSI 编码的名字。
func ANSIName() string { return ansiName }

// ANSIToUTF16 在非 Windows 平台不支持。
func ANSIToUTF16([]byte) ([]uint16, bool) { return nil, false }

// UTF16ToANSI 在非 Windows 平台不支持。
func UTF16ToANSI([]uint16) ([]byte, bool) { return nil, false }
