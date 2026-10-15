//go:build windows

package updater

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// installAndRestart 在 Windows 上无法覆盖正在运行的可执行文件，
// 因此写一个批处理：等待本进程退出 → 覆盖 exe → 重新启动。
func installAndRestart(newBin []byte) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	newPath := exe + ".new"
	if err := os.WriteFile(newPath, newBin, 0o755); err != nil {
		return err
	}
	batPath := exe + ".update.bat"
	script := "@echo off\r\n" +
		"timeout /t 2 /nobreak >nul\r\n" +
		fmt.Sprintf("move /y \"%s\" \"%s\" >nul\r\n", newPath, exe) +
		fmt.Sprintf("start \"\" \"%s\"\r\n", exe) +
		"del \"%~f0\"\r\n"
	if err := os.WriteFile(batPath, []byte(script), 0o755); err != nil {
		return err
	}
	cmd := exec.Command("cmd", "/c", "start", "", "/min", batPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	// 给 HTTP 响应留出返回时间后退出，由批处理完成替换与重启。
	time.AfterFunc(1200*time.Millisecond, func() { os.Exit(0) })
	return nil
}
