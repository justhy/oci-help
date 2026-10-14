/*
甲骨文云API文档
https://docs.oracle.com/en-us/iaas/api/#/en/iaas/20160918/

实例:
https://docs.oracle.com/en-us/iaas/api/#/en/iaas/20160918/Instance/
VCN:
https://docs.oracle.com/en-us/iaas/api/#/en/iaas/20160918/Vcn/
Subnet:
https://docs.oracle.com/en-us/iaas/api/#/en/iaas/20160918/Subnet/
VNIC:
https://docs.oracle.com/en-us/iaas/api/#/en/iaas/20160918/Vnic/
VnicAttachment:
https://docs.oracle.com/en-us/iaas/api/#/en/iaas/20160918/VnicAttachment/
私有IP
https://docs.oracle.com/en-us/iaas/api/#/en/iaas/20160918/PrivateIp/
公共IP
https://docs.oracle.com/en-us/iaas/api/#/en/iaas/20160918/PublicIp/

获取可用性域
https://docs.oracle.com/en-us/iaas/api/#/en/identity/20160918/AvailabilityDomain/ListAvailabilityDomains
*/
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/oracle/oci-go-sdk/v54/common"
	"github.com/oracle/oci-go-sdk/v54/core"
	"gopkg.in/ini.v1"
)

const (
	defConfigFilePath = "./oci-help.ini"
	IPsFilePrefix     = "IPs"
)

// 应用级（全局）配置与状态。账号相关的状态已迁移到 AccountCtx。
var (
	configFilePath      string
	ctx                 context.Context = context.Background()
	oracleSections      []*ini.Section
	oracleSection       *ini.Section
	instanceBaseSection *ini.Section
	instance            Instance // CLI 创建实例时使用的当前模板
	proxy               string
	token               string
	chat_id             string
	cmd                 string
	sendMessageUrl      string
	editMessageUrl      string
	EACH                bool

	// active 为 CLI 当前选中的账号上下文
	active *AccountCtx

	// 构建信息，由构建脚本通过 -ldflags "-X main.Version=..." 注入；
	// 直接 go build 时为下列默认值。
	Version   = "dev"
	Commit    = ""
	BuildTime = ""
)

type Oracle struct {
	User         string `ini:"user"`
	Fingerprint  string `ini:"fingerprint"`
	Tenancy      string `ini:"tenancy"`
	Region       string `ini:"region"`
	Key_file     string `ini:"key_file"`
	Key_password string `ini:"key_password"`
}

type Instance struct {
	AvailabilityDomain     string  `ini:"availabilityDomain"`
	SSH_Public_Key         string  `ini:"ssh_authorized_key"`
	VcnDisplayName         string  `ini:"vcnDisplayName"`
	SubnetDisplayName      string  `ini:"subnetDisplayName"`
	Shape                  string  `ini:"shape"`
	OperatingSystem        string  `ini:"OperatingSystem"`
	OperatingSystemVersion string  `ini:"OperatingSystemVersion"`
	InstanceDisplayName    string  `ini:"instanceDisplayName"`
	Ocpus                  float32 `ini:"cpus"`
	MemoryInGBs            float32 `ini:"memoryInGBs"`
	Burstable              string  `ini:"burstable"`
	BootVolumeSizeInGBs    int64   `ini:"bootVolumeSizeInGBs"`
	Sum                    int32   `ini:"sum"`
	Each                   int32   `ini:"each"`
	Retry                  int32   `ini:"retry"`
	CloudInit              string  `ini:"cloud-init"`
	MinTime                int32   `ini:"minTime"`
	MaxTime                int32   `ini:"maxTime"`
}

