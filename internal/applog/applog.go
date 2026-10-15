// Package applog 提供控制台输出助手：带时间戳的打印、错误高亮、
// 双击启动检测与退出前暂停等，供 CLI 与各内部包共用。
package applog

import (
	"bufio"
	"fmt"
	"os"
	"time"
)

// Printf 打印带时间戳前缀的信息。
func Printf(format string, a ...interface{}) {
	fmt.Printf("%s ", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Printf(format, a...)
}

// Errf 以红色打印错误。
func Errf(desc, detail string) {
	fmt.Printf("\033[1;31mError: %s. %s\033[0m\n", desc, detail)
}

// ErrExit 打印启动阶段的致命错误并退出。
// 双击运行时控制台窗口会随进程关闭，这里先等待回车，避免看不到错误原因。
func ErrExit(desc, detail string) {
	Errf(desc, detail)
	PauseIfDoubleClick()
	os.Exit(1)
}

// PauseIfDoubleClick 双击启动（控制台仅挂本进程）时等待回车再退出。
func PauseIfDoubleClick() {
	if !IsDoubleClickLaunch() {
		return
	}
	fmt.Print("\n按回车键退出...")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}
