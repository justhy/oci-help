package job

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"oci-help/internal/config"
	"oci-help/internal/oci"
)

/* ------------------------------------------------------------------ 异步任务 */

var (
	jobsMu sync.Mutex
	jobs   = map[string]*Job{}
	jobSeq int64
)

// Summary 是任务列表用的轻量视图，不含日志，避免每次轮询都传输大量文本。
type Summary struct {
	ID       string    `json:"id"`
	Type     string    `json:"type"`
	Account  string    `json:"account"`
	Status   string    `json:"status"`
	Stage    string    `json:"stage"`
	Done     int       `json:"done"`
	Total    int       `json:"total"`
	Error    string    `json:"error"`
	Started  time.Time `json:"started"`
	Ended    time.Time `json:"ended"`
	Stopping bool      `json:"stopping"`
	LogCount int       `json:"logCount"` // 累计日志行数（含已被裁剪的部分）
}

// Detail 是任务详情视图，只返回 since 之后的增量日志。
type Detail struct {
	Summary
	Logs    []string `json:"logs"`
	LogFrom int      `json:"logFrom"` // 本次返回的首行在完整日志中的序号
	LogNext int      `json:"logNext"` // 客户端下次请求应使用的 since
}

type Job struct {
	ID      string
	Type    string
	Account string
	Status  string
	Stage   string
	Done    int
	Total   int
	Logs    []string
	Error   string
	Started time.Time
	Ended   time.Time

	mu         sync.Mutex
	logDropped int   // 因超出上限被裁剪的日志行数
	stopped    int32 // 原子标志：是否已请求停止
}

const jobLogLimit = 2000

// NewJob 注册一个新的异步任务（ID 由类型 + 时间戳 + 序列号生成）。
func NewJob(typ, account string) *Job {
	jobsMu.Lock()
	jobSeq++
	id := fmt.Sprintf("%s-%d-%d", typ, time.Now().Unix(), jobSeq)
	jobsMu.Unlock()
	return NewJobWithID(id, typ, account)
}

// NewJobWithID 以指定 ID 注册任务，供持久化抢机任务复用固定 ID（便于重启后继续展示）。
func NewJobWithID(id, typ, account string) *Job {
	j := &Job{ID: id, Type: typ, Account: account, Status: "running", Started: time.Now()}
	jobsMu.Lock()
	jobs[id] = j
	jobsMu.Unlock()
	return j
}

// JobByID 按 ID 查找任务；不存在时返回 nil。
func JobByID(id string) *Job {
	jobsMu.Lock()
	defer jobsMu.Unlock()
	return jobs[id]
}

// JobIDs 返回当前全部任务 ID（顺序不保证）。
func JobIDs() []string {
	jobsMu.Lock()
	defer jobsMu.Unlock()
	out := make([]string, 0, len(jobs))
	for id := range jobs {
		out = append(out, id)
	}
	return out
}

// Remove 从注册表中删除任务（不检查运行状态，调用方需自行判断）。
func Remove(id string) {
	jobsMu.Lock()
	delete(jobs, id)
	jobsMu.Unlock()
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func (j *Job) Log(format string, a ...interface{}) {
	line := ansiRe.ReplaceAllString(fmt.Sprintf(format, a...), "")
	for _, l := range strings.Split(strings.TrimRight(line, "\n"), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		j.mu.Lock()
		j.Logs = append(j.Logs, l)
		if len(j.Logs) > jobLogLimit {
			j.logDropped += len(j.Logs) - jobLogLimit
			j.Logs = j.Logs[len(j.Logs)-jobLogLimit:]
		}
		j.mu.Unlock()
	}
}

func (j *Job) SetProgress(stage string, done, total int) {
	j.mu.Lock()
	j.Stage = stage
	j.Done = done
	j.Total = total
	j.mu.Unlock()
}

// FinishWith 以指定状态结束任务。
func (j *Job) FinishWith(status, msg string) {
	j.mu.Lock()
	j.Status = status
	j.Ended = time.Now()
	if msg != "" {
		j.Logs = append(j.Logs, msg)
	}
	j.mu.Unlock()
}

func (j *Job) Finish(msg string) {
	j.FinishWith("success", msg)
}

func (j *Job) Fail(msg string) {
	j.mu.Lock()
	j.Status = "failed"
	j.Error = msg
	j.Ended = time.Now()
	j.Logs = append(j.Logs, "❌ "+msg)
	j.mu.Unlock()
}

// Canceled 标记任务被用户中止。
func (j *Job) Canceled(msg string) {
	j.mu.Lock()
	j.Status = "canceled"
	j.Ended = time.Now()
	if msg != "" {
		j.Logs = append(j.Logs, msg)
	}
	j.mu.Unlock()
}

// Running 把任务标记回运行中（用于暂停后恢复）。
func (j *Job) Running(msg string) {
	j.mu.Lock()
	j.Status = "running"
	j.Error = ""
	j.Ended = time.Time{}
	if msg != "" {
		j.Logs = append(j.Logs, msg)
	}
	j.mu.Unlock()
}

// Stop 请求停止任务；正在运行的创建循环会在下一次检查时退出。
func (j *Job) Stop() {
	atomic.StoreInt32(&j.stopped, 1)
}

func (j *Job) IsStopped() bool {
	return atomic.LoadInt32(&j.stopped) == 1
}

// CurrentStatus 安全读取任务状态。
func (j *Job) CurrentStatus() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.Status
}

