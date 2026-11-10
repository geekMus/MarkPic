//go:build !windows

package main

// 非 Windows 平台由系统自己处理缩放，这里不需要做任何事。

func enableDPIAwareness() {}

func systemScale() float64 { return 1 }

func scaleFor(uintptr) float64 { return 1 }
