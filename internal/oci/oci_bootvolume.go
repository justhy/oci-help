package oci

import (
	"net/http"
	"sync"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/core"
)

/* ------------------------------------------------------------------ 引导卷 */

// 列出引导卷（单个可用性域）
func (ac *AccountCtx) getBootVolumes(availabilityDomain *string) ([]core.BootVolume, error) {
	req := core.ListBootVolumesRequest{
		AvailabilityDomain: availabilityDomain,
		CompartmentId:      common.String(ac.Oracle.Tenancy),
		RequestMetadata:    getCustomRequestMetadataWithRetryPolicy(),
	}
	resp, err := ac.storageClient.ListBootVolumes(Ctx, req)
	return resp.Items, err
}

// ListAllBootVolumes 并发遍历所有可用性域，汇总引导卷。
func (ac *AccountCtx) ListAllBootVolumes() ([]core.BootVolume, error) {
	ads, err := ac.EnsureAvailabilityDomains()
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
func (ac *AccountCtx) GetBootVolume(bootVolumeId *string) (core.BootVolume, error) {
	req := core.GetBootVolumeRequest{
		BootVolumeId:    bootVolumeId,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}
	resp, err := ac.storageClient.GetBootVolume(Ctx, req)
	return resp.BootVolume, err
}

// 更新引导卷
func (ac *AccountCtx) UpdateBootVolume(bootVolumeId *string, sizeInGBs *int64, vpusPerGB *int64) (core.BootVolume, error) {
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
	resp, err := ac.storageClient.UpdateBootVolume(Ctx, req)
	return resp.BootVolume, err
}

// 删除引导卷
func (ac *AccountCtx) DeleteBootVolume(bootVolumeId *string) (*http.Response, error) {
	req := core.DeleteBootVolumeRequest{
		BootVolumeId:    bootVolumeId,
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	}
	resp, err := ac.storageClient.DeleteBootVolume(Ctx, req)
	return resp.RawResponse, err
}

// 分离引导卷
func (ac *AccountCtx) DetachBootVolume(bootVolumeAttachmentId *string) (*http.Response, error) {
	req := core.DetachBootVolumeRequest{
		BootVolumeAttachmentId: bootVolumeAttachmentId,
		RequestMetadata:        getCustomRequestMetadataWithRetryPolicy(),
	}
	resp, err := ac.computeClient.DetachBootVolume(Ctx, req)
	return resp.RawResponse, err
}

// 获取引导卷附件
func (ac *AccountCtx) ListBootVolumeAttachments(availabilityDomain, compartmentId, bootVolumeId *string) ([]core.BootVolumeAttachment, error) {
	req := core.ListBootVolumeAttachmentsRequest{
		AvailabilityDomain: availabilityDomain,
		CompartmentId:      compartmentId,
		BootVolumeId:       bootVolumeId,
		RequestMetadata:    getCustomRequestMetadataWithRetryPolicy(),
	}
	resp, err := ac.computeClient.ListBootVolumeAttachments(Ctx, req)
	return resp.Items, err
}
