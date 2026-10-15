package oci

import (
	"errors"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/core"
)

/* ------------------------------------------------------------------ VCN / 子网 / VNIC 基础查询 */

func (ac *AccountCtx) getVcn(vcnID string) (core.Vcn, error) {
	resp, err := ac.networkClient.GetVcn(Ctx, core.GetVcnRequest{
		VcnId:           &vcnID,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	return resp.Vcn, err
}

func (ac *AccountCtx) getSubnet(subnetID string) (core.Subnet, error) {
	resp, err := ac.networkClient.GetSubnet(Ctx, core.GetSubnetRequest{
		SubnetId:        &subnetID,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	return resp.Subnet, err
}

func (ac *AccountCtx) listSubnetsOfVcn(vcnID string) ([]core.Subnet, error) {
	resp, err := ac.networkClient.ListSubnets(Ctx, core.ListSubnetsRequest{
		CompartmentId:   common.String(ac.Oracle.Tenancy),
		VcnId:           &vcnID,
		Limit:           common.Int(100),
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	return resp.Items, err
}

// primaryVnic 返回实例的第一块 VNIC。
func (ac *AccountCtx) primaryVnic(instanceID string) (core.Vnic, error) {
	vnics, err := ac.GetInstanceVnics(&instanceID)
	if err != nil {
		return core.Vnic{}, err
	}
	if len(vnics) == 0 {
		return core.Vnic{}, errors.New("实例没有可用的 VNIC")
	}
	return vnics[0], nil
}

// getVcnByInstance 由实例的 VNIC → 子网 → VCN 反查所属 VCN。
func (ac *AccountCtx) getVcnByInstance(instanceID string) (core.Vcn, error) {
	vnic, err := ac.primaryVnic(instanceID)
	if err != nil {
		return core.Vcn{}, err
	}
	if vnic.SubnetId == nil {
		return core.Vcn{}, errors.New("VNIC 未关联子网")
	}
	subnet, err := ac.getSubnet(*vnic.SubnetId)
	if err != nil {
		return core.Vcn{}, err
	}
	if subnet.VcnId == nil {
		return core.Vcn{}, errors.New("子网未关联 VCN")
	}
	return ac.getVcn(*subnet.VcnId)
}
