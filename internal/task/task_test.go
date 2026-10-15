package task

import (
	"testing"

	"oci-help/internal/config"
	"oci-help/internal/store"
)

func TestTaskToInstance(t *testing.T) {
	amd := TaskToInstance(store.CreateTask{Architecture: "AMD", Disk: 47, OperationSystem: "Ubuntu", CreateNumbers: 1}, 2)
	if amd.Shape != "VM.Standard.E2.1.Micro" {
		t.Fatalf("AMD 规格错误: %s", amd.Shape)
	}
	if amd.Sum != 2 || amd.Retry != -1 {
		t.Fatalf("sum/retry 错误: %d/%d", amd.Sum, amd.Retry)
	}
	if amd.OperatingSystem != "Canonical Ubuntu" || amd.OperatingSystemVersion != "20.04" {
		t.Fatalf("OS 归一化错误: %s %s", amd.OperatingSystem, amd.OperatingSystemVersion)
	}

	arm := TaskToInstance(store.CreateTask{Architecture: "ARM", Ocpus: 4, Memory: 24, Disk: 100}, 1)
	if arm.Shape != "VM.Standard.A1.Flex" || arm.Ocpus != 4 || arm.MemoryInGBs != 24 {
		t.Fatalf("ARM 规格错误: %+v", arm)
	}
}

// TestTaskToInstanceRetryInterval 回归：任务配置的「重试间隔最小/最大」必须映射到
// RetryMinTime/RetryMaxTime，且成功连创间隔固定为 1 秒（不再被重试间隔配置影响）。
func TestTaskToInstanceRetryInterval(t *testing.T) {
	inst := TaskToInstance(store.CreateTask{Architecture: "AMD", RetryMin: 5, RetryMax: 30}, 1)
	if inst.MinTime != 1 || inst.MaxTime != 1 {
		t.Fatalf("成功连创间隔应固定为 1s, got %d/%d", inst.MinTime, inst.MaxTime)
	}
	if inst.RetryMinTime != 5 || inst.RetryMaxTime != 30 {
		t.Fatalf("RetryMinTime/RetryMaxTime = %d/%d, want 5/30", inst.RetryMinTime, inst.RetryMaxTime)
	}

	// 未配置时留 0，交由 oci.RetryDelay 回退到 MinTime/MaxTime（即 1s）。
	unset := TaskToInstance(store.CreateTask{Architecture: "AMD"}, 1)
	if unset.RetryMinTime != 0 || unset.RetryMaxTime != 0 {
		t.Fatalf("未配置时应为 0, got %d/%d", unset.RetryMinTime, unset.RetryMaxTime)
	}
}

func TestNormalizeOS(t *testing.T) {
	cases := []struct{ in, wantName, wantVer string }{
		{"Ubuntu", "Canonical Ubuntu", "20.04"},
		{"CentOS", "CentOS", "8"},
		{"Oracle Linux", "Oracle Linux", "8"},
		{"", "Canonical Ubuntu", "20.04"},
	}
	for _, c := range cases {
		n, v := NormalizeOS(c.in, "")
		if n != c.wantName || v != c.wantVer {
			t.Fatalf("NormalizeOS(%q)=%q,%q want %q,%q", c.in, n, v, c.wantName, c.wantVer)
		}
	}
}

// 编译期约束：确保模板类型不会在不经意间改变。
var _ config.Instance
