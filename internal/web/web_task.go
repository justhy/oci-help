package web

import (
	"net/http"
	"oci-help/internal/oci"
	"oci-help/internal/store"
	"oci-help/internal/task"
	"strings"
)

func handleTasks(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodGet) {
		return
	}
	tasks, err := task.Manager.List()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取任务失败: "+err.Error())
		return
	}
	// 补上租户名，便于前端展示
	for i := range tasks {
		if t, ok, _ := store.DB.GetTenant(tasks[i].TenantID); ok {
			tasks[i].TenantName = t.Name
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"tasks": tasks})
}

func handleTaskSave(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodPost) {
		return
	}
	var req taskSaveRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.TenantID == "" {
		writeErr(w, http.StatusBadRequest, "请选择租户")
		return
	}
	if _, ok, err := store.DB.GetTenant(req.TenantID); err != nil || !ok {
		writeErr(w, http.StatusBadRequest, "租户不存在")
		return
	}
	if req.CreateNumbers <= 0 {
		writeErr(w, http.StatusBadRequest, "创建数量必须大于 0")
		return
	}
	if req.RetryMin < 0 || req.RetryMax < 0 {
		writeErr(w, http.StatusBadRequest, "重试间隔不能为负数")
		return
	}
	if req.Disk <= 0 {
		req.Disk = 47
	}
	// 既没配 SSH 公钥、也没填 Root 密码 → 自动生成一个随机 root 密码，
	// 保证抢到的机器一定能登录。（SSH 公钥由前端按架构从 oci-help.ini 预填；
	// 用户把它清空即表示本次不使用 SSH。）
	sshKey := strings.TrimSpace(req.SSHPublicKey)
	rootPwd := req.RootPassword
	generatedPwd := false
	if sshKey == "" && rootPwd == "" {
		rootPwd = oci.RandomPassword(12)
		generatedPwd = true
	}
	if strings.TrimSpace(req.Architecture) == "" {
		req.Architecture = "AMD"
	}
	t := store.CreateTask{
		ID:                 req.ID,
		TenantID:           req.TenantID,
		Region:             strings.TrimSpace(req.Region),
		Architecture:       strings.ToUpper(strings.TrimSpace(req.Architecture)),
		Ocpus:              req.Ocpus,
		Memory:             req.Memory,
		Disk:               req.Disk,
		RetryMin:           req.RetryMin,
		RetryMax:           req.RetryMax,
		CreateNumbers:      req.CreateNumbers,
		RootPassword:       rootPwd,
		OperationSystem:    req.OperationSystem,
		OSVersion:          req.OSVersion,
		SSHPublicKey:       sshKey,
		InstanceName:       req.InstanceName,
		AvailabilityDomain: req.AvailabilityDomain,
	}
	// AMD 免费实例固定规格
	if t.Architecture == "AMD" {
		t.Ocpus = 1
		t.Memory = 1
	}
	saved, err := task.Manager.Add(t)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "保存任务失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"task": saved, "generatedRootPassword": generatedPwd})
}

func handleTaskPause(w http.ResponseWriter, r *http.Request) {
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
	if err := task.Manager.Pause(req.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, "暂停失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "任务已暂停"})
}

func handleTaskResume(w http.ResponseWriter, r *http.Request) {
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
	if err := task.Manager.Resume(req.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, "恢复失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "任务已恢复"})
}

func handleTaskDelete(w http.ResponseWriter, r *http.Request) {
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
	if err := task.Manager.Stop(req.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, "删除失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "任务已删除"})
}
