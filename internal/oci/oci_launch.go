package oci

import (
	"errors"
	"fmt"
	"io"
	"math"
	"oci-help/internal/config"
	"oci-help/internal/notify"
	"os"
	"strings"
	"time"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/core"
	"github.com/oracle/oci-go-sdk/v65/identity"
)

/* ------------------------------------------------------------------ 创建实例（含抢机重试） */

// 导出实例公共 IP 到文件
func (ac *AccountCtx) ListInstancesIPs(filePath string) {
	var vnicAttachments []core.VnicAttachment
	var vas []core.VnicAttachment
	var nextPage *string
	var err error
	for {
		vas, nextPage, err = ac.ListVnicAttachments(nil, nextPage)
		if err != nil {
			// 某页失败时立即停止，避免静默丢弃后续页；错误在下方统一上报。
			break
		}
		vnicAttachments = append(vnicAttachments, vas...)
		if nextPage == nil || len(vas) == 0 {
			break
		}
	}

	if err != nil {
		ac.logf("ListVnicAttachments Error: %s\n", err.Error())
		return
	}
	file, err := os.OpenFile(filePath, os.O_APPEND|os.O_WRONLY, os.ModeAppend)
	if err != nil {
		ac.logf("打开文件失败, Error: %s\n", err.Error())
		return
	}
	defer file.Close()
	_, err = io.WriteString(file, "["+ac.Name+"]\n")
	if err != nil {
		ac.logf("%s\n", err.Error())
	}
	for _, vnicAttachment := range vnicAttachments {
		vnic, err := ac.GetVnic(vnicAttachment.VnicId)
		if err != nil {
			ac.logf("IP地址获取失败, %s\n", err.Error())
			continue
		}
		if vnic.PublicIp == nil {
			continue
		}
		ac.logf("[%s] 实例: %s, IP: %s\n", ac.Name, S(vnic.DisplayName), *vnic.PublicIp)
		_, err = io.WriteString(file, "实例: "+S(vnic.DisplayName)+", IP: "+*vnic.PublicIp+"\n")
		if err != nil {
			ac.logf("写入文件失败, Error: %s\n", err.Error())
		}
	}
	_, err = io.WriteString(file, "\n")
	if err != nil {
		ac.logf("%s\n", err.Error())
	}
}

