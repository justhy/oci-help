package oci

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"oci-help/internal/config"
	"time"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/core"
)

// RescueOptions 控制「自动救援/缩小硬盘」的行为。
type RescueOptions struct {
	DiskGB          int64  `json:"diskGB"`          // 新引导卷大小，默认 47
	RootPassword    string `json:"rootPassword"`    // 新系统 root 密码，留空则随机生成
	KeepBackup      bool   `json:"keepBackup"`      // 是否保留原引导卷备份
	OperatingSystem string `json:"operatingSystem"` // 默认 Canonical Ubuntu
	OSVersion       string `json:"osVersion"`       // 默认 20.04
}

// AutoRescue 通过「新建一台最小规格实例 → 克隆其引导卷 → 挂到目标实例」的方式，
// 实现救砖与缩小引导卷（OCI 引导卷只能扩容，缩小必须重建）。
//
// 步骤与参考项目一致：
//  1. 关机
//  2. 备份原引导卷
//  3. 分离原引导卷
//  4. 删除原引导卷
//  5. 新建 47GB 的 AMD 实例
//  6. 克隆新实例引导卷
//  7. 将克隆卷挂到目标实例
//  8. 终止新建实例（并可选删除备份）
//  9. 启动目标实例
func (ac *AccountCtx) AutoRescue(instanceID string, opts RescueOptions) error {
	if opts.DiskGB <= 0 {
		opts.DiskGB = 47
	}
	if opts.OperatingSystem == "" {
		opts.OperatingSystem = "Canonical Ubuntu"
	}
	if opts.OSVersion == "" {
		opts.OSVersion = "20.04"
	}
	if opts.RootPassword == "" {
		opts.RootPassword = randomPassword(12)
	}

	instance, err := ac.GetInstance(&instanceID)
	if err != nil {
		return fmt.Errorf("获取实例失败: %w", err)
	}
	name := S(instance.DisplayName)

	// 找到当前引导卷
	attachment, err := ac.bootVolumeAttachmentOfInstance(instanceID)
	if err != nil {
		return err
	}
	oldBV, err := ac.GetBootVolume(attachment.BootVolumeId)
	if err != nil {
		return fmt.Errorf("获取原引导卷失败: %w", err)
	}

	ac.logf("（1/9）⌛ 正在关机...\n")
	if _, err := ac.InstanceAction(&instanceID, core.InstanceActionActionStop); err != nil {
		return fmt.Errorf("关机失败: %w", err)
	}
	if err := ac.waitInstanceState(instanceID, core.InstanceLifecycleStateStopped); err != nil {
		return err
	}
	ac.logf("（1/9）✅ 关机成功\n")

	ac.logf("（2/9）⌛ 正在备份原引导卷...\n")
	bvBackup, err := ac.storageClient.CreateBootVolumeBackup(Ctx, core.CreateBootVolumeBackupRequest{
		CreateBootVolumeBackupDetails: core.CreateBootVolumeBackupDetails{
			Type:         core.CreateBootVolumeBackupDetailsTypeFull,
			BootVolumeId: oldBV.Id,
			DisplayName:  common.String("Old-BootVolume-Backup"),
		},
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	if err != nil {
		return fmt.Errorf("备份原引导卷失败: %w", err)
	}
	ac.logf("（2/9）✅ 备份原引导卷成功: %s\n", S(bvBackup.BootVolumeBackup.DisplayName))

	ac.logf("（3/9）⌛ 正在分离原引导卷...\n")
	if _, err := ac.computeClient.DetachBootVolume(Ctx, core.DetachBootVolumeRequest{
		BootVolumeAttachmentId: attachment.Id,
		RequestMetadata:        getCustomRequestMetadataWithRetryPolicy(),
	}); err != nil {
		return fmt.Errorf("分离原引导卷失败: %w", err)
	}
	ac.waitBootVolumeBackupAvailable(S(bvBackup.BootVolumeBackup.Id))
	ac.logf("（3/9）✅ 分离原引导卷成功\n")

	ac.logf("（4/9）⌛ 正在删除原引导卷...\n")
	if _, err := ac.storageClient.DeleteBootVolume(Ctx, core.DeleteBootVolumeRequest{
		BootVolumeId:    oldBV.Id,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}); err != nil {
		return fmt.Errorf("删除原引导卷失败: %w", err)
	}
	ac.waitBootVolumeTerminated(S(oldBV.Id))
	ac.logf("（4/9）✅ 删除原引导卷成功\n")

	// 原引导卷已删除：此后任一步骤失败，目标实例都会处于"无机盘"状态，
	// 只能靠备份手工恢复，因此错误信息里必须带上备份的名称与 OCID。
	recoverHint := func(step string, cause error) error {
		return fmt.Errorf("%s失败: %w（原引导卷已删除，目标实例可能无法启动，请用备份 %s [%s] 手工恢复）",
			step, cause, S(bvBackup.BootVolumeBackup.DisplayName), S(bvBackup.BootVolumeBackup.Id))
	}

	ac.logf("（5/9）⌛ 正在创建 %dGB 的 AMD 实例（约需数分钟）...\n", opts.DiskGB)
	tmpInst := config.Instance{
		Shape:                  "VM.Standard.E2.1.Micro",
		OperatingSystem:        opts.OperatingSystem,
		OperatingSystemVersion: opts.OSVersion,
		BootVolumeSizeInGBs:    opts.DiskGB,
		Sum:                    1,
		InstanceDisplayName:    fmt.Sprintf("rescue-tmp-%d", time.Now().Unix()),
		AvailabilityDomain:     S(instance.AvailabilityDomain),
		CloudInit:              RootPasswordCloudInit(opts.RootPassword),
	}
	newInst, err := ac.launchRescueInstance(tmpInst)
	if err != nil {
		return recoverHint("创建临时 AMD 实例", err)
	}
	ac.logf("（5/9）✅ 临时实例创建成功: %s\n", S(newInst.DisplayName))

	// 克隆临时实例引导卷
	ac.logf("（6/9）⌛ 正在克隆临时实例引导卷...\n")
	tmpAttachment, err := ac.bootVolumeAttachmentOfInstance(S(newInst.Id))
	if err != nil {
		return err
	}
	tmpBV, err := ac.GetBootVolume(tmpAttachment.BootVolumeId)
	if err != nil {
		return fmt.Errorf("获取临时实例引导卷失败: %w", err)
	}
	cloneResp, err := ac.storageClient.CreateBootVolume(Ctx, core.CreateBootVolumeRequest{
		CreateBootVolumeDetails: core.CreateBootVolumeDetails{
			CompartmentId:      common.String(ac.Oracle.Tenancy),
			AvailabilityDomain: oldBV.AvailabilityDomain,
			DisplayName:        common.String("Cloned-Boot-Volume"),
			SourceDetails: core.BootVolumeSourceFromBootVolumeDetails{
				Id: tmpBV.Id,
			},
		},
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	if err != nil {
		return recoverHint("克隆引导卷", err)
	}
	cloneBV := cloneResp.BootVolume
	ac.waitBootVolumeAvailable(S(cloneBV.Id))
	ac.logf("（6/9）✅ 克隆引导卷成功\n")

	ac.logf("（7/9）⌛ 正在将克隆卷挂载到目标实例...\n")
	if _, err := ac.computeClient.AttachBootVolume(Ctx, core.AttachBootVolumeRequest{
		AttachBootVolumeDetails: core.AttachBootVolumeDetails{
			DisplayName:  common.String("New-Boot-Volume"),
			BootVolumeId: cloneBV.Id,
			InstanceId:   &instanceID,
		},
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}); err != nil {
		return recoverHint("挂载克隆卷", err)
	}
	ac.logf("（7/9）✅ 挂载克隆卷成功\n")

	ac.logf("（8/9）⌛ 正在清理临时实例...\n")
	if err := ac.TerminateInstance(newInst.Id); err != nil {
		ac.logf("（8/9）⚠️ 终止临时实例失败(忽略): %v\n", err)
	}
	if !opts.KeepBackup {
		if _, err := ac.storageClient.DeleteBootVolumeBackup(Ctx, core.DeleteBootVolumeBackupRequest{
			BootVolumeBackupId: bvBackup.BootVolumeBackup.Id,
			RequestMetadata:    getCustomRequestMetadataWithRetryPolicy(),
		}); err != nil {
			ac.logf("（8/9）⚠️ 删除备份失败(忽略): %v\n", err)
		}
	}
	ac.logf("（8/9）✅ 清理完成\n")

	ac.logf("（9/9）⌛ 正在启动目标实例...\n")
	if _, err := ac.InstanceAction(&instanceID, core.InstanceActionActionStart); err != nil {
		return fmt.Errorf("启动实例失败: %w", err)
	}
	if err := ac.waitInstanceState(instanceID, core.InstanceLifecycleStateRunning); err != nil {
		return err
	}
	ips, _ := ac.getInstancePublicIps(&instanceID)
	ac.logf("（9/9）🎉 实例 %s 救援成功！公网IP: %s, SSH 账号 root, 密码: %s\n",
		name, joinStrings(ips), opts.RootPassword)
	return nil
}

/* ------------------------------------------------------------------ 内部辅助 */

// launchRescueInstance 启动单台实例并等待其进入 RUNNING，返回实例对象。
func (ac *AccountCtx) launchRescueInstance(inst config.Instance) (core.Instance, error) {
	image, err := ac.GetImage(inst)
	if err != nil {
		return core.Instance{}, fmt.Errorf("获取镜像失败: %w", err)
	}
	shape, err := ac.getShape(image.Id, inst.Shape)
	if err != nil {
		return core.Instance{}, fmt.Errorf("获取规格失败: %w", err)
	}
	subnet, err := ac.CreateOrGetNetworkInfrastructure(inst)
	if err != nil {
		return core.Instance{}, fmt.Errorf("获取子网失败: %w", err)
	}

	metaData := map[string]string{}
	if inst.SSH_Public_Key != "" {
		metaData["ssh_authorized_keys"] = inst.SSH_Public_Key
	}
	if inst.CloudInit != "" {
		metaData["user_data"] = inst.CloudInit
	}

	req := core.LaunchInstanceRequest{
		LaunchInstanceDetails: core.LaunchInstanceDetails{
			CompartmentId: common.String(ac.Oracle.Tenancy),
			DisplayName:   common.String(inst.InstanceDisplayName),
			Shape:         shape.Shape,
			SourceDetails: core.InstanceSourceViaImageDetails{
				ImageId:             image.Id,
				BootVolumeSizeInGBs: common.Int64(inst.BootVolumeSizeInGBs),
			},
			CreateVnicDetails: &core.CreateVnicDetails{SubnetId: subnet.Id},
			Metadata:          metaData,
		},
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}
	if inst.AvailabilityDomain != "" {
		req.LaunchInstanceDetails.AvailabilityDomain = common.String(inst.AvailabilityDomain)
	}

	var resp core.LaunchInstanceResponse
	var lastErr error
	for attempt := 0; attempt < 20; attempt++ {
		resp, lastErr = ac.computeClient.LaunchInstance(Ctx, req)
		if lastErr == nil {
			break
		}
		time.Sleep(10 * time.Second)
	}
	if lastErr != nil {
		return core.Instance{}, lastErr
	}
	ins := resp.Instance
	if ins.Id == nil {
		return core.Instance{}, errors.New("创建实例未返回 ID")
	}
	if err := ac.waitInstanceState(S(ins.Id), core.InstanceLifecycleStateRunning); err != nil {
		return ins, err
	}
	return ins, nil
}

// bootVolumeAttachmentOfInstance 返回实例的引导卷挂载信息。
func (ac *AccountCtx) bootVolumeAttachmentOfInstance(instanceID string) (core.BootVolumeAttachment, error) {
	var page *string
	for {
		resp, err := ac.computeClient.ListBootVolumeAttachments(Ctx, core.ListBootVolumeAttachmentsRequest{
			CompartmentId:   common.String(ac.Oracle.Tenancy),
			InstanceId:      &instanceID,
			Page:            page,
			RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
		})
		if err != nil {
			return core.BootVolumeAttachment{}, fmt.Errorf("获取引导卷挂载信息失败: %w", err)
		}
		for _, a := range resp.Items {
			if a.LifecycleState == core.BootVolumeAttachmentLifecycleStateAttached {
				return a, nil
			}
		}
		if resp.OpcNextPage == nil || len(resp.Items) == 0 {
			// 没有 ATTACHED 的也返回第一条，交由调用方判断
			if len(resp.Items) > 0 {
				return resp.Items[0], nil
			}
			break
		}
		page = resp.OpcNextPage
	}
	return core.BootVolumeAttachment{}, errors.New("实例没有可用的引导卷")
}

func (ac *AccountCtx) waitInstanceState(instanceID string, want core.InstanceLifecycleStateEnum) error {
	for i := 0; i < 300; i++ {
		ins, err := ac.GetInstance(&instanceID)
		if err == nil && ins.LifecycleState == want {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("等待实例进入 %s 超时", want)
}

func (ac *AccountCtx) waitBootVolumeAvailable(id string) {
	for i := 0; i < 300; i++ {
		resp, err := ac.storageClient.GetBootVolume(Ctx, core.GetBootVolumeRequest{
			BootVolumeId:    &id,
			RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
		})
		if err == nil && resp.BootVolume.LifecycleState == core.BootVolumeLifecycleStateAvailable {
			return
		}
		time.Sleep(time.Second)
	}
}

func (ac *AccountCtx) waitBootVolumeTerminated(id string) {
	for i := 0; i < 300; i++ {
		resp, err := ac.storageClient.GetBootVolume(Ctx, core.GetBootVolumeRequest{
			BootVolumeId:    &id,
			RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
		})
		if err != nil || resp.BootVolume.LifecycleState == core.BootVolumeLifecycleStateTerminated {
			return
		}
		time.Sleep(time.Second)
	}
}

func (ac *AccountCtx) waitBootVolumeBackupAvailable(id string) {
	for i := 0; i < 300; i++ {
		resp, err := ac.storageClient.GetBootVolumeBackup(Ctx, core.GetBootVolumeBackupRequest{
			BootVolumeBackupId: &id,
			RequestMetadata:    getCustomRequestMetadataWithRetryPolicy(),
		})
		if err == nil && resp.BootVolumeBackup.LifecycleState == core.BootVolumeBackupLifecycleStateAvailable {
			return
		}
		time.Sleep(time.Second)
	}
}

// RootPasswordCloudInit 生成设置 root 密码并允许密码登录的 cloud-init。
func RootPasswordCloudInit(password string) string {
	return "#cloud-config\n" +
		"ssh_pwauth: true\n" +
		"disable_root: false\n" +
		"chpasswd:\n" +
		"  expire: false\n" +
		"  list: |\n" +
		"    root:" + password + "\n"
}

func randomPassword(n int) string {
	const chars = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, n)
	for i := range b {
		v, err := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		if err != nil {
			b[i] = chars[time.Now().UnixNano()%int64(len(chars))]
			continue
		}
		b[i] = chars[v.Int64()]
	}
	return string(b)
}

func joinStrings(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}
