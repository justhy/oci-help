package web

import (
	"net/http"
	"oci-help/internal/config"
	"oci-help/internal/oci"
	"oci-help/internal/store"
	"os"
	"path/filepath"
	"strings"
)

/* ------------------------------------------------------------------ 租户（账号）配置 CRUD */

type tenantSaveRequest struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	User        string `json:"user"`
	Fingerprint string `json:"fingerprint"`
	Tenancy     string `json:"tenancy"`
	Region      string `json:"region"`
	KeyFile     string `json:"keyFile"`
	KeyPassword string `json:"keyPassword"`
	Proxy       string `json:"proxy"`
	// KeyContent 为可选：粘贴私钥内容时写入 keys/<name>.pem 并自动填充 keyFile。
	KeyContent string `json:"keyContent"`
}

func handleTenantSave(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodPost) {
		return
	}
	var req tenantSaveRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	t := store.Tenant{
		ID:          req.ID,
		Name:        strings.TrimSpace(req.Name),
		User:        strings.TrimSpace(req.User),
		Fingerprint: strings.TrimSpace(req.Fingerprint),
		Tenancy:     strings.TrimSpace(req.Tenancy),
		Region:      strings.TrimSpace(req.Region),
		KeyFile:     strings.TrimSpace(req.KeyFile),
		KeyPassword: req.KeyPassword,
		Proxy:       strings.TrimSpace(req.Proxy),
	}

	// 粘贴私钥内容时落盘到 keys 目录
	if strings.TrimSpace(req.KeyContent) != "" {
		path, err := writeKeyFile(t.Name, req.KeyContent)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "保存私钥失败: "+err.Error())
			return
		}
		t.KeyFile = path
	}

	if err := store.ValidTenant(t); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	// 名称唯一性校验（新增或改名时）
	if exist, ok, err := store.DB.GetTenantByName(t.Name); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	} else if ok && exist.ID != t.ID {
		writeErr(w, http.StatusBadRequest, "账号名称已存在: "+t.Name)
		return
	}

	saved, err := store.DB.UpsertTenant(t)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "保存失败: "+err.Error())
		return
	}
	if err := oci.ReloadTenants(); err != nil {
		writeErr(w, http.StatusInternalServerError, "重新加载账号失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"tenant": saved})
}

func handleTenantDelete(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodPost) {
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.ID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 id")
		return
	}
	if err := store.DB.DeleteTenant(req.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, "删除失败: "+err.Error())
		return
	}
	if err := oci.ReloadTenants(); err != nil {
		writeErr(w, http.StatusInternalServerError, "重新加载账号失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "账号已删除"})
}

// writeKeyFile 把私钥内容写入 <db 目录>/keys/<name>.pem，返回写入路径。
func writeKeyFile(name, content string) (string, error) {
	dir := filepath.Join(filepath.Dir(config.C.DBPath), "keys")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	safe := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_").Replace(name)
	if safe == "" {
		safe = "key"
	}
	path := filepath.Join(dir, safe+".pem")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

/* ------------------------------------------------------------------ 抢机任务 CRUD */

type taskSaveRequest struct {
	ID                 string  `json:"id"`
	TenantID           string  `json:"tenantId"`
	Region             string  `json:"region"`
	Architecture       string  `json:"architecture"`
	Ocpus              float32 `json:"ocpus"`
	Memory             float32 `json:"memory"`
	Disk               int64   `json:"disk"`
	RetryMin           int     `json:"retryMin"`
	RetryMax           int     `json:"retryMax"`
	CreateNumbers      int     `json:"createNumbers"`
	RootPassword       string  `json:"rootPassword"`
	OperationSystem    string  `json:"operationSystem"`
	OSVersion          string  `json:"osVersion"`
	SSHPublicKey       string  `json:"sshPublicKey"`
	InstanceName       string  `json:"instanceName"`
	AvailabilityDomain string  `json:"availabilityDomain"`
}
