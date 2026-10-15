package oci

import (
	"errors"
	"fmt"
	"strings"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/core"
)

/* ------------------------------------------------------------------ 网络安全组（NSG） */

// NsgView 是网络安全组的展示视图。
type NsgView struct {
	ID             string `json:"id"`
	VcnID          string `json:"vcnId"`
	DisplayName    string `json:"displayName"`
	LifecycleState string `json:"lifecycleState"`
	TimeCreated    string `json:"timeCreated"`
}

// NsgRuleSpec 描述一条要新增/更新的 NSG 规则。
type NsgRuleSpec struct {
	Direction   string `json:"direction"` // ingress | egress
	Protocol    string `json:"protocol"`  // all | tcp | udp | icmp
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Description string `json:"description"`
	PortMin     int    `json:"portMin"`
	PortMax     int    `json:"portMax"`
	Stateless   bool   `json:"stateless"`
}

// NsgRuleView 是 NSG 规则的展示视图。
type NsgRuleView struct {
	ID          string `json:"id"`
	Direction   string `json:"direction"`
	Protocol    string `json:"protocol"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	PortMin     int    `json:"portMin"`
	PortMax     int    `json:"portMax"`
	Description string `json:"description"`
	Stateless   bool   `json:"stateless"`
}

// ListNsgs 列出 VCN 下的全部网络安全组。
func (ac *AccountCtx) ListNsgs(vcnID string) ([]NsgView, error) {
	out := []NsgView{}
	var page *string
	for {
		req := core.ListNetworkSecurityGroupsRequest{
			CompartmentId:   common.String(ac.Oracle.Tenancy),
			Limit:           common.Int(100),
			Page:            page,
			RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
		}
		if vcnID != "" {
			req.VcnId = common.String(vcnID)
		}
		resp, err := ac.networkClient.ListNetworkSecurityGroups(Ctx, req)
		if err != nil {
			return out, err
		}
		for i := range resp.Items {
			g := resp.Items[i]
			out = append(out, NsgView{
				ID:             S(g.Id),
				VcnID:          S(g.VcnId),
				DisplayName:    S(g.DisplayName),
				LifecycleState: string(g.LifecycleState),
				TimeCreated:    sdkTimeString(g.TimeCreated),
			})
		}
		if resp.OpcNextPage == nil || len(resp.Items) == 0 {
			break
		}
		page = resp.OpcNextPage
	}
	return out, nil
}

// ListNsgRules 列出 NSG 的全部规则。
func (ac *AccountCtx) ListNsgRules(nsgID string) ([]NsgRuleView, error) {
	out := []NsgRuleView{}
	var page *string
	for {
		resp, err := ac.networkClient.ListNetworkSecurityGroupSecurityRules(Ctx, core.ListNetworkSecurityGroupSecurityRulesRequest{
			NetworkSecurityGroupId: &nsgID,
			Limit:                  common.Int(100),
			Page:                   page,
			RequestMetadata:        getCustomRequestMetadataWithRetryPolicy(),
		})
		if err != nil {
			return out, err
		}
		for i := range resp.Items {
			r := resp.Items[i]
			v := NsgRuleView{
				ID:          S(r.Id),
				Direction:   strings.ToLower(string(r.Direction)),
				Protocol:    protocolName(S(r.Protocol)),
				Source:      S(r.Source),
				Destination: S(r.Destination),
				Description: S(r.Description),
				Stateless:   BVal(r.IsStateless),
			}
			v.PortMin, v.PortMax = portRange(r.Protocol, r.TcpOptions, r.UdpOptions)
			out = append(out, v)
		}
		if resp.OpcNextPage == nil || len(resp.Items) == 0 {
			break
		}
		page = resp.OpcNextPage
	}
	return out, nil
}

// CreateNsg 在 VCN 下创建一个网络安全组。
func (ac *AccountCtx) CreateNsg(vcnID, name string) (NsgView, error) {
	if vcnID == "" || strings.TrimSpace(name) == "" {
		return NsgView{}, errors.New("VCN 与名称不能为空")
	}
	resp, err := ac.networkClient.CreateNetworkSecurityGroup(Ctx, core.CreateNetworkSecurityGroupRequest{
		CreateNetworkSecurityGroupDetails: core.CreateNetworkSecurityGroupDetails{
			CompartmentId: common.String(ac.Oracle.Tenancy),
			VcnId:         common.String(vcnID),
			DisplayName:   common.String(name),
		},
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	if err != nil {
		return NsgView{}, fmt.Errorf("创建网络安全组失败: %w", err)
	}
	g := resp.NetworkSecurityGroup
	return NsgView{
		ID:             S(g.Id),
		VcnID:          S(g.VcnId),
		DisplayName:    S(g.DisplayName),
		LifecycleState: string(g.LifecycleState),
		TimeCreated:    sdkTimeString(g.TimeCreated),
	}, nil
}

// DeleteNsg 删除网络安全组。
func (ac *AccountCtx) DeleteNsg(nsgID string) error {
	if nsgID == "" {
		return errors.New("缺少 NSG ID")
	}
	if _, err := ac.networkClient.DeleteNetworkSecurityGroup(Ctx, core.DeleteNetworkSecurityGroupRequest{
		NetworkSecurityGroupId: &nsgID,
		RequestMetadata:        getCustomRequestMetadataWithRetryPolicy(),
	}); err != nil {
		return fmt.Errorf("删除网络安全组失败: %w", err)
	}
	return nil
}

// AddNsgRules 向 NSG 追加规则。
func (ac *AccountCtx) AddNsgRules(nsgID string, specs []NsgRuleSpec) error {
	if nsgID == "" {
		return errors.New("缺少 NSG ID")
	}
	if len(specs) == 0 {
		return errors.New("没有要添加的规则")
	}
	rules := make([]core.AddSecurityRuleDetails, 0, len(specs))
	for _, spec := range specs {
		r, err := BuildAddNsgRule(spec)
		if err != nil {
			return err
		}
		rules = append(rules, r)
	}
	_, err := ac.networkClient.AddNetworkSecurityGroupSecurityRules(Ctx, core.AddNetworkSecurityGroupSecurityRulesRequest{
		NetworkSecurityGroupId: &nsgID,
		AddNetworkSecurityGroupSecurityRulesDetails: core.AddNetworkSecurityGroupSecurityRulesDetails{
			SecurityRules: rules,
		},
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	if err != nil {
		return fmt.Errorf("新增 NSG 规则失败: %w", err)
	}
	return nil
}

// UpdateNsgRule 更新一条 NSG 规则（按规则 ID 整体替换）。
func (ac *AccountCtx) UpdateNsgRule(nsgID, ruleID string, spec NsgRuleSpec) error {
	if nsgID == "" || ruleID == "" {
		return errors.New("缺少 NSG 或规则 ID")
	}
	rule, err := buildUpdateNsgRule(ruleID, spec)
	if err != nil {
		return err
	}
	_, err = ac.networkClient.UpdateNetworkSecurityGroupSecurityRules(Ctx, core.UpdateNetworkSecurityGroupSecurityRulesRequest{
		NetworkSecurityGroupId: &nsgID,
		UpdateNetworkSecurityGroupSecurityRulesDetails: core.UpdateNetworkSecurityGroupSecurityRulesDetails{
			SecurityRules: []core.UpdateSecurityRuleDetails{rule},
		},
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	if err != nil {
		return fmt.Errorf("更新 NSG 规则失败: %w", err)
	}
	return nil
}

// RemoveNsgRules 按规则 ID 删除 NSG 规则。
func (ac *AccountCtx) RemoveNsgRules(nsgID string, ruleIDs []string) error {
	if nsgID == "" {
		return errors.New("缺少 NSG ID")
	}
	if len(ruleIDs) == 0 {
		return errors.New("没有要删除的规则")
	}
	_, err := ac.networkClient.RemoveNetworkSecurityGroupSecurityRules(Ctx, core.RemoveNetworkSecurityGroupSecurityRulesRequest{
		NetworkSecurityGroupId: &nsgID,
		RemoveNetworkSecurityGroupSecurityRulesDetails: core.RemoveNetworkSecurityGroupSecurityRulesDetails{
			SecurityRuleIds: ruleIDs,
		},
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	if err != nil {
		return fmt.Errorf("删除 NSG 规则失败: %w", err)
	}
	return nil
}

// ListNsgVnics 列出已绑定该 NSG 的 VNIC。
func (ac *AccountCtx) ListNsgVnics(nsgID string) ([]string, error) {
	resp, err := ac.networkClient.ListNetworkSecurityGroupVnics(Ctx, core.ListNetworkSecurityGroupVnicsRequest{
		NetworkSecurityGroupId: &nsgID,
		Limit:                  common.Int(100),
		RequestMetadata:        getCustomRequestMetadataWithRetryPolicy(),
	})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(resp.Items))
	for i := range resp.Items {
		if resp.Items[i].VnicId != nil {
			out = append(out, *resp.Items[i].VnicId)
		}
	}
	return out, nil
}

/* ------------------------------------------------------------------ 规则构造 */

func BuildAddNsgRule(spec NsgRuleSpec) (core.AddSecurityRuleDetails, error) {
	direction, isEgress, err := nsgDirection(spec.Direction)
	if err != nil {
		return core.AddSecurityRuleDetails{}, err
	}
	r := core.AddSecurityRuleDetails{
		Direction:   direction,
		Protocol:    common.String(protocolCode(spec.Protocol)),
		IsStateless: common.Bool(spec.Stateless),
	}
	if spec.Description != "" {
		r.Description = common.String(spec.Description)
	}
	if isEgress {
		r.Destination = common.String(spec.Destination)
		r.DestinationType = core.AddSecurityRuleDetailsDestinationTypeCidrBlock
	} else {
		r.Source = common.String(spec.Source)
		r.SourceType = core.AddSecurityRuleDetailsSourceTypeCidrBlock
	}
	if pr := nsgPortRange(spec); pr != nil {
		switch strings.ToLower(spec.Protocol) {
		case "tcp":
			r.TcpOptions = &core.TcpOptions{DestinationPortRange: pr}
		case "udp":
			r.UdpOptions = &core.UdpOptions{DestinationPortRange: pr}
		}
	}
	return r, nil
}

func buildUpdateNsgRule(ruleID string, spec NsgRuleSpec) (core.UpdateSecurityRuleDetails, error) {
	direction, isEgress, err := nsgDirection(spec.Direction)
	if err != nil {
		return core.UpdateSecurityRuleDetails{}, err
	}
	var dir core.UpdateSecurityRuleDetailsDirectionEnum = core.UpdateSecurityRuleDetailsDirectionIngress
	if direction == core.AddSecurityRuleDetailsDirectionEgress {
		dir = core.UpdateSecurityRuleDetailsDirectionEgress
	}
	r := core.UpdateSecurityRuleDetails{
		Id:          common.String(ruleID),
		Direction:   dir,
		Protocol:    common.String(protocolCode(spec.Protocol)),
		IsStateless: common.Bool(spec.Stateless),
	}
	if spec.Description != "" {
		r.Description = common.String(spec.Description)
	}
	if isEgress {
		r.Destination = common.String(spec.Destination)
		r.DestinationType = core.UpdateSecurityRuleDetailsDestinationTypeCidrBlock
	} else {
		r.Source = common.String(spec.Source)
		r.SourceType = core.UpdateSecurityRuleDetailsSourceTypeCidrBlock
	}
	if pr := nsgPortRange(spec); pr != nil {
		switch strings.ToLower(spec.Protocol) {
		case "tcp":
			r.TcpOptions = &core.TcpOptions{DestinationPortRange: pr}
		case "udp":
			r.UdpOptions = &core.UdpOptions{DestinationPortRange: pr}
		}
	}
	return r, nil
}

func nsgDirection(direction string) (core.AddSecurityRuleDetailsDirectionEnum, bool, error) {
	switch strings.ToLower(strings.TrimSpace(direction)) {
	case "egress", "out":
		return core.AddSecurityRuleDetailsDirectionEgress, true, nil
	case "ingress", "in", "":
		return core.AddSecurityRuleDetailsDirectionIngress, false, nil
	default:
		return "", false, fmt.Errorf("未知方向: %s", direction)
	}
}

func nsgPortRange(spec NsgRuleSpec) *core.PortRange {
	if spec.PortMin == 0 && spec.PortMax == 0 {
		return nil
	}
	return &core.PortRange{Min: common.Int(spec.PortMin), Max: common.Int(spec.PortMax)}
}
