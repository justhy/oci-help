package web

import (
	"net/http"
	"oci-help/internal/job"
	"oci-help/internal/oci"
)

/* ------------------------------------------------------------------ 500Mbps */

type instanceOpRequest struct {
	Account     string `json:"account"`
	ID          string `json:"id"`
	SSHPort     int    `json:"sshPort"`
	RetainNatGw bool   `json:"retainNatGw"`
	RetainNlb   bool   `json:"retainNlb"`
}

func handle500MEnable(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodPost) {
		return
	}
	var req instanceOpRequest
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
	port := req.SSHPort
	if port <= 0 {
		port = 22
	}
	job := startActionJob("500m", ac.Name, ac, func(jb *job.Job, ac *oci.AccountCtx) error {
		jb.Log("开始为实例开启下行 500Mbps（NAT 网关 + 路由表 + 网络负载均衡器）...")
		ip, err := ac.Enable500M(req.ID, port)
		if err != nil {
			return err
		}
		jb.Log("已开启下行 500Mbps，公网IP: %s", ip)
		return nil
	})
	writeJSON(w, http.StatusOK, map[string]string{"jobId": job.ID})
}

func handle500MDisable(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodPost) {
		return
	}
	var req instanceOpRequest
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
	job := startActionJob("500m-off", ac.Name, ac, func(jb *job.Job, ac *oci.AccountCtx) error {
		jb.Log("正在关闭下行 500Mbps...")
		return ac.Disable500M(req.ID, req.RetainNatGw, req.RetainNlb)
	})
	writeJSON(w, http.StatusOK, map[string]string{"jobId": job.ID})
}
