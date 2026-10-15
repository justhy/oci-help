package oci

import (
	"oci-help/internal/config"
	"time"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/core"
	"github.com/oracle/oci-go-sdk/v65/example/helpers"
)

/* ------------------------------------------------------------------ 网络基础设施 */

// 创建或获取基础网络设施
func (ac *AccountCtx) CreateOrGetNetworkInfrastructure(inst config.Instance) (subnet core.Subnet, err error) {
	var vcn core.Vcn
	vcn, err = ac.createOrGetVcn(inst)
	if err != nil {
		return
	}
	var gateway core.InternetGateway
	gateway, err = ac.createOrGetInternetGateway(vcn.Id)
	if err != nil {
		return
	}
	_, err = ac.createOrGetRouteTable(gateway.Id, vcn.Id)
	if err != nil {
		return
	}
	subnet, err = ac.createOrGetSubnetWithDetails(
		vcn.Id,
		common.String(inst.SubnetDisplayName),
		common.String("10.0.0.0/20"),
		common.String("subnetdns"),
		common.String(inst.AvailabilityDomain))
	return
}

func (ac *AccountCtx) createOrGetSubnetWithDetails(vcnID *string,
	displayName *string, cidrBlock *string, dnsLabel *string, availableDomain *string) (subnet core.Subnet, err error) {
	var subnets []core.Subnet
	subnets, err = ac.listSubnets(vcnID)
	if err != nil {
		return
	}

	if displayName == nil {
		displayName = common.String("")
	}

	if len(subnets) > 0 && *displayName == "" {
		subnet = subnets[0]
		return
	}

	// check if the subnet has already been created
	for _, element := range subnets {
		if *element.DisplayName == *displayName {
			// find the subnet, return it
			subnet = element
			return
		}
	}

	// create a new subnet
	ac.logf("开始创建Subnet（没有可用的Subnet，或指定的Subnet不存在）\n")
	// 子网名称为空，以当前时间为名称创建子网
	if *displayName == "" {
		displayName = common.String(time.Now().Format("subnet-20060102-1504"))
	}
	request := core.CreateSubnetRequest{}
	request.CompartmentId = &ac.Oracle.Tenancy
	request.CidrBlock = cidrBlock
	request.DisplayName = displayName
	request.DnsLabel = dnsLabel
	request.RequestMetadata = getCustomRequestMetadataWithRetryPolicy()

	request.VcnId = vcnID
	var r core.CreateSubnetResponse
	r, err = ac.networkClient.CreateSubnet(Ctx, request)
	if err != nil {
		return
	}
	pollUntilAvailable := func(r common.OCIOperationResponse) bool {
		if converted, ok := r.Response.(core.GetSubnetResponse); ok {
			return converted.LifecycleState != core.SubnetLifecycleStateAvailable
		}
		return true
	}

	pollGetRequest := core.GetSubnetRequest{
		SubnetId:        r.Id,
		RequestMetadata: helpers.GetRequestMetadataWithCustomizedRetryPolicy(pollUntilAvailable),
	}

	_, err = ac.networkClient.GetSubnet(Ctx, pollGetRequest)
	if err != nil {
		return
	}

	// update the security rules
	getReq := core.GetSecurityListRequest{
		SecurityListId:  common.String(r.SecurityListIds[0]),
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}

	var getResp core.GetSecurityListResponse
	getResp, err = ac.networkClient.GetSecurityList(Ctx, getReq)
	if err != nil {
		return
	}

	newRules := append(getResp.IngressSecurityRules, core.IngressSecurityRule{
		Protocol: common.String("all"), // 允许所有协议
		Source:   common.String("0.0.0.0/0"),
	})

	updateReq := core.UpdateSecurityListRequest{
		SecurityListId:  common.String(r.SecurityListIds[0]),
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}

	updateReq.IngressSecurityRules = newRules

	_, err = ac.networkClient.UpdateSecurityList(Ctx, updateReq)
	if err != nil {
		return
	}
	ac.logf("Subnet创建成功: %s\n", *r.Subnet.DisplayName)
	subnet = r.Subnet
	return
}

// 列出指定虚拟云网络 (VCN) 中的所有子网
func (ac *AccountCtx) listSubnets(vcnID *string) (subnets []core.Subnet, err error) {
	request := core.ListSubnetsRequest{
		CompartmentId:   &ac.Oracle.Tenancy,
		VcnId:           vcnID,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}
	var r core.ListSubnetsResponse
	r, err = ac.networkClient.ListSubnets(Ctx, request)
	if err != nil {
		return
	}
	subnets = r.Items
	return
}

