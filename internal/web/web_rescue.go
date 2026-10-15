package web

import (
	"net/http"
	"oci-help/internal/job"
	"oci-help/internal/oci"
)

/* ------------------------------------------------------------------ 自动救援 */

func handleRescue(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodPost) {
		return
	}
	var req struct {
		Account         string `json:"account"`
		ID              string `json:"id"`
		DiskGB          int64  `json:"diskGB"`
		RootPassword    string `json:"rootPassword"`
		KeepBackup      bool   `json:"keepBackup"`
		OperatingSystem string `json:"operatingSystem"`
		OSVersion       string `json:"osVersion"`
	}
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
	opts := oci.RescueOptions{
		DiskGB:          req.DiskGB,
		RootPassword:    req.RootPassword,
		KeepBackup:      req.KeepBackup,
		OperatingSystem: req.OperatingSystem,
		OSVersion:       req.OSVersion,
	}
	job := startActionJob("rescue", ac.Name, ac, func(jb *job.Job, ac *oci.AccountCtx) error {
		return ac.AutoRescue(req.ID, opts)
	})
	writeJSON(w, http.StatusOK, map[string]string{"jobId": job.ID})
}
