package oci

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/core"
)

func TestVcnCompartment(t *testing.T) {
	cases := []struct {
		name     string
		vcn      core.Vcn
		fallback string
		want     string
	}{
		{"优先用 VCN 自己的 compartment",
			core.Vcn{CompartmentId: common.String("ocid1.compartment.oc1..vcn")}, "tenancy",
			"ocid1.compartment.oc1..vcn"},
		{"VCN 未带 compartment 时回退",
			core.Vcn{}, "tenancy", "tenancy"},
		{"空串也回退",
			core.Vcn{CompartmentId: common.String("")}, "tenancy", "tenancy"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := vcnCompartment(c.vcn, c.fallback); got != c.want {
				t.Fatalf("vcnCompartment=%q want %q", got, c.want)
			}
		})
	}
}

// TestPickNatGateway 覆盖回归：只查 AVAILABLE 会漏掉 PROVISIONING/TERMINATING 的网关，
// 导致重复创建并撞上 "NAT gateway limit per VCN reached"。
func TestPickNatGateway(t *testing.T) {
	nat := func(id string, st core.NatGatewayLifecycleStateEnum) core.NatGateway {
		return core.NatGateway{Id: common.String(id), LifecycleState: st}
	}
	cases := []struct {
		name  string
		items []core.NatGateway
		want  string
		ok    bool
	}{
		{"空列表", nil, "", false},
		{"只有 TERMINATED 视为没有",
			[]core.NatGateway{nat("n-old", core.NatGatewayLifecycleStateTerminated)}, "", false},
		{"AVAILABLE 直接复用",
			[]core.NatGateway{nat("n1", core.NatGatewayLifecycleStateAvailable)}, "n1", true},
		{"PROVISIONING 也必须复用",
			[]core.NatGateway{nat("n2", core.NatGatewayLifecycleStateProvisioning)}, "n2", true},
		{"TERMINATING 也必须复用",
			[]core.NatGateway{nat("n3", core.NatGatewayLifecycleStateTerminating)}, "n3", true},
		{"多个时优先 AVAILABLE", []core.NatGateway{
			nat("n-p", core.NatGatewayLifecycleStateProvisioning),
			nat("n-a", core.NatGatewayLifecycleStateAvailable),
		}, "n-a", true},
		{"跳过 TERMINATED 选其它", []core.NatGateway{
			nat("n-old", core.NatGatewayLifecycleStateTerminated),
			nat("n-p", core.NatGatewayLifecycleStateProvisioning),
		}, "n-p", true},
		{"忽略空 ID",
			[]core.NatGateway{{LifecycleState: core.NatGatewayLifecycleStateAvailable}}, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := pickNatGateway(c.items)
			if got != c.want || ok != c.ok {
				t.Fatalf("pickNatGateway=%q,%v want %q,%v", got, ok, c.want, c.ok)
			}
		})
	}
}

func TestNatGatewayCounts(t *testing.T) {
	if natGatewayCounts(core.NatGatewayLifecycleStateTerminated) {
		t.Fatal("TERMINATED 不应占用上限")
	}
	for _, st := range []core.NatGatewayLifecycleStateEnum{
		core.NatGatewayLifecycleStateProvisioning,
		core.NatGatewayLifecycleStateAvailable,
		core.NatGatewayLifecycleStateTerminating,
	} {
		if !natGatewayCounts(st) {
			t.Fatalf("%s 应占用上限", st)
		}
	}
}

func TestIsLimitExceeded(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"LimitExceeded", fakeServiceError{400, "LimitExceeded", "NAT gateway limit per VCN reached"}, true},
		{"大小写不敏感", fakeServiceError{400, "limitexceeded", "x"}, true},
		{"包装后仍识别", fmt.Errorf("创建 NAT 网关失败: %w", fakeServiceError{400, "LimitExceeded", "x"}), true},
		{"其它错误码", fakeServiceError{400, "InvalidParameter", "x"}, false},
		{"网络错误", errors.New("dial tcp: refused"), false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isLimitExceeded(c.err); got != c.want {
				t.Fatalf("isLimitExceeded=%v want %v", got, c.want)
			}
		})
	}
}

func TestNatGatewayLimitHint(t *testing.T) {
	h := natGatewayLimitHint()
	for _, k := range []string{"NAT 网关", "限额", "compartment"} {
		if !strings.Contains(h, k) {
			t.Fatalf("提示应包含 %q，实际: %s", k, h)
		}
	}
}

// TestIsOurNlb 覆盖回归：清理 NLB 时必须只删本工具创建的那个，
// 否则会误删同 compartment 下用户自己的网络负载均衡器。
func TestIsOurNlb(t *testing.T) {
	subnets := map[string]bool{"subnet-1": true}
	cases := []struct {
		name        string
		displayName string
		subnetID    string
		want        bool
	}{
		{"本工具创建且在 VCN 子网内", "nlb-1792050906", "subnet-1", true},
		{"前缀对但不在本 VCN 子网", "nlb-1792050906", "subnet-other", false},
		{"子网为空", "nlb-1792050906", "", false},
		{"用户自己的 NLB（无前缀）", "my-load-balancer", "subnet-1", false},
		{"前缀对但后缀非数字", "nlb-prod", "subnet-1", false},
		{"只有前缀", "nlb-", "subnet-1", false},
		{"大小写不同", "NLB-123", "subnet-1", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isOurNlb(c.displayName, c.subnetID, subnets); got != c.want {
				t.Fatalf("isOurNlb(%q,%q)=%v want %v", c.displayName, c.subnetID, got, c.want)
			}
		})
	}
}

func TestSubnetIDSet(t *testing.T) {
	set := subnetIDSet([]core.Subnet{
		{Id: common.String("s1")},
		{Id: common.String("s2")},
		{},
	})
	if !set["s1"] || !set["s2"] || len(set) != 2 {
		t.Fatalf("subnetIDSet=%v", set)
	}
}
