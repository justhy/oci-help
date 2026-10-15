package task

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"oci-help/internal/config"
	"oci-help/internal/job"
	"oci-help/internal/oci"
	"oci-help/internal/store"
)

// TaskManager 管理持久化的抢机任务：
//   - 任务配置写入本地数据库，服务重启后自动恢复执行（断点续抢）
//   - 每个任务对应一个长生命周期 job.Job，日志与进度复用 Web 的任务页
type TaskManager struct {
	mu     sync.Mutex
	active map[string]*activeTask
}

type activeTask struct {
	task    store.CreateTask
	job     *job.Job
	stopCh  chan struct{}
	paused  int32 // 原子读写：1 表示已暂停
	started int32 // 原子读写：执行循环是否已启动（保证单循环）
}

// Manager 是进程级抢机任务管理器，由 StartTaskManager 初始化。
var Manager = &TaskManager{active: map[string]*activeTask{}}

// StartTaskManager 从数据库恢复全部抢机任务并开始执行（错峰启动，避免重启瞬间大量 API 调用）。
func StartTaskManager() {
	tasks, err := store.DB.ListTasks()
	if err != nil {
		fmt.Printf("恢复抢机任务失败: %v\n", err)
		return
	}
	if len(tasks) == 0 {
		return
	}
	fmt.Printf("正在恢复 %d 个抢机任务...\n", len(tasks))
	for i := range tasks {
		t := tasks[i]
		delay := time.Duration(i) * 5 * time.Second
		time.AfterFunc(delay, func() { Manager.load(t) })
	}
}

// load 注册并（在未暂停时）启动一个任务。
func (tm *TaskManager) load(t store.CreateTask) {
	tm.mu.Lock()
	if _, ok := tm.active[t.ID]; ok {
		tm.mu.Unlock()
		return
	}
	jb := job.NewJobWithID(t.ID, "task", t.TenantName)
	at := &activeTask{task: t, job: jb, stopCh: make(chan struct{}), paused: int32(t.Paused)}
	tm.active[t.ID] = at
	tm.mu.Unlock()

	jb.Log("【抢机任务】已加载: 账号=%s 区域=%s 架构=%s 规格=%gC/%gG 磁盘=%dGB 数量=%d 间隔=%ds",
		t.TenantName, t.Region, t.Architecture, t.Ocpus, t.Memory, t.Disk, t.CreateNumbers, t.Interval)
	if t.CreateNumbers <= 0 {
		jb.Log("【抢机任务】创建数量为 0，任务已完成")
		jb.FinishWith("success", "任务已完成（创建数量为 0）")
		return
	}
	if t.Paused == 1 {
		jb.Canceled("【抢机任务】任务处于暂停状态，等待恢复")
		jb.SetProgress("已暂停", 0, t.CreateNumbers)
		return
	}
	tm.spawn(at)
}

// spawn 启动任务的执行循环；借助 CAS 保证同一任务只会有一个执行循环。
func (tm *TaskManager) spawn(at *activeTask) {
	if !atomic.CompareAndSwapInt32(&at.started, 0, 1) {
		return
	}
	go tm.run(at)
}

