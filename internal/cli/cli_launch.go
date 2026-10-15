package cli

import (
	"fmt"
	"oci-help/internal/applog"
	"oci-help/internal/config"
	"oci-help/internal/notify"
	"oci-help/internal/oci"
	"oci-help/internal/store"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"gopkg.in/ini.v1"
)

// instanceTemplates 汇总全局 [INSTANCE] 与指定账号下的实例模板。
// 只依赖 INI，不需要构造 OCI 客户端（凭证无效时也能读取）。
func instanceTemplates(t *store.Tenant) []*ini.Section {
	return config.C.InstanceTemplates(t.IniSec)
}

// currentTemplates 汇总全局 [INSTANCE] 与当前账号下的实例模板。
func currentTemplates() []*ini.Section {
	var iniSec *ini.Section
	if active != nil && active.Tenant != nil {
		iniSec = active.Tenant.IniSec
	}
	return config.C.InstanceTemplates(iniSec)
}

func listLaunchInstanceTemplates() {
	instanceSections := currentTemplates()
	if len(instanceSections) == 0 {
		fmt.Printf("\033[1;31m未找到实例模版, 回车返回上一级菜单.\033[0m")
		fmt.Scanln()
		showMainMenu()
		return
	}

	for {
		fmt.Printf("\n\033[1;32m选择对应的实例模版开始创建实例\033[0m \n(当前账号: %s)\n\n", active.Name)
		w := new(tabwriter.Writer)
		w.Init(os.Stdout, 4, 8, 1, '\t', 0)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t\n", "序号", "配置", "CPU个数", "内存(GB)")
		for i, instanceSec := range instanceSections {
			cpu := instanceSec.Key("cpus").Value()
			if cpu == "" {
				cpu = "-"
			}
			memory := instanceSec.Key("memoryInGBs").Value()
			if memory == "" {
				memory = "-"
			}
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t\n", i+1, instanceSec.Key("shape").Value(), cpu, memory)
		}
		w.Flush()
		fmt.Printf("\n")
		var input string
		var index int
		for {
			fmt.Print("请输入需要创建的实例的序号: ")
			_, err := fmt.Scanln(&input)
			if err != nil {
				showMainMenu()
				return
			}
			index, _ = strconv.Atoi(input)
			if 0 < index && index <= len(instanceSections) {
				break
			} else {
				input = ""
				index = 0
				fmt.Printf("\033[1;31m错误! 请输入正确的序号\033[0m\n")
			}
		}

		instanceSection := instanceSections[index-1]
		var inst config.Instance
		if err := instanceSection.MapTo(&inst); err != nil {
			applog.Errf("解析实例模版参数失败", err.Error())
			continue
		}

		active.LaunchInstances(active.AvailabilityDomains(), inst)
	}
}

func multiBatchLaunchInstances() {
	IPsFilePath := config.IPsFilePrefix + "-" + time.Now().Format("2006-01-02-150405.txt")
	for _, t := range oci.Tenants() {
		ac, err := oci.NewAccountCtx(&t)
		if err != nil {
			applog.Errf("初始化账号失败", err.Error())
			continue
		}
		if _, err = ac.EnsureAvailabilityDomains(); err != nil {
			applog.Errf("获取可用性域失败", err.Error())
			continue
		}
		batchLaunchInstances(ac)
		batchListInstancesIp(IPsFilePath, ac)
		command(config.C.Cmd)
		oci.SleepRandomSecond(5, 5)
	}
}

// command 执行配置的抢到实例后命令（cli.cmd）。
func command(cmd string) {
	res := strings.Fields(cmd)
	if len(res) == 0 {
		return
	}
	name := res[0]
	out, err := exec.Command(name, res[1:]...).CombinedOutput()
	fmt.Println("执行命令:", strings.Join(res, " "))
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(string(out))
}

func batchLaunchInstances(ac *oci.AccountCtx) {
	instanceSections := instanceTemplates(ac.Tenant)
	if len(instanceSections) == 0 {
		return
	}

	applog.Printf("\033[1;36m[%s] 开始创建\033[0m\n", ac.Name)
	var SUM, NUM int32 = 0, 0
	notify.SendMessage(fmt.Sprintf("[%s]", ac.Name), "开始创建")

	for _, instanceSec := range instanceSections {
		inst := config.Instance{}
		err := instanceSec.MapTo(&inst)
		if err != nil {
			applog.Errf("解析实例模版参数失败", err.Error())
			continue
		}

		sum, num := ac.LaunchInstances(ac.AvailabilityDomains(), inst)

		SUM = SUM + sum
		NUM = NUM + num
	}
	applog.Printf("\033[1;36m[%s] 结束创建。创建实例总数: %d, 成功 %d , 失败 %d\033[0m\n", ac.Name, SUM, NUM, SUM-NUM)
	text := fmt.Sprintf("结束创建。创建实例总数: %d, 成功 %d , 失败 %d", SUM, NUM, SUM-NUM)
	notify.SendMessage(fmt.Sprintf("[%s]", ac.Name), text)
}

func multiBatchListInstancesIp() {
	IPsFilePath := config.IPsFilePrefix + "-" + time.Now().Format("2006-01-02-150405.txt")
	_, err := os.Stat(IPsFilePath)
	if err != nil && os.IsNotExist(err) {
		os.Create(IPsFilePath)
	}

	fmt.Printf("正在导出实例公共IP地址...\n")
	for _, t := range oci.Tenants() {
		ac, err := oci.NewAccountCtx(&t)
		if err != nil {
			applog.Errf("初始化账号失败", err.Error())
			continue
		}
		ac.ListInstancesIPs(IPsFilePath)
	}
	fmt.Printf("导出实例公共IP地址完成，请查看文件 %s\n", IPsFilePath)
}

func batchListInstancesIp(filePath string, ac *oci.AccountCtx) {
	_, err := os.Stat(filePath)
	if err != nil && os.IsNotExist(err) {
		os.Create(filePath)
	}
	fmt.Printf("正在导出实例公共IP地址...\n")
	ac.ListInstancesIPs(filePath)
	fmt.Printf("导出实例IP地址完成，请查看文件 %s\n", filePath)
}
