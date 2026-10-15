package cli

import (
	"fmt"
	"oci-help/internal/applog"
	"oci-help/internal/config"
	"oci-help/internal/oci"
	"oci-help/internal/store"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// active 是 CLI 当前选中的账号上下文（同一次运行中只操作一个账号）。
var active *oci.AccountCtx

// Run 进入 CLI 主流程：选择账号 → 展示主菜单。
func Run() {
	tenants := oci.Tenants()
	var chosen *store.Tenant
	if len(tenants) == 1 {
		chosen = &tenants[0]
	} else {
		fmt.Printf("\n\033[1;32m%s\033[0m\n\n", "欢迎使用甲骨文实例管理工具")
		w := new(tabwriter.Writer)
		w.Init(os.Stdout, 4, 8, 1, '\t', 0)
		fmt.Fprintf(w, "%s\t%s\t\n", "序号", "账号")
		for i, t := range tenants {
			fmt.Fprintf(w, "%d\t%s\t\n", i+1, t.Name)
		}
		w.Flush()
		fmt.Printf("\n")
		var input string
		var index int
		for {
			fmt.Print("请输入账号对应的序号进入相关操作: ")
			_, err := fmt.Scanln(&input)
			if err != nil {
				return
			}
			if strings.EqualFold(input, "oci") {
				multiBatchLaunchInstances()
				Run()
				return
			} else if strings.EqualFold(input, "ip") {
				multiBatchListInstancesIp()
				Run()
				return
			}
			index, _ = strconv.Atoi(input)
			if 0 < index && index <= len(tenants) {
				break
			} else {
				index = 0
				input = ""
				fmt.Printf("\033[1;31m错误! 请输入正确的序号\033[0m\n")
			}
		}
		chosen = &tenants[index-1]
	}

	ac, err := oci.NewAccountCtx(chosen)
	if err != nil {
		applog.Errf("初始化账号失败", err.Error())
		return
	}
	active = ac

	// 获取可用性域
	fmt.Println("正在获取可用性域...")
	if _, err := active.EnsureAvailabilityDomains(); err != nil {
		applog.Errf("获取可用性域失败", err.Error())
		return
	}

	showMainMenu()
}

func showMainMenu() {
	fmt.Printf("\n\033[1;32m欢迎使用甲骨文实例管理工具\033[0m \n(当前账号: %s)\n\n", active.Name)
	fmt.Printf("\033[1;36m%s\033[0m %s\n", "1.", "查看实例")
	fmt.Printf("\033[1;36m%s\033[0m %s\n", "2.", "创建实例")
	fmt.Printf("\033[1;36m%s\033[0m %s\n", "3.", "管理引导卷")
	fmt.Print("\n请输入序号进入相关操作: ")
	var input string
	var num int
	fmt.Scanln(&input)
	if strings.EqualFold(input, "oci") {
		batchLaunchInstances(active)
		showMainMenu()
		return
	} else if strings.EqualFold(input, "ip") {
		IPsFilePath := config.IPsFilePrefix + "-" + time.Now().Format("2006-01-02-150405.txt")
		batchListInstancesIp(IPsFilePath, active)
		showMainMenu()
		return
	}
	num, _ = strconv.Atoi(input)
	switch num {
	case 1:
		listInstances()
	case 2:
		listLaunchInstanceTemplates()
	case 3:
		listBootVolumes()
	default:
		if len(oci.Tenants()) > 1 {
			Run()
		}
	}
}