// run 是任务的执行循环：每轮尝试创建剩余实例，成功后落库并继续下一轮。
// 暂停时循环在此阻塞等待恢复（而非退出），因此 Resume 不会产生第二个执行循环。
func (tm *TaskManager) run(at *activeTask) {
	defer func() {
		if r := recover(); r != nil {
			at.job.Fail(fmt.Sprintf("任务异常: %v", r))
		}
	}()

	interval := at.task.Interval
	if interval <= 0 {
		interval = 60
	}

	for {
		if tm.shouldStop(at) {
			at.job.Canceled("任务已停止")
			return
		}
		if tm.isPaused(at) {
			at.job.SetProgress("已暂停", 0, at.task.CreateNumbers)
			if !tm.waitUnpaused(at) {
				at.job.Canceled("任务已停止")
				return
			}
			continue
		}

		remaining := at.task.CreateNumbers
		if remaining <= 0 {
			at.job.FinishWith("success", "全部实例已创建完成 🎉")
			return
		}

		ac, err := accountForTask(at.task)
		if err != nil {
			at.job.Fail("初始化账号失败: " + err.Error())
			return
		}
		ads, err := ac.EnsureAvailabilityDomains()
		if err != nil {
			at.job.Log("获取可用性域失败: %v，%d 秒后重试", err, interval)
			if !tm.sleep(at, interval) {
				at.job.Canceled("任务已停止")
				return
			}
			continue
		}

		inst := TaskToInstance(at.task, remaining)
		jc := *ac
		jc.Logger = at.job.Log
		jc.Progress = at.job.SetProgress
		jc.Stopped = func() bool { return tm.shouldStop(at) || tm.isPaused(at) }

		_, num := jc.LaunchInstances(ads, inst)
		if num > 0 {
			remaining -= int(num)
			at.task.CreateNumbers = remaining
			_ = store.DB.SetTaskCreateNumbers(at.task.ID, remaining)
			at.job.Log("【抢机任务】本轮成功创建 %d 台，剩余 %d 台", num, remaining)
		}
		if remaining <= 0 {
			at.job.FinishWith("success", "全部实例已创建完成 🎉")
			return
		}
		if !tm.sleep(at, interval) {
			at.job.Canceled("任务已停止")
			return
		}
	}
}

