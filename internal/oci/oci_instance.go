package oci

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/core"
	"github.com/oracle/oci-go-sdk/v65/identity"
)

/* ------------------------------------------------------------------ 身份 / 可用性域 */

// 列出符合条件的可用性域
func (ac *AccountCtx) ListAvailabilityDomains() ([]identity.AvailabilityDomain, error) {
	req := identity.ListAvailabilityDomainsRequest{
		CompartmentId:   common.String(ac.Oracle.Tenancy),
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}
	resp, err := ac.identityClient.ListAvailabilityDomains(Ctx, req)
	return resp.Items, err
}

func (ac *AccountCtx) getUsers() {
	req := identity.ListUsersRequest{
		CompartmentId:   &ac.Oracle.Tenancy,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}
	resp, _ := ac.identityClient.ListUsers(Ctx, req)
	for _, user := range resp.Items {
		var userName string
		if user.Name != nil {
			userName = *user.Name
		}
		var email string
		if user.Email != nil {
			email = *user.Email
		}
		fmt.Println("用户名:", userName, "邮箱:", email)
	}
}

/* ------------------------------------------------------------------ 实例 */

func (ac *AccountCtx) ListInstances(page *string) ([]core.Instance, *string, error) {
	req := core.ListInstancesRequest{
		CompartmentId:   common.String(ac.Oracle.Tenancy),
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
		Limit:           common.Int(100),
		Page:            page,
	}
	resp, err := ac.computeClient.ListInstances(Ctx, req)
	return resp.Items, resp.OpcNextPage, err
}

// ListAllInstances 分页拉取账号下全部实例。
func (ac *AccountCtx) ListAllInstances() ([]core.Instance, error) {
	var instances []core.Instance
	var nextPage *string
	for {
		ins, page, err := ac.ListInstances(nextPage)
		if err != nil {
			return instances, err
		}
		instances = append(instances, ins...)
		if page == nil || len(ins) == 0 {
			break
		}
		nextPage = page
	}
	return instances, nil
}

// ListInstancesWithIps 拉取实例列表，并并发补齐每个实例的公共 IP。
func (ac *AccountCtx) ListInstancesWithIps() ([]core.Instance, map[string][]string, error) {
	instances, err := ac.ListAllInstances()
	if err != nil {
		return nil, nil, err
	}
	ips := make(map[string][]string, len(instances))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i := range instances {
		if instances[i].Id == nil {
			continue
		}
		id := *instances[i].Id
		wg.Add(1)
		sem <- struct{}{}
		go func(id string) {
			defer wg.Done()
			defer func() { <-sem }()
			list := ac.instancePublicIpsOnce(common.String(id))
			mu.Lock()
			ips[id] = list
			mu.Unlock()
		}(id)
	}
	wg.Wait()
	return instances, ips, nil
}

func (ac *AccountCtx) GetInstance(instanceId *string) (core.Instance, error) {
	req := core.GetInstanceRequest{
		InstanceId:      instanceId,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}
	resp, err := ac.computeClient.GetInstance(Ctx, req)
	return resp.Instance, err
}

func (ac *AccountCtx) ListVnicAttachments(instanceId *string, page *string) ([]core.VnicAttachment, *string, error) {
	req := core.ListVnicAttachmentsRequest{
		CompartmentId:   common.String(ac.Oracle.Tenancy),
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
		Limit:           common.Int(100),
		Page:            page,
	}
	if instanceId != nil && *instanceId != "" {
		req.InstanceId = instanceId
	}
	resp, err := ac.computeClient.ListVnicAttachments(Ctx, req)
	return resp.Items, resp.OpcNextPage, err
}

func (ac *AccountCtx) GetVnic(vnicID *string) (core.Vnic, error) {
	req := core.GetVnicRequest{
		VnicId:          vnicID,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}
	resp, err := ac.networkClient.GetVnic(Ctx, req)
	if err != nil && resp.RawResponse != nil {
		err = errors.New(resp.RawResponse.Status)
	}
	return resp.Vnic, err
}

func (ac *AccountCtx) GetInstanceVnics(instanceId *string) (vnics []core.Vnic, err error) {
	vnicAttachments, _, err := ac.ListVnicAttachments(instanceId, nil)
	if err != nil {
		return
	}
	for _, vnicAttachment := range vnicAttachments {
		vnic, vnicErr := ac.GetVnic(vnicAttachment.VnicId)
		if vnicErr != nil {
			ac.logf("GetVnic error: %s\n", vnicErr.Error())
			continue
		}
		vnics = append(vnics, vnic)
	}
	return
}

