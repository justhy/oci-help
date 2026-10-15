package oci

import (
	"errors"
	"time"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/core"
)

/* ------------------------------------------------------------------ 公共 IP */

func (ac *AccountCtx) ChangePublicIp(vnics []core.Vnic) (publicIp core.PublicIp, err error) {
	var vnic core.Vnic
	foundPrimary := false
	for _, v := range vnics {
		if BVal(v.IsPrimary) {
			vnic = v
			foundPrimary = true
		}
	}
	if !foundPrimary {
		return publicIp, errors.New("未找到主 VNIC")
	}
	if vnic.Id == nil {
		return publicIp, errors.New("主 VNIC 缺少 OCID")
	}
	ac.logf("正在获取私有IP...\n")
	var privateIps []core.PrivateIp
	privateIps, err = ac.getPrivateIps(vnic.Id)
	if err != nil {
		ac.logf("获取私有IP失败: %s\n", err.Error())
		return
	}
	var privateIp core.PrivateIp
	for _, p := range privateIps {
		if BVal(p.IsPrimary) {
			privateIp = p
		}
	}
	if privateIp.Id == nil {
		return publicIp, errors.New("未找到主私有IP")
	}

	ac.logf("正在获取公共IP OCID...\n")
	publicIp, err = ac.getPublicIp(privateIp.Id)
	if err != nil {
		ac.logf("获取公共IP OCID 失败: %s\n", err.Error())
	}
	// 实例可能尚未分配公共IP，此时跳过删除直接创建新的公共IP。
	if publicIp.Id != nil {
		ac.logf("正在删除公共IP...\n")
		_, err = ac.deletePublicIp(publicIp.Id)
		if err != nil {
			ac.logf("删除公共IP 失败: %s\n", err.Error())
		}
	}
	time.Sleep(3 * time.Second)
	ac.logf("正在创建公共IP...\n")
	publicIp, err = ac.createPublicIp(privateIp.Id)
	return
}

// 更新指定的VNIC
func (ac *AccountCtx) updateVnic(vnicId *string) (core.Vnic, error) {
	req := core.UpdateVnicRequest{
		VnicId:            vnicId,
		UpdateVnicDetails: core.UpdateVnicDetails{SkipSourceDestCheck: common.Bool(true)},
		RequestMetadata:   getCustomRequestMetadataWithRetryPolicy(),
	}
	resp, err := ac.networkClient.UpdateVnic(Ctx, req)
	return resp.Vnic, err
}

// 获取指定VNIC的私有IP
func (ac *AccountCtx) getPrivateIps(vnicId *string) ([]core.PrivateIp, error) {
	req := core.ListPrivateIpsRequest{
		VnicId:          vnicId,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}
	resp, err := ac.networkClient.ListPrivateIps(Ctx, req)
	if err == nil && (resp.Items == nil || len(resp.Items) == 0) {
		err = errors.New("私有IP为空")
	}
	return resp.Items, err
}

// 获取分配给指定私有IP的公共IP
func (ac *AccountCtx) getPublicIp(privateIpId *string) (core.PublicIp, error) {
	req := core.GetPublicIpByPrivateIpIdRequest{
		GetPublicIpByPrivateIpIdDetails: core.GetPublicIpByPrivateIpIdDetails{PrivateIpId: privateIpId},
		RequestMetadata:                 getCustomRequestMetadataWithRetryPolicy(),
	}
	resp, err := ac.networkClient.GetPublicIpByPrivateIpId(Ctx, req)
	if err == nil && resp.PublicIp.Id == nil {
		err = errors.New("未分配公共IP")
	}
	return resp.PublicIp, err
}

// 删除公共IP
func (ac *AccountCtx) deletePublicIp(publicIpId *string) (core.DeletePublicIpResponse, error) {
	req := core.DeletePublicIpRequest{
		PublicIpId:      publicIpId,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy()}
	return ac.networkClient.DeletePublicIp(Ctx, req)
}

// 创建公共IP
func (ac *AccountCtx) createPublicIp(privateIpId *string) (core.PublicIp, error) {
	var publicIp core.PublicIp
	req := core.CreatePublicIpRequest{
		CreatePublicIpDetails: core.CreatePublicIpDetails{
			CompartmentId: common.String(ac.Oracle.Tenancy),
			Lifetime:      core.CreatePublicIpDetailsLifetimeEphemeral,
			PrivateIpId:   privateIpId,
		},
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}
	resp, err := ac.networkClient.CreatePublicIp(Ctx, req)
	publicIp = resp.PublicIp
	return publicIp, err
}

// 更新保留公共IP
func (ac *AccountCtx) updatePublicIp(publicIpId *string, privateIpId *string) (core.PublicIp, error) {
	req := core.UpdatePublicIpRequest{
		PublicIpId: publicIpId,
		UpdatePublicIpDetails: core.UpdatePublicIpDetails{
			PrivateIpId: privateIpId,
		},
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}
	resp, err := ac.networkClient.UpdatePublicIp(Ctx, req)
	return resp.PublicIp, err
}
