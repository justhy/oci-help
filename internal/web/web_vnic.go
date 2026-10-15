package web

import (
	"net/http"
	"oci-help/internal/job"
	"oci-help/internal/oci"
)

/* ------------------------------------------------------------------ VNIC */

func handleVnicList(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodGet) {
		return
	}
	instanceID := r.URL.Query().Get("instanceId")
	if instanceID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 instanceId")
		return
	}
	ac, err := oci.GetAccount(r.URL.Query().Get("account"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	vnics, err := ac.ListVnicViews(instanceID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "获取 VNIC 失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"vnics": vnics})
}

func handleVnicAttach(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodPost) {
		return
	}
	var req struct {
		Account        string   `json:"account"`
		InstanceID     string   `json:"instanceId"`
		SubnetID       string   `json:"subnetId"`
		DisplayName    string   `json:"displayName"`
		AssignPublicIP bool     `json:"assignPublicIp"`
		HostnameLabel  string   `json:"hostnameLabel"`
		PrivateIP      string   `json:"privateIp"`
		NsgIds         []string `json:"nsgIds"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.InstanceID == "" || req.SubnetID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 instanceId 或 subnetId")
		return
	}
	ac, err := oci.GetAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	opts := oci.AttachVnicOptions{
		InstanceID:     req.InstanceID,
		SubnetID:       req.SubnetID,
		DisplayName:    req.DisplayName,
		AssignPublicIP: req.AssignPublicIP,
		HostnameLabel:  req.HostnameLabel,
		PrivateIP:      req.PrivateIP,
		NsgIds:         req.NsgIds,
	}
	job := startActionJob("vnic-attach", ac.Name, ac, func(jb *job.Job, ac *oci.AccountCtx) error {
		jb.Log("正在为实例附加辅助 VNIC（子网: %s）...", opts.SubnetID)
		attachmentID, vnicID, err := ac.AttachSecondaryVnic(opts)
		if err != nil {
			return err
		}
		jb.Log("VNIC 附加成功: attachment=%s vnic=%s", attachmentID, vnicID)
		return nil
	})
	writeJSON(w, http.StatusOK, map[string]string{"jobId": job.ID})
}

func handleVnicDetach(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodPost) {
		return
	}
	var req struct {
		Account      string `json:"account"`
		AttachmentID string `json:"attachmentId"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.AttachmentID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 attachmentId")
		return
	}
	ac, err := oci.GetAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	job := startActionJob("vnic-detach", ac.Name, ac, func(jb *job.Job, ac *oci.AccountCtx) error {
		jb.Log("正在分离 VNIC（attachment=%s）...", req.AttachmentID)
		return ac.DetachVnic(req.AttachmentID)
	})
	writeJSON(w, http.StatusOK, map[string]string{"jobId": job.ID})
}

func handleVnicUpdate(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodPost) {
		return
	}
	var req struct {
		Account             string   `json:"account"`
		VnicID              string   `json:"vnicId"`
		DisplayName         string   `json:"displayName"`
		HostnameLabel       string   `json:"hostnameLabel"`
		RouteTableID        string   `json:"routeTableId"`
		NsgIds              []string `json:"nsgIds"`
		SkipSourceDestCheck *bool    `json:"skipSourceDestCheck"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.VnicID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 vnicId")
		return
	}
	ac, err := oci.GetAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	_, err = ac.UpdateVnicAttrs(oci.VnicUpdateOptions{
		VnicID:              req.VnicID,
		DisplayName:         req.DisplayName,
		HostnameLabel:       req.HostnameLabel,
		RouteTableID:        req.RouteTableID,
		NsgIds:              req.NsgIds,
		SkipSourceDestCheck: req.SkipSourceDestCheck,
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "VNIC 已更新"})
}

func handleVnicPrivateIps(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodGet) {
		return
	}
	vnicID := r.URL.Query().Get("vnicId")
	if vnicID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 vnicId")
		return
	}
	ac, err := oci.GetAccount(r.URL.Query().Get("account"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ips, err := ac.ListPrivateIpsOfVnic(vnicID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "获取私有 IP 失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"privateIps": ips})
}

func handleVnicPrivateIpAdd(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodPost) {
		return
	}
	var req struct {
		Account       string `json:"account"`
		VnicID        string `json:"vnicId"`
		IPAddress     string `json:"ipAddress"`
		HostnameLabel string `json:"hostnameLabel"`
		DisplayName   string `json:"displayName"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.VnicID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 vnicId")
		return
	}
	ac, err := oci.GetAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ip, err := ac.AddSecondaryPrivateIp(req.VnicID, req.IPAddress, req.HostnameLabel, req.DisplayName)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"privateIp": ip})
}

func handleVnicPrivateIpDelete(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodPost) {
		return
	}
	var req struct {
		Account     string `json:"account"`
		PrivateIpID string `json:"privateIpId"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.PrivateIpID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 privateIpId")
		return
	}
	ac, err := oci.GetAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := ac.DeletePrivateIp(req.PrivateIpID); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "私有 IP 已删除"})
}

func handleVnicPrivateIpUpdate(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodPost) {
		return
	}
	var req struct {
		Account       string `json:"account"`
		PrivateIpID   string `json:"privateIpId"`
		HostnameLabel string `json:"hostnameLabel"`
		TargetVnicID  string `json:"targetVnicId"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.PrivateIpID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 privateIpId")
		return
	}
	ac, err := oci.GetAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := ac.UpdatePrivateIp(req.PrivateIpID, req.HostnameLabel, req.TargetVnicID); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "私有 IP 已更新"})
}

func handleVnicPublicIpReserve(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodPost) {
		return
	}
	var req struct {
		Account     string `json:"account"`
		PrivateIpID string `json:"privateIpId"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.PrivateIpID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 privateIpId")
		return
	}
	ac, err := oci.GetAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ip, err := ac.ReservePublicIp(req.PrivateIpID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "已保留公网 IP: " + ip, "publicIp": ip})
}

func handleVnicPublicIpRelease(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodPost) {
		return
	}
	var req struct {
		Account    string `json:"account"`
		PublicIpID string `json:"publicIpId"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.PublicIpID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 publicIpId")
		return
	}
	ac, err := oci.GetAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := ac.ReleasePublicIp(req.PublicIpID); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "公网 IP 已释放"})
}
