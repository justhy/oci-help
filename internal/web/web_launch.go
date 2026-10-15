package web

import (
	"net/http"
	"oci-help/internal/config"
	"oci-help/internal/job"
	"oci-help/internal/oci"

	"gopkg.in/ini.v1"
)

/* ------------------------------------------------------------------ 模板 / 创建实例 */

func handleTemplates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	// 模版只来自 oci-help.ini，无需构造 OCI 客户端（凭证无效时也能读取）。
	name := r.URL.Query().Get("account")
	tenant := oci.FindTenant(name)
	if tenant == nil {
		if name == "" {
			writeErr(w, http.StatusBadRequest, "请指定账号")
		} else {
			writeErr(w, http.StatusBadRequest, "账号不存在: "+name)
		}
		return
	}
	sections := config.C.InstanceTemplates(tenant.IniSec)
	out := make([]templateView, 0, len(sections))
	for _, sec := range sections {
		inst := config.Instance{}
		if err := sec.MapTo(&inst); err != nil {
			continue
		}
		out = append(out, templateView{
			Name:                   sec.Name(),
			Shape:                  inst.Shape,
			Ocpus:                  inst.Ocpus,
			MemoryInGBs:            inst.MemoryInGBs,
			BootVolumeSizeInGBs:    inst.BootVolumeSizeInGBs,
			Sum:                    inst.Sum,
			Each:                   inst.Each,
			Retry:                  inst.Retry,
			AvailabilityDomain:     inst.AvailabilityDomain,
			OperatingSystem:        inst.OperatingSystem,
			OperatingSystemVersion: inst.OperatingSystemVersion,
			SSHPublicKey:           inst.SSH_Public_Key,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"templates": out})
}

type launchRequest struct {
	Account             string   `json:"account"`
	Template            string   `json:"template"`
	Sum                 *int32   `json:"sum"`
	Each                *int32   `json:"each"`
	Retry               *int32   `json:"retry"`
	Ocpus               *float32 `json:"ocpus"`
	MemoryInGBs         *float32 `json:"memoryInGBs"`
	BootVolumeSizeInGBs *int64   `json:"bootVolumeSizeInGBs"`
	AvailabilityDomain  *string  `json:"availabilityDomain"`
	InstanceDisplayName *string  `json:"instanceDisplayName"`
	SSHPublicKey        *string  `json:"sshPublicKey"`
}

func handleLaunch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req launchRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	ac, err := oci.GetAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var target *ini.Section
	for _, sec := range ac.Templates() {
		if sec.Name() == req.Template {
			target = sec
			break
		}
	}
	if target == nil {
		writeErr(w, http.StatusBadRequest, "实例模版不存在: "+req.Template)
		return
	}
	inst := config.Instance{}
	if err := target.MapTo(&inst); err != nil {
		writeErr(w, http.StatusBadRequest, "解析实例模版失败: "+err.Error())
		return
	}
	// 应用前端覆盖参数
	if req.Sum != nil {
		inst.Sum = *req.Sum
	}
	if req.Each != nil {
		inst.Each = *req.Each
	}
	if req.Retry != nil {
		inst.Retry = *req.Retry
	}
	if req.Ocpus != nil {
		inst.Ocpus = *req.Ocpus
	}
	if req.MemoryInGBs != nil {
		inst.MemoryInGBs = *req.MemoryInGBs
	}
	if req.BootVolumeSizeInGBs != nil {
		inst.BootVolumeSizeInGBs = *req.BootVolumeSizeInGBs
	}
	if req.AvailabilityDomain != nil {
		inst.AvailabilityDomain = *req.AvailabilityDomain
	}
	if req.InstanceDisplayName != nil {
		inst.InstanceDisplayName = *req.InstanceDisplayName
	}
	if req.SSHPublicKey != nil && *req.SSHPublicKey != "" {
		inst.SSH_Public_Key = *req.SSHPublicKey
	}

	// 校验创建个数，避免 sum/each 为 0 或负数时任务"成功"却什么都没创建。
	if inst.Sum < 0 || inst.Each < 0 {
		writeErr(w, http.StatusBadRequest, "sum 与 each 不能为负数")
		return
	}
	if inst.Sum == 0 && inst.Each == 0 {
		writeErr(w, http.StatusBadRequest, "创建个数必须大于 0：请设置 sum（总个数）或 each（每个可用性域个数）")
		return
	}
	// 指定了 availabilityDomain 时会在该域内创建 sum 个，each 不再生效；显式拒绝而非静默忽略。
	if inst.AvailabilityDomain != "" && inst.Each > 0 {
		writeErr(w, http.StatusBadRequest, "已指定 availabilityDomain 时 each 不生效：请在指定可用性域中用 sum 指定创建个数")
		return
	}
	if inst.Retry < -1 {
		writeErr(w, http.StatusBadRequest, "retry 不能小于 -1（-1 表示一直重试）")
		return
	}

	jb := job.StartLaunchJob(ac, inst)
	writeJSON(w, http.StatusOK, map[string]interface{}{"jobId": jb.ID})
}
