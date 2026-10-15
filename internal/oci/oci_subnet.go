package oci

import (
	"errors"
	"fmt"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/core"
)

/* ------------------------------------------------------------------ 子网查询 */

// SubnetView 是子网的展示视图。
type SubnetView struct {
	ID                      string   `json:"id"`
	VcnID                   string   `json:"vcnId"`
	DisplayName             string   `json:"displayName"`
	CidrBlock               string   `json:"cidrBlock"`
	Ipv6CidrBlocks          []string `json:"ipv6CidrBlocks"`
	LifecycleState          string   `json:"lifecycleState"`
	AvailabilityDomain      string   `json:"availabilityDomain"`
	ProhibitInternetIngress bool     `json:"prohibitInternetIngress"`
	ProhibitPublicIpOnVnic  bool     `json:"prohibitPublicIpOnVnic"`
	SecurityListIds         []string `json:"securityListIds"`
	RouteTableID            string   `json:"routeTableId"`
	DnsLabel                string   `json:"dnsLabel"`
	VirtualRouterIP         string   `json:"virtualRouterIp"`
	TimeCreated             string   `json:"timeCreated"`
}

func subnetViewOf(sn core.Subnet) SubnetView {
	return SubnetView{
		ID:                      S(sn.Id),
		VcnID:                   S(sn.VcnId),
		DisplayName:             S(sn.DisplayName),
		CidrBlock:               S(sn.CidrBlock),
		Ipv6CidrBlocks:          sn.Ipv6CidrBlocks,
		LifecycleState:          string(sn.LifecycleState),
		AvailabilityDomain:      S(sn.AvailabilityDomain),
		ProhibitInternetIngress: BVal(sn.ProhibitInternetIngress),
		ProhibitPublicIpOnVnic:  BVal(sn.ProhibitPublicIpOnVnic),
		SecurityListIds:         sn.SecurityListIds,
		RouteTableID:            S(sn.RouteTableId),
		DnsLabel:                S(sn.DnsLabel),
		VirtualRouterIP:         S(sn.VirtualRouterIp),
		TimeCreated:             sdkTimeString(sn.TimeCreated),
	}
}

// ListSubnetViews 分页列出 VCN 下的全部子网。
func (ac *AccountCtx) ListSubnetViews(vcnID string) ([]SubnetView, error) {
	out := []SubnetView{}
	var page *string
	for {
		resp, err := ac.networkClient.ListSubnets(Ctx, core.ListSubnetsRequest{
			CompartmentId:   common.String(ac.Oracle.Tenancy),
			VcnId:           &vcnID,
			Limit:           common.Int(100),
			Page:            page,
			RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
		})
		if err != nil {
			return out, err
		}
		for i := range resp.Items {
			out = append(out, subnetViewOf(resp.Items[i]))
		}
		if resp.OpcNextPage == nil || len(resp.Items) == 0 {
			break
		}
		page = resp.OpcNextPage
	}
	return out, nil
}

/* ------------------------------------------------------------------ 子网创建 / 修改 / 删除 */

// SubnetCreateOptions 描述创建子网的参数。
type SubnetCreateOptions struct {
	VcnID                   string   `json:"vcnId"`
	DisplayName             string   `json:"displayName"`
	CidrBlock               string   `json:"cidrBlock"`
	DnsLabel                string   `json:"dnsLabel"`
	AvailabilityDomain      string   `json:"availabilityDomain"`
	ProhibitInternetIngress bool     `json:"prohibitInternetIngress"`
	RouteTableID            string   `json:"routeTableId"`
	SecurityListIds         []string `json:"securityListIds"`
}

// CreateSubnet 在 VCN 下创建一个子网。
func (ac *AccountCtx) CreateSubnet(opts SubnetCreateOptions) (SubnetView, error) {
	if opts.VcnID == "" || opts.CidrBlock == "" {
		return SubnetView{}, errors.New("VCN 与 CIDR 不能为空")
	}
	details := core.CreateSubnetDetails{
		CompartmentId:           common.String(ac.Oracle.Tenancy),
		VcnId:                   common.String(opts.VcnID),
		CidrBlock:               common.String(opts.CidrBlock),
		ProhibitInternetIngress: common.Bool(opts.ProhibitInternetIngress),
	}
	if opts.DisplayName != "" {
		details.DisplayName = common.String(opts.DisplayName)
	}
	if opts.DnsLabel != "" {
		details.DnsLabel = common.String(opts.DnsLabel)
	}
	if opts.AvailabilityDomain != "" {
		details.AvailabilityDomain = common.String(opts.AvailabilityDomain)
	}
	if opts.RouteTableID != "" {
		details.RouteTableId = common.String(opts.RouteTableID)
	}
	if len(opts.SecurityListIds) > 0 {
		details.SecurityListIds = opts.SecurityListIds
	}
	resp, err := ac.networkClient.CreateSubnet(Ctx, core.CreateSubnetRequest{
		CreateSubnetDetails: details,
		RequestMetadata:     getCustomRequestMetadataWithRetryPolicy(),
	})
	if err != nil {
		return SubnetView{}, fmt.Errorf("创建子网失败: %w", err)
	}
	return subnetViewOf(resp.Subnet), nil
}

// SubnetUpdateOptions 描述要修改的子网属性；未提供的字段保持原值。
type SubnetUpdateOptions struct {
	SubnetID        string   `json:"subnetId"`
	DisplayName     string   `json:"displayName"`
	RouteTableID    string   `json:"routeTableId"`
	SecurityListIds []string `json:"securityListIds"`
}

// UpdateSubnetAttrs 修改子网的名称 / 路由表 / 安全列表。
func (ac *AccountCtx) UpdateSubnetAttrs(opts SubnetUpdateOptions) error {
	if opts.SubnetID == "" {
		return errors.New("缺少子网 ID")
	}
	details := core.UpdateSubnetDetails{}
	if opts.DisplayName != "" {
		details.DisplayName = common.String(opts.DisplayName)
	}
	if opts.RouteTableID != "" {
		details.RouteTableId = common.String(opts.RouteTableID)
	}
	if opts.SecurityListIds != nil {
		details.SecurityListIds = opts.SecurityListIds
	}
	_, err := ac.networkClient.UpdateSubnet(Ctx, core.UpdateSubnetRequest{
		SubnetId:            &opts.SubnetID,
		UpdateSubnetDetails: details,
		RequestMetadata:     getCustomRequestMetadataWithRetryPolicy(),
	})
	if err != nil {
		return fmt.Errorf("修改子网失败: %w", err)
	}
	return nil
}

// DeleteSubnet 删除子网（要求子网内已无 VNIC 等资源）。
func (ac *AccountCtx) DeleteSubnet(subnetID string) error {
	if subnetID == "" {
		return errors.New("缺少子网 ID")
	}
	if _, err := ac.networkClient.DeleteSubnet(Ctx, core.DeleteSubnetRequest{
		SubnetId:        &subnetID,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}); err != nil {
		return fmt.Errorf("删除子网失败: %w", err)
	}
	return nil
}
