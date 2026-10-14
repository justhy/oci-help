package main

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/oracle/oci-go-sdk/v54/common"
	"github.com/oracle/oci-go-sdk/v54/core"
	"github.com/oracle/oci-go-sdk/v54/example/helpers"
	"github.com/oracle/oci-go-sdk/v54/identity"
	"gopkg.in/ini.v1"
)

// adCache 缓存可用性域。用指针持有锁，保证 AccountCtx 被复制（如创建实例任务）时共享同一份缓存，
// 同时避免复制带锁的结构体。
type adCache struct {
	mu   sync.Mutex
	list []identity.AvailabilityDomain
}

// AccountCtx 承载单个甲骨文账号的凭证、SDK 客户端与运行时状态。
// 每个账号一个实例，CLI 与 Web 共用同一套操作；不同账号之间互不干扰，可并发调用。
type AccountCtx struct {
	Section *ini.Section
	Name    string
	Oracle  Oracle

	provider       common.ConfigurationProvider
	computeClient  core.ComputeClient
	networkClient  core.VirtualNetworkClient
	storageClient  core.BlockstorageClient
	identityClient identity.IdentityClient
	// ads 指向可用性域缓存。用指针持有锁，保证 AccountCtx 被复制（如创建实例任务）时
	// 共享同一份缓存，同时避免复制带锁的结构体。
	ads *adCache

	// Logger 输出进度日志；为 nil 时回退到 CLI 的 printf。
	Logger func(format string, a ...interface{})
	// Progress 推送进度（stage/done/total）；为 nil 时忽略。
	Progress func(stage string, done, total int)
	// Stopped 返回 true 时中止批量创建；为 nil 时视为不中止。
	Stopped func() bool
}

// stopped 判断当前是否已被要求停止。
func (ac *AccountCtx) stopped() bool {
	return ac.Stopped != nil && ac.Stopped()
}

// sleepRandomSecond 在 min~max 秒之间随机等待，期间轮询停止请求；返回 false 表示被中止。
func (ac *AccountCtx) sleepRandomSecond(min, max int32) bool {
	var second int32
	if min <= 0 || max <= 0 {
		second = 1
	} else if min >= max {
		second = max
	} else {
		second = rand.Int31n(max-min) + min
	}
	ac.logf("Sleep %d Second...\n", second)
	deadline := time.Now().Add(time.Duration(second) * time.Second)
	for {
		if ac.stopped() {
			return false
		}
		remain := time.Until(deadline)
		if remain <= 0 {
			return true
		}
		if remain > 200*time.Millisecond {
			remain = 200 * time.Millisecond
		}
		time.Sleep(remain)
	}
}

// newAccountCtx 根据配置节构造账号上下文并初始化各 SDK 客户端。
func newAccountCtx(sec *ini.Section) (*AccountCtx, error) {
	ac := &AccountCtx{Section: sec, Name: sec.Name(), ads: &adCache{}}
	if err := sec.MapTo(&ac.Oracle); err != nil {
		return nil, fmt.Errorf("解析账号相关参数失败: %w", err)
	}
	var err error
	if ac.provider, err = getProvider(ac.Oracle); err != nil {
		return nil, fmt.Errorf("获取 Provider 失败: %w", err)
	}
	if ac.computeClient, err = core.NewComputeClientWithConfigurationProvider(ac.provider); err != nil {
		return nil, fmt.Errorf("创建 ComputeClient 失败: %w", err)
	}
	setProxyOrNot(&ac.computeClient.BaseClient)
	if ac.networkClient, err = core.NewVirtualNetworkClientWithConfigurationProvider(ac.provider); err != nil {
		return nil, fmt.Errorf("创建 VirtualNetworkClient 失败: %w", err)
	}
	setProxyOrNot(&ac.networkClient.BaseClient)
	if ac.storageClient, err = core.NewBlockstorageClientWithConfigurationProvider(ac.provider); err != nil {
		return nil, fmt.Errorf("创建 BlockstorageClient 失败: %w", err)
	}
	setProxyOrNot(&ac.storageClient.BaseClient)
	if ac.identityClient, err = identity.NewIdentityClientWithConfigurationProvider(ac.provider); err != nil {
		return nil, fmt.Errorf("创建 IdentityClient 失败: %w", err)
	}
	setProxyOrNot(&ac.identityClient.BaseClient)
	return ac, nil
}

