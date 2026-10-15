package web

import (
	"fmt"
	"net/http"
	"oci-help/internal/job"
	"oci-help/internal/oci"
)

// startActionJob 在后台执行一次耗时操作，并把日志/进度写入 job.Job。
// 返回的 Job 可被前端「任务」页轮询查看。
func startActionJob(typ, account string, ac *oci.AccountCtx, fn func(jb *job.Job, ac *oci.AccountCtx) error) *job.Job {
	jb := job.NewJob(typ, account)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				jb.Fail(fmt.Sprintf("任务异常: %v", r))
			}
		}()
		jc := *ac
		jc.Logger = jb.Log
		jc.Progress = jb.SetProgress
		jc.Stopped = jb.IsStopped
		if err := fn(jb, &jc); err != nil {
			jb.Fail(err.Error())
			return
		}
		if jb.IsStopped() {
			jb.Canceled("任务已被中止")
			return
		}
		jb.Finish("操作完成")
	}()
	return jb
}

func methodGuard(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return false
	}
	return true
}
