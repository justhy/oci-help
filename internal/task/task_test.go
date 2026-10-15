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
