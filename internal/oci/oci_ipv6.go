package oci

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/core"
)

/* ------------------------------------------------------------------ IPv6 */

// AttachIpv6 为实例附加 IPv6 地址：补齐 VCN/子网的 IPv6 CIDR、放行 IPv6 安全规则，
// 再在实例的主 VNIC 上创建 IPv6。返回分配到的 IPv6 地址。
func (ac *AccountCtx) AttachIpv6(instanceID string) (string, error) {
	vnic, err := ac.primaryVnic(instanceID)
	if err != nil {
		return "", err
	}
	vcn, err := ac.getVcnByInstance(instanceID)
	if err != nil {
		return "", err
	}

	// 1) VCN 添加 IPv6 CIDR（Oracle 分配的 GUA 前缀）
	if len(vcn.Ipv6CidrBlocks) == 0 {
		_, err = ac.networkClient.AddIpv6VcnCidr(Ctx, core.AddIpv6VcnCidrRequest{
			VcnId: vcn.Id,
			AddVcnIpv6CidrDetails: core.AddVcnIpv6CidrDetails{
				IsOracleGuaAllocationEnabled: common.Bool(true),
			},
			RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
		})
		if err != nil {
			return "", fmt.Errorf("为 VCN 添加 IPv6 CIDR 失败: %w", err)
		}
		// 重新读取以拿到分配到的前缀
		for i := 0; i < 30; i++ {
			time.Sleep(time.Second)
			if v, e := ac.getVcn(*vcn.Id); e == nil && len(v.Ipv6CidrBlocks) > 0 {
				vcn = v
				break
			}
		}
		if len(vcn.Ipv6CidrBlocks) == 0 {
			return "", errors.New("等待 VCN IPv6 CIDR 分配超时")
		}
	}
	v6Cidr := vcn.Ipv6CidrBlocks[0]
	subnetV6Cidr := v6Cidr
	if strings.HasSuffix(v6Cidr, "/56") {
		subnetV6Cidr = strings.TrimSuffix(v6Cidr, "/56") + "/64"
	}

	// 2) 子网添加 IPv6 CIDR
	subnets, err := ac.listSubnetsOfVcn(*vcn.Id)
	if err != nil {
		return "", fmt.Errorf("获取子网失败: %w", err)
	}
	for i := range subnets {
		if subnets[i].Ipv6CidrBlock != nil && *subnets[i].Ipv6CidrBlock != "" {
			continue
		}
		_, e := ac.networkClient.UpdateSubnet(Ctx, core.UpdateSubnetRequest{
			SubnetId: subnets[i].Id,
			UpdateSubnetDetails: core.UpdateSubnetDetails{
				Ipv6CidrBlock: common.String(subnetV6Cidr),
			},
			RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
		})
		if e != nil && !strings.Contains(strings.ToLower(e.Error()), "ula cidr") {
			ac.logf("为子网 %s 添加 IPv6 CIDR 失败: %v\n", S(subnets[i].DisplayName), e)
		}
	}

	// 3) 放行 IPv6 全部端口（入站/出站）
	if err := ac.addReleaseRules(*vcn.Id, "::/0", false); err != nil {
		ac.logf("放行 IPv6 安全规则失败(忽略): %v\n", err)
	}

	// 4) 在 VNIC 上创建 IPv6
	resp, err := ac.networkClient.CreateIpv6(Ctx, core.CreateIpv6Request{
		CreateIpv6Details: core.CreateIpv6Details{
			VnicId: vnic.Id,
		},
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	if err != nil {
		return "", fmt.Errorf("创建 IPv6 失败: %w", err)
	}
	return S(resp.Ipv6.IpAddress), nil
}
