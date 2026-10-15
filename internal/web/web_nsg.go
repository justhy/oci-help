package web

import (
	"net/http"
	"oci-help/internal/oci"
)

/* ------------------------------------------------------------------ NSG */

func handleNsgs(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodGet) {
		return
	}
	ac, err := oci.GetAccount(r.URL.Query().Get("account"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	nsgs, err := ac.ListNsgs(r.URL.Query().Get("vcnId"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, "获取网络安全组失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"nsgs": nsgs})
}

func handleNsgRules(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodGet) {
		return
	}
	nsgID := r.URL.Query().Get("nsgId")
	if nsgID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 nsgId")
		return
	}
	ac, err := oci.GetAccount(r.URL.Query().Get("account"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	rules, err := ac.ListNsgRules(nsgID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "获取 NSG 规则失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"rules": rules})
}

func handleNsgVnics(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodGet) {
		return
	}
	nsgID := r.URL.Query().Get("nsgId")
	if nsgID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 nsgId")
		return
	}
	ac, err := oci.GetAccount(r.URL.Query().Get("account"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	vnics, err := ac.ListNsgVnics(nsgID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "获取 NSG 绑定的 VNIC 失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"vnicIds": vnics})
}

func handleNsgCreate(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodPost) {
		return
	}
	var req struct {
		Account string `json:"account"`
		VcnID   string `json:"vcnId"`
		Name    string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.VcnID == "" || req.Name == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 vcnId 或 name")
		return
	}
	ac, err := oci.GetAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	nsg, err := ac.CreateNsg(req.VcnID, req.Name)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"nsg": nsg})
}

func handleNsgDelete(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodPost) {
		return
	}
	var req struct {
		Account string `json:"account"`
		NsgID   string `json:"nsgId"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.NsgID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 nsgId")
		return
	}
	ac, err := oci.GetAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := ac.DeleteNsg(req.NsgID); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "网络安全组已删除"})
}

func handleNsgRuleAdd(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodPost) {
		return
	}
	var req struct {
		Account string          `json:"account"`
		NsgID   string          `json:"nsgId"`
		Rule    oci.NsgRuleSpec `json:"rule"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.NsgID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 nsgId")
		return
	}
	ac, err := oci.GetAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := ac.AddNsgRules(req.NsgID, []oci.NsgRuleSpec{req.Rule}); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "规则已添加"})
}

func handleNsgRuleUpdate(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodPost) {
		return
	}
	var req struct {
		Account string          `json:"account"`
		NsgID   string          `json:"nsgId"`
		RuleID  string          `json:"ruleId"`
		Rule    oci.NsgRuleSpec `json:"rule"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.NsgID == "" || req.RuleID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 nsgId 或 ruleId")
		return
	}
	ac, err := oci.GetAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := ac.UpdateNsgRule(req.NsgID, req.RuleID, req.Rule); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "规则已更新"})
}

func handleNsgRuleRemove(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodPost) {
		return
	}
	var req struct {
		Account string   `json:"account"`
		NsgID   string   `json:"nsgId"`
		RuleIDs []string `json:"ruleIds"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.NsgID == "" || len(req.RuleIDs) == 0 {
		writeErr(w, http.StatusBadRequest, "缺少参数 nsgId 或 ruleIds")
		return
	}
	ac, err := oci.GetAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := ac.RemoveNsgRules(req.NsgID, req.RuleIDs); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "规则已删除"})
}
