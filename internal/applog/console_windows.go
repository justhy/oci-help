//go:build windows

package applog

import (
	"syscall"
	"unsafe"
)

var (
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleProcessList = kernel32.NewProc("GetConsoleProcessList")
	procGetConsoleWindow      = kernel32.NewProc("GetConsoleWindow")
)

// IsDoubleClickLaunch 判断当前进程是否由资源管理器双击启动。
// 判据：进程拥有控制台窗口，且该控制台只挂着本进程。
// 从已有终端启动时控制台里还有 shell 进程，重定向/服务方式运行时没有控制台窗口。
func IsDoubleClickLaunch() bool {
	if hwnd, _, _ := procGetConsoleWindow.Call(); hwnd == 0 {
		return false
	}
	var pids [2]uint32
	n, _, _ := procGetConsoleProcessList.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	return n == 1
}
