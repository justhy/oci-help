package oci

import (
	"errors"
	"fmt"
	"time"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/core"
)

/* ------------------------------------------------------------------ VNIC 查询 */

// VnicView 是 VNIC 的展示视图。
type VnicView struct {
	ID                  string   `json:"id"`
	AttachmentID        string   `json:"attachmentId"`
	DisplayName         string   `json:"displayName"`
	IsPrimary           bool     `json:"isPrimary"`
	MacAddress          string   `json:"macAddress"`
	PrivateIP           string   `json:"privateIp"`
	PublicIP            string   `json:"publicIp"`
	SubnetID            string   `json:"subnetId"`
	NsgIds              []string `json:"nsgIds"`
	RouteTableID        string   `json:"routeTableId"`
	HostnameLabel       string   `json:"hostnameLabel"`
	SkipSourceDestCheck bool     `json:"skipSourceDestCheck"`
	Ipv6Addresses       []string `json:"ipv6Addresses"`
	State               string   `json:"state"`
}

// listAllVnicAttachments 分页拉取实例的全部 VNIC 挂载。
func (ac *AccountCtx) listAllVnicAttachments(instanceID string) ([]core.VnicAttachment, error) {
	out := []core.VnicAttachment{}
	var page *string
	for {
		items, next, err := ac.ListVnicAttachments(&instanceID, page)
		if err != nil {
			return out, err
		}
		out = append(out, items...)
		if next == nil || len(items) == 0 {
			break
		}
		page = next
	}
	return out, nil
}

// ListVnicViews 列出实例的全部 VNIC 及其属性。
func (ac *AccountCtx) ListVnicViews(instanceID string) ([]VnicView, error) {
	attachments, err := ac.listAllVnicAttachments(instanceID)
	if err != nil {
		return nil, err
	}
	out := make([]VnicView, 0, len(attachments))
	for i := range attachments {
		a := attachments[i]
		if a.VnicId == nil || *a.VnicId == "" {
			continue
		}
		v, err := ac.GetVnic(a.VnicId)
		if err != nil {
			continue
		}
		out = append(out, vnicViewOf(a, v))
	}
	return out, nil
}

func vnicViewOf(a core.VnicAttachment, v core.Vnic) VnicView {
	return VnicView{
		ID:                  S(v.Id),
		AttachmentID:        S(a.Id),
		DisplayName:         S(v.DisplayName),
		IsPrimary:           BVal(v.IsPrimary),
		MacAddress:          S(v.MacAddress),
		PrivateIP:           S(v.PrivateIp),
		PublicIP:            S(v.PublicIp),
		SubnetID:            S(v.SubnetId),
		NsgIds:              v.NsgIds,
		RouteTableID:        S(v.RouteTableId),
		HostnameLabel:       S(v.HostnameLabel),
		SkipSourceDestCheck: BVal(v.SkipSourceDestCheck),
		Ipv6Addresses:       v.Ipv6Addresses,
		State:               string(v.LifecycleState),
	}
}

/* ------------------------------------------------------------------ 辅助 VNIC 附加 / 分离 */

// AttachVnicOptions 描述附加一块辅助 VNIC 的参数。
type AttachVnicOptions struct {
	InstanceID     string
	SubnetID       string
	DisplayName    string
	AssignPublicIP bool
	NsgIds         []string
	HostnameLabel  string
	PrivateIP      string
}

