package oci

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/core"
	"github.com/oracle/oci-go-sdk/v65/networkloadbalancer"
)

/* ------------------------------------------------------------------ 一键开启/关闭免费 AMD 实例下行 500Mbps */

// Enable500M 通过 NAT 网关 + 独立路由表 + 网络负载均衡器，并让实例 VNIC 走 NAT 路由表，
// 使免费 AMD 实例（VM.Standard.E2.1.Micro）获得约 500Mbps 下行。
// sshPort 用于 NLB 健康检查端口。返回 NLB 分配到的公网 IP。
func (ac *AccountCtx) Enable500M(instanceID string, sshPort int) (string, error) {
	instance, err := ac.GetInstance(&instanceID)
	if err != nil {
		return "", err
	}
	if !strings.Contains(S(instance.Shape), "E2.1.Micro") {
		return "", fmt.Errorf("该实例（%s）不支持一键开启下行 500Mbps，仅支持免费 AMD 实例 VM.Standard.E2.1.Micro", S(instance.Shape))
	}
	vcn, err := ac.getVcnByInstance(instanceID)
	if err != nil {
		return "", err
	}
	vnic, err := ac.primaryVnic(instanceID)
	if err != nil {
		return "", err
	}
	compartment := ac.Oracle.Tenancy

	// NAT 网关
	natList, err := ac.networkClient.ListNatGateways(Ctx, core.ListNatGatewaysRequest{
		CompartmentId:   common.String(compartment),
		VcnId:           vcn.Id,
		LifecycleState:  core.NatGatewayLifecycleStateAvailable,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	if err != nil {
		return "", fmt.Errorf("查询 NAT 网关失败: %w", err)
	}
	var natID string
	if len(natList.Items) > 0 {
		natID = S(natList.Items[0].Id)
	} else {
		resp, e := ac.networkClient.CreateNatGateway(Ctx, core.CreateNatGatewayRequest{
			CreateNatGatewayDetails: core.CreateNatGatewayDetails{
				CompartmentId: common.String(compartment),
				VcnId:         vcn.Id,
				DisplayName:   common.String("nat-gateway"),
			},
			RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
		})
		if e != nil {
			return "", fmt.Errorf("创建 NAT 网关失败: %w", e)
		}
		natID = S(resp.NatGateway.Id)
		if err := ac.waitNatGatewayAvailable(natID); err != nil {
			return "", err
		}
	}

	// 路由表：找一条 0.0.0.0/0 → NAT 的，否则新建
	rtList, err := ac.networkClient.ListRouteTables(Ctx, core.ListRouteTablesRequest{
		CompartmentId:   common.String(compartment),
		VcnId:           vcn.Id,
		LifecycleState:  core.RouteTableLifecycleStateAvailable,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	if err != nil {
		return "", fmt.Errorf("查询路由表失败: %w", err)
	}
	var natRT *core.RouteTable
	for i := range rtList.Items {
		for _, rule := range rtList.Items[i].RouteRules {
			if routeDest(&rule) == "0.0.0.0/0" && S(rule.NetworkEntityId) == natID {
				natRT = &rtList.Items[i]
				break
			}
		}
		if natRT != nil {
			break
		}
	}

	// 检查是否已有其他 AMD 实例占用 NAT 路由表
	if natRT != nil {
		instances, _ := ac.ListAllInstances()
		for i := range instances {
			if !strings.Contains(S(instances[i].Shape), "E2.1.Micro") || S(instances[i].Id) == instanceID {
				continue
			}
			if v, e := ac.primaryVnic(S(instances[i].Id)); e == nil && S(v.RouteTableId) == S(natRT.Id) {
				return "", errors.New("已有其他免费 AMD 实例绑定该 NAT 路由表，请先关闭其 500Mbps")
			}
		}
	}

	if natRT == nil {
		resp, e := ac.networkClient.CreateRouteTable(Ctx, core.CreateRouteTableRequest{
			CreateRouteTableDetails: core.CreateRouteTableDetails{
				CompartmentId: common.String(compartment),
				VcnId:         vcn.Id,
				DisplayName:   common.String("nat-route"),
				RouteRules: []core.RouteRule{{
					Destination:     common.String("0.0.0.0/0"),
					DestinationType: core.RouteRuleDestinationTypeCidrBlock,
					NetworkEntityId: common.String(natID),
				}},
			},
			RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
		})
		if e != nil {
			return "", fmt.Errorf("创建 NAT 路由表失败: %w", e)
		}
		natRT = &resp.RouteTable
		if err := ac.waitRouteTableAvailable(S(natRT.Id)); err != nil {
			return "", err
		}
	}

	// 子网（取第一个）
	subnets, err := ac.listSubnetsOfVcn(S(vcn.Id))
	if err != nil || len(subnets) == 0 {
		return "", fmt.Errorf("未找到可用子网: %w", err)
	}
	subnetID := S(subnets[0].Id)

	// 删除已存在的 NLB
	if nlbs, e := ac.nlbClient.ListNetworkLoadBalancers(Ctx, networkloadbalancer.ListNetworkLoadBalancersRequest{
		CompartmentId:   common.String(compartment),
		LifecycleState:  networkloadbalancer.ListNetworkLoadBalancersLifecycleStateActive,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}); e == nil {
		for _, n := range nlbs.Items {
			if n.Id != nil {
				_, _ = ac.nlbClient.DeleteNetworkLoadBalancer(Ctx, networkloadbalancer.DeleteNetworkLoadBalancerRequest{
					NetworkLoadBalancerId: n.Id,
					RequestMetadata:       getCustomRequestMetadataWithRetryPolicy(),
				})
			}
		}
	}

	// 实例主私网 IP（作为 NLB 后端）
	privateIP := ""
	if ips, e := ac.getPrivateIps(vnic.Id); e == nil && len(ips) > 0 {
		privateIP = S(ips[0].IpAddress)
	}
	if privateIP == "" {
		return "", errors.New("无法获取实例私网 IP")
	}

	// 创建 NLB
	nlb, err := ac.createNLB(compartment, subnetID, privateIP, instanceID, sshPort)
	if err != nil {
		return "", err
	}
	publicIP := ""
	for _, ip := range nlb.IpAddresses {
		if ip.IpAddress != nil && !isPrivateIP(*ip.IpAddress) {
			publicIP = *ip.IpAddress
		}
	}

	// 实例 VNIC 绑定 NAT 路由表 + 跳过源/目的检查
	_, err = ac.networkClient.UpdateVnic(Ctx, core.UpdateVnicRequest{
		VnicId: vnic.Id,
		UpdateVnicDetails: core.UpdateVnicDetails{
			RouteTableId:        natRT.Id,
			SkipSourceDestCheck: common.Bool(true),
		},
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	if err != nil {
		return "", fmt.Errorf("绑定实例 VNIC 路由表失败: %w", err)
	}

	// 放行所有端口
	if err := ac.ReleaseAllPorts(S(vcn.Id)); err != nil {
		ac.logf("放行安全列表失败(忽略): %v\n", err)
	}
	ac.logf("实例 %s 已开启下行 500Mbps，公网IP: %s\n", S(instance.DisplayName), publicIP)
	return publicIP, nil
}

// Disable500M 关闭下行 500Mbps：恢复 VNIC 到 VCN 默认路由表，并按需删除 NAT 网关、NLB 与 NAT 路由表。
func (ac *AccountCtx) Disable500M(instanceID string, retainNatGw, retainNlb bool) error {
	instance, err := ac.GetInstance(&instanceID)
	if err != nil {
		return err
	}
	if !strings.Contains(S(instance.Shape), "E2.1.Micro") {
		return fmt.Errorf("该实例（%s）不是免费 AMD 实例", S(instance.Shape))
	}
	vcn, err := ac.getVcnByInstance(instanceID)
	if err != nil {
		return err
	}
	vnic, err := ac.primaryVnic(instanceID)
	if err != nil {
		return err
	}
	compartment := ac.Oracle.Tenancy

	rtList, err := ac.networkClient.ListRouteTables(Ctx, core.ListRouteTablesRequest{
		CompartmentId:   common.String(compartment),
		VcnId:           vcn.Id,
		LifecycleState:  core.RouteTableLifecycleStateAvailable,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	if err != nil {
		return fmt.Errorf("查询路由表失败: %w", err)
	}
	natList, err := ac.networkClient.ListNatGateways(Ctx, core.ListNatGatewaysRequest{
		CompartmentId:   common.String(compartment),
		VcnId:           vcn.Id,
		LifecycleState:  core.NatGatewayLifecycleStateAvailable,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	if err != nil {
		return fmt.Errorf("查询 NAT 网关失败: %w", err)
	}

	// 找出 NAT 路由表并从候选路由表中剔除
	removable := make(map[string]bool)
	for i := range rtList.Items {
		for _, rule := range rtList.Items[i].RouteRules {
			if routeDest(&rule) != "0.0.0.0/0" {
				continue
			}
			for _, nat := range natList.Items {
				if S(rule.NetworkEntityId) == S(nat.Id) {
					removable[S(rtList.Items[i].Id)] = true
				}
			}
		}
	}

	// 选一条保留路由表（优先默认路由表，否则第一条非 NAT 路由表）
	var keepRT *core.RouteTable
	for i := range rtList.Items {
		if S(rtList.Items[i].Id) == S(vcn.DefaultRouteTableId) {
			keepRT = &rtList.Items[i]
			break
		}
	}
	if keepRT == nil {
		for i := range rtList.Items {
			if !removable[S(rtList.Items[i].Id)] {
				keepRT = &rtList.Items[i]
				break
			}
		}
	}
	if keepRT == nil {
		return errors.New("没有可恢复的路由表")
	}

	// 恢复 VNIC 路由表
	if _, err := ac.networkClient.UpdateVnic(Ctx, core.UpdateVnicRequest{
		VnicId: vnic.Id,
		UpdateVnicDetails: core.UpdateVnicDetails{
			RouteTableId: keepRT.Id,
		},
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}); err != nil {
		return fmt.Errorf("恢复 VNIC 路由表失败: %w", err)
	}

	// 清空并删除 NAT 路由表
	for id := range removable {
		if id == S(keepRT.Id) {
			continue
		}
		if !retainNatGw {
			_, _ = ac.networkClient.UpdateRouteTable(Ctx, core.UpdateRouteTableRequest{
				RtId:                    &id,
				UpdateRouteTableDetails: core.UpdateRouteTableDetails{RouteRules: []core.RouteRule{}},
				RequestMetadata:         getCustomRequestMetadataWithRetryPolicy(),
			})
		}
	}

	if !retainNatGw {
		for _, nat := range natList.Items {
			_, _ = ac.networkClient.DeleteNatGateway(Ctx, core.DeleteNatGatewayRequest{
				NatGatewayId:    nat.Id,
				RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
			})
		}
		for id := range removable {
			if id == S(keepRT.Id) {
				continue
			}
			_, _ = ac.networkClient.DeleteRouteTable(Ctx, core.DeleteRouteTableRequest{
				RtId:            &id,
				RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
			})
		}
	}

	if !retainNlb {
		if nlbs, e := ac.nlbClient.ListNetworkLoadBalancers(Ctx, networkloadbalancer.ListNetworkLoadBalancersRequest{
			CompartmentId:   common.String(compartment),
			LifecycleState:  networkloadbalancer.ListNetworkLoadBalancersLifecycleStateActive,
			RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
		}); e == nil {
			for _, n := range nlbs.Items {
				if n.Id != nil {
					_, _ = ac.nlbClient.DeleteNetworkLoadBalancer(Ctx, networkloadbalancer.DeleteNetworkLoadBalancerRequest{
						NetworkLoadBalancerId: n.Id,
						RequestMetadata:       getCustomRequestMetadataWithRetryPolicy(),
					})
				}
			}
		}
	}
	return nil
}

func (ac *AccountCtx) createNLB(compartment, subnetID, privateIP, instanceID string, sshPort int) (networkloadbalancer.NetworkLoadBalancer, error) {
	if sshPort <= 0 {
		sshPort = 22
	}
	details := networkloadbalancer.CreateNetworkLoadBalancerDetails{
		DisplayName:   common.String(fmt.Sprintf("nlb-%d", time.Now().Unix())),
		CompartmentId: common.String(compartment),
		IsPrivate:     common.Bool(false),
		SubnetId:      common.String(subnetID),
		Listeners: map[string]networkloadbalancer.ListenerDetails{
			"listener1": {
				Name:                  common.String("listener1"),
				DefaultBackendSetName: common.String("backend1"),
				Protocol:              networkloadbalancer.ListenerProtocolsTcpAndUdp,
				Port:                  common.Int(0),
			},
		},
		BackendSets: map[string]networkloadbalancer.BackendSetDetails{
			"backend1": {
				IsPreserveSource: common.Bool(true),
				IsFailOpen:       common.Bool(true),
				Policy:           networkloadbalancer.NetworkLoadBalancingPolicyTwoTuple,
				HealthChecker: &networkloadbalancer.HealthChecker{
					Protocol: networkloadbalancer.HealthCheckProtocolsTcp,
					Port:     common.Int(sshPort),
				},
				Backends: []networkloadbalancer.Backend{{
					TargetId:  common.String(instanceID),
					IpAddress: common.String(privateIP),
					Port:      common.Int(0),
					Weight:    common.Int(1),
				}},
			},
		},
	}

	var nlb networkloadbalancer.NetworkLoadBalancer
	var lastErr error
	for attempt := 1; attempt <= 10; attempt++ {
		resp, err := ac.nlbClient.CreateNetworkLoadBalancer(Ctx, networkloadbalancer.CreateNetworkLoadBalancerRequest{
			CreateNetworkLoadBalancerDetails: details,
			RequestMetadata:                  getCustomRequestMetadataWithRetryPolicy(),
		})
		if err == nil {
			nlb = resp.NetworkLoadBalancer
			lastErr = nil
			break
		}
		lastErr = err
		time.Sleep(15 * time.Second)
	}
	if lastErr != nil {
		return networkloadbalancer.NetworkLoadBalancer{}, fmt.Errorf("创建网络负载均衡器失败: %w", lastErr)
	}
	if nlb.Id == nil {
		return networkloadbalancer.NetworkLoadBalancer{}, errors.New("创建网络负载均衡器未返回 ID")
	}
	// 等待 Active 并取回公网 IP
	for i := 0; i < 120; i++ {
		resp, err := ac.nlbClient.GetNetworkLoadBalancer(Ctx, networkloadbalancer.GetNetworkLoadBalancerRequest{
			NetworkLoadBalancerId: nlb.Id,
			RequestMetadata:       getCustomRequestMetadataWithRetryPolicy(),
		})
		if err == nil && resp.NetworkLoadBalancer.LifecycleState == networkloadbalancer.LifecycleStateActive {
			return resp.NetworkLoadBalancer, nil
		}
		time.Sleep(time.Second)
	}
	return nlb, nil
}

func (ac *AccountCtx) waitNatGatewayAvailable(id string) error {
	for i := 0; i < 60; i++ {
		resp, err := ac.networkClient.GetNatGateway(Ctx, core.GetNatGatewayRequest{
			NatGatewayId:    &id,
			RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
		})
		if err == nil && resp.NatGateway.LifecycleState == core.NatGatewayLifecycleStateAvailable {
			return nil
		}
		time.Sleep(time.Second)
	}
	return errors.New("等待 NAT 网关可用超时")
}

func (ac *AccountCtx) waitRouteTableAvailable(id string) error {
	for i := 0; i < 60; i++ {
		resp, err := ac.networkClient.GetRouteTable(Ctx, core.GetRouteTableRequest{
			RtId:            &id,
			RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
		})
		if err == nil && resp.RouteTable.LifecycleState == core.RouteTableLifecycleStateAvailable {
			return nil
		}
		time.Sleep(time.Second)
	}
	return errors.New("等待路由表可用超时")
}

// routeDest 兼容 destination 与已废弃的 cidrBlock 两种字段。
func routeDest(r *core.RouteRule) string {
	if r == nil {
		return ""
	}
	if r.Destination != nil {
		return *r.Destination
	}
	return S(r.CidrBlock)
}

// isPrivateIP 判断是否为私有/保留地址，用于从 NLB 地址中挑选公网 IP。
func isPrivateIP(ip string) bool {
	if ip == "" {
		return true
	}
	if strings.Contains(ip, ":") {
		return false
	}
	parts := strings.Split(ip, ".")
	if len(parts) != 4 {
		return true
	}
	first := parts[0]
	switch first {
	case "10", "127", "0":
		return true
	case "192":
		return parts[1] == "168"
	case "172":
		return parts[1] >= "16" && parts[1] <= "31"
	case "169":
		return parts[1] == "254"
	}
	return false
}