// 创建一个新的虚拟云网络 (VCN) 或获取已经存在的虚拟云网络
func (ac *AccountCtx) createOrGetVcn(inst config.Instance) (core.Vcn, error) {
	var vcn core.Vcn
	vcnItems, err := ac.ListVcns()
	if err != nil {
		return vcn, err
	}
	displayName := common.String(inst.VcnDisplayName)
	if len(vcnItems) > 0 && *displayName == "" {
		vcn = vcnItems[0]
		return vcn, err
	}
	for _, element := range vcnItems {
		if *element.DisplayName == inst.VcnDisplayName {
			vcn = element
			return vcn, err
		}
	}
	// create a new VCN
	ac.logf("开始创建VCN（没有可用的VCN，或指定的VCN不存在）\n")
	if *displayName == "" {
		displayName = common.String(time.Now().Format("vcn-20060102-1504"))
	}
	request := core.CreateVcnRequest{}
	request.RequestMetadata = getCustomRequestMetadataWithRetryPolicy()
	request.CidrBlock = common.String("10.0.0.0/16")
	request.CompartmentId = common.String(ac.Oracle.Tenancy)
	request.DisplayName = displayName
	request.DnsLabel = common.String("vcndns")
	r, err := ac.networkClient.CreateVcn(Ctx, request)
	if err != nil {
		return vcn, err
	}
	ac.logf("VCN创建成功: %s\n", *r.Vcn.DisplayName)
	vcn = r.Vcn
	return vcn, err
}

// 列出所有虚拟云网络 (VCN)
func (ac *AccountCtx) ListVcns() ([]core.Vcn, error) {
	request := core.ListVcnsRequest{
		CompartmentId:   &ac.Oracle.Tenancy,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}
	r, err := ac.networkClient.ListVcns(Ctx, request)
	if err != nil {
		return nil, err
	}
	return r.Items, err
}

// 创建或者获取 Internet 网关
func (ac *AccountCtx) createOrGetInternetGateway(vcnID *string) (core.InternetGateway, error) {
	var gateway core.InternetGateway
	listGWRequest := core.ListInternetGatewaysRequest{
		CompartmentId:   &ac.Oracle.Tenancy,
		VcnId:           vcnID,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}

	listGWRespone, err := ac.networkClient.ListInternetGateways(Ctx, listGWRequest)
	if err != nil {
		ac.logf("Internet gateway list error: %s\n", err.Error())
		return gateway, err
	}

	if len(listGWRespone.Items) >= 1 {
		gateway = listGWRespone.Items[0]
	} else {
		ac.logf("开始创建Internet网关\n")
		enabled := true
		createGWDetails := core.CreateInternetGatewayDetails{
			CompartmentId: &ac.Oracle.Tenancy,
			IsEnabled:     &enabled,
			VcnId:         vcnID,
		}

		createGWRequest := core.CreateInternetGatewayRequest{
			CreateInternetGatewayDetails: createGWDetails,
			RequestMetadata:              getCustomRequestMetadataWithRetryPolicy()}

		createGWResponse, err := ac.networkClient.CreateInternetGateway(Ctx, createGWRequest)

		if err != nil {
			ac.logf("Internet gateway create error: %s\n", err.Error())
			return gateway, err
		}
		gateway = createGWResponse.InternetGateway
		ac.logf("Internet网关创建成功: %s\n", *gateway.DisplayName)
	}
	return gateway, err
}

// 创建或者获取路由表
func (ac *AccountCtx) createOrGetRouteTable(gatewayID, VcnID *string) (routeTable core.RouteTable, err error) {
	listRTRequest := core.ListRouteTablesRequest{
		CompartmentId:   &ac.Oracle.Tenancy,
		VcnId:           VcnID,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}
	var listRTResponse core.ListRouteTablesResponse
	listRTResponse, err = ac.networkClient.ListRouteTables(Ctx, listRTRequest)
	if err != nil {
		ac.logf("Route table list error: %s\n", err.Error())
		return
	}

	cidrRange := "0.0.0.0/0"
	rr := core.RouteRule{
		NetworkEntityId: gatewayID,
		Destination:     &cidrRange,
		DestinationType: core.RouteRuleDestinationTypeCidrBlock,
	}

	if len(listRTResponse.Items) >= 1 {
		if len(listRTResponse.Items[0].RouteRules) >= 1 {
			routeTable = listRTResponse.Items[0]
		} else {
			ac.logf("路由表未添加规则，开始添加Internet路由规则\n")
			updateRTDetails := core.UpdateRouteTableDetails{
				RouteRules: []core.RouteRule{rr},
			}

			updateRTRequest := core.UpdateRouteTableRequest{
				RtId:                    listRTResponse.Items[0].Id,
				UpdateRouteTableDetails: updateRTDetails,
				RequestMetadata:         getCustomRequestMetadataWithRetryPolicy(),
			}
			var updateRTResponse core.UpdateRouteTableResponse
			updateRTResponse, err = ac.networkClient.UpdateRouteTable(Ctx, updateRTRequest)
			if err != nil {
				ac.logf("Error updating route table: %s\n", err)
				return
			}
			ac.logf("Internet路由规则添加成功\n")
			routeTable = updateRTResponse.RouteTable
		}
	} else {
		ac.logf("Error could not find VCN default route table, VCN OCID: %s Could not find route table.\n", *VcnID)
	}
	return
}