// ensureAvailabilityDomains 惰性加载可用性域，供引导卷等按域查询使用。
// 加锁保护：同一个 AccountCtx 会被多个 Web 请求/后台任务共享。
func (ac *AccountCtx) ensureAvailabilityDomains() ([]identity.AvailabilityDomain, error) {
	if ac.ads == nil {
		return nil, errors.New("账号上下文未初始化")
	}
	ac.ads.mu.Lock()
	defer ac.ads.mu.Unlock()
	if len(ac.ads.list) > 0 {
		return ac.ads.list, nil
	}
	ads, err := ac.ListAvailabilityDomains()
	if err != nil {
		return nil, err
	}
	ac.ads.list = ads
	return ads, nil
}

// AvailabilityDomains 返回已缓存的可用性域（尚未加载时返回 nil）。
func (ac *AccountCtx) AvailabilityDomains() []identity.AvailabilityDomain {
	if ac.ads == nil {
		return nil
	}
	ac.ads.mu.Lock()
	defer ac.ads.mu.Unlock()
	return ac.ads.list
}

func (ac *AccountCtx) logf(format string, a ...interface{}) {
	if ac.Logger != nil {
		ac.Logger(format, a...)
		return
	}
	printf(format, a...)
}

func (ac *AccountCtx) progress(stage string, done, total int) {
	if ac.Progress != nil {
		ac.Progress(stage, done, total)
	}
}

func getProvider(oracle Oracle) (common.ConfigurationProvider, error) {
	content, err := ioutil.ReadFile(oracle.Key_file)
	if err != nil {
		return nil, err
	}
	privateKey := string(content)
	privateKeyPassphrase := common.String(oracle.Key_password)
	return common.NewRawConfigurationProvider(oracle.Tenancy, oracle.User, oracle.Region, oracle.Fingerprint, privateKey, privateKeyPassphrase), nil
}

/* ------------------------------------------------------------------ 身份 / 可用性域 */

// 列出符合条件的可用性域
func (ac *AccountCtx) ListAvailabilityDomains() ([]identity.AvailabilityDomain, error) {
	req := identity.ListAvailabilityDomainsRequest{
		CompartmentId:   common.String(ac.Oracle.Tenancy),
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}
	resp, err := ac.identityClient.ListAvailabilityDomains(ctx, req)
	return resp.Items, err
}