// sleep 等待指定秒数，期间可被停止或暂停请求打断。
// 返回 false 表示被停止；因暂停而提前返回时返回 true（交由主循环处理暂停）。
func (tm *TaskManager) sleep(at *activeTask, seconds int) bool {
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	for {
		if tm.shouldStop(at) {
			return false
		}
		if tm.isPaused(at) || time.Now().After(deadline) {
			return true
		}
		select {
		case <-at.stopCh:
			return false
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// waitUnpaused 阻塞直到任务恢复或被停止；返回 false 表示已被停止。
func (tm *TaskManager) waitUnpaused(at *activeTask) bool {
	for {
		if tm.shouldStop(at) {
			return false
		}
		if !tm.isPaused(at) {
			return true
		}
		select {
		case <-at.stopCh:
			return false
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// shouldStop 返回任务是否已被永久停止（从注册表移除或停止通道关闭）。
func (tm *TaskManager) shouldStop(at *activeTask) bool {
	tm.mu.Lock()
	cur, alive := tm.active[at.task.ID]
	tm.mu.Unlock()
	if !alive || cur != at {
		return true
	}
	select {
	case <-at.stopCh:
		return true
	default:
	}
	return false
}

// isPaused 返回任务当前是否处于暂停。
func (tm *TaskManager) isPaused(at *activeTask) bool {
	return atomic.LoadInt32(&at.paused) == 1
}

// Add 新增一条抢机任务并立即开始执行。
func (tm *TaskManager) Add(t store.CreateTask) (store.CreateTask, error) {
	if t.Interval <= 0 {
		t.Interval = 60
	}
	saved, err := store.DB.UpsertTask(t)
	if err != nil {
		return store.CreateTask{}, err
	}
	tm.load(saved)
	return saved, nil
}

// Pause 暂停任务：仅置暂停标志，执行循环会在下一次检查时阻塞等待恢复。
func (tm *TaskManager) Pause(id string) error {
	if err := store.DB.SetTaskPaused(id, 1); err != nil {
		return err
	}
	tm.mu.Lock()
	if at, ok := tm.active[id]; ok {
		atomic.StoreInt32(&at.paused, 1)
		at.job.Canceled("任务已暂停")
	}
	tm.mu.Unlock()
	return nil
}

// Resume 恢复任务：清除暂停标志即可让既有执行循环继续，无需新建执行循环。
func (tm *TaskManager) Resume(id string) error {
	if err := store.DB.SetTaskPaused(id, 0); err != nil {
		return err
	}
	tm.mu.Lock()
	if at, ok := tm.active[id]; ok {
		atomic.StoreInt32(&at.paused, 0)
		at.job.Running("【抢机任务】已恢复执行")
		at.job.SetProgress("运行中", 0, at.task.CreateNumbers)
		started := atomic.LoadInt32(&at.started) == 1
		tm.mu.Unlock()
		if !started {
			tm.spawn(at)
		}
		return nil
	}
	tm.mu.Unlock()

	if t, found, err := store.DB.GetTask(id); err == nil && found {
		t.Paused = 0
		tm.load(t)
	}
	return nil
}

// Stop 停止并删除任务。
func (tm *TaskManager) Stop(id string) error {
	tm.mu.Lock()
	if at, ok := tm.active[id]; ok {
		atomic.StoreInt32(&at.paused, 1)
		select {
		case <-at.stopCh:
		default:
			close(at.stopCh)
		}
		delete(tm.active, id)
	}
	tm.mu.Unlock()
	if err := store.DB.DeleteTask(id); err != nil {
		return err
	}
	job.Remove(id)
	return nil
}

// List 返回数据库中的全部任务。
func (tm *TaskManager) List() ([]store.CreateTask, error) { return store.DB.ListTasks() }

// taskToInstance 把抢机任务配置转成创建实例用的模板。
func TaskToInstance(t store.CreateTask, remaining int) config.Instance {
	shape := "VM.Standard.E2.1.Micro"
	arch := strings.ToUpper(t.Architecture)
	if strings.Contains(arch, "ARM") {
		shape = "VM.Standard.A1.Flex"
	}
	osName, osVer := NormalizeOS(t.OperationSystem, t.OSVersion)
	inst := config.Instance{
		Shape:                  shape,
		OperatingSystem:        osName,
		OperatingSystemVersion: osVer,
		BootVolumeSizeInGBs:    t.Disk,
		Sum:                    int32(remaining),
		Retry:                  -1,
		AvailabilityDomain:     t.AvailabilityDomain,
		InstanceDisplayName:    t.InstanceName,
		SSH_Public_Key:         t.SSHPublicKey,
		MinTime:                1,
		MaxTime:                1,
	}
	if strings.Contains(arch, "ARM") {
		inst.Ocpus = t.Ocpus
		inst.MemoryInGBs = t.Memory
	}
	if t.RootPassword != "" {
		inst.CloudInit = oci.RootPasswordCloudInit(t.RootPassword)
	}
	return inst
}

// normalizeOS 把用户输入的架构/系统名映射为镜像查询所需的值。
func NormalizeOS(os, version string) (string, string) {
	lower := strings.ToLower(os)
	switch {
	case strings.Contains(lower, "ubuntu"):
		if version == "" {
			version = "20.04"
		}
		return "Canonical Ubuntu", version
	case strings.Contains(lower, "centos"):
		if version == "" {
			version = "8"
		}
		return "CentOS", version
	case strings.Contains(lower, "oracle"):
		if version == "" {
			version = "8"
		}
		return "Oracle Linux", version
	default:
		if os == "" {
			os = "Canonical Ubuntu"
		}
		if version == "" {
			version = "20.04"
		}
		return os, version
	}
}

// accountForTask 根据任务配置构造 oci.AccountCtx（任务可覆盖租户的 region）。
func accountForTask(t store.CreateTask) (*oci.AccountCtx, error) {
	tenant, ok, err := store.DB.GetTenant(t.TenantID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("租户不存在: %s", t.TenantID)
	}
	if t.Region != "" {
		tenant.Region = t.Region
	}
	return oci.NewAccountCtx(&tenant)
}

// shapeForArch 返回架构对应的默认规格名（供 Web 端展示/校验）。
func ShapeForArch(arch string) string {
	if strings.Contains(strings.ToUpper(arch), "ARM") {
		return "VM.Standard.A1.Flex"
	}
	return "VM.Standard.E2.1.Micro"
}
