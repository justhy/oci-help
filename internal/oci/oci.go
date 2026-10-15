package oci

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/core"
)

// Ctx 是所有 OCI API 调用的默认上下文（进程级，无取消需求）。
var Ctx = context.Background()

// SleepRandomSecond 在 min~max 秒之间随机等待（含边界），参数非法时退化为 1 秒。
func SleepRandomSecond(min, max int32) {
	var second int32
	if min <= 0 || max <= 0 {
		second = 1
	} else if min >= max {
		second = max
	} else {
		second = rand.Int31n(max-min) + min
	}
	time.Sleep(time.Duration(second) * time.Second)
}

/* ------------------------------------------------------------------ 指针安全取值 */

// S 安全读取 *string；nil 返回空串。
func S(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// F32 安全读取 *float32。
func F32(v *float32) float32 {
	if v == nil {
		return 0
	}
	return *v
}

// I64 安全读取 *int64。
func I64(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

// BVal 安全读取 *bool。
func BVal(v *bool) bool {
	return v != nil && *v
}

// ShapeOcpus / ShapeMemoryInGBs / ShapeBandwidth 安全读取实例规格。
// SDK 中 Instance.ShapeConfig 本身是可选的指针字段，可能为 nil。
func ShapeOcpus(sc *core.InstanceShapeConfig) float32 {
	if sc == nil {
		return 0
	}
	return F32(sc.Ocpus)
}

func ShapeMemoryInGBs(sc *core.InstanceShapeConfig) float32 {
	if sc == nil {
		return 0
	}
	return F32(sc.MemoryInGBs)
}

func ShapeBandwidth(sc *core.InstanceShapeConfig) float32 {
	if sc == nil {
		return 0
	}
	return F32(sc.NetworkingBandwidthInGbps)
}

/* ------------------------------------------------------------------ 状态名映射 */

// InstanceStateName 把实例生命周期状态映射为中文可读名称。
func InstanceStateName(state core.InstanceLifecycleStateEnum) string {
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
		friendlyState = "已停止"
	case core.InstanceLifecycleStateTerminating:
		friendlyState = "正在终止"
	case core.InstanceLifecycleStateTerminated:
		friendlyState = "已终止"
	default:
		friendlyState = string(state)
	}
	return friendlyState
}

// BootVolumeStateName 把引导卷生命周期状态映射为中文可读名称。
func BootVolumeStateName(state core.BootVolumeLifecycleStateEnum) string {
	var friendlyState string
	switch state {
	case core.BootVolumeLifecycleStateProvisioning:
		friendlyState = "正在预配"
	case core.BootVolumeLifecycleStateRestoring:
		friendlyState = "正在恢复"
	case core.BootVolumeLifecycleStateAvailable:
		friendlyState = "可用"
	case core.BootVolumeLifecycleStateTerminating:
		friendlyState = "正在终止"
	case core.BootVolumeLifecycleStateTerminated:
		friendlyState = "已终止"
	case core.BootVolumeLifecycleStateFaulty:
		friendlyState = "故障"
	default:
		friendlyState = string(state)
	}
	return friendlyState
}

// FormatDuration 把时长格式化为易读字符串。
func FormatDuration(d time.Duration) string {
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

/* ------------------------------------------------------------------ 重试策略 */

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