// LaunchInstances 按模板批量创建实例（含抢机重试逻辑）。
// 返回值 sum: 创建实例总数; num: 创建成功的个数。
func (ac *AccountCtx) LaunchInstances(ads []identity.AvailabilityDomain, inst config.Instance) (sum, num int32) {
	if ac.stopped() {
		ac.logf("任务已取消，未开始创建\n")
		return inst.Sum, 0
	}
	/* 创建实例的几种情况
	 * 1. 设置了 availabilityDomain 参数，即在设置的可用性域中创建 sum 个实例。
	 * 2. 没有设置 availabilityDomain 但是设置了 each 参数。即在获取的每个可用性域中创建 each 个实例，创建的实例总数 sum =  each * adCount。
	 * 3. 没有设置 availabilityDomain 且没有设置 each 参数，即在获取到的可用性域中创建的实例总数为 sum。
	 */

	var adCount int32 = int32(len(ads))
	adName := common.String(inst.AvailabilityDomain)
	each := inst.Each
	sum = inst.Sum

	var usableAds = make([]identity.AvailabilityDomain, 0)

	var AD_NOT_FIXED bool = false
	var EACH_AD = false
	if adName == nil || *adName == "" {
		AD_NOT_FIXED = true
		if each > 0 {
			EACH_AD = true
			sum = each * adCount
		} else {
			EACH_AD = false
			usableAds = ads
		}
	}

	// each 模式依赖可用性域数量（sum = each * adCount），为空时无法确定创建目标，明确报错。
	if EACH_AD && adCount == 0 {
		ac.logf("\033[1;31m[%s] 未获取到任何可用性域，无法按 each 模式创建实例\033[0m\n", ac.Name)
		return inst.Sum, 0
	}

	// 创建个数为 0/负数时直接返回，避免"任务成功但什么都没创建"。
	if sum <= 0 {
		ac.logf("\033[1;31m[%s] 创建个数为 %d，请检查模板的 sum/each 配置\033[0m\n", ac.Name, sum)
		return
	}

	name := inst.InstanceDisplayName
	if name == "" {
		name = time.Now().Format("instance-20060102-1504")
	}
	displayName := common.String(name)
	if sum > 1 {
		displayName = common.String(name + "-1")
	}

	request := core.LaunchInstanceRequest{}
	request.CompartmentId = common.String(ac.Oracle.Tenancy)
	request.DisplayName = displayName

	ac.logf("正在获取系统镜像...\n")
	image, err := ac.GetImage(inst)
	if err != nil {
		ac.logf("获取系统镜像失败: %s\n", err.Error())
		return
	}
	ac.logf("系统镜像: %s\n", S(image.DisplayName))

	var shape core.Shape
	if strings.Contains(strings.ToLower(inst.Shape), "flex") && inst.Ocpus > 0 && inst.MemoryInGBs > 0 {
		shape.Shape = &inst.Shape
		shape.Ocpus = &inst.Ocpus
		shape.MemoryInGBs = &inst.MemoryInGBs
	} else {
		ac.logf("正在获取Shape信息...\n")
		shape, err = ac.getShape(image.Id, inst.Shape)
		if err != nil {
			ac.logf("获取Shape信息失败: %s\n", err.Error())
			return
		}
	}

	request.Shape = shape.Shape
	// 安全读取规格字段（SDK 中均为可选指针），供后续日志与 Telegram 消息使用。
	shapeName := S(shape.Shape)
	shapeOcpusVal := F32(shape.Ocpus)
	shapeMemoryVal := F32(shape.MemoryInGBs)
	if strings.Contains(strings.ToLower(shapeName), "flex") {
		request.ShapeConfig = &core.LaunchInstanceShapeConfigDetails{
			Ocpus:       shape.Ocpus,
			MemoryInGBs: shape.MemoryInGBs,
		}
		if inst.Burstable == "1/8" {
			request.ShapeConfig.BaselineOcpuUtilization = core.LaunchInstanceShapeConfigDetailsBaselineOcpuUtilization8
		} else if inst.Burstable == "1/2" {
			request.ShapeConfig.BaselineOcpuUtilization = core.LaunchInstanceShapeConfigDetailsBaselineOcpuUtilization2
		}
	}

	ac.logf("正在获取子网...\n")
	subnet, err := ac.CreateOrGetNetworkInfrastructure(inst)
	if err != nil {
		ac.logf("获取子网失败: %s\n", err.Error())
		return
	}
	ac.logf("子网: %s\n", S(subnet.DisplayName))
	request.CreateVnicDetails = &core.CreateVnicDetails{SubnetId: subnet.Id}

	sd := core.InstanceSourceViaImageDetails{}
	sd.ImageId = image.Id
	if inst.BootVolumeSizeInGBs > 0 {
		sd.BootVolumeSizeInGBs = common.Int64(inst.BootVolumeSizeInGBs)
	}
	request.SourceDetails = sd
	request.IsPvEncryptionInTransitEnabled = common.Bool(true)

	metaData := map[string]string{}
	metaData["ssh_authorized_keys"] = inst.SSH_Public_Key
	if inst.CloudInit != "" {
		metaData["user_data"] = inst.CloudInit
	}
	request.Metadata = metaData

	minTime := inst.MinTime
	maxTime := inst.MaxTime
	retryMin := inst.RetryMinTime
	retryMax := inst.RetryMaxTime

	SKIP_RETRY_MAP := make(map[string]bool) // key 为可用性域名称，避免下标跨轮错位
	var usableAdsTemp = make([]identity.AvailabilityDomain, 0)

	retry := inst.Retry
	var failTimes int32 = 0
	// 连续命中限流（429）的次数，用于指数退避；成功创建后清零。
	var rateLimitStreak int32 = 0

	var runTimes int32 = 0

	var adIndex int32 = 0
	var pos int32 = 0
	var SUCCESS = false

	var startTime = time.Now()

	var bootVolumeSize float64
	if inst.BootVolumeSizeInGBs > 0 {
		bootVolumeSize = float64(inst.BootVolumeSizeInGBs)
	} else if image.SizeInMBs != nil {
		bootVolumeSize = math.Round(float64(*image.SizeInMBs) / float64(1024))
	}
	ac.logf("\033[1;36m[%s] 开始创建 %s 实例, OCPU: %g 内存: %g 引导卷: %g \033[0m\n", ac.Name, shapeName, shapeOcpusVal, shapeMemoryVal, bootVolumeSize)
	ac.progress("launch", 0, int(sum))
	if config.C.Each {
		text := fmt.Sprintf("正在尝试创建第 %d 个实例...⏳\n账号: %s\n区域: %s\n实例配置: %s\nOCPU计数: %g\n内存(GB): %g\n引导卷(GB): %g\n创建个数: %d", pos+1, ac.Name, ac.Oracle.Region, shapeName, shapeOcpusVal, shapeMemoryVal, bootVolumeSize, sum)
		_, err := notify.SendMessage("", text)
		if err != nil {
			ac.logf("Telegram 消息提醒发送失败: %s\n", err.Error())
		}
	}

	for pos < sum {

		// 每个实例开始前检查是否已被要求停止（Web 界面"停止任务"）。
		if ac.stopped() {
			ac.logf("\033[1;33m[%s] 任务已被中止，停止创建\033[0m\n", ac.Name)
			return
		}

		if AD_NOT_FIXED {
			if EACH_AD {
				// 每个可用性域创建 each 个：第 pos 个实例的目标域为第 pos/each 个域，
				// 当前目标域可重试失败时（failTimes>0）轮转到下一个域继续尝试。
				adName = ads[eachADIndex(pos, each, failTimes, adCount)].Name
			} else {
				if SUCCESS {
					adIndex = 0
				}
				if adIndex >= adCount {
					adIndex = 0
				}
				adName = usableAds[adIndex].Name
				adIndex++
			}
		}

		runTimes++
		ac.logf("\033[1;36m[%s] 正在尝试创建第 %d 个实例, AD: %s\033[0m\n", ac.Name, pos+1, S(adName))
		ac.logf("\033[1;36m[%s] 当前尝试次数: %d \033[0m\n", ac.Name, runTimes)
		request.AvailabilityDomain = adName
		createResp, err := ac.computeClient.LaunchInstance(Ctx, request)

		if err == nil {
			SUCCESS = true
			num++
			rateLimitStreak = 0

			duration := FormatDuration(time.Since(startTime))

			ac.logf("\033[1;32m[%s] 第 %d 个实例抢到了🎉, 正在启动中请稍等...⌛️ \033[0m\n", ac.Name, pos+1)
			var msg notify.Message
			var msgErr error
			var text string
			if config.C.Each {
				text = fmt.Sprintf("第 %d 个实例抢到了🎉, 正在启动中请稍等...⌛️\n账号: %s\n区域: %s\n实例名称: %s\n公共IP: 获取中...⏳\n可用性域:%s\n实例配置: %s\nOCPU计数: %g\n内存(GB): %g\n引导卷(GB): %g\n创建个数: %d\n尝试次数: %d\n耗时: %s", pos+1, ac.Name, ac.Oracle.Region, S(createResp.Instance.DisplayName), S(createResp.Instance.AvailabilityDomain), shapeName, shapeOcpusVal, shapeMemoryVal, bootVolumeSize, sum, runTimes, duration)
				msg, msgErr = notify.SendMessage("", text)
			}
			var strIps string
			ips, err := ac.getInstancePublicIps(createResp.Instance.Id)
			if err != nil {
				ac.logf("\033[1;32m[%s] 第 %d 个实例抢到了🎉, 但是启动失败❌ 错误信息: \033[0m%s\n", ac.Name, pos+1, err.Error())
				text = fmt.Sprintf("第 %d 个实例抢到了🎉, 但是启动失败❌实例已被终止😔\n账号: %s\n区域: %s\n实例名称: %s\n可用性域:%s\n实例配置: %s\nOCPU计数: %g\n内存(GB): %g\n引导卷(GB): %g\n创建个数: %d\n尝试次数: %d\n耗时: %s", pos+1, ac.Name, ac.Oracle.Region, S(createResp.Instance.DisplayName), S(createResp.Instance.AvailabilityDomain), shapeName, shapeOcpusVal, shapeMemoryVal, bootVolumeSize, sum, runTimes, duration)
			} else {
				strIps = strings.Join(ips, ",")
				ac.logf("\033[1;32m[%s] 第 %d 个实例抢到了🎉, 启动成功✅. 实例名称: %s, 公共IP: %s\033[0m\n", ac.Name, pos+1, S(createResp.Instance.DisplayName), strIps)
				text = fmt.Sprintf("第 %d 个实例抢到了🎉, 启动成功✅\n账号: %s\n区域: %s\n实例名称: %s\n公共IP: %s\n可用性域:%s\n实例配置: %s\nOCPU计数: %g\n内存(GB): %g\n引导卷(GB): %g\n创建个数: %d\n尝试次数: %d\n耗时: %s", pos+1, ac.Name, ac.Oracle.Region, S(createResp.Instance.DisplayName), strIps, S(createResp.Instance.AvailabilityDomain), shapeName, shapeOcpusVal, shapeMemoryVal, bootVolumeSize, sum, runTimes, duration)
			}
			if config.C.Each {
				if msgErr != nil {
					notify.SendMessage("", text)
				} else {
					notify.EditMessage(msg.MessageId, "", text)
				}
			}

			if !ac.sleepRandomSecond(minTime, maxTime) {
				ac.logf("\033[1;33m[%s] 任务已被中止，停止创建\033[0m\n", ac.Name)
				return
			}

			displayName = common.String(fmt.Sprintf("%s-%d", name, pos+1))
			request.DisplayName = displayName

		} else {
			SUCCESS = false
			SKIP_RETRY := false

			notRetryable, errInfo := classifyLaunchError(err)

			if notRetryable {
				// 不可重试
				duration := FormatDuration(time.Since(startTime))
				ac.logf("\033[1;31m[%s] 第 %d 个实例创建失败了❌, 错误信息: \033[0m%s\n", ac.Name, pos+1, errInfo)
				if config.C.Each {
					text := fmt.Sprintf("第 %d 个实例创建失败了❌\n错误信息: %s\n账号: %s\n区域: %s\n可用性域: %s\n实例配置: %s\nOCPU计数: %g\n内存(GB): %g\n引导卷(GB): %g\n创建个数: %d\n尝试次数: %d\n耗时:%s", pos+1, errInfo, ac.Name, ac.Oracle.Region, S(adName), shapeName, shapeOcpusVal, shapeMemoryVal, bootVolumeSize, sum, runTimes, duration)
					notify.SendMessage("", text)
				}

				SKIP_RETRY = true
				if AD_NOT_FIXED && !EACH_AD {
					SKIP_RETRY_MAP[S(adName)] = true
				}

			} else {
				// 可重试
				ac.logf("\033[1;31m[%s] 创建失败, Error: \033[0m%s\n", ac.Name, errInfo)

				SKIP_RETRY = false
				if AD_NOT_FIXED && !EACH_AD {
					SKIP_RETRY_MAP[S(adName)] = false
				}
			}

			// 失败重试等待：在任务配置的 [retryMin,retryMax] 内随机（未配置时回退 MinTime/MaxTime）；
			// 连续命中限流（429）时按 2 的幂指数退避，成功创建后清零。
			if isRateLimitError(err) {
				rateLimitStreak++
			} else {
				rateLimitStreak = 0
			}
			delay := applyBackoff(RetryDelay(retryMin, retryMax, minTime, maxTime), rateLimitStreak)
			if rateLimitStreak > 0 {
				ac.logf("\033[1;33m[%s] 命中限流(429)，连续第 %d 次，退避后等待 %d 秒\033[0m\n", ac.Name, rateLimitStreak, delay)
			}
			if !ac.sleepSeconds(delay) {
				ac.logf("\033[1;33m[%s] 任务已被中止，停止创建\033[0m\n", ac.Name)
				return
			}

			if AD_NOT_FIXED {
				if !EACH_AD {
					if adIndex < adCount {
						continue
					} else {
						failTimes++

						for _, ad := range usableAds {
							if !SKIP_RETRY_MAP[S(ad.Name)] {
								usableAdsTemp = append(usableAdsTemp, ad)
							}
						}

						usableAds = usableAdsTemp
						adCount = int32(len(usableAds))

						usableAdsTemp = nil
						for k := range SKIP_RETRY_MAP {
							delete(SKIP_RETRY_MAP, k)
						}

						if (retry < 0 || failTimes <= retry) && adCount > 0 {
							continue
						}
					}

					adIndex = 0

				} else {
					failTimes++
					if (retry < 0 || failTimes <= retry) && !SKIP_RETRY {
						continue
					}
				}

			} else {
				failTimes++
				if (retry < 0 || failTimes <= retry) && !SKIP_RETRY {
					continue
				}
			}
		}

		usableAds = ads
		adCount = int32(len(usableAds))
		usableAdsTemp = nil
		for k := range SKIP_RETRY_MAP {
			delete(SKIP_RETRY_MAP, k)
		}

		failTimes = 0
		runTimes = 0
		startTime = time.Now()

		pos++
		ac.progress("launch", int(pos), int(sum))

		if pos < sum && config.C.Each {
			text := fmt.Sprintf("正在尝试创建第 %d 个实例...⏳\n账号: %s\n区域: %s\n实例配置: %s\nOCPU计数: %g\n内存(GB): %g\n引导卷(GB): %g\n创建个数: %d", pos+1, ac.Name, ac.Oracle.Region, shapeName, shapeOcpusVal, shapeMemoryVal, bootVolumeSize, sum)
			notify.SendMessage("", text)
		}
	}
	return
}

