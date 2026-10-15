package web

import (
	"net/http"
	"oci-help/internal/oci"
)

/* ------------------------------------------------------------------ 租户管理 */

func handleTenantInfo(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodGet) {
		return
	}
	ac, err := oci.GetAccount(r.URL.Query().Get("account"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	info, err := ac.GetTenantInfo()
	if err != nil {
		writeErr(w, http.StatusBadGateway, "获取租户信息失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"tenant": info})
}

func handleTenantApiKeys(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodGet) {
		return
	}
	ac, err := oci.GetAccount(r.URL.Query().Get("account"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	userID := r.URL.Query().Get("userId")
	if userID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 userId")
		return
	}
	keys, err := ac.ListApiKeys(userID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "获取 API 密钥失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"apiKeys": keys})
}

func handleTenantMfa(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodGet) {
		return
	}
	ac, err := oci.GetAccount(r.URL.Query().Get("account"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	userID := r.URL.Query().Get("userId")
	if userID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 userId")
		return
	}
	devices, err := ac.ListMfaDevices(userID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "获取 MFA 设备失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"mfaDevices": devices})
}

func handleTenantDeleteApiKey(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodPost) {
		return
	}
	var req struct {
		Account     string `json:"account"`
		UserID      string `json:"userId"`
		Fingerprint string `json:"fingerprint"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	ac, err := oci.GetAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := ac.DeleteApiKey(req.UserID, req.Fingerprint); err != nil {
		writeErr(w, http.StatusBadGateway, "删除 API 密钥失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "API 密钥已删除"})
}

func handleTenantDeleteMfa(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodPost) {
		return
	}
	var req struct {
		Account  string `json:"account"`
		UserID   string `json:"userId"`
		DeviceID string `json:"deviceId"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	ac, err := oci.GetAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := ac.DeleteMfaDevice(req.UserID, req.DeviceID); err != nil {
		writeErr(w, http.StatusBadGateway, "删除 MFA 设备失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "MFA 设备已删除"})
}

func handleTenantDeleteUser(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodPost) {
		return
	}
	var req struct {
		Account string `json:"account"`
		UserID  string `json:"userId"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	ac, err := oci.GetAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := ac.DeleteUser(req.UserID); err != nil {
		writeErr(w, http.StatusBadGateway, "删除用户失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "用户已删除"})
}

// handleTenantResetPassword 重置用户控制台密码，返回新密码。
func handleTenantResetPassword(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodPost) {
		return
	}
	var req struct {
		Account string `json:"account"`
		UserID  string `json:"userId"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.UserID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 userId")
		return
	}
	ac, err := oci.GetAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	pw, err := ac.ResetUserPassword(req.UserID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"password": pw})
}

// handleTenantUpdateUser 更新用户邮箱与描述。
func handleTenantUpdateUser(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodPost) {
		return
	}
	var req struct {
		Account     string `json:"account"`
		UserID      string `json:"userId"`
		Email       string `json:"email"`
		Description string `json:"description"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.UserID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 userId")
		return
	}
	ac, err := oci.GetAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := ac.UpdateUserInfo(req.UserID, req.Email, req.Description); err != nil {
		writeErr(w, http.StatusBadGateway, "更新用户信息失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "用户信息已更新"})
}
