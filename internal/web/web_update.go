package web

import (
	"net/http"
	"oci-help/internal/updater"
)

/* ------------------------------------------------------------------ 一键自动更新 */

func handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodGet) {
		return
	}
	res, err := updater.CheckUpdate()
	if err != nil {
		writeErr(w, http.StatusBadGateway, "检查更新失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"update": res})
}

func handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodPost) {
		return
	}
	if err := updater.ApplyUpdate(); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "更新已下载，服务正在重启..."})
}
