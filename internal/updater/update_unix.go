//go:build !windows

package updater

import (
	"os"
	"syscall"
	"time"
)

// installAndRestart 在类 Unix 系统上原地替换可执行文件，随后用新二进制重新执行自身。
func installAndRestart(newBin []byte) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	tmp := exe + ".new"
	if err := os.WriteFile(tmp, newBin, 0o755); err != nil {
		return err
	}
	// 先备份旧文件，再原子替换（运行中的文件在 Unix 上允许被重命名/覆盖）。
	_ = os.Rename(exe, exe+".old")
	if err := os.Rename(tmp, exe); err != nil {
		return err
	}
	time.AfterFunc(time.Second, func() {
		if err := syscall.Exec(exe, os.Args, os.Environ()); err != nil {
			os.Exit(0)
		}
	})
	return nil
}
