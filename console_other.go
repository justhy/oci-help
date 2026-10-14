//go:build !windows

package main

// isDoubleClickLaunch 仅 Windows 需要判断双击启动。
func isDoubleClickLaunch() bool {
	return false
}
