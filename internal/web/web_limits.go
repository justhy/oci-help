package web

import (
	"net/http"
	"oci-help/internal/oci"
)

/* ------------------------------------------------------------------ 配额查询 */

func handleLimitServices(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodGet) {
		return
	}
	ac, err := oci.GetAccount(r.URL.Query().Get("account"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	services, err := ac.ListLimitServices()
	if err != nil {
		writeErr(w, http.StatusBadGateway, "获取服务列表失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"services": services})
}

func handleLimits(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodGet) {
		return
	}
	ac, err := oci.GetAccount(r.URL.Query().Get("account"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	items, err := ac.GetLimits(r.URL.Query().Get("service"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, "查询配额失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"limits": items})
}
