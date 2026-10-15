package web

import (
	"net/http"
	"oci-help/internal/oci"
)

/* ------------------------------------------------------------------ 安全列表 */

func handleSecurityRules(w http.ResponseWriter, r *http.Request) {
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
	ingress, egress, err := ac.ListSecurityRules(vcnID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "获取安全列表失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ingress": ingress, "egress": egress})
}

type securityRuleRequest struct {
	Account     string `json:"account"`
	VcnID       string `json:"vcnId"`
	Direction   string `json:"direction"`
	Protocol    string `json:"protocol"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	PortMin     int    `json:"portMin"`
	PortMax     int    `json:"portMax"`
	Description string `json:"description"`
}

func handleSecurityRuleAdd(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodPost) {
		return
	}
	var req securityRuleRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.VcnID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 vcnId")
		return
	}
	ac, err := oci.GetAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	spec := oci.SecurityRuleSpec{
		Direction:   req.Direction,
		Protocol:    req.Protocol,
		Source:      req.Source,
		Destination: req.Destination,
		PortMin:     req.PortMin,
		PortMax:     req.PortMax,
		Description: req.Description,
	}
	if err := ac.AddSecurityRules(req.VcnID, []oci.SecurityRuleSpec{spec}); err != nil {
		writeErr(w, http.StatusBadGateway, "新增规则失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "规则已添加"})
}

type securityRuleRemoveRequest struct {
	Account string `json:"account"`
	VcnID   string `json:"vcnId"`
	Ingress []int  `json:"ingress"`
	Egress  []int  `json:"egress"`
}

func handleSecurityRuleRemove(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodPost) {
		return
	}
	var req securityRuleRemoveRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.VcnID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 vcnId")
		return
	}
	ac, err := oci.GetAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := ac.RemoveSecurityRules(req.VcnID, req.Ingress, req.Egress); err != nil {
		writeErr(w, http.StatusBadGateway, "删除规则失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "规则已删除"})
}

// handleSecurityRelease 一键放行 VCN 安全列表的所有端口与协议。
func handleSecurityRelease(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodPost) {
		return
	}
	var req struct {
		Account string `json:"account"`
		VcnID   string `json:"vcnId"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.VcnID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 vcnId")
		return
	}
	ac, err := oci.GetAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := ac.ReleaseAllPorts(req.VcnID); err != nil {
		writeErr(w, http.StatusBadGateway, "放行失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "已放行所有端口与协议"})
}
