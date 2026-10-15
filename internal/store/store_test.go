package store

import (
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := OpenStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestTenantCRUD(t *testing.T) {
	s := newTestStore(t)

	if n, err := s.CountTenants(); err != nil || n != 0 {
		t.Fatalf("初始应为空, n=%d err=%v", n, err)
	}

	saved, err := s.UpsertTenant(Tenant{
		Name: "sg-01", User: "u1", Fingerprint: "fp", Tenancy: "t1", Region: "ap-singapore-1", KeyFile: "k.pem",
	})
	if err != nil {
		t.Fatalf("保存租户失败: %v", err)
	}
	if saved.ID == "" {
		t.Fatal("应自动生成 ID")
	}

	list, err := s.ListTenants()
	if err != nil || len(list) != 1 {
		t.Fatalf("应有 1 个租户, got %d err=%v", len(list), err)
	}

	// 更新（同名不同 region）
	saved.Region = "ap-tokyo-1"
	if _, err := s.UpsertTenant(saved); err != nil {
		t.Fatalf("更新租户失败: %v", err)
	}
	got, ok, err := s.GetTenant(saved.ID)
	if err != nil || !ok {
		t.Fatalf("读取租户失败: ok=%v err=%v", ok, err)
	}
	if got.Region != "ap-tokyo-1" {
		t.Fatalf("region 未更新: %s", got.Region)
	}

	if err := s.DeleteTenant(saved.ID); err != nil {
		t.Fatalf("删除租户失败: %v", err)
	}
	if n, _ := s.CountTenants(); n != 0 {
		t.Fatalf("删除后应为空, n=%d", n)
	}
}

func TestCreateTaskPersistence(t *testing.T) {
	s := newTestStore(t)

	tenant, err := s.UpsertTenant(Tenant{Name: "sg", User: "u", Fingerprint: "f", Tenancy: "t", Region: "r", KeyFile: "k"})
	if err != nil {
		t.Fatal(err)
	}

	task, err := s.UpsertTask(CreateTask{
		TenantID: tenant.ID, Region: "ap-singapore-1", Architecture: "AMD",
		Ocpus: 1, Memory: 1, Disk: 47, RetryMin: 5, RetryMax: 30, CreateNumbers: 2,
		OperationSystem: "Canonical Ubuntu", OSVersion: "20.04",
	})
	if err != nil {
		t.Fatalf("保存任务失败: %v", err)
	}
	if task.ID == "" || task.CreateTime == "" {
		t.Fatal("应生成 ID 与创建时间")
	}

	// 模拟重启：重新打开数据库应能读回任务（断点续抢的关键）
	list, err := s.ListTasks()
	if err != nil || len(list) != 1 {
		t.Fatalf("应读回 1 个任务, got %d err=%v", len(list), err)
	}
	if list[0].CreateNumbers != 2 || list[0].Disk != 47 || list[0].OSVersion != "20.04" ||
		list[0].RetryMin != 5 || list[0].RetryMax != 30 {
		t.Fatalf("任务字段未正确持久化: %+v", list[0])
	}

	if err := s.SetTaskPaused(task.ID, 1); err != nil {
		t.Fatal(err)
	}
	got, ok, _ := s.GetTask(task.ID)
	if !ok || got.Paused != 1 {
		t.Fatalf("暂停标记未持久化: %+v", got)
	}

	if err := s.SetTaskCreateNumbers(task.ID, 1); err != nil {
		t.Fatal(err)
	}
	got, _, _ = s.GetTask(task.ID)
	if got.CreateNumbers != 1 {
		t.Fatalf("剩余数量未更新: %d", got.CreateNumbers)
	}

	if err := s.DeleteTask(task.ID); err != nil {
		t.Fatal(err)
	}
	if l, _ := s.ListTasks(); len(l) != 0 {
		t.Fatalf("删除后应无任务, got %d", len(l))
	}
}

func TestDeleteTenantCascadesTasks(t *testing.T) {
	s := newTestStore(t)
	tenant, _ := s.UpsertTenant(Tenant{Name: "sg", User: "u", Fingerprint: "f", Tenancy: "t", Region: "r", KeyFile: "k"})
	if _, err := s.UpsertTask(CreateTask{TenantID: tenant.ID, CreateNumbers: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTenant(tenant.ID); err != nil {
		t.Fatal(err)
	}
	if l, _ := s.ListTasks(); len(l) != 0 {
		t.Fatalf("删除租户应级联删除任务, got %d", len(l))
	}
}

func TestKV(t *testing.T) {
	s := newTestStore(t)
	if _, ok, _ := s.GetKV("missing"); ok {
		t.Fatal("不存在的键不应返回 ok")
	}
	if err := s.SetKV("k", "v1", "t"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetKV("k", "v2", "t"); err != nil {
		t.Fatal(err)
	}
	v, ok, err := s.GetKV("k")
	if err != nil || !ok || v != "v2" {
		t.Fatalf("kv 读写异常: v=%q ok=%v err=%v", v, ok, err)
	}
}