/* ------------------------------------------------------------------ 抢机重试决策（可单测的纯逻辑） */

// classifyLaunchError 判断创建实例返回的错误是否属于"不可重试"，并返回用于展示的信息。
//
// 关键点：只有确实是 OCI 服务错误（common.ServiceError）时才可依据 HTTP 状态码判断；
// 网络类错误（代理断连、超时、DNS 失败等）的 IsServiceError 为 false 且 ServiceError
// 为 nil 接口，必须归入可重试分支，否则调用 GetHTTPStatusCode 会 nil panic。
func classifyLaunchError(err error) (notRetryable bool, msg string) {
	if err == nil {
		return false, ""
	}
	servErr, isServErr := asServiceError(err)
	if !isServErr {
		return false, err.Error()
	}
	msg = servErr.GetMessage()
	code := servErr.GetHTTPStatusCode()
	switch {
	case code >= 400 && code <= 405:
		return true, msg
	case code == 409 && !strings.EqualFold(servErr.GetCode(), "IncorrectState"):
		return true, msg
	case code == 412, code == 413, code == 422, code == 431, code == 501:
		return true, msg
	}
	return false, msg
}

// eachADIndex 返回 each 模式下第 pos 个实例的目标可用性域下标。
// 当前目标域可重试失败时（failTimes>0）轮转到后续域，避免 retry=-1 时死锁在容量耗尽的域上。
// 下标始终落在 [0, adCount) 内，调用方可安全用于 ads 索引。
func eachADIndex(pos, each, failTimes, adCount int32) int32 {
	if adCount <= 0 || each <= 0 {
		return 0
	}
	target := pos / each
	if failTimes > 0 {
		target += failTimes
	}
	idx := target % adCount
	if idx < 0 {
		idx += adCount
	}
	return idx
}