func (ac *AccountCtx) getUsers() {
	req := identity.ListUsersRequest{
		CompartmentId:   &ac.Oracle.Tenancy,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}
	resp, _ := ac.identityClient.ListUsers(ctx, req)
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
	resp, err := ac.computeClient.ListInstances(ctx, req)
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

func (ac *AccountCtx) getInstance(instanceId *string) (core.Instance, error) {
	req := core.GetInstanceRequest{
		InstanceId:      instanceId,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}
	resp, err := ac.computeClient.GetInstance(ctx, req)
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
	resp, err := ac.computeClient.ListVnicAttachments(ctx, req)
	return resp.Items, resp.OpcNextPage, err
}

func (ac *AccountCtx) GetVnic(vnicID *string) (core.Vnic, error) {
	req := core.GetVnicRequest{
		VnicId:          vnicID,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}
	resp, err := ac.networkClient.GetVnic(ctx, req)
	if err != nil && resp.RawResponse != nil {
		err = errors.New(resp.RawResponse.Status)
	}
	return resp.Vnic, err
}

func (ac *AccountCtx) getInstanceVnics(instanceId *string) (vnics []core.Vnic, err error) {
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
		if ins.LifecycleState != core.InstanceLifecycleStateRunning {
			ins, err = ac.getInstance(instanceId)
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
func (ac *AccountCtx) terminateInstance(id *string) error {
	request := core.TerminateInstanceRequest{
		InstanceId:         id,
		PreserveBootVolume: common.Bool(false),
		RequestMetadata:    getCustomRequestMetadataWithRetryPolicy(),
	}
	_, err := ac.computeClient.TerminateInstance(ctx, request)
	return err
}

func (ac *AccountCtx) updateInstance(instanceId *string, displayName *string, ocpus, memoryInGBs *float32,
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
	return ac.computeClient.UpdateInstance(ctx, req)
}

func (ac *AccountCtx) instanceAction(instanceId *string, action core.InstanceActionActionEnum) (ins core.Instance, err error) {
	req := core.InstanceActionRequest{
		InstanceId:      instanceId,
		Action:          action,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}
	resp, err := ac.computeClient.InstanceAction(ctx, req)
	ins = resp.Instance
	return
}

/* ------------------------------------------------------------------ 公共 IP */

func (ac *AccountCtx) changePublicIp(vnics []core.Vnic) (publicIp core.PublicIp, err error) {
	var vnic core.Vnic
	foundPrimary := false
	for _, v := range vnics {
		if bval(v.IsPrimary) {
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
		if bval(p.IsPrimary) {
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
	resp, err := ac.networkClient.UpdateVnic(ctx, req)
	return resp.Vnic, err
}

// 获取指定VNIC的私有IP
func (ac *AccountCtx) getPrivateIps(vnicId *string) ([]core.PrivateIp, error) {
	req := core.ListPrivateIpsRequest{
		VnicId:          vnicId,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}
	resp, err := ac.networkClient.ListPrivateIps(ctx, req)
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
	resp, err := ac.networkClient.GetPublicIpByPrivateIpId(ctx, req)
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
	return ac.networkClient.DeletePublicIp(ctx, req)
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
	resp, err := ac.networkClient.CreatePublicIp(ctx, req)
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
	resp, err := ac.networkClient.UpdatePublicIp(ctx, req)
	return resp.PublicIp, err
}

/* ------------------------------------------------------------------ 引导卷 */

// 列出引导卷（单个可用性域）
func (ac *AccountCtx) getBootVolumes(availabilityDomain *string) ([]core.BootVolume, error) {
	req := core.ListBootVolumesRequest{
		AvailabilityDomain: availabilityDomain,
		CompartmentId:      common.String(ac.Oracle.Tenancy),
		RequestMetadata:    getCustomRequestMetadataWithRetryPolicy(),
	}
	resp, err := ac.storageClient.ListBootVolumes(ctx, req)
	return resp.Items, err
}

// ListAllBootVolumes 并发遍历所有可用性域，汇总引导卷。
func (ac *AccountCtx) ListAllBootVolumes() ([]core.BootVolume, error) {
	ads, err := ac.ensureAvailabilityDomains()
	if err != nil {
		return nil, err
	}
	var mu sync.Mutex
	var out []core.BootVolume
	var wg sync.WaitGroup
	for _, ad := range ads {
		wg.Add(1)
		go func(adName *string) {
			defer wg.Done()
			volumes, err := ac.getBootVolumes(adName)
			if err != nil {
				ac.logf("获取引导卷失败: %s\n", err.Error())
				return
			}
			mu.Lock()
			out = append(out, volumes...)
			mu.Unlock()
		}(ad.Name)
	}
	wg.Wait()
	return out, nil
}

// 获取指定引导卷
func (ac *AccountCtx) getBootVolume(bootVolumeId *string) (core.BootVolume, error) {
	req := core.GetBootVolumeRequest{
		BootVolumeId:    bootVolumeId,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}
	resp, err := ac.storageClient.GetBootVolume(ctx, req)
	return resp.BootVolume, err
}

// 更新引导卷
func (ac *AccountCtx) updateBootVolume(bootVolumeId *string, sizeInGBs *int64, vpusPerGB *int64) (core.BootVolume, error) {
	updateBootVolumeDetails := core.UpdateBootVolumeDetails{}
	if sizeInGBs != nil {
		updateBootVolumeDetails.SizeInGBs = sizeInGBs
	}
	if vpusPerGB != nil {
		updateBootVolumeDetails.VpusPerGB = vpusPerGB
	}
	req := core.UpdateBootVolumeRequest{
		BootVolumeId:            bootVolumeId,
		UpdateBootVolumeDetails: updateBootVolumeDetails,
		RequestMetadata:         getCustomRequestMetadataWithRetryPolicy(),
	}
	resp, err := ac.storageClient.UpdateBootVolume(ctx, req)
	return resp.BootVolume, err
}

// 删除引导卷
func (ac *AccountCtx) deleteBootVolume(bootVolumeId *string) (*http.Response, error) {
	req := core.DeleteBootVolumeRequest{
		BootVolumeId:    bootVolumeId,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}
	resp, err := ac.storageClient.DeleteBootVolume(ctx, req)
	return resp.RawResponse, err
}

// 分离引导卷
func (ac *AccountCtx) detachBootVolume(bootVolumeAttachmentId *string) (*http.Response, error) {
	req := core.DetachBootVolumeRequest{
		BootVolumeAttachmentId: bootVolumeAttachmentId,
		RequestMetadata:        getCustomRequestMetadataWithRetryPolicy(),
	}
	resp, err := ac.computeClient.DetachBootVolume(ctx, req)
	return resp.RawResponse, err
}

// 获取引导卷附件
func (ac *AccountCtx) listBootVolumeAttachments(availabilityDomain, compartmentId, bootVolumeId *string) ([]core.BootVolumeAttachment, error) {
	req := core.ListBootVolumeAttachmentsRequest{
		AvailabilityDomain: availabilityDomain,
		CompartmentId:      compartmentId,
		BootVolumeId:       bootVolumeId,
		RequestMetadata:    getCustomRequestMetadataWithRetryPolicy(),
	}
	resp, err := ac.computeClient.ListBootVolumeAttachments(ctx, req)
	return resp.Items, err
}

/* ------------------------------------------------------------------ 网络基础设施 */

// 创建或获取基础网络设施
func (ac *AccountCtx) CreateOrGetNetworkInfrastructure(inst Instance) (subnet core.Subnet, err error) {
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
	r, err = ac.networkClient.CreateSubnet(ctx, request)
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

	_, err = ac.networkClient.GetSubnet(ctx, pollGetRequest)
	if err != nil {
		return
	}

	// update the security rules
	getReq := core.GetSecurityListRequest{
		SecurityListId:  common.String(r.SecurityListIds[0]),
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}

	var getResp core.GetSecurityListResponse
	getResp, err = ac.networkClient.GetSecurityList(ctx, getReq)
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

	_, err = ac.networkClient.UpdateSecurityList(ctx, updateReq)
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
	r, err = ac.networkClient.ListSubnets(ctx, request)
	if err != nil {
		return
	}
	subnets = r.Items
	return
}

// 创建一个新的虚拟云网络 (VCN) 或获取已经存在的虚拟云网络
func (ac *AccountCtx) createOrGetVcn(inst Instance) (core.Vcn, error) {
	var vcn core.Vcn
	vcnItems, err := ac.listVcns()
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
	r, err := ac.networkClient.CreateVcn(ctx, request)
	if err != nil {
		return vcn, err
	}
	ac.logf("VCN创建成功: %s\n", *r.Vcn.DisplayName)
	vcn = r.Vcn
	return vcn, err
}

// 列出所有虚拟云网络 (VCN)
func (ac *AccountCtx) listVcns() ([]core.Vcn, error) {
	request := core.ListVcnsRequest{
		CompartmentId:   &ac.Oracle.Tenancy,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}
	r, err := ac.networkClient.ListVcns(ctx, request)
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

	listGWRespone, err := ac.networkClient.ListInternetGateways(ctx, listGWRequest)
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

		createGWResponse, err := ac.networkClient.CreateInternetGateway(ctx, createGWRequest)

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
	listRTResponse, err = ac.networkClient.ListRouteTables(ctx, listRTRequest)
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
			updateRTResponse, err = ac.networkClient.UpdateRouteTable(ctx, updateRTRequest)
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

/* ------------------------------------------------------------------ 镜像 / Shape */

// 获取符合条件系统镜像中的第一个
func (ac *AccountCtx) GetImage(inst Instance) (image core.Image, err error) {
	var images []core.Image
	images, err = ac.listImages(inst)
	if err != nil {
		return
	}
	if len(images) > 0 {
		image = images[0]
	} else {
		err = fmt.Errorf("未找到[%s %s]的镜像, 或该镜像不支持[%s]", inst.OperatingSystem, inst.OperatingSystemVersion, inst.Shape)
	}
	return
}

// 列出所有符合条件的系统镜像
func (ac *AccountCtx) listImages(inst Instance) ([]core.Image, error) {
	if inst.OperatingSystem == "" || inst.OperatingSystemVersion == "" {
		return nil, errors.New("操作系统类型和版本不能为空, 请检查配置文件")
	}
	request := core.ListImagesRequest{
		CompartmentId:          common.String(ac.Oracle.Tenancy),
		OperatingSystem:        common.String(inst.OperatingSystem),
		OperatingSystemVersion: common.String(inst.OperatingSystemVersion),
		Shape:                  common.String(inst.Shape),
		RequestMetadata:        getCustomRequestMetadataWithRetryPolicy(),
	}
	r, err := ac.computeClient.ListImages(ctx, request)
	return r.Items, err
}

func (ac *AccountCtx) getShape(imageId *string, shapeName string) (core.Shape, error) {
	var shape core.Shape
	shapes, err := ac.listShapes(imageId)
	if err != nil {
		return shape, err
	}
	for _, s := range shapes {
		if strings.EqualFold(*s.Shape, shapeName) {
			shape = s
			return shape, nil
		}
	}
	err = errors.New("没有符合条件的Shape")
	return shape, err
}

// ListShapes Lists the shapes that can be used to launch an instance within the specified compartment.
func (ac *AccountCtx) listShapes(imageID *string) ([]core.Shape, error) {
	request := core.ListShapesRequest{
		CompartmentId:   common.String(ac.Oracle.Tenancy),
		ImageId:         imageID,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}
	r, err := ac.computeClient.ListShapes(ctx, request)
	if err == nil && (r.Items == nil || len(r.Items) == 0) {
		err = errors.New("没有符合条件的Shape")
	}
	return r.Items, err
}

/* ------------------------------------------------------------------ 创建实例（含抢机重试） */

// 导出实例公共 IP 到文件
func (ac *AccountCtx) ListInstancesIPs(filePath string) {
	var vnicAttachments []core.VnicAttachment
	var vas []core.VnicAttachment
	var nextPage *string
	var err error
	for {
		vas, nextPage, err = ac.ListVnicAttachments(nil, nextPage)
		if err == nil {
			vnicAttachments = append(vnicAttachments, vas...)
		}
		if nextPage == nil || len(vas) == 0 {
			break
		}
	}

	if err != nil {
		ac.logf("ListVnicAttachments Error: %s\n", err.Error())
		return
	}
	file, err := os.OpenFile(filePath, os.O_APPEND|os.O_WRONLY, os.ModeAppend)
	if err != nil {
		ac.logf("打开文件失败, Error: %s\n", err.Error())
		return
	}
	defer file.Close()
	_, err = io.WriteString(file, "["+ac.Name+"]\n")
	if err != nil {
		ac.logf("%s\n", err.Error())
	}
	for _, vnicAttachment := range vnicAttachments {
		vnic, err := ac.GetVnic(vnicAttachment.VnicId)
		if err != nil {
			ac.logf("IP地址获取失败, %s\n", err.Error())
			continue
		}
		if vnic.PublicIp == nil {
			continue
		}
		ac.logf("[%s] 实例: %s, IP: %s\n", ac.Name, s(vnic.DisplayName), *vnic.PublicIp)
		_, err = io.WriteString(file, "实例: "+s(vnic.DisplayName)+", IP: "+*vnic.PublicIp+"\n")
		if err != nil {
			ac.logf("写入文件失败, Error: %s\n", err.Error())
		}
	}
	_, err = io.WriteString(file, "\n")
	if err != nil {
		ac.logf("%s\n", err.Error())
	}
}

// LaunchInstances 按模板批量创建实例（含抢机重试逻辑）。
// 返回值 sum: 创建实例总数; num: 创建成功的个数。
func (ac *AccountCtx) LaunchInstances(ads []identity.AvailabilityDomain, inst Instance) (sum, num int32) {
	if ac.stopped() {
		ac.logf("任务已取消，未开始创建\n")
		return inst.Sum, 0
	}
	/* 创建实例的几种情况
	 * 1. 设置了 availabilityDomain 参数，即在设置的可用性域中创建 sum 个实例。
	 * 2. 没有设置 availabilityDomain 但是设置了 each 参数。即在获取的每个可用性域中创建 each 个实例，创建的实例总数 sum =  each * adCount。
	 * 3. 没有设置 availabilityDomain 且没有设置 each 参数，即在获取到的可用性域中创建的实例总数为 sum。
	 */

	var adCount int32 = int32(len(ads))
	adName := common.String(inst.AvailabilityDomain)
	each := inst.Each
	sum = inst.Sum

	var usableAds = make([]identity.AvailabilityDomain, 0)

	var AD_NOT_FIXED bool = false
	var EACH_AD = false
	if adName == nil || *adName == "" {
		AD_NOT_FIXED = true
		if each > 0 {
			EACH_AD = true
			sum = each * adCount
		} else {
			EACH_AD = false
			usableAds = ads
		}
	}

	// 创建个数为 0/负数时直接返回，避免"任务成功但什么都没创建"。
	if sum <= 0 {
		ac.logf("\033[1;31m[%s] 创建个数为 %d，请检查模板的 sum/each 配置\033[0m\n", ac.Name, sum)
		return
	}

	name := inst.InstanceDisplayName
	if name == "" {
		name = time.Now().Format("instance-20060102-1504")
	}
	displayName := common.String(name)
	if sum > 1 {
		displayName = common.String(name + "-1")
	}

	request := core.LaunchInstanceRequest{}
	request.CompartmentId = common.String(ac.Oracle.Tenancy)
	request.DisplayName = displayName

	ac.logf("正在获取系统镜像...\n")
	image, err := ac.GetImage(inst)
	if err != nil {
		ac.logf("获取系统镜像失败: %s\n", err.Error())
		return
	}
	ac.logf("系统镜像: %s\n", s(image.DisplayName))

	var shape core.Shape
	if strings.Contains(strings.ToLower(inst.Shape), "flex") && inst.Ocpus > 0 && inst.MemoryInGBs > 0 {
		shape.Shape = &inst.Shape
		shape.Ocpus = &inst.Ocpus
		shape.MemoryInGBs = &inst.MemoryInGBs
	} else {
		ac.logf("正在获取Shape信息...\n")
		shape, err = ac.getShape(image.Id, inst.Shape)
		if err != nil {
			ac.logf("获取Shape信息失败: %s\n", err.Error())
			return
		}
	}

	request.Shape = shape.Shape
	// 安全读取规格字段（SDK 中均为可选指针），供后续日志与 Telegram 消息使用。
	shapeName := s(shape.Shape)
	shapeOcpusVal := f32(shape.Ocpus)
	shapeMemoryVal := f32(shape.MemoryInGBs)
	if strings.Contains(strings.ToLower(shapeName), "flex") {
		request.ShapeConfig = &core.LaunchInstanceShapeConfigDetails{
			Ocpus:       shape.Ocpus,
			MemoryInGBs: shape.MemoryInGBs,
		}
		if inst.Burstable == "1/8" {
			request.ShapeConfig.BaselineOcpuUtilization = core.LaunchInstanceShapeConfigDetailsBaselineOcpuUtilization8
		} else if inst.Burstable == "1/2" {
			request.ShapeConfig.BaselineOcpuUtilization = core.LaunchInstanceShapeConfigDetailsBaselineOcpuUtilization2
		}
	}

	ac.logf("正在获取子网...\n")
	subnet, err := ac.CreateOrGetNetworkInfrastructure(inst)
	if err != nil {
		ac.logf("获取子网失败: %s\n", err.Error())
		return
	}
	ac.logf("子网: %s\n", s(subnet.DisplayName))
	request.CreateVnicDetails = &core.CreateVnicDetails{SubnetId: subnet.Id}

	sd := core.InstanceSourceViaImageDetails{}
	sd.ImageId = image.Id
	if inst.BootVolumeSizeInGBs > 0 {
		sd.BootVolumeSizeInGBs = common.Int64(inst.BootVolumeSizeInGBs)
	}
	request.SourceDetails = sd
	request.IsPvEncryptionInTransitEnabled = common.Bool(true)

	metaData := map[string]string{}
	metaData["ssh_authorized_keys"] = inst.SSH_Public_Key
	if inst.CloudInit != "" {
		metaData["user_data"] = inst.CloudInit
	}
	request.Metadata = metaData

	minTime := inst.MinTime
	maxTime := inst.MaxTime

	SKIP_RETRY_MAP := make(map[int32]bool)
	var usableAdsTemp = make([]identity.AvailabilityDomain, 0)

	retry := inst.Retry
	var failTimes int32 = 0

	var runTimes int32 = 0

	var adIndex int32 = 0
	var pos int32 = 0
	var SUCCESS = false

	var startTime = time.Now()

	var bootVolumeSize float64
	if inst.BootVolumeSizeInGBs > 0 {
		bootVolumeSize = float64(inst.BootVolumeSizeInGBs)
	} else if image.SizeInMBs != nil {
		bootVolumeSize = math.Round(float64(*image.SizeInMBs) / float64(1024))
	}
	ac.logf("\033[1;36m[%s] 开始创建 %s 实例, OCPU: %g 内存: %g 引导卷: %g \033[0m\n", ac.Name, shapeName, shapeOcpusVal, shapeMemoryVal, bootVolumeSize)
	ac.progress("launch", 0, int(sum))
	if EACH {
		text := fmt.Sprintf("正在尝试创建第 %d 个实例...⏳\n账号: %s\n区域: %s\n实例配置: %s\nOCPU计数: %g\n内存(GB): %g\n引导卷(GB): %g\n创建个数: %d", pos+1, ac.Name, ac.Oracle.Region, shapeName, shapeOcpusVal, shapeMemoryVal, bootVolumeSize, sum)
		_, err := sendMessage("", text)
		if err != nil {
			ac.logf("Telegram 消息提醒发送失败: %s\n", err.Error())
		}
	}

	for pos < sum {

		// 每个实例开始前检查是否已被要求停止（Web 界面"停止任务"）。
		if ac.stopped() {
			ac.logf("\033[1;33m[%s] 任务已被中止，停止创建\033[0m\n", ac.Name)
			return
		}

		if AD_NOT_FIXED {
			if EACH_AD {
				if pos%each == 0 && failTimes == 0 {
					adName = ads[adIndex].Name
					adIndex++
				}
			} else {
				if SUCCESS {
					adIndex = 0
				}
				if adIndex >= adCount {
					adIndex = 0
				}
				adName = usableAds[adIndex].Name
				adIndex++
			}
		}

		runTimes++
		ac.logf("\033[1;36m[%s] 正在尝试创建第 %d 个实例, AD: %s\033[0m\n", ac.Name, pos+1, s(adName))
		ac.logf("\033[1;36m[%s] 当前尝试次数: %d \033[0m\n", ac.Name, runTimes)
		request.AvailabilityDomain = adName
		createResp, err := ac.computeClient.LaunchInstance(ctx, request)

		if err == nil {
			SUCCESS = true
			num++

			duration := fmtDuration(time.Since(startTime))

			ac.logf("\033[1;32m[%s] 第 %d 个实例抢到了🎉, 正在启动中请稍等...⌛️ \033[0m\n", ac.Name, pos+1)
			var msg Message
			var msgErr error
			var text string
			if EACH {
				text = fmt.Sprintf("第 %d 个实例抢到了🎉, 正在启动中请稍等...⌛️\n账号: %s\n区域: %s\n实例名称: %s\n公共IP: 获取中...⏳\n可用性域:%s\n实例配置: %s\nOCPU计数: %g\n内存(GB): %g\n引导卷(GB): %g\n创建个数: %d\n尝试次数: %d\n耗时: %s", pos+1, ac.Name, ac.Oracle.Region, s(createResp.Instance.DisplayName), s(createResp.Instance.AvailabilityDomain), shapeName, shapeOcpusVal, shapeMemoryVal, bootVolumeSize, sum, runTimes, duration)
				msg, msgErr = sendMessage("", text)
			}
			var strIps string
			ips, err := ac.getInstancePublicIps(createResp.Instance.Id)
			if err != nil {
				ac.logf("\033[1;32m[%s] 第 %d 个实例抢到了🎉, 但是启动失败❌ 错误信息: \033[0m%s\n", ac.Name, pos+1, err.Error())
				text = fmt.Sprintf("第 %d 个实例抢到了🎉, 但是启动失败❌实例已被终止😔\n账号: %s\n区域: %s\n实例名称: %s\n可用性域:%s\n实例配置: %s\nOCPU计数: %g\n内存(GB): %g\n引导卷(GB): %g\n创建个数: %d\n尝试次数: %d\n耗时: %s", pos+1, ac.Name, ac.Oracle.Region, s(createResp.Instance.DisplayName), s(createResp.Instance.AvailabilityDomain), shapeName, shapeOcpusVal, shapeMemoryVal, bootVolumeSize, sum, runTimes, duration)
			} else {
				strIps = strings.Join(ips, ",")
				ac.logf("\033[1;32m[%s] 第 %d 个实例抢到了🎉, 启动成功✅. 实例名称: %s, 公共IP: %s\033[0m\n", ac.Name, pos+1, s(createResp.Instance.DisplayName), strIps)
				text = fmt.Sprintf("第 %d 个实例抢到了🎉, 启动成功✅\n账号: %s\n区域: %s\n实例名称: %s\n公共IP: %s\n可用性域:%s\n实例配置: %s\nOCPU计数: %g\n内存(GB): %g\n引导卷(GB): %g\n创建个数: %d\n尝试次数: %d\n耗时: %s", pos+1, ac.Name, ac.Oracle.Region, s(createResp.Instance.DisplayName), strIps, s(createResp.Instance.AvailabilityDomain), shapeName, shapeOcpusVal, shapeMemoryVal, bootVolumeSize, sum, runTimes, duration)
			}
			if EACH {
				if msgErr != nil {
					sendMessage("", text)
				} else {
					editMessage(msg.MessageId, "", text)
				}
			}

			if !ac.sleepRandomSecond(minTime, maxTime) {
				ac.logf("\033[1;33m[%s] 任务已被中止，停止创建\033[0m\n", ac.Name)
				return
			}

			displayName = common.String(fmt.Sprintf("%s-%d", name, pos+1))
			request.DisplayName = displayName

		} else {
			SUCCESS = false
			errInfo := err.Error()
			SKIP_RETRY := false

			servErr, isServErr := common.IsServiceError(err)

			if isServErr && (400 <= servErr.GetHTTPStatusCode() && servErr.GetHTTPStatusCode() <= 405) ||
				(servErr.GetHTTPStatusCode() == 409 && !strings.EqualFold(servErr.GetCode(), "IncorrectState")) ||
				servErr.GetHTTPStatusCode() == 412 || servErr.GetHTTPStatusCode() == 413 || servErr.GetHTTPStatusCode() == 422 ||
				servErr.GetHTTPStatusCode() == 431 || servErr.GetHTTPStatusCode() == 501 {
				// 不可重试
				if isServErr {
					errInfo = servErr.GetMessage()
				}
				duration := fmtDuration(time.Since(startTime))
				ac.logf("\033[1;31m[%s] 第 %d 个实例创建失败了❌, 错误信息: \033[0m%s\n", ac.Name, pos+1, errInfo)
				if EACH {
					text := fmt.Sprintf("第 %d 个实例创建失败了❌\n错误信息: %s\n账号: %s\n区域: %s\n可用性域: %s\n实例配置: %s\nOCPU计数: %g\n内存(GB): %g\n引导卷(GB): %g\n创建个数: %d\n尝试次数: %d\n耗时:%s", pos+1, errInfo, ac.Name, ac.Oracle.Region, s(adName), shapeName, shapeOcpusVal, shapeMemoryVal, bootVolumeSize, sum, runTimes, duration)
					sendMessage("", text)
				}

				SKIP_RETRY = true
				if AD_NOT_FIXED && !EACH_AD {
					SKIP_RETRY_MAP[adIndex-1] = true
				}

			} else {
				// 可重试
				if isServErr {
					errInfo = servErr.GetMessage()
				}
				ac.logf("\033[1;31m[%s] 创建失败, Error: \033[0m%s\n", ac.Name, errInfo)

				SKIP_RETRY = false
				if AD_NOT_FIXED && !EACH_AD {
					SKIP_RETRY_MAP[adIndex-1] = false
				}
			}

			if !ac.sleepRandomSecond(minTime, maxTime) {
				ac.logf("\033[1;33m[%s] 任务已被中止，停止创建\033[0m\n", ac.Name)
				return
			}

			if AD_NOT_FIXED {
				if !EACH_AD {
					if adIndex < adCount {
						continue
					} else {
						failTimes++

						for index, skip := range SKIP_RETRY_MAP {
							if !skip {
								usableAdsTemp = append(usableAdsTemp, usableAds[index])
							}
						}

						usableAds = usableAdsTemp
						adCount = int32(len(usableAds))

						usableAdsTemp = nil
						for k := range SKIP_RETRY_MAP {
							delete(SKIP_RETRY_MAP, k)
						}

						if (retry < 0 || failTimes <= retry) && adCount > 0 {
							continue
						}
					}

					adIndex = 0

				} else {
					failTimes++
					if (retry < 0 || failTimes <= retry) && !SKIP_RETRY {
						continue
					}
				}

			} else {
				failTimes++
				if (retry < 0 || failTimes <= retry) && !SKIP_RETRY {
					continue
				}
			}
		}

		usableAds = ads
		adCount = int32(len(usableAds))
		usableAdsTemp = nil
		for k := range SKIP_RETRY_MAP {
			delete(SKIP_RETRY_MAP, k)
		}

		failTimes = 0
		runTimes = 0
		startTime = time.Now()

		pos++
		ac.progress("launch", int(pos), int(sum))

		if pos < sum && EACH {
			text := fmt.Sprintf("正在尝试创建第 %d 个实例...⏳\n账号: %s\n区域: %s\n实例配置: %s\nOCPU计数: %g\n内存(GB): %g\n引导卷(GB): %g\n创建个数: %d", pos+1, ac.Name, ac.Oracle.Region, shapeName, shapeOcpusVal, shapeMemoryVal, bootVolumeSize, sum)
			sendMessage("", text)
		}
	}
	return
}
