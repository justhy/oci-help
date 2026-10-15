package oci

import (
	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/core"
)

/* ------------------------------------------------------------------ 路由表 / 安全列表清单（供 UI 下拉选择） */

// NamedRef 是「ID + 名称」的通用引用视图。
type NamedRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ListRouteTableRefs 列出 VCN 下的路由表。
func (ac *AccountCtx) ListRouteTableRefs(vcnID string) ([]NamedRef, error) {
	resp, err := ac.networkClient.ListRouteTables(Ctx, core.ListRouteTablesRequest{
		CompartmentId:   common.String(ac.Oracle.Tenancy),
		VcnId:           &vcnID,
		Limit:           common.Int(100),
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	if err != nil {
		return nil, err
	}
	out := make([]NamedRef, 0, len(resp.Items))
	for i := range resp.Items {
		out = append(out, NamedRef{ID: S(resp.Items[i].Id), Name: S(resp.Items[i].DisplayName)})
	}
	return out, nil
}

// ListSecurityListRefs 列出 VCN 下的安全列表。
func (ac *AccountCtx) ListSecurityListRefs(vcnID string) ([]NamedRef, error) {
	resp, err := ac.networkClient.ListSecurityLists(Ctx, core.ListSecurityListsRequest{
		CompartmentId:   common.String(ac.Oracle.Tenancy),
		VcnId:           &vcnID,
		Limit:           common.Int(100),
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	if err != nil {
		return nil, err
	}
	out := make([]NamedRef, 0, len(resp.Items))
	for i := range resp.Items {
		out = append(out, NamedRef{ID: S(resp.Items[i].Id), Name: S(resp.Items[i].DisplayName)})
	}
	return out, nil
}