/* ------------------------------------------------------------------ 重试等待与限流退避（可单测的纯逻辑） */

// maxRetryBackoffSeconds 是限流指数退避的等待上限（秒）。
const maxRetryBackoffSeconds = 180

// asServiceError 从错误链中提取 OCI 服务错误。
// 用 errors.As 而非 common.IsServiceError：后者只做一次类型断言，
// 错误一旦被包装（fmt.Errorf %w 等）就会漏判，导致限流退避失效。
func asServiceError(err error) (common.ServiceError, bool) {
	if err == nil {
		return nil, false
	}
	var servErr common.ServiceError
	if errors.As(err, &servErr) {
		return servErr, true
	}
	return nil, false
}

// RetryDelay 返回一次失败重试的等待秒数：在 [min,max] 闭区间内随机。
// min<=0 时用 fallbackMin，max<=0 时用 fallbackMax；全部无效时退化为 1 秒。
func RetryDelay(min, max, fallbackMin, fallbackMax int32) int32 {
	if min <= 0 {
		min = fallbackMin
	}
	if max <= 0 {
		max = fallbackMax
	}
	return randomSecond(min, max)
}

// isRateLimitError 判断错误是否为限流。
// 依次匹配：HTTP 429、错误码含 TooManyRequests、错误文案含 "too many requests"
// （OCI 的限流文案形如 "Too many requests for the user"）。
func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	if servErr, ok := asServiceError(err); ok {
		if servErr.GetHTTPStatusCode() == 429 {
			return true
		}
		if strings.Contains(strings.ToLower(servErr.GetCode()), "toomanyrequest") {
			return true
		}
	}
	return strings.Contains(strings.ToLower(err.Error()), "too many requests")
}

// applyBackoff 对基础等待秒数做指数退避：连续第 streak 次限流时乘以 2^(streak-1)，
// 上限 maxRetryBackoffSeconds；streak<=1（非连续限流）时原样返回。
func applyBackoff(base int32, streak int32) int32 {
	if base <= 0 || streak <= 1 {
		return base
	}
	shift := streak - 1
	if shift > 16 {
		shift = 16
	}
	d := int64(base) << uint(shift)
	if d > maxRetryBackoffSeconds {
		d = maxRetryBackoffSeconds
	}
	return int32(d)
}