// AttachSecondaryVnic 为实例附加一块辅助 VNIC，返回 (attachmentID, vnicID)。
func (ac *AccountCtx) AttachSecondaryVnic(opts AttachVnicOptions) (string, string, error) {
	if opts.InstanceID == "" || opts.SubnetID == "" {
		return "", "", errors.New("实例与子网不能为空")
	}
	vnicDetails := core.CreateVnicDetails{
		SubnetId:       common.String(opts.SubnetID),
		AssignPublicIp: common.Bool(opts.AssignPublicIP),
	}
	if opts.DisplayName != "" {
		vnicDetails.DisplayName = common.String(opts.DisplayName)
	}
	if len(opts.NsgIds) > 0 {
		vnicDetails.NsgIds = opts.NsgIds
	}
	if opts.HostnameLabel != "" {
		vnicDetails.HostnameLabel = common.String(opts.HostnameLabel)
	}
	if opts.PrivateIP != "" {
		vnicDetails.PrivateIp = common.String(opts.PrivateIP)
	}

	resp, err := ac.computeClient.AttachVnic(Ctx, core.AttachVnicRequest{
		AttachVnicDetails: core.AttachVnicDetails{
			InstanceId:        common.String(opts.InstanceID),
			CreateVnicDetails: &vnicDetails,
			DisplayName:       common.String("secondary-vnic"),
		},
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	if err != nil {
		return "", "", fmt.Errorf("附加 VNIC 失败: %w", err)
	}
	attachmentID := S(resp.VnicAttachment.Id)

	// 等待挂载完成，拿到 VNIC OCID
	for i := 0; i < 60; i++ {
		att, err := ac.computeClient.GetVnicAttachment(Ctx, core.GetVnicAttachmentRequest{
			VnicAttachmentId: &attachmentID,
			RequestMetadata:  getCustomRequestMetadataWithRetryPolicy(),
		})
		if err == nil {
			if att.VnicAttachment.LifecycleState == core.VnicAttachmentLifecycleStateAttached && att.VnicAttachment.VnicId != nil {
				return attachmentID, *att.VnicAttachment.VnicId, nil
			}
			if att.VnicAttachment.LifecycleState == core.VnicAttachmentLifecycleStateAttached {
				return attachmentID, "", nil
			}
		}
		time.Sleep(time.Second)
	}
	return attachmentID, S(resp.VnicAttachment.VnicId), nil
}

// DetachVnic 分离辅助 VNIC（会一并删除该 VNIC）。
func (ac *AccountCtx) DetachVnic(attachmentID string) error {
	if attachmentID == "" {
		return errors.New("缺少 VNIC 挂载 ID")
	}
	_, err := ac.computeClient.DetachVnic(Ctx, core.DetachVnicRequest{
		VnicAttachmentId: &attachmentID,
		RequestMetadata:  getCustomRequestMetadataWithRetryPolicy(),
	})
	if err != nil {
		return fmt.Errorf("分离 VNIC 失败: %w", err)
	}
	return nil
}

/* ------------------------------------------------------------------ 修改 VNIC 属性 */

// VnicUpdateOptions 描述要修改的 VNIC 属性；未提供的字段保持原值。
type VnicUpdateOptions struct {
	VnicID              string
	DisplayName         string
	HostnameLabel       string
	RouteTableID        string
	NsgIds              []string
	SkipSourceDestCheck *bool
}

// UpdateVnicAttrs 修改 VNIC 的名称 / 主机名 / 路由表 / NSG / 跳过源目的检查。
func (ac *AccountCtx) UpdateVnicAttrs(opts VnicUpdateOptions) (core.Vnic, error) {
	if opts.VnicID == "" {
		return core.Vnic{}, errors.New("缺少 VNIC ID")
	}
	details := core.UpdateVnicDetails{}
	if opts.DisplayName != "" {
		details.DisplayName = common.String(opts.DisplayName)
	}
	if opts.HostnameLabel != "" {
		details.HostnameLabel = common.String(opts.HostnameLabel)
	}
	if opts.RouteTableID != "" {
		details.RouteTableId = common.String(opts.RouteTableID)
	}
	if opts.NsgIds != nil {
		details.NsgIds = opts.NsgIds
	}
	if opts.SkipSourceDestCheck != nil {
		details.SkipSourceDestCheck = opts.SkipSourceDestCheck
	}
	resp, err := ac.networkClient.UpdateVnic(Ctx, core.UpdateVnicRequest{
		VnicId:            &opts.VnicID,
		UpdateVnicDetails: details,
		RequestMetadata:   getCustomRequestMetadataWithRetryPolicy(),
	})
	if err != nil {
		return core.Vnic{}, fmt.Errorf("修改 VNIC 失败: %w", err)
	}
	return resp.Vnic, nil
}

/* ------------------------------------------------------------------ 私有 IP */

// PrivateIpView 是私有 IP 的展示视图。
type PrivateIpView struct {
	ID            string `json:"id"`
	IPAddress     string `json:"ipAddress"`
	IsPrimary     bool   `json:"isPrimary"`
	HostnameLabel string `json:"hostnameLabel"`
	VnicID        string `json:"vnicId"`
	SubnetID      string `json:"subnetId"`
	Lifetime      string `json:"lifetime"`
	PublicIP      string `json:"publicIp"`
	PublicIpID    string `json:"publicIpId"`
}

// ListPrivateIpsOfVnic 列出 VNIC 上的全部私有 IP（含公网 IP 关联）。
func (ac *AccountCtx) ListPrivateIpsOfVnic(vnicID string) ([]PrivateIpView, error) {
	resp, err := ac.networkClient.ListPrivateIps(Ctx, core.ListPrivateIpsRequest{
		VnicId:          &vnicID,
		Limit:           common.Int(100),
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	if err != nil {
		return nil, err
	}
	out := make([]PrivateIpView, 0, len(resp.Items))
	for i := range resp.Items {
		ip := resp.Items[i]
		v := PrivateIpView{
			ID:            S(ip.Id),
			IPAddress:     S(ip.IpAddress),
			IsPrimary:     BVal(ip.IsPrimary),
			HostnameLabel: S(ip.HostnameLabel),
			VnicID:        S(ip.VnicId),
			SubnetID:      S(ip.SubnetId),
			Lifetime:      string(ip.Lifetime),
		}
		if ip.Id != nil {
			if pub, err := ac.getPublicIp(ip.Id); err == nil {
				v.PublicIP = S(pub.IpAddress)
				v.PublicIpID = S(pub.Id)
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// AddSecondaryPrivateIp 在 VNIC 上新增一个辅助私有 IP。
func (ac *AccountCtx) AddSecondaryPrivateIp(vnicID, ipAddress, hostnameLabel, displayName string) (PrivateIpView, error) {
	if vnicID == "" {
		return PrivateIpView{}, errors.New("缺少 VNIC ID")
	}
	details := core.CreatePrivateIpDetails{
		VnicId: common.String(vnicID),
	}
	if ipAddress != "" {
		details.IpAddress = common.String(ipAddress)
	}
	if hostnameLabel != "" {
		details.HostnameLabel = common.String(hostnameLabel)
	}
	if displayName != "" {
		details.DisplayName = common.String(displayName)
	}
	resp, err := ac.networkClient.CreatePrivateIp(Ctx, core.CreatePrivateIpRequest{
		CreatePrivateIpDetails: details,
		RequestMetadata:        getCustomRequestMetadataWithRetryPolicy(),
	})
	if err != nil {
		return PrivateIpView{}, fmt.Errorf("新增私有 IP 失败: %w", err)
	}
	ip := resp.PrivateIp
	return PrivateIpView{
		ID:            S(ip.Id),
		IPAddress:     S(ip.IpAddress),
		IsPrimary:     BVal(ip.IsPrimary),
		HostnameLabel: S(ip.HostnameLabel),
		VnicID:        S(ip.VnicId),
		SubnetID:      S(ip.SubnetId),
		Lifetime:      string(ip.Lifetime),
	}, nil
}

// DeletePrivateIp 删除辅助私有 IP（主私有 IP 不可删除）。
func (ac *AccountCtx) DeletePrivateIp(privateIpID string) error {
	if privateIpID == "" {
		return errors.New("缺少私有 IP ID")
	}
	_, err := ac.networkClient.DeletePrivateIp(Ctx, core.DeletePrivateIpRequest{
		PrivateIpId:     &privateIpID,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	if err != nil {
		return fmt.Errorf("删除私有 IP 失败: %w", err)
	}
	return nil
}

// UpdatePrivateIp 修改辅助私有 IP 的主机名，或迁移到同子网内的另一块 VNIC。
func (ac *AccountCtx) UpdatePrivateIp(privateIpID, hostnameLabel, targetVnicID string) error {
	if privateIpID == "" {
		return errors.New("缺少私有 IP ID")
	}
	details := core.UpdatePrivateIpDetails{}
	if hostnameLabel != "" {
		details.HostnameLabel = common.String(hostnameLabel)
	}
	if targetVnicID != "" {
		details.VnicId = common.String(targetVnicID)
	}
	_, err := ac.networkClient.UpdatePrivateIp(Ctx, core.UpdatePrivateIpRequest{
		PrivateIpId:            &privateIpID,
		UpdatePrivateIpDetails: details,
		RequestMetadata:        getCustomRequestMetadataWithRetryPolicy(),
	})
	if err != nil {
		return fmt.Errorf("修改私有 IP 失败: %w", err)
	}
	return nil
}

/* ------------------------------------------------------------------ 公网 IP */

// ReservePublicIp 为指定私有 IP 保留一个公网 IP（reserved 生命周期，可保留/迁移）。
func (ac *AccountCtx) ReservePublicIp(privateIpID string) (string, error) {
	if privateIpID == "" {
		return "", errors.New("缺少私有 IP ID")
	}
	resp, err := ac.networkClient.CreatePublicIp(Ctx, core.CreatePublicIpRequest{
		CreatePublicIpDetails: core.CreatePublicIpDetails{
			CompartmentId: common.String(ac.Oracle.Tenancy),
			Lifetime:      core.CreatePublicIpDetailsLifetimeReserved,
			PrivateIpId:   common.String(privateIpID),
		},
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	if err != nil {
		return "", fmt.Errorf("保留公网 IP 失败: %w", err)
	}
	return S(resp.PublicIp.IpAddress), nil
}

// ReleasePublicIp 释放公网 IP。
func (ac *AccountCtx) ReleasePublicIp(publicIpID string) error {
	if publicIpID == "" {
		return errors.New("缺少公网 IP ID")
	}
	if _, err := ac.deletePublicIp(&publicIpID); err != nil {
		return fmt.Errorf("释放公网 IP 失败: %w", err)
	}
	return nil
}
