package oci

import (
	"errors"
	"fmt"
	"strings"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/core"
)

/* ------------------------------------------------------------------ 安全列表 */
// SecurityRuleSpec 描述一条要新增的安全规则。
type SecurityRuleSpec struct {
	Direction   string `json:"direction"` // ingress | egress
	Protocol    string `json:"protocol"`  // all | tcp | udp | icmp
	Source      string `json:"source"`    // 入站源 CIDR
	Destination string `json:"destination"`
	Description string `json:"description"`
	PortMin     int    `json:"portMin"`
	PortMax     int    `json:"portMax"`
	Stateless   bool   `json:"stateless"`
}

// SecurityRuleView 是安全列表规则的展示视图。
type SecurityRuleView struct {
	Direction   string `json:"direction"`
	Protocol    string `json:"protocol"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	PortMin     int    `json:"portMin"`
	PortMax     int    `json:"portMax"`
	Description string `json:"description"`
	Stateless   bool   `json:"stateless"`
}

func (ac *AccountCtx) getSecurityList(vcnID string) (core.SecurityList, error) {
	vcn, err := ac.getVcn(vcnID)
	if err != nil {
		return core.SecurityList{}, err
	}
	if vcn.DefaultSecurityListId == nil {
		return core.SecurityList{}, errors.New("该 VCN 没有默认安全列表")
	}
	resp, err := ac.networkClient.GetSecurityList(Ctx, core.GetSecurityListRequest{
		SecurityListId:  vcn.DefaultSecurityListId,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	return resp.SecurityList, err
}

// ListSecurityRules 返回 VCN 默认安全列表中的入站与出站规则。
func (ac *AccountCtx) ListSecurityRules(vcnID string) (ingress []SecurityRuleView, egress []SecurityRuleView, err error) {
	sl, err := ac.getSecurityList(vcnID)
	if err != nil {
		return nil, nil, err
	}
	for i := range sl.IngressSecurityRules {
		r := sl.IngressSecurityRules[i]
		v := SecurityRuleView{
			Direction:   "ingress",
			Protocol:    protocolName(S(r.Protocol)),
			Source:      S(r.Source),
			Description: S(r.Description),
			Stateless:   BVal(r.IsStateless),
		}
		v.PortMin, v.PortMax = portRange(r.Protocol, r.TcpOptions, r.UdpOptions)
		ingress = append(ingress, v)
	}
	for i := range sl.EgressSecurityRules {
		r := sl.EgressSecurityRules[i]
		v := SecurityRuleView{
			Direction:   "egress",
			Protocol:    protocolName(S(r.Protocol)),
			Destination: S(r.Destination),
			Description: S(r.Description),
			Stateless:   BVal(r.IsStateless),
		}
		v.PortMin, v.PortMax = portRange(r.Protocol, r.TcpOptions, r.UdpOptions)
		egress = append(egress, v)
	}
	return ingress, egress, nil
}

func protocolName(p string) string {
	switch p {
	case "6":
		return "tcp"
	case "17":
		return "udp"
	case "1":
		return "icmp"
	case "all", "":
		return "all"
	default:
		return p
	}
}

func protocolCode(p string) string {
	switch strings.ToLower(p) {
	case "tcp":
		return "6"
	case "udp":
		return "17"
	case "icmp":
		return "1"
	case "all":
		return "all"
	default:
		return p
	}
}

func portRange(protocol *string, tcp *core.TcpOptions, udp *core.UdpOptions) (int, int) {
	p := S(protocol)
	if p == "6" && tcp != nil {
		if tcp.DestinationPortRange != nil {
			return iInt(tcp.DestinationPortRange.Min), iInt(tcp.DestinationPortRange.Max)
		}
		if tcp.SourcePortRange != nil {
			return iInt(tcp.SourcePortRange.Min), iInt(tcp.SourcePortRange.Max)
		}
	}
	if p == "17" && udp != nil {
		if udp.DestinationPortRange != nil {
			return iInt(udp.DestinationPortRange.Min), iInt(udp.DestinationPortRange.Max)
		}
		if udp.SourcePortRange != nil {
			return iInt(udp.SourcePortRange.Min), iInt(udp.SourcePortRange.Max)
		}
	}
	return 0, 0
}

func iInt(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

// buildIngress / buildEgress 把 SecurityRuleSpec 转成 SDK 规则对象。
func buildIngress(spec SecurityRuleSpec) core.IngressSecurityRule {
	r := core.IngressSecurityRule{
		Protocol:    common.String(protocolCode(spec.Protocol)),
		Source:      common.String(spec.Source),
		SourceType:  core.IngressSecurityRuleSourceTypeCidrBlock,
		IsStateless: common.Bool(spec.Stateless),
	}
	if spec.Description != "" {
		r.Description = common.String(spec.Description)
	}
	applyPorts(spec, func(p *core.PortRange) { r.TcpOptions = &core.TcpOptions{DestinationPortRange: p} },
		func(p *core.PortRange) { r.UdpOptions = &core.UdpOptions{DestinationPortRange: p} })
	return r
}

func buildEgress(spec SecurityRuleSpec) core.EgressSecurityRule {
	r := core.EgressSecurityRule{
		Protocol:        common.String(protocolCode(spec.Protocol)),
		Destination:     common.String(spec.Destination),
		DestinationType: core.EgressSecurityRuleDestinationTypeCidrBlock,
		IsStateless:     common.Bool(spec.Stateless),
	}
	if spec.Description != "" {
		r.Description = common.String(spec.Description)
	}
	applyPorts(spec, func(p *core.PortRange) { r.TcpOptions = &core.TcpOptions{DestinationPortRange: p} },
		func(p *core.PortRange) { r.UdpOptions = &core.UdpOptions{DestinationPortRange: p} })
	return r
}

func applyPorts(spec SecurityRuleSpec, setTCP, setUDP func(*core.PortRange)) {
	if spec.PortMin == 0 && spec.PortMax == 0 {
		return
	}
	pr := &core.PortRange{Min: common.Int(spec.PortMin), Max: common.Int(spec.PortMax)}
	switch strings.ToLower(spec.Protocol) {
	case "tcp":
		setTCP(pr)
	case "udp":
		setUDP(pr)
	}
}

// AddSecurityRules 在 VCN 默认安全列表中追加规则。
func (ac *AccountCtx) AddSecurityRules(vcnID string, specs []SecurityRuleSpec) error {
	sl, err := ac.getSecurityList(vcnID)
	if err != nil {
		return err
	}
	vcn, err := ac.getVcn(vcnID)
	if err != nil {
		return err
	}
	for _, spec := range specs {
		switch strings.ToLower(spec.Direction) {
		case "ingress", "in", "":
			sl.IngressSecurityRules = append(sl.IngressSecurityRules, buildIngress(spec))
		case "egress", "out":
			sl.EgressSecurityRules = append(sl.EgressSecurityRules, buildEgress(spec))
		default:
			return fmt.Errorf("未知方向: %s", spec.Direction)
		}
	}
	return ac.updateSecurityList(*vcn.DefaultSecurityListId, sl.IngressSecurityRules, sl.EgressSecurityRules)
}

// RemoveSecurityRules 按索引删除规则（索引来自 ListSecurityRules 的返回顺序）。
func (ac *AccountCtx) RemoveSecurityRules(vcnID string, ingressIdx, egressIdx []int) error {
	sl, err := ac.getSecurityList(vcnID)
	if err != nil {
		return err
	}
	vcn, err := ac.getVcn(vcnID)
	if err != nil {
		return err
	}
	ingress := filterRules(sl.IngressSecurityRules, ingressIdx)
	egress := filterEgressRules(sl.EgressSecurityRules, egressIdx)
	return ac.updateSecurityList(*vcn.DefaultSecurityListId, ingress, egress)
}

func filterRules(rules []core.IngressSecurityRule, remove []int) []core.IngressSecurityRule {
	if len(remove) == 0 {
		return rules
	}
	skip := map[int]bool{}
	for _, i := range remove {
		skip[i] = true
	}
	out := make([]core.IngressSecurityRule, 0, len(rules))
	for i := range rules {
		if !skip[i] {
			out = append(out, rules[i])
		}
	}
	return out
}

func filterEgressRules(rules []core.EgressSecurityRule, remove []int) []core.EgressSecurityRule {
	if len(remove) == 0 {
		return rules
	}
	skip := map[int]bool{}
	for _, i := range remove {
		skip[i] = true
	}
	out := make([]core.EgressSecurityRule, 0, len(rules))
	for i := range rules {
		if !skip[i] {
			out = append(out, rules[i])
		}
	}
	return out
}

func (ac *AccountCtx) updateSecurityList(listID string, ingress []core.IngressSecurityRule, egress []core.EgressSecurityRule) error {
	_, err := ac.networkClient.UpdateSecurityList(Ctx, core.UpdateSecurityListRequest{
		SecurityListId: &listID,
		UpdateSecurityListDetails: core.UpdateSecurityListDetails{
			IngressSecurityRules: ingress,
			EgressSecurityRules:  egress,
		},
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	return err
}

// addReleaseRules 向 VCN 默认安全列表追加「放行该 CIDR 全部协议」的入站/出站规则。
// ipv6Only=false 时使用 IPv4 (0.0.0.0/0)；否则使用传入的 IPv6 CIDR。
func (ac *AccountCtx) addReleaseRules(vcnID, cidr string, ipv6Only bool) error {
	sl, err := ac.getSecurityList(vcnID)
	if err != nil {
		return err
	}
	vcn, err := ac.getVcn(vcnID)
	if err != nil {
		return err
	}
	in := core.IngressSecurityRule{
		Protocol:   common.String("all"),
		Source:     common.String(cidr),
		SourceType: core.IngressSecurityRuleSourceTypeCidrBlock,
	}
	out := core.EgressSecurityRule{
		Protocol:        common.String("all"),
		Destination:     common.String(cidr),
		DestinationType: core.EgressSecurityRuleDestinationTypeCidrBlock,
	}
	sl.IngressSecurityRules = append(sl.IngressSecurityRules, in)
	sl.EgressSecurityRules = append(sl.EgressSecurityRules, out)
	return ac.updateSecurityList(*vcn.DefaultSecurityListId, sl.IngressSecurityRules, sl.EgressSecurityRules)
}

// ReleaseAllPorts 放行 VCN 默认安全列表中的全部端口与协议（IPv4 0.0.0.0/0，
// 若 VCN 已启用 IPv6 则同时放行 ::/0）。
func (ac *AccountCtx) ReleaseAllPorts(vcnID string) error {
	if err := ac.addReleaseRules(vcnID, "0.0.0.0/0", false); err != nil {
		return err
	}
	if vcn, err := ac.getVcn(vcnID); err == nil && len(vcn.Ipv6CidrBlocks) > 0 {
		if err := ac.addReleaseRules(vcnID, "::/0", true); err != nil {
			ac.logf("放行 IPv6 规则失败: %v\n", err)
		}
	}
	return nil
}
