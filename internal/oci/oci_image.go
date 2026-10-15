package oci

import (
	"errors"
	"fmt"
	"oci-help/internal/config"
	"strings"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/core"
)

/* ------------------------------------------------------------------ 镜像 / Shape */

// 获取符合条件系统镜像中的第一个
func (ac *AccountCtx) GetImage(inst config.Instance) (image core.Image, err error) {
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
func (ac *AccountCtx) listImages(inst config.Instance) ([]core.Image, error) {
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
	r, err := ac.computeClient.ListImages(Ctx, request)
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
	r, err := ac.computeClient.ListShapes(Ctx, request)
	if err == nil && (r.Items == nil || len(r.Items) == 0) {
		err = errors.New("没有符合条件的Shape")
	}
	return r.Items, err
}