// instancePublicIpsOnce 单次尝试获取实例公共 IP（不做重试，适合列表场景）。
func (ac *AccountCtx) instancePublicIpsOnce(instanceId *string) []string {
	var ips []string
	vnicAttachments, _, err := ac.ListVnicAttachments(instanceId, nil)
	if err != nil {
		return ips
	}
	for _, vnicAttachment := range vnicAttachments {
		vnic, vnicErr := ac.GetVnic(vnicAttachment.VnicId)
		if vnicErr != nil {
			continue
		}
		if vnic.PublicIp != nil && *vnic.PublicIp != "" {
			ips = append(ips, *vnic.PublicIp)
		}
	}
	return ips
}

// 根据实例OCID获取公共IP（带重试，避免刚抢到实例正在预配时取不到）
func (ac *AccountCtx) getInstancePublicIps(instanceId *string) (ips []string, err error) {
	var ins core.Instance
	for i := 0; i < 100; i++ {
		// 每轮都检查停止请求，避免预配慢时"停止任务"最长要等 5 分钟才生效。
		if ac.stopped() {
			err = errors.New("任务已中止")
			return
		}
		if ins.LifecycleState != core.InstanceLifecycleStateRunning {
			ins, err = ac.GetInstance(instanceId)
			if err != nil {
				continue
			}
			if ins.LifecycleState == core.InstanceLifecycleStateTerminating || ins.LifecycleState == core.InstanceLifecycleStateTerminated {
				err = errors.New("实例已终止😔")
				return
			}
		}

		var vnicAttachments []core.VnicAttachment
		vnicAttachments, _, err = ac.ListVnicAttachments(instanceId, nil)
		if err != nil {
			continue
		}
		if len(vnicAttachments) > 0 {
			for _, vnicAttachment := range vnicAttachments {
				vnic, vnicErr := ac.GetVnic(vnicAttachment.VnicId)
				if vnicErr != nil {
					ac.logf("GetVnic error: %s\n", vnicErr.Error())
					continue
				}
				if vnic.PublicIp != nil && *vnic.PublicIp != "" {
					ips = append(ips, *vnic.PublicIp)
				}
			}
			return
		}
		time.Sleep(3 * time.Second)
	}
	return
}

// 终止实例
// https://docs.oracle.com/en-us/iaas/api/#/en/iaas/20160918/Instance/TerminateInstance
func (ac *AccountCtx) TerminateInstance(id *string) error {
	request := core.TerminateInstanceRequest{
		InstanceId:         id,
		PreserveBootVolume: common.Bool(false),
		RequestMetadata:    getCustomRequestMetadataWithRetryPolicy(),
	}
	_, err := ac.computeClient.TerminateInstance(Ctx, request)
	return err
}

func (ac *AccountCtx) UpdateInstance(instanceId *string, displayName *string, ocpus, memoryInGBs *float32,
	details []core.InstanceAgentPluginConfigDetails, disable *bool) (core.UpdateInstanceResponse, error) {
	updateInstanceDetails := core.UpdateInstanceDetails{}
	if displayName != nil && *displayName != "" {
		updateInstanceDetails.DisplayName = displayName
	}
	shapeConfig := core.UpdateInstanceShapeConfigDetails{}
	if ocpus != nil && *ocpus > 0 {
		shapeConfig.Ocpus = ocpus
	}
	if memoryInGBs != nil && *memoryInGBs > 0 {
		shapeConfig.MemoryInGBs = memoryInGBs
	}
	updateInstanceDetails.ShapeConfig = &shapeConfig

	// Oracle Cloud Agent 配置
	if disable != nil && details != nil {
		for i := 0; i < len(details); i++ {
			if *disable {
				details[i].DesiredState = core.InstanceAgentPluginConfigDetailsDesiredStateDisabled
			} else {
				details[i].DesiredState = core.InstanceAgentPluginConfigDetailsDesiredStateEnabled
			}
		}
		agentConfig := core.UpdateInstanceAgentConfigDetails{
			IsMonitoringDisabled:  disable, // 是否禁用监控插件
			IsManagementDisabled:  disable, // 是否禁用管理插件
			AreAllPluginsDisabled: disable, // 是否禁用所有可用的插件（管理和监控插件）
			PluginsConfig:         details,
		}
		updateInstanceDetails.AgentConfig = &agentConfig
	}

	req := core.UpdateInstanceRequest{
		InstanceId:            instanceId,
		UpdateInstanceDetails: updateInstanceDetails,
		RequestMetadata:       getCustomRequestMetadataWithRetryPolicy(),
	}
	return ac.computeClient.UpdateInstance(Ctx, req)
}

func (ac *AccountCtx) InstanceAction(instanceId *string, action core.InstanceActionActionEnum) (ins core.Instance, err error) {
	req := core.InstanceActionRequest{
		InstanceId:      instanceId,
		Action:          action,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}
	resp, err := ac.computeClient.InstanceAction(Ctx, req)
	ins = resp.Instance
	return
}
