package web

import (
	"net/http"
	"oci-help/internal/oci"
)

/* ------------------------------------------------------------------ 引导卷 */

func handleBootVolumes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ac, err := oci.GetAccount(r.URL.Query().Get("account"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	volumes, err := ac.ListAllBootVolumes()
	if err != nil {
		writeErr(w, http.StatusBadGateway, "获取引导卷失败: "+err.Error())
		return
	}
	out := make([]bootVolumeView, 0, len(volumes))
	for i := range volumes {
		v := volumes[i]
		out = append(out, bootVolumeView{
			ID:                 oci.S(v.Id),
			DisplayName:        oci.S(v.DisplayName),
			State:              oci.BootVolumeStateName(v.LifecycleState),
			SizeInGBs:          oci.I64(v.SizeInGBs),
			VpusPerGB:          oci.I64(v.VpusPerGB),
			AvailabilityDomain: oci.S(v.AvailabilityDomain),
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"bootVolumes": out})
}

func handleBootVolume(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ac, err := oci.GetAccount(r.URL.Query().Get("account"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 id")
		return
	}
	bv, err := ac.GetBootVolume(&id)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "获取引导卷详情失败: "+err.Error())
		return
	}
	attachments, _ := ac.ListBootVolumeAttachments(bv.AvailabilityDomain, bv.CompartmentId, bv.Id)
	attViews := make([]bootVolumeAttachmentView, 0, len(attachments))
	for _, att := range attachments {
		name := ""
		if att.InstanceId != nil {
			if ins, err := ac.GetInstance(att.InstanceId); err == nil {
				name = oci.S(ins.DisplayName)
			}
		}
		attViews = append(attViews, bootVolumeAttachmentView{
			ID:           oci.S(att.Id),
			InstanceId:   oci.S(att.InstanceId),
			InstanceName: name,
			State:        string(att.LifecycleState),
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"bootVolume": bootVolumeView{
			ID:                 oci.S(bv.Id),
			DisplayName:        oci.S(bv.DisplayName),
			State:              oci.BootVolumeStateName(bv.LifecycleState),
			SizeInGBs:          oci.I64(bv.SizeInGBs),
			VpusPerGB:          oci.I64(bv.VpusPerGB),
			AvailabilityDomain: oci.S(bv.AvailabilityDomain),
		},
		"attachments": attViews,
	})
}

type bootVolumeUpdateRequest struct {
	Account   string `json:"account"`
	ID        string `json:"id"`
	SizeInGBs *int64 `json:"sizeInGBs"`
	VpusPerGB *int64 `json:"vpusPerGB"`
}

func handleBootVolumeUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req bootVolumeUpdateRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.ID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 id")
		return
	}
	if req.SizeInGBs == nil && req.VpusPerGB == nil {
		writeErr(w, http.StatusBadRequest, "请提供 sizeInGBs 或 vpusPerGB")
		return
	}
	ac, err := oci.GetAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := ac.UpdateBootVolume(&req.ID, req.SizeInGBs, req.VpusPerGB); err != nil {
		writeErr(w, http.StatusBadGateway, "修改引导卷失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "修改引导卷成功，请稍后查看状态"})
}

func handleBootVolumeDetach(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req bootVolumeUpdateRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.ID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 id")
		return
	}
	ac, err := oci.GetAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	bv, err := ac.GetBootVolume(&req.ID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "获取引导卷详情失败: "+err.Error())
		return
	}
	attachments, err := ac.ListBootVolumeAttachments(bv.AvailabilityDomain, bv.CompartmentId, bv.Id)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "获取引导卷附件失败: "+err.Error())
		return
	}
	if len(attachments) == 0 {
		writeJSON(w, http.StatusOK, map[string]string{"message": "引导卷未附加到任何实例"})
		return
	}
	for _, att := range attachments {
		if _, err := ac.DetachBootVolume(att.Id); err != nil {
			writeErr(w, http.StatusBadGateway, "分离引导卷失败: "+err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "分离引导卷成功，请稍后查看状态"})
}

func handleBootVolumeDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req bootVolumeUpdateRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.ID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 id")
		return
	}
	ac, err := oci.GetAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := ac.DeleteBootVolume(&req.ID); err != nil {
		writeErr(w, http.StatusBadGateway, "终止引导卷失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "终止引导卷成功，请稍后查看状态"})
}