type Message struct {
	OK          bool `json:"ok"`
	Result      `json:"result"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
}
type Result struct {
	MessageId int `json:"message_id"`
}

func main() {
	flag.StringVar(&configFilePath, "config", defConfigFilePath, "配置文件路径")
	flag.StringVar(&configFilePath, "c", defConfigFilePath, "配置文件路径")
	flag.BoolVar(&webMode, "web", false, "启动 Web 管理界面")
	flag.BoolVar(&webMode, "w", false, "启动 Web 管理界面")
	flag.StringVar(&webAddr, "addr", "", "Web 监听地址, 例如 :8080 (默认取配置文件或 127.0.0.1:8080)")
	flag.BoolVar(&noBrowser, "no-browser", false, "启动 Web 界面时不自动打开浏览器")
	flag.Parse()

	// 未显式指定配置文件时，当前目录找不到就回退到可执行文件所在目录，
	// 这样把 exe 与 oci-help.ini 放在一起双击运行也能正常启动。
	configFilePath = resolveConfigPath(configFilePath)

	cfg, err := ini.Load(configFilePath)
	if err != nil {
		printlnErrAndPause("配置文件读取失败", err.Error()+"；可用 -c 指定配置文件路径")
	}
	defSec := cfg.Section(ini.DefaultSection)
	proxy = defSec.Key("proxy").Value()
	cmd = defSec.Key("cmd").Value()

	// Telegram 通知配置从 [Telegram] 段读取；
	// 为兼容旧配置，若 [Telegram] 段不存在则回退到默认段顶部。
	if tgSec, err := cfg.GetSection("Telegram"); err == nil {
		token = tgSec.Key("token").Value()
		chat_id = tgSec.Key("chat_id").Value()
	}
	if token == "" && chat_id == "" {
		token = defSec.Key("token").Value()
		chat_id = defSec.Key("chat_id").Value()
	}
	if defSec.HasKey("EACH") {
		EACH, _ = defSec.Key("EACH").Bool()
	} else {
		EACH = true
	}
	sendMessageUrl = "https://api.telegram.org/bot" + token + "/sendMessage"
	editMessageUrl = "https://api.telegram.org/bot" + token + "/editMessageText"
	rand.Seed(time.Now().UnixNano())

	// 加载 Web 配置
	if err := loadWebConfig(cfg); err != nil {
		printlnErrAndPause("Web 配置错误", err.Error())
	}

	sections := cfg.Sections()
	oracleSections = []*ini.Section{}
	for _, sec := range sections {
		if len(sec.ParentKeys()) == 0 {
			user := sec.Key("user").Value()
			fingerprint := sec.Key("fingerprint").Value()
			tenancy := sec.Key("tenancy").Value()
			region := sec.Key("region").Value()
			key_file := sec.Key("key_file").Value()
			if user != "" && fingerprint != "" && tenancy != "" && region != "" && key_file != "" {
				oracleSections = append(oracleSections, sec)
			}
		}
	}
	if len(oracleSections) == 0 {
		printlnErrAndPause("未找到正确的配置信息",
			"请在 "+configFilePath+" 中至少配置一个账号的 user/fingerprint/tenancy/region/key_file，参考: https://github.com/lemoex/oci-help")
	}
	instanceBaseSection = cfg.Section("INSTANCE")

	if webMode {
		if err := runWebServer(); err != nil {
			printlnErr("Web 服务启动失败", err.Error())
		}
		return
	}

	listOracleAccount()
}

func listOracleAccount() {
	if len(oracleSections) == 1 {
		oracleSection = oracleSections[0]
	} else {
		fmt.Printf("\n\033[1;32m%s\033[0m\n\n", "欢迎使用甲骨文实例管理工具")
		w := new(tabwriter.Writer)
		w.Init(os.Stdout, 4, 8, 1, '\t', 0)
		fmt.Fprintf(w, "%s\t%s\t\n", "序号", "账号")
		for i, section := range oracleSections {
			fmt.Fprintf(w, "%d\t%s\t\n", i+1, section.Name())
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
				listOracleAccount()
				return
			} else if strings.EqualFold(input, "ip") {
				multiBatchListInstancesIp()
				listOracleAccount()
				return
			}
			index, _ = strconv.Atoi(input)
			if 0 < index && index <= len(oracleSections) {
				break
			} else {
				index = 0
				input = ""
				fmt.Printf("\033[1;31m错误! 请输入正确的序号\033[0m\n")
			}
		}
		oracleSection = oracleSections[index-1]
	}

	ac, err := newAccountCtx(oracleSection)
	if err != nil {
		printlnErr("初始化账号失败", err.Error())
		return
	}
	active = ac

	// 获取可用性域
	fmt.Println("正在获取可用性域...")
	if _, err := active.ensureAvailabilityDomains(); err != nil {
		printlnErr("获取可用性域失败", err.Error())
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
		IPsFilePath := IPsFilePrefix + "-" + time.Now().Format("2006-01-02-150405.txt")
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
		if len(oracleSections) > 1 {
			listOracleAccount()
		}
	}
}

func listInstances() {
	fmt.Println("正在获取实例数据...")
	instances, err := active.ListAllInstances()
	if err != nil {
		printlnErr("获取失败, 回车返回上一级菜单.", err.Error())
		fmt.Scanln()
		showMainMenu()
		return
	}
	if len(instances) == 0 {
		fmt.Printf("\033[1;32m实例为空, 回车返回上一级菜单.\033[0m")
		fmt.Scanln()
		showMainMenu()
		return
	}
	fmt.Printf("\n\033[1;32m实例信息\033[0m \n(当前账号: %s)\n\n", active.Name)
	w := new(tabwriter.Writer)
	w.Init(os.Stdout, 4, 8, 1, '\t', 0)
	fmt.Fprintf(w, "%s\t%s\t%s\t%s\t\n", "序号", "名称", "状态　　", "配置")
	for i, ins := range instances {
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t\n", i+1, *ins.DisplayName, getInstanceState(ins.LifecycleState), *ins.Shape)
	}
	w.Flush()
	fmt.Println("--------------------")
	fmt.Printf("\n\033[1;32ma: %s   b: %s   c: %s   d: %s\033[0m\n", "启动全部", "停止全部", "重启全部", "终止全部")
	var input string
	var index int
	for {
		fmt.Print("请输入序号查看实例详细信息: ")
		_, err := fmt.Scanln(&input)
		if err != nil {
			showMainMenu()
			return
		}
		switch input {
		case "a":
			fmt.Printf("确定启动全部实例？(输入 y 并回车): ")
			var input string
			fmt.Scanln(&input)
			if strings.EqualFold(input, "y") {
				for _, ins := range instances {
					_, err := active.instanceAction(ins.Id, core.InstanceActionActionStart)
					if err != nil {
						fmt.Printf("\033[1;31m实例 %s 启动失败.\033[0m %s\n", *ins.DisplayName, err.Error())
					} else {
						fmt.Printf("\033[1;32m实例 %s 启动成功.\033[0m\n", *ins.DisplayName)
					}
				}
			} else {
				continue
			}
			time.Sleep(1 * time.Second)
			listInstances()
			return
		case "b":
			fmt.Printf("确定停止全部实例？(输入 y 并回车): ")
			var input string
			fmt.Scanln(&input)
			if strings.EqualFold(input, "y") {
				for _, ins := range instances {
					_, err := active.instanceAction(ins.Id, core.InstanceActionActionSoftstop)
					if err != nil {
						fmt.Printf("\033[1;31m实例 %s 停止失败.\033[0m %s\n", *ins.DisplayName, err.Error())
					} else {
						fmt.Printf("\033[1;32m实例 %s 停止成功.\033[0m\n", *ins.DisplayName)
					}
				}
			} else {
				continue
			}
			time.Sleep(1 * time.Second)
			listInstances()
			return
		case "c":
			fmt.Printf("确定重启全部实例？(输入 y 并回车): ")
			var input string
			fmt.Scanln(&input)
			if strings.EqualFold(input, "y") {
				for _, ins := range instances {
					_, err := active.instanceAction(ins.Id, core.InstanceActionActionSoftreset)
					if err != nil {
						fmt.Printf("\033[1;31m实例 %s 重启失败.\033[0m %s\n", *ins.DisplayName, err.Error())
					} else {
						fmt.Printf("\033[1;32m实例 %s 重启成功.\033[0m\n", *ins.DisplayName)
					}
				}
			} else {
				continue
			}
			time.Sleep(1 * time.Second)
			listInstances()
			return
		case "d":
			fmt.Printf("确定终止全部实例？(输入 y 并回车): ")
			var input string
			fmt.Scanln(&input)
			if strings.EqualFold(input, "y") {
				for _, ins := range instances {
					err := active.terminateInstance(ins.Id)
					if err != nil {
						fmt.Printf("\033[1;31m实例 %s 终止失败.\033[0m %s\n", *ins.DisplayName, err.Error())
					} else {
						fmt.Printf("\033[1;32m实例 %s 终止成功.\033[0m\n", *ins.DisplayName)
					}
				}
			} else {
				continue
			}
			time.Sleep(1 * time.Second)
			listInstances()
			return
		}
		index, _ = strconv.Atoi(input)
		if 0 < index && index <= len(instances) {
			break
		} else {
			input = ""
			index = 0
			fmt.Printf("\033[1;31m错误! 请输入正确的序号\033[0m\n")
		}
	}
	instanceDetails(instances[index-1].Id)
}

func instanceDetails(instanceId *string) {
	for {
		fmt.Println("正在获取实例详细信息...")
		instance, err := active.getInstance(instanceId)
		if err != nil {
			fmt.Printf("\033[1;31m获取实例详细信息失败, 回车返回上一级菜单.\033[0m")
			fmt.Scanln()
			listInstances()
			return
		}
		vnics, err := active.getInstanceVnics(instanceId)
		if err != nil {
			fmt.Printf("\033[1;31m获取实例VNIC失败, 回车返回上一级菜单.\033[0m")
			fmt.Scanln()
			listInstances()
			return
		}
		var publicIps = make([]string, 0)
		var strPublicIps string
		for _, vnic := range vnics {
			if vnic.PublicIp != nil {
				publicIps = append(publicIps, *vnic.PublicIp)
			}
		}
		strPublicIps = strings.Join(publicIps, ",")

		fmt.Printf("\n\033[1;32m实例详细信息\033[0m \n(当前账号: %s)\n\n", active.Name)
		fmt.Println("--------------------")
		fmt.Printf("名称: %s\n", s(instance.DisplayName))
		fmt.Printf("状态: %s\n", getInstanceState(instance.LifecycleState))
		fmt.Printf("公共IP: %s\n", strPublicIps)
		fmt.Printf("可用性域: %s\n", s(instance.AvailabilityDomain))
		fmt.Printf("配置: %s\n", s(instance.Shape))
		fmt.Printf("OCPU计数: %g\n", shapeOcpus(instance.ShapeConfig))
		fmt.Printf("网络带宽(Gbps): %g\n", shapeBandwidth(instance.ShapeConfig))
		fmt.Printf("内存(GB): %g\n\n", shapeMemoryInGBs(instance.ShapeConfig))
		fmt.Printf("Oracle Cloud Agent 插件配置情况\n")
		if instance.AgentConfig != nil {
			fmt.Printf("监控插件已禁用？: %t\n", bval(instance.AgentConfig.IsMonitoringDisabled))
			fmt.Printf("管理插件已禁用？: %t\n", bval(instance.AgentConfig.IsManagementDisabled))
			fmt.Printf("所有插件均已禁用？: %t\n", bval(instance.AgentConfig.AreAllPluginsDisabled))
			for _, value := range instance.AgentConfig.PluginsConfig {
				fmt.Printf("%s: %s\n", s(value.Name), value.DesiredState)
			}
		} else {
			fmt.Printf("未获取到插件配置\n")
		}
		fmt.Println("--------------------")
		fmt.Printf("\n\033[1;32m1: %s   2: %s   3: %s   4: %s   5: %s\033[0m\n", "启动", "停止", "重启", "终止", "更换公共IP")
		fmt.Printf("\033[1;32m6: %s   7: %s   8: %s\033[0m\n", "升级/降级", "修改名称", "Oracle Cloud Agent 插件配置")
		var input string
		var num int
		fmt.Print("\n请输入需要执行操作的序号: ")
		fmt.Scanln(&input)
		num, _ = strconv.Atoi(input)
		switch num {
		case 1:
			_, err := active.instanceAction(instance.Id, core.InstanceActionActionStart)
			if err != nil {
				fmt.Printf("\033[1;31m启动实例失败.\033[0m %s\n", err.Error())
			} else {
				fmt.Printf("\033[1;32m正在启动实例, 请稍后查看实例状态\033[0m\n")
			}
			time.Sleep(1 * time.Second)

		case 2:
			_, err := active.instanceAction(instance.Id, core.InstanceActionActionSoftstop)
			if err != nil {
				fmt.Printf("\033[1;31m停止实例失败.\033[0m %s\n", err.Error())
			} else {
				fmt.Printf("\033[1;32m正在停止实例, 请稍后查看实例状态\033[0m\n")
			}
			time.Sleep(1 * time.Second)

		case 3:
			_, err := active.instanceAction(instance.Id, core.InstanceActionActionSoftreset)
			if err != nil {
				fmt.Printf("\033[1;31m重启实例失败.\033[0m %s\n", err.Error())
			} else {
				fmt.Printf("\033[1;32m正在重启实例, 请稍后查看实例状态\033[0m\n")
			}
			time.Sleep(1 * time.Second)

		case 4:
			fmt.Printf("确定终止实例？(输入 y 并回车): ")
			var input string
			fmt.Scanln(&input)
			if strings.EqualFold(input, "y") {
				err := active.terminateInstance(instance.Id)
				if err != nil {
					fmt.Printf("\033[1;31m终止实例失败.\033[0m %s\n", err.Error())
				} else {
					fmt.Printf("\033[1;32m正在终止实例, 请稍后查看实例状态\033[0m\n")
				}
				time.Sleep(1 * time.Second)
			}

		case 5:
			if len(vnics) == 0 {
				fmt.Printf("\033[1;31m实例已终止或获取实例VNIC失败，请稍后重试.\033[0m\n")
				break
			}
			fmt.Printf("将删除当前公共IP并创建一个新的公共IP。确定更换实例公共IP？(输入 y 并回车): ")
			var input string
			fmt.Scanln(&input)
			if strings.EqualFold(input, "y") {
				publicIp, err := active.changePublicIp(vnics)
				if err != nil {
					fmt.Printf("\033[1;31m更换实例公共IP失败.\033[0m %s\n", err.Error())
				} else {
					fmt.Printf("\033[1;32m更换实例公共IP成功, 实例公共IP: \033[0m%s\n", *publicIp.IpAddress)
				}
				time.Sleep(1 * time.Second)
			}

		case 6:
			fmt.Printf("升级/降级实例, 请输入CPU个数: ")
			var input string
			var ocpus float32
			var memoryInGBs float32
			fmt.Scanln(&input)
			value, _ := strconv.ParseFloat(input, 32)
			ocpus = float32(value)
			input = ""
			fmt.Printf("升级/降级实例, 请输入内存大小: ")
			fmt.Scanln(&input)
			value, _ = strconv.ParseFloat(input, 32)
			memoryInGBs = float32(value)
			fmt.Println("正在升级/降级实例...")
			_, err := active.updateInstance(instance.Id, nil, &ocpus, &memoryInGBs, nil, nil)
			if err != nil {
				fmt.Printf("\033[1;31m升级/降级实例失败.\033[0m %s\n", err.Error())
			} else {
				fmt.Printf("\033[1;32m升级/降级实例成功.\033[0m\n")
			}
			time.Sleep(1 * time.Second)

		case 7:
			fmt.Printf("请为实例输入一个新的名称: ")
			var input string
			fmt.Scanln(&input)
			fmt.Println("正在修改实例名称...")
			_, err := active.updateInstance(instance.Id, &input, nil, nil, nil, nil)
			if err != nil {
				fmt.Printf("\033[1;31m修改实例名称失败.\033[0m %s\n", err.Error())
			} else {
				fmt.Printf("\033[1;32m修改实例名称成功.\033[0m\n")
			}
			time.Sleep(1 * time.Second)

		case 8:
			fmt.Printf("Oracle Cloud Agent 插件配置, 请输入 (1: 启用管理和监控插件; 2: 禁用管理和监控插件): ")
			var input string
			fmt.Scanln(&input)
			if input == "1" {
				disable := false
				_, err := active.updateInstance(instance.Id, nil, nil, nil, instance.AgentConfig.PluginsConfig, &disable)
				if err != nil {
					fmt.Printf("\033[1;31m启用管理和监控插件失败.\033[0m %s\n", err.Error())
				} else {
					fmt.Printf("\033[1;32m启用管理和监控插件成功.\033[0m\n")
				}
			} else if input == "2" {
				disable := true
				_, err := active.updateInstance(instance.Id, nil, nil, nil, instance.AgentConfig.PluginsConfig, &disable)
				if err != nil {
					fmt.Printf("\033[1;31m禁用管理和监控插件失败.\033[0m %s\n", err.Error())
				} else {
					fmt.Printf("\033[1;32m禁用管理和监控插件成功.\033[0m\n")
				}
			} else {
				fmt.Printf("\033[1;31m输入错误.\033[0m\n")
			}
			time.Sleep(1 * time.Second)

		default:
			listInstances()
			return
		}
	}
}

func listBootVolumes() {
	var bootVolumes []core.BootVolume
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, ad := range active.AvailabilityDomains() {
		wg.Add(1)
		go func(adName *string) {
			defer wg.Done()
			volumes, err := active.getBootVolumes(adName)
			if err != nil {
				printlnErr("获取引导卷失败", err.Error())
			} else {
				mu.Lock()
				bootVolumes = append(bootVolumes, volumes...)
				mu.Unlock()
			}
		}(ad.Name)
	}
	wg.Wait()

	fmt.Printf("\n\033[1;32m引导卷\033[0m \n(当前账号: %s)\n\n", active.Name)
	w := new(tabwriter.Writer)
	w.Init(os.Stdout, 4, 8, 1, '\t', 0)
	fmt.Fprintf(w, "%s\t%s\t%s\t%s\t\n", "序号", "名称", "状态　　", "大小(GB)")
	for i, volume := range bootVolumes {
		fmt.Fprintf(w, "%d\t%s\t%s\t%d\t\n", i+1, *volume.DisplayName, getBootVolumeState(volume.LifecycleState), *volume.SizeInGBs)
	}
	w.Flush()
	fmt.Printf("\n")
	var input string
	var index int
	for {
		fmt.Print("请输入序号查看引导卷详细信息: ")
		_, err := fmt.Scanln(&input)
		if err != nil {
			showMainMenu()
			return
		}
		index, _ = strconv.Atoi(input)
		if 0 < index && index <= len(bootVolumes) {
			break
		} else {
			input = ""
			index = 0
			fmt.Printf("\033[1;31m错误! 请输入正确的序号\033[0m\n")
		}
	}
	bootvolumeDetails(bootVolumes[index-1].Id)
}

func bootvolumeDetails(bootVolumeId *string) {
	for {
		fmt.Println("正在获取引导卷详细信息...")
		bootVolume, err := active.getBootVolume(bootVolumeId)
		if err != nil {
			fmt.Printf("\033[1;31m获取引导卷详细信息失败, 回车返回上一级菜单.\033[0m")
			fmt.Scanln()
			listBootVolumes()
			return
		}

		attachments, err := active.listBootVolumeAttachments(bootVolume.AvailabilityDomain, bootVolume.CompartmentId, bootVolume.Id)
		attachIns := make([]string, 0)
		if err != nil {
			attachIns = append(attachIns, err.Error())
		} else {
			for _, attachment := range attachments {
				ins, err := active.getInstance(attachment.InstanceId)
				if err != nil {
					attachIns = append(attachIns, err.Error())
				} else {
					attachIns = append(attachIns, *ins.DisplayName)
				}
			}
		}

		var performance string
		switch *bootVolume.VpusPerGB {
		case 10:
			performance = fmt.Sprintf("均衡 (VPU:%d)", *bootVolume.VpusPerGB)
		case 20:
			performance = fmt.Sprintf("性能较高 (VPU:%d)", *bootVolume.VpusPerGB)
		default:
			performance = fmt.Sprintf("UHP (VPU:%d)", *bootVolume.VpusPerGB)
		}

		fmt.Printf("\n\033[1;32m引导卷详细信息\033[0m \n(当前账号: %s)\n\n", active.Name)
		fmt.Println("--------------------")
		fmt.Printf("名称: %s\n", *bootVolume.DisplayName)
		fmt.Printf("状态: %s\n", getBootVolumeState(bootVolume.LifecycleState))
		fmt.Printf("可用性域: %s\n", *bootVolume.AvailabilityDomain)
		fmt.Printf("大小(GB): %d\n", *bootVolume.SizeInGBs)
		fmt.Printf("性能: %s\n", performance)
		fmt.Printf("附加的实例: %s\n", strings.Join(attachIns, ","))
		fmt.Println("--------------------")
		fmt.Printf("\n\033[1;32m1: %s   2: %s   3: %s   4: %s\033[0m\n", "修改性能", "修改大小", "分离引导卷", "终止引导卷")
		var input string
		var num int
		fmt.Print("\n请输入需要执行操作的序号: ")
		fmt.Scanln(&input)
		num, _ = strconv.Atoi(input)
		switch num {
		case 1:
			fmt.Printf("修改引导卷性能, 请输入 (1: 均衡; 2: 性能较高): ")
			var input string
			fmt.Scanln(&input)
			if input == "1" {
				_, err := active.updateBootVolume(bootVolume.Id, nil, common.Int64(10))
				if err != nil {
					fmt.Printf("\033[1;31m修改引导卷性能失败.\033[0m %s\n", err.Error())
				} else {
					fmt.Printf("\033[1;32m修改引导卷性能成功, 请稍后查看引导卷状态\033[0m\n")
				}
			} else if input == "2" {
				_, err := active.updateBootVolume(bootVolume.Id, nil, common.Int64(20))
				if err != nil {
					fmt.Printf("\033[1;31m修改引导卷性能失败.\033[0m %s\n", err.Error())
				} else {
					fmt.Printf("\033[1;32m修改引导卷性能成功, 请稍后查看引导卷信息\033[0m\n")
				}
			} else {
				fmt.Printf("\033[1;31m输入错误.\033[0m\n")
			}
			time.Sleep(1 * time.Second)

		case 2:
			fmt.Printf("修改引导卷大小, 请输入 (例如修改为50GB, 输入50): ")
			var input string
			var sizeInGBs int64
			fmt.Scanln(&input)
			sizeInGBs, _ = strconv.ParseInt(input, 10, 64)
			if sizeInGBs > 0 {
				_, err := active.updateBootVolume(bootVolume.Id, &sizeInGBs, nil)
				if err != nil {
					fmt.Printf("\033[1;31m修改引导卷大小失败.\033[0m %s\n", err.Error())
				} else {
					fmt.Printf("\033[1;32m修改引导卷大小成功, 请稍后查看引导卷信息\033[0m\n")
				}
			} else {
				fmt.Printf("\033[1;31m输入错误.\033[0m\n")
			}
			time.Sleep(1 * time.Second)

		case 3:
			fmt.Printf("确定分离引导卷？(输入 y 并回车): ")
			var input string
			fmt.Scanln(&input)
			if strings.EqualFold(input, "y") {
				for _, attachment := range attachments {
					_, err := active.detachBootVolume(attachment.Id)
					if err != nil {
						fmt.Printf("\033[1;31m分离引导卷失败.\033[0m %s\n", err.Error())
					} else {
						fmt.Printf("\033[1;32m分离引导卷成功, 请稍后查看引导卷信息\033[0m\n")
					}
				}
			}
			time.Sleep(1 * time.Second)

		case 4:
			fmt.Printf("确定终止引导卷？(输入 y 并回车): ")
			var input string
			fmt.Scanln(&input)
			if strings.EqualFold(input, "y") {
				_, err := active.deleteBootVolume(bootVolume.Id)
				if err != nil {
					fmt.Printf("\033[1;31m终止引导卷失败.\033[0m %s\n", err.Error())
				} else {
					fmt.Printf("\033[1;32m终止引导卷成功, 请稍后查看引导卷信息\033[0m\n")
				}

			}
			time.Sleep(1 * time.Second)

		default:
			listBootVolumes()
			return
		}
	}
}

// instanceTemplates 汇总全局 [INSTANCE] 与当前账号下的实例模板。
func instanceTemplates(ac *AccountCtx) []*ini.Section {
	var instanceSections []*ini.Section
	instanceSections = append(instanceSections, instanceBaseSection.ChildSections()...)
	if ac != nil && ac.Section != nil {
		instanceSections = append(instanceSections, ac.Section.ChildSections()...)
	}
	return instanceSections
}

func listLaunchInstanceTemplates() {
	instanceSections := instanceTemplates(active)
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
		instance = Instance{}
		err := instanceSection.MapTo(&instance)
		if err != nil {
			printlnErr("解析实例模版参数失败", err.Error())
			continue
		}

		active.LaunchInstances(active.AvailabilityDomains(), instance)
	}
}

func multiBatchLaunchInstances() {
	IPsFilePath := IPsFilePrefix + "-" + time.Now().Format("2006-01-02-150405.txt")
	for _, sec := range oracleSections {
		ac, err := newAccountCtx(sec)
		if err != nil {
			printlnErr("初始化账号失败", err.Error())
			continue
		}
		if _, err = ac.ensureAvailabilityDomains(); err != nil {
			printlnErr("获取可用性域失败", err.Error())
			continue
		}
		batchLaunchInstances(ac)
		batchListInstancesIp(IPsFilePath, ac)
		command(cmd)
		sleepRandomSecond(5, 5)
	}
}

func batchLaunchInstances(ac *AccountCtx) {
	instanceSections := instanceTemplates(ac)
	if len(instanceSections) == 0 {
		return
	}

	printf("\033[1;36m[%s] 开始创建\033[0m\n", ac.Name)
	var SUM, NUM int32 = 0, 0
	sendMessage(fmt.Sprintf("[%s]", ac.Name), "开始创建")

	for _, instanceSec := range instanceSections {
		inst := Instance{}
		err := instanceSec.MapTo(&inst)
		if err != nil {
			printlnErr("解析实例模版参数失败", err.Error())
			continue
		}

		sum, num := ac.LaunchInstances(ac.AvailabilityDomains(), inst)

		SUM = SUM + sum
		NUM = NUM + num
	}
	printf("\033[1;36m[%s] 结束创建。创建实例总数: %d, 成功 %d , 失败 %d\033[0m\n", ac.Name, SUM, NUM, SUM-NUM)
	text := fmt.Sprintf("结束创建。创建实例总数: %d, 成功 %d , 失败 %d", SUM, NUM, SUM-NUM)
	sendMessage(fmt.Sprintf("[%s]", ac.Name), text)
}

func multiBatchListInstancesIp() {
	IPsFilePath := IPsFilePrefix + "-" + time.Now().Format("2006-01-02-150405.txt")
	_, err := os.Stat(IPsFilePath)
	if err != nil && os.IsNotExist(err) {
		os.Create(IPsFilePath)
	}

	fmt.Printf("正在导出实例公共IP地址...\n")
	for _, sec := range oracleSections {
		ac, err := newAccountCtx(sec)
		if err != nil {
			printlnErr("初始化账号失败", err.Error())
			continue
		}
		ac.ListInstancesIPs(IPsFilePath)
	}
	fmt.Printf("导出实例公共IP地址完成，请查看文件 %s\n", IPsFilePath)
}

func batchListInstancesIp(filePath string, ac *AccountCtx) {
	_, err := os.Stat(filePath)
	if err != nil && os.IsNotExist(err) {
		os.Create(filePath)
	}
	fmt.Printf("正在导出实例公共IP地址...\n")
	ac.ListInstancesIPs(filePath)
	fmt.Printf("导出实例IP地址完成，请查看文件 %s\n", filePath)
}

func sendMessage(name, text string) (msg Message, err error) {
	if token != "" && chat_id != "" {
		data := url.Values{
			"parse_mode": {"Markdown"},
			"chat_id":    {chat_id},
			"text":       {"🔰*甲骨文通知* " + name + "\n" + text},
		}
		var req *http.Request
		req, err = http.NewRequest(http.MethodPost, sendMessageUrl, strings.NewReader(data.Encode()))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		client := common.BaseClient{HTTPClient: &http.Client{}}
		setProxyOrNot(&client)
		var resp *http.Response
		resp, err = client.HTTPClient.Do(req)
		if err != nil {
			return
		}
		var body []byte
		body, err = ioutil.ReadAll(resp.Body)
		if err != nil {
			return
		}
		err = json.Unmarshal(body, &msg)
		if err != nil {
			return
		}
		if !msg.OK {
			err = errors.New(msg.Description)
			return
		}
	}
	return
}

func editMessage(messageId int, name, text string) (msg Message, err error) {
	if token != "" && chat_id != "" {
		data := url.Values{
			"parse_mode": {"Markdown"},
			"chat_id":    {chat_id},
			"message_id": {strconv.Itoa(messageId)},
			"text":       {"🔰*甲骨文通知* " + name + "\n" + text},
		}
		var req *http.Request
		req, err = http.NewRequest(http.MethodPost, editMessageUrl, strings.NewReader(data.Encode()))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		client := common.BaseClient{HTTPClient: &http.Client{}}
		setProxyOrNot(&client)
		var resp *http.Response
		resp, err = client.HTTPClient.Do(req)
		if err != nil {
			return
		}
		var body []byte
		body, err = ioutil.ReadAll(resp.Body)
		if err != nil {
			return
		}
		err = json.Unmarshal(body, &msg)
		if err != nil {
			return
		}
		if !msg.OK {
			err = errors.New(msg.Description)
			return
		}

	}
	return
}

func setProxyOrNot(client *common.BaseClient) {
	if proxy != "" {
		proxyURL, err := url.Parse(proxy)
		if err != nil {
			printlnErr("URL parse failed", err.Error())
			return
		}
		client.HTTPClient = &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyURL(proxyURL),
			},
		}
	}
}

func getInstanceState(state core.InstanceLifecycleStateEnum) string {
	var friendlyState string
	switch state {
	case core.InstanceLifecycleStateMoving:
		friendlyState = "正在移动"
	case core.InstanceLifecycleStateProvisioning:
		friendlyState = "正在预配"
	case core.InstanceLifecycleStateRunning:
		friendlyState = "正在运行"
	case core.InstanceLifecycleStateStarting:
		friendlyState = "正在启动"
	case core.InstanceLifecycleStateStopping:
		friendlyState = "正在停止"
	case core.InstanceLifecycleStateStopped:
		friendlyState = "已停止　"
	case core.InstanceLifecycleStateTerminating:
		friendlyState = "正在终止"
	case core.InstanceLifecycleStateTerminated:
		friendlyState = "已终止　"
	default:
		friendlyState = string(state)
	}
	return friendlyState
}

func getBootVolumeState(state core.BootVolumeLifecycleStateEnum) string {
	var friendlyState string
	switch state {
	case core.BootVolumeLifecycleStateProvisioning:
		friendlyState = "正在预配"
	case core.BootVolumeLifecycleStateRestoring:
		friendlyState = "正在恢复"
	case core.BootVolumeLifecycleStateAvailable:
		friendlyState = "可用　　"
	case core.BootVolumeLifecycleStateTerminating:
		friendlyState = "正在终止"
	case core.BootVolumeLifecycleStateTerminated:
		friendlyState = "已终止　"
	case core.BootVolumeLifecycleStateFaulty:
		friendlyState = "故障　　"
	default:
		friendlyState = string(state)
	}
	return friendlyState
}

func fmtDuration(d time.Duration) string {
	if d.Seconds() < 1 {
		return "< 1 秒"
	}
	var buffer bytes.Buffer

	days := int(d / (time.Hour * 24))
	hours := int((d % (time.Hour * 24)).Hours())
	minutes := int((d % time.Hour).Minutes())
	seconds := int((d % time.Minute).Seconds())

	if days > 0 {
		buffer.WriteString(fmt.Sprintf("%d 天 ", days))
	}
	if hours > 0 {
		buffer.WriteString(fmt.Sprintf("%d 时 ", hours))
	}
	if minutes > 0 {
		buffer.WriteString(fmt.Sprintf("%d 分 ", minutes))
	}
	if seconds > 0 {
		buffer.WriteString(fmt.Sprintf("%d 秒", seconds))
	}
	return buffer.String()
}

func sleepRandomSecond(min, max int32) {
	var second int32
	if min <= 0 || max <= 0 {
		second = 1
	} else if min >= max {
		second = max
	} else {
		second = rand.Int31n(max-min) + min
	}
	printf("Sleep %d Second...\n", second)
	time.Sleep(time.Duration(second) * time.Second)
}

func printf(format string, a ...interface{}) {
	fmt.Printf("%s ", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Printf(format, a...)
}

func printlnErr(desc, detail string) {
	fmt.Printf("\033[1;31mError: %s. %s\033[0m\n", desc, detail)
}

// printlnErrAndPause 打印启动阶段的致命错误并退出。
// 双击运行时控制台窗口会随进程关闭，这里先等待回车，避免看不到错误原因。
func printlnErrAndPause(desc, detail string) {
	printlnErr(desc, detail)
	pauseIfDoubleClick()
	os.Exit(1)
}

func pauseIfDoubleClick() {
	if !isDoubleClickLaunch() {
		return
	}
	fmt.Print("\n按回车键退出...")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

// resolveConfigPath 未显式指定配置文件时，按 当前目录 → 可执行文件所在目录 的顺序查找。
func resolveConfigPath(p string) string {
	if p != defConfigFilePath {
		return p
	}
	if _, err := os.Stat(p); err == nil {
		return p
	}
	exe, err := os.Executable()
	if err != nil {
		return p
	}
	alt := filepath.Join(filepath.Dir(exe), defConfigFilePath)
	if _, err := os.Stat(alt); err == nil {
		return alt
	}
	return p
}

func getCustomRequestMetadataWithRetryPolicy() common.RequestMetadata {
	return common.RequestMetadata{
		RetryPolicy: getCustomRetryPolicy(),
	}
}

func getCustomRetryPolicy() *common.RetryPolicy {
	attempts := uint(3)
	retryOnAllNon200ResponseCodes := func(r common.OCIOperationResponse) bool {
		return !(r.Error == nil && 199 < r.Response.HTTPResponse().StatusCode && r.Response.HTTPResponse().StatusCode < 300)
	}
	policy := common.NewRetryPolicyWithOptions(
		common.WithConditionalOption(!false, common.ReplaceWithValuesFromRetryPolicy(common.DefaultRetryPolicyWithoutEventualConsistency())),
		common.WithMaximumNumberAttempts(attempts),
		common.WithShouldRetryOperation(retryOnAllNon200ResponseCodes))
	return &policy
}

func command(cmd string) {
	res := strings.Fields(cmd)
	if len(res) > 0 {
		fmt.Println("执行命令:", strings.Join(res, " "))
		name := res[0]
		arg := res[1:]
		out, err := exec.Command(name, arg...).CombinedOutput()
		if err == nil {
			fmt.Println(string(out))
		} else {
			fmt.Println(err)
		}
	}
}
