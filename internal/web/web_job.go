package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"oci-help/internal/config"
	"oci-help/internal/job"
	"oci-help/internal/oci"
	"oci-help/internal/task"
)

func handleJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	list := job.All()
	writeJSON(w, http.StatusOK, map[string]interface{}{"jobs": list})
}

func handleJob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := r.URL.Query().Get("id")
	since, _ := strconv.Atoi(r.URL.Query().Get("since"))
	j := job.JobByID(id)
	if j == nil {
		writeErr(w, http.StatusNotFound, "任务不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"job": j.DetailSince(since)})
}

// handleJobCancel 请求停止一个正在运行的任务。
//
// 抢机任务（type=task）的执行循环由 task.Manager 控制，只认暂停标志与停止通道；
// 仅调用 job.Stop() 只改内存标志，循环读不到，会出现"停止中"却仍在运行。
// 因此抢机任务走 task.Manager.Pause（会持久化 paused，重启后也不会自动续抢）。
func handleJobCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
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
	j := job.JobByID(req.ID)
	if j == nil {
		writeErr(w, http.StatusNotFound, "任务不存在")
		return
	}
	if j.Type == "task" {
		if err := task.Manager.Pause(req.ID); err != nil {
			writeErr(w, http.StatusInternalServerError, "暂停抢机任务失败: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"message": "抢机任务已暂停"})
		return
	}
	j.Stop()
	writeJSON(w, http.StatusOK, map[string]string{"message": "已请求停止任务，正在等待当前尝试结束"})
}

// handleJobDelete 删除单个任务（运行中的任务不允许删除）。
func handleJobDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
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
	j := job.JobByID(req.ID)
	if j == nil {
		writeErr(w, http.StatusNotFound, "任务不存在")
		return
	}
	if j.CurrentStatus() == "running" {
		writeErr(w, http.StatusBadRequest, "运行中的任务不能删除，请先停止")
		return
	}
	job.Remove(req.ID)
	writeJSON(w, http.StatusOK, map[string]string{"message": "任务已删除"})
}

// handleJobsDelete 批量删除任务日志。
// body.status 为空 → 删除该分类下全部非运行中任务；否则只删除该状态的任务。
// body.type 为空 → 全部任务；"task" → 仅抢机任务；"action" → 仅操作类任务（IPv6/500M/救援/网络等）。
// 运行中的任务永远不会被删除。
func handleJobsDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Status string `json:"status"`
		Type   string `json:"type"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.Status == "running" {
		writeErr(w, http.StatusBadRequest, "不能删除运行中的任务")
		return
	}
	switch req.Type {
	case "", "task", "action":
	default:
		writeErr(w, http.StatusBadRequest, "type 只能是 task / action 或留空")
		return
	}
	removed := job.RemoveByFilter(func(j *job.Job) bool {
		st := j.CurrentStatus()
		if st == "running" {
			return false
		}
		if req.Type == "task" && j.Type != "task" {
			return false
		}
		if req.Type == "action" && j.Type == "task" {
			return false
		}
		return req.Status == "" || st == req.Status
	})
	writeJSON(w, http.StatusOK, map[string]interface{}{"removed": removed})
}

/* ------------------------------------------------------------------ 导出 IP */

func handleExportIps(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req actionRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	ac, err := oci.GetAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	tmp, err := os.CreateTemp("", "oci-help-ips-*.txt")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "创建临时文件失败: "+err.Error())
		return
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	ac.ListInstancesIPs(tmpPath)
	content, err := os.ReadFile(tmpPath)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取结果失败: "+err.Error())
		return
	}
	filename := config.IPsFilePrefix + "-" + time.Now().Format("2006-01-02-150405") + ".txt"
	writeJSON(w, http.StatusOK, map[string]string{
		"filename": filepath.Base(filename),
		"content":  string(content),
	})
}
