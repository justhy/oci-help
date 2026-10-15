package web

import (
	"net/http"
	"oci-help/internal/oci"
)

/* ------------------------------------------------------------------ 路由表 / 安全列表清单 */

func handleRouteTables(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodGet) {
		return
	}
	vcnID := r.URL.Query().Get("vcnId")
	if vcnID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 vcnId")
		return
	}
	ac, err := oci.GetAccount(r.URL.Query().Get("account"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	items, err := ac.ListRouteTableRefs(vcnID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "获取路由表失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"routeTables": items})
}

func handleSecurityLists(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodGet) {
		return
	}
	vcnID := r.URL.Query().Get("vcnId")
	if vcnID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 vcnId")
		return
	}
	ac, err := oci.GetAccount(r.URL.Query().Get("account"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	items, err := ac.ListSecurityListRefs(vcnID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "获取安全列表失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"securityLists": items})
}
