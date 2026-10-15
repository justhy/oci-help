package oci

import (
	"sort"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/limits"
)

// LimitItem 是一条配额（服务限制）记录。
type LimitItem struct {
	ServiceName        string `json:"serviceName"`
	LimitName          string `json:"limitName"`
	Description        string `json:"description"`
	ScopeType          string `json:"scopeType"`
	AvailabilityDomain string `json:"availabilityDomain"`
	ServiceLimit       int64  `json:"serviceLimit"`
	Used               int64  `json:"used"`
	Available          int64  `json:"available"`
	HasAvailability    bool   `json:"hasAvailability"`
}

// ListLimitServices 返回账号可查询限制的服务名列表。
func (ac *AccountCtx) ListLimitServices() ([]string, error) {
	resp, err := ac.limitsClient.ListServices(Ctx, limits.ListServicesRequest{
		CompartmentId:   common.String(ac.Oracle.Tenancy),
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, svc := range resp.Items {
		if svc.Name != nil && *svc.Name != "" && !seen[*svc.Name] {
			seen[*svc.Name] = true
			out = append(out, *svc.Name)
		}
	}
	sort.Strings(out)
	return out, nil
}

// GetLimits 查询指定服务的限制定义、当前值及可用量。
// serviceName 为空时查询全部服务。
func (ac *AccountCtx) GetLimits(serviceName string) ([]LimitItem, error) {
	defs, err := ac.listLimitDefinitions(serviceName)
	if err != nil {
		return nil, err
	}
	var items []LimitItem
	for _, def := range defs {
		if def.ServiceName == nil || def.Name == nil {
			continue
		}
		svc := *def.ServiceName
		limitName := *def.Name
		scope := "REGION"
		if def.ScopeType != "" {
			scope = string(def.ScopeType)
		}
		values, verr := ac.limitsClient.ListLimitValues(Ctx, limits.ListLimitValuesRequest{
			CompartmentId:   common.String(ac.Oracle.Tenancy),
			ServiceName:     common.String(svc),
			Name:            common.String(limitName),
			RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
		})
		if verr != nil || len(values.Items) == 0 {
			items = append(items, LimitItem{
				ServiceName: svc,
				LimitName:   limitName,
				Description: S(def.Description),
				ScopeType:   scope,
			})
			continue
		}
		for _, v := range values.Items {
			item := LimitItem{
				ServiceName:        svc,
				LimitName:          limitName,
				Description:        S(def.Description),
				ScopeType:          scope,
				AvailabilityDomain: S(v.AvailabilityDomain),
				ServiceLimit:       I64(v.Value),
			}
			// 查询已用/可用量（部分限制不支持）
			avail, aerr := ac.limitsClient.GetResourceAvailability(Ctx, limits.GetResourceAvailabilityRequest{
				ServiceName:        common.String(svc),
				LimitName:          common.String(limitName),
				CompartmentId:      common.String(ac.Oracle.Tenancy),
				AvailabilityDomain: v.AvailabilityDomain,
				RequestMetadata:    getCustomRequestMetadataWithRetryPolicy(),
			})
			if aerr == nil {
				item.Used = I64(avail.Used)
				item.Available = I64(avail.Available)
				item.HasAvailability = true
			}
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ServiceName != items[j].ServiceName {
			return items[i].ServiceName < items[j].ServiceName
		}
		return items[i].LimitName < items[j].LimitName
	})
	return items, nil
}

func (ac *AccountCtx) listLimitDefinitions(serviceName string) ([]limits.LimitDefinitionSummary, error) {
	var out []limits.LimitDefinitionSummary
	var page *string
	req := limits.ListLimitDefinitionsRequest{
		CompartmentId:   common.String(ac.Oracle.Tenancy),
		Page:            page,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}
	if serviceName != "" {
		req.ServiceName = common.String(serviceName)
	}
	for {
		req.Page = page
		resp, err := ac.limitsClient.ListLimitDefinitions(Ctx, req)
		if err != nil {
			return out, err
		}
		out = append(out, resp.Items...)
		if resp.OpcNextPage == nil || len(resp.Items) == 0 {
			break
		}
		page = resp.OpcNextPage
	}
	return out, nil
}
