package web

import (
	"net/http"
	"oci-help/internal/job"
	"oci-help/internal/oci"
)

/* ------------------------------------------------------------------ VCN / IPv6 */

// handleVcns 返回账号下的全部 VCN。
func handleVcns(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodGet) {
		return
	}
	ac, err := oci.GetAccount(r.URL.Query().Get("account"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	vcns, err := ac.ListVcns()
	if err != nil {
		writeErr(w, http.StatusBadGateway, "获取 VCN 失败: "+err.Error())
		return
	}
	type vcnView struct {
		ID                  string `json:"id"`
		DisplayName         string `json:"displayName"`
		Ipv6Enabled         bool   `json:"ipv6Enabled"`
		DefaultSecurityList string `json:"defaultSecurityListId"`
	}
	out := make([]vcnView, 0, len(vcns))
	for i := range vcns {
		out = append(out, vcnView{
			ID:                  oci.S(vcns[i].Id),
			DisplayName:         oci.S(vcns[i].DisplayName),
			Ipv6Enabled:         len(vcns[i].Ipv6CidrBlocks) > 0,
			DefaultSecurityList: oci.S(vcns[i].DefaultSecurityListId),
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"vcns": out})
}

// handleIpv6Attach 为实例附加 IPv6（后台任务）。
func handleIpv6Attach(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodPost) {
		return
	}
	var req actionRequest
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
	job := startActionJob("ipv6", ac.Name, ac, func(jb *job.Job, ac *oci.AccountCtx) error {
		jb.Log("正在为实例附加 IPv6...")
		addr, err := ac.AttachIpv6(req.ID)
		if err != nil {
			return err
		}
		jb.Log("IPv6 附加成功: %s", addr)
		return nil
	})
	writeJSON(w, http.StatusOK, map[string]string{"jobId": job.ID})
}
