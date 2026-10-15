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

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/core"
)

func listBootVolumes() {
	// ListAllBootVolumes 内部已并发遍历全部可用性域；此处若再按 AD 循环调用，
	// 会放大成 N×N 次请求（N 为可用性域数量）。
	var bootVolumes []core.BootVolume
	volumes, err := active.ListAllBootVolumes()
	if err != nil {
		applog.Errf("获取引导卷失败", err.Error())
	} else {
		bootVolumes = volumes
	}

	fmt.Printf("\n\033[1;32m引导卷\033[0m \n(当前账号: %s)\n\n", active.Name)
	w := new(tabwriter.Writer)
	w.Init(os.Stdout, 4, 8, 1, '\t', 0)
	fmt.Fprintf(w, "%s\t%s\t%s\t%s\t\n", "序号", "名称", "状态", "大小(GB)")
	for i, volume := range bootVolumes {
		fmt.Fprintf(w, "%d\t%s\t%s\t%d\t\n", i+1, *volume.DisplayName, oci.BootVolumeStateName(volume.LifecycleState), *volume.SizeInGBs)
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
		bootVolume, err := active.GetBootVolume(bootVolumeId)
		if err != nil {
			fmt.Printf("\033[1;31m获取引导卷详细信息失败, 回车返回上一级菜单.\033[0m")
			fmt.Scanln()
			listBootVolumes()
			return
		}

		attachments, err := active.ListBootVolumeAttachments(bootVolume.AvailabilityDomain, bootVolume.CompartmentId, bootVolume.Id)
		attachIns := make([]string, 0)
		if err != nil {
			attachIns = append(attachIns, err.Error())
		} else {
			for _, attachment := range attachments {
				ins, err := active.GetInstance(attachment.InstanceId)
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
		fmt.Printf("状态: %s\n", oci.BootVolumeStateName(bootVolume.LifecycleState))
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
				_, err := active.UpdateBootVolume(bootVolume.Id, nil, common.Int64(10))
				if err != nil {
					fmt.Printf("\033[1;31m修改引导卷性能失败.\033[0m %s\n", err.Error())
				} else {
					fmt.Printf("\033[1;32m修改引导卷性能成功, 请稍后查看引导卷状态\033[0m\n")
				}
			} else if input == "2" {
				_, err := active.UpdateBootVolume(bootVolume.Id, nil, common.Int64(20))
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
				_, err := active.UpdateBootVolume(bootVolume.Id, &sizeInGBs, nil)
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
					_, err := active.DetachBootVolume(attachment.Id)
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
				_, err := active.DeleteBootVolume(bootVolume.Id)
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
