//go:build !windows

package applog

// IsDoubleClickLaunch 仅 Windows 需要判断双击启动。
func IsDoubleClickLaunch() bool {
	return false
}
