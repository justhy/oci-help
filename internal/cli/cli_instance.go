package cli

import (
	"fmt"
	"oci-help/internal/applog"
	"oci-help/internal/oci"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/oracle/oci-go-sdk/v65/core"
)

func listInstances() {
	fmt.Println("正在获取实例数据...")
	instances, err := active.ListAllInstances()
	if err != nil {
		applog.Errf("获取失败, 回车返回上一级菜单.", err.Error())
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
	fmt.Fprintf(w, "%s\t%s\t%s\t%s\t\n", "序号", "名称", "状态", "配置")
	for i, ins := range instances {
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t\n", i+1, *ins.DisplayName, oci.InstanceStateName(ins.LifecycleState), *ins.Shape)
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
					_, err := active.InstanceAction(ins.Id, core.InstanceActionActionStart)
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
					_, err := active.InstanceAction(ins.Id, core.InstanceActionActionSoftstop)
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
					_, err := active.InstanceAction(ins.Id, core.InstanceActionActionSoftreset)
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
					err := active.TerminateInstance(ins.Id)
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
		instance, err := active.GetInstance(instanceId)
		if err != nil {
			fmt.Printf("\033[1;31m获取实例详细信息失败, 回车返回上一级菜单.\033[0m")
			fmt.Scanln()
			listInstances()
			return
		}
		vnics, err := active.GetInstanceVnics(instanceId)
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
		fmt.Printf("名称: %s\n", oci.S(instance.DisplayName))
		fmt.Printf("状态: %s\n", oci.InstanceStateName(instance.LifecycleState))
		fmt.Printf("公共IP: %s\n", strPublicIps)
		fmt.Printf("可用性域: %s\n", oci.S(instance.AvailabilityDomain))
		fmt.Printf("配置: %s\n", oci.S(instance.Shape))
		fmt.Printf("OCPU计数: %g\n", oci.ShapeOcpus(instance.ShapeConfig))
		fmt.Printf("网络带宽(Gbps): %g\n", oci.ShapeBandwidth(instance.ShapeConfig))
		fmt.Printf("内存(GB): %g\n\n", oci.ShapeMemoryInGBs(instance.ShapeConfig))
		fmt.Printf("Oracle Cloud Agent 插件配置情况\n")
		if instance.AgentConfig != nil {
			fmt.Printf("监控插件已禁用？: %t\n", oci.BVal(instance.AgentConfig.IsMonitoringDisabled))
			fmt.Printf("管理插件已禁用？: %t\n", oci.BVal(instance.AgentConfig.IsManagementDisabled))
			fmt.Printf("所有插件均已禁用？: %t\n", oci.BVal(instance.AgentConfig.AreAllPluginsDisabled))
			for _, value := range instance.AgentConfig.PluginsConfig {
				fmt.Printf("%s: %s\n", oci.S(value.Name), value.DesiredState)
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
			_, err := active.InstanceAction(instance.Id, core.InstanceActionActionStart)
			if err != nil {
				fmt.Printf("\033[1;31m启动实例失败.\033[0m %s\n", err.Error())
			} else {
				fmt.Printf("\033[1;32m正在启动实例, 请稍后查看实例状态\033[0m\n")
			}
			time.Sleep(1 * time.Second)

		case 2:
			_, err := active.InstanceAction(instance.Id, core.InstanceActionActionSoftstop)
			if err != nil {
				fmt.Printf("\033[1;31m停止实例失败.\033[0m %s\n", err.Error())
			} else {
				fmt.Printf("\033[1;32m正在停止实例, 请稍后查看实例状态\033[0m\n")
			}
			time.Sleep(1 * time.Second)

		case 3:
			_, err := active.InstanceAction(instance.Id, core.InstanceActionActionSoftreset)
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
				err := active.TerminateInstance(instance.Id)
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
				publicIp, err := active.ChangePublicIp(vnics)
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
			_, err := active.UpdateInstance(instance.Id, nil, &ocpus, &memoryInGBs, nil, nil)
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
			_, err := active.UpdateInstance(instance.Id, &input, nil, nil, nil, nil)
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
				_, err := active.UpdateInstance(instance.Id, nil, nil, nil, instance.AgentConfig.PluginsConfig, &disable)
				if err != nil {
					fmt.Printf("\033[1;31m启用管理和监控插件失败.\033[0m %s\n", err.Error())
				} else {
					fmt.Printf("\033[1;32m启用管理和监控插件成功.\033[0m\n")
				}
			} else if input == "2" {
				disable := true
				_, err := active.UpdateInstance(instance.Id, nil, nil, nil, instance.AgentConfig.PluginsConfig, &disable)
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