func (j *Job) Summary() Summary {
	j.mu.Lock()
	defer j.mu.Unlock()
	return Summary{
		ID: j.ID, Type: j.Type, Account: j.Account, Status: j.Status,
		Stage: j.Stage, Done: j.Done, Total: j.Total, Error: j.Error,
		Started: j.Started, Ended: j.Ended,
		Stopping: j.IsStopped(), LogCount: j.logDropped + len(j.Logs),
	}
}

// DetailSince 返回序号 since 之后的日志。日志超出上限被裁剪时，
// 返回的 LogFrom 会大于请求的 since，客户端据此丢弃本地旧日志。
func (j *Job) DetailSince(since int) Detail {
	j.mu.Lock()
	defer j.mu.Unlock()
	start := since - j.logDropped
	if start < 0 {
		start = 0
	}
	if start > len(j.Logs) {
		start = len(j.Logs)
	}
	logs := make([]string, len(j.Logs)-start)
	copy(logs, j.Logs[start:])
	return Detail{
		Summary: Summary{
			ID: j.ID, Type: j.Type, Account: j.Account, Status: j.Status,
			Stage: j.Stage, Done: j.Done, Total: j.Total, Error: j.Error,
			Started: j.Started, Ended: j.Ended,
			Stopping: j.IsStopped(), LogCount: j.logDropped + len(j.Logs),
		},
		Logs:    logs,
		LogFrom: j.logDropped + start,
		LogNext: j.logDropped + len(j.Logs),
	}
}

// StartLaunchJob 在后台执行创建实例任务，日志与进度写入 Job。
func StartLaunchJob(ac *oci.AccountCtx, inst config.Instance) *Job {
	job := NewJob("launch", ac.Name)
	go runLaunchJob(job, ac, inst)
	return job
}

func runLaunchJob(job *Job, ac *oci.AccountCtx, inst config.Instance) {
	defer func() {
		if rec := recover(); rec != nil {
			job.Fail(fmt.Sprintf("任务异常: %v", rec))
		}
	}()
	ads, err := ac.EnsureAvailabilityDomains()
	if err != nil {
		job.Fail("获取可用性域失败: " + err.Error())
		return
	}
	// 使用独立副本，避免污染缓存中的 AccountCtx
	jc := *ac
	jc.Logger = job.Log
	jc.Progress = job.SetProgress
	jc.Stopped = job.IsStopped
	sum, num := jc.LaunchInstances(ads, inst)
	msg := fmt.Sprintf("创建总数: %d, 成功: %d, 失败: %d", sum, num, sum-num)
	if job.IsStopped() {
		job.Canceled("任务已被中止。" + msg)
		return
	}
	job.FinishWith(launchStatus(sum, num), "任务结束。"+msg)
}

// launchStatus 根据创建结果判定任务状态：
// 全部成功才算 success；一个都没成功为 failed；介于两者之间为 partial。
func launchStatus(sum, num int32) string {
	switch {
	case sum > 0 && num >= sum:
		return "success"
	case num == 0:
		return "failed"
	default:
		return "partial"
	}
}

// All 返回当前全部任务的轻量视图（顺序不保证）。
func All() []Summary {
	jobsMu.Lock()
	defer jobsMu.Unlock()
	out := make([]Summary, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, j.Summary())
	}
	return out
}

// RemoveByFilter 删除满足条件的任务并返回删除数量。
// keep 返回 true 表示删除；运行中的任务永远不会被删除。
func RemoveByFilter(keep func(j *Job) bool) int {
	jobsMu.Lock()
	defer jobsMu.Unlock()
	removed := 0
	for id, j := range jobs {
		if j.CurrentStatus() == "running" {
			continue
		}
		if keep(j) {
			delete(jobs, id)
			removed++
		}
	}
	return removed
}
