package store

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/ini.v1"
	_ "modernc.org/sqlite"
)

// DB 是进程级默认数据库实例，由程序入口打开后赋值。
// 业务代码通过 store.DB 访问，避免在各包间传递指针。
var DB *Store

// Store 是本地持久化层，基于纯 Go 的 SQLite 实现（modernc.org/sqlite），
// 无需 cgo，可保持 CGO_ENABLED=0 静态编译。
//
// 表结构：
//
//	oci_user        账号（租户）配置，替代原先 oci-help.ini 中的账号段
//	oci_create_task 抢机任务，服务重启后据此恢复调度（断点续抢）
//	oci_kv          键值配置（面板设置、版本信息等）
type Store struct {
	mu sync.Mutex
	db *sql.DB
}

// Tenant 是一个甲骨文账号（租户）配置。
type Tenant struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	User             string `json:"user"`
	Fingerprint      string `json:"fingerprint"`
	Tenancy          string `json:"tenancy"`
	Region           string `json:"region"`
	KeyFile          string `json:"keyFile"`
	KeyPassword      string `json:"keyPassword"`
	Proxy            string `json:"proxy"`
	PlanType         string `json:"planType"`
	AccountStatus    string `json:"accountStatus"`
	TenantName       string `json:"tenantName"`
	TenantCreateTime string `json:"tenantCreateTime"`
	CreateTime       string `json:"createTime"`

	// IniSec 指向 oci-help.ini 中同名段（若存在）。
	// 仅用于查找该账号下的实例模版 [账号.INSTANCE.*]，不参与持久化。
	IniSec *ini.Section `json:"-"`
}

// CreateTask 是一条持久化的抢机任务。字段对齐参考项目 oci_create_task 表。
type CreateTask struct {
	ID                 string  `json:"id"`
	TenantID           string  `json:"tenantId"`
	TenantName         string  `json:"tenantName"`
	Region             string  `json:"region"`
	Ocpus              float32 `json:"ocpus"`
	Memory             float32 `json:"memory"`
	Disk               int64   `json:"disk"`
	Architecture       string  `json:"architecture"`
	RetryMin           int     `json:"retryMin"` // 失败重试等待下限（秒），<=0 表示未设置
	RetryMax           int     `json:"retryMax"` // 失败重试等待上限（秒），<=0 表示未设置
	CreateNumbers      int     `json:"createNumbers"`
	RootPassword       string  `json:"rootPassword"`
	OperationSystem    string  `json:"operationSystem"`
	OSVersion          string  `json:"osVersion"`
	SSHPublicKey       string  `json:"sshPublicKey"`
	InstanceName       string  `json:"instanceName"`
	AvailabilityDomain string  `json:"availabilityDomain"`
	CreateTime         string  `json:"createTime"`
	Paused             int     `json:"paused"`
}

const (
	dbFileName = "./oci-help.db"

	schemaSQL = `
CREATE TABLE IF NOT EXISTS oci_user (
  id                 TEXT PRIMARY KEY,
  name               TEXT NOT NULL,
  user               TEXT,
  fingerprint        TEXT,
  tenancy            TEXT,
  region             TEXT,
  key_file           TEXT,
  key_password       TEXT,
  proxy              TEXT,
  plan_type          TEXT,
  account_status     TEXT,
  tenant_name        TEXT,
  tenant_create_time TEXT,
  create_time        TEXT
);
CREATE TABLE IF NOT EXISTS oci_create_task (
  id                  TEXT PRIMARY KEY,
  user_id             TEXT,
  oci_region          TEXT,
  ocpus               REAL,
  memory              REAL,
  disk                INTEGER,
  architecture        TEXT,
  retry_min           INTEGER,
  retry_max           INTEGER,
  create_numbers      INTEGER,
  root_password       TEXT,
  operation_system    TEXT,
  os_version          TEXT,
  ssh_public_key      TEXT,
  instance_name       TEXT,
  availability_domain TEXT,
  create_time         TEXT,
  paused              INTEGER DEFAULT 0
);
CREATE TABLE IF NOT EXISTS oci_kv (
  code  TEXT PRIMARY KEY,
  value TEXT,
  type  TEXT
);
`
)

// OpenStore 打开（必要时创建）本地数据库。
func OpenStore(path string) (*Store, error) {
	if path == "" {
		path = dbFileName
	}
	// 统一为斜杠形式，避免 Windows 绝对路径在 file: URI 中被误解析。
	dsn := "file:" + filepath.ToSlash(path) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite 写入串行化：单连接可避免 database is locked。
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) migrate() error {
	if _, err := s.db.Exec(schemaSQL); err != nil {
		return err
	}
	// 老库补列：SQLite 不支持 ADD COLUMN IF NOT EXISTS，重复添加会报 duplicate column，忽略即可。
	for _, col := range []string{"retry_min", "retry_max"} {
		if _, err := s.db.Exec(`ALTER TABLE oci_create_task ADD COLUMN ` + col + ` INTEGER`); err != nil &&
			!strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	return nil
}

func nowStr() string { return time.Now().Format("2006-01-02 15:04:05") }

/* ------------------------------------------------------------------ 租户 */

func (s *Store) ListTenants() ([]Tenant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT id,name,user,fingerprint,tenancy,region,key_file,key_password,
		COALESCE(proxy,''),COALESCE(plan_type,''),COALESCE(account_status,''),
		COALESCE(tenant_name,''),COALESCE(tenant_create_time,''),COALESCE(create_time,'')
		FROM oci_user ORDER BY create_time, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Tenant{}
	for rows.Next() {
		var t Tenant
		if err := rows.Scan(&t.ID, &t.Name, &t.User, &t.Fingerprint, &t.Tenancy, &t.Region,
			&t.KeyFile, &t.KeyPassword, &t.Proxy, &t.PlanType, &t.AccountStatus,
			&t.TenantName, &t.TenantCreateTime, &t.CreateTime); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) GetTenant(id string) (Tenant, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getTenantLocked(id)
}

func (s *Store) getTenantLocked(id string) (Tenant, bool, error) {
	var t Tenant
	row := s.db.QueryRow(`SELECT id,name,user,fingerprint,tenancy,region,key_file,key_password,
		COALESCE(proxy,''),COALESCE(plan_type,''),COALESCE(account_status,''),
		COALESCE(tenant_name,''),COALESCE(tenant_create_time,''),COALESCE(create_time,'')
		FROM oci_user WHERE id=?`, id)
	err := row.Scan(&t.ID, &t.Name, &t.User, &t.Fingerprint, &t.Tenancy, &t.Region,
		&t.KeyFile, &t.KeyPassword, &t.Proxy, &t.PlanType, &t.AccountStatus,
		&t.TenantName, &t.TenantCreateTime, &t.CreateTime)
	if errors.Is(err, sql.ErrNoRows) {
		return Tenant{}, false, nil
	}
	if err != nil {
		return Tenant{}, false, err
	}
	return t, true, nil
}

// GetTenantByName 按账号名查询（名字唯一）。
func (s *Store) GetTenantByName(name string) (Tenant, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var t Tenant
	row := s.db.QueryRow(`SELECT id,name,user,fingerprint,tenancy,region,key_file,key_password,
		COALESCE(proxy,''),COALESCE(plan_type,''),COALESCE(account_status,''),
		COALESCE(tenant_name,''),COALESCE(tenant_create_time,''),COALESCE(create_time,'')
		FROM oci_user WHERE name=? LIMIT 1`, name)
	err := row.Scan(&t.ID, &t.Name, &t.User, &t.Fingerprint, &t.Tenancy, &t.Region,
		&t.KeyFile, &t.KeyPassword, &t.Proxy, &t.PlanType, &t.AccountStatus,
		&t.TenantName, &t.TenantCreateTime, &t.CreateTime)
	if errors.Is(err, sql.ErrNoRows) {
		return Tenant{}, false, nil
	}
	if err != nil {
		return Tenant{}, false, err
	}
	return t, true, nil
}

// UpsertTenant 新增或更新账号；id 为空时自动生成。
func (s *Store) UpsertTenant(t Tenant) (Tenant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.ID == "" {
		t.ID = genID("u")
	}
	if t.CreateTime == "" {
		t.CreateTime = nowStr()
	}
	_, err := s.db.Exec(`INSERT INTO oci_user
		(id,name,user,fingerprint,tenancy,region,key_file,key_password,proxy,plan_type,account_status,tenant_name,tenant_create_time,create_time)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name, user=excluded.user, fingerprint=excluded.fingerprint,
			tenancy=excluded.tenancy, region=excluded.region, key_file=excluded.key_file,
			key_password=excluded.key_password, proxy=excluded.proxy, plan_type=excluded.plan_type,
			account_status=excluded.account_status, tenant_name=excluded.tenant_name,
			tenant_create_time=excluded.tenant_create_time`,
		t.ID, t.Name, t.User, t.Fingerprint, t.Tenancy, t.Region, t.KeyFile, t.KeyPassword,
		t.Proxy, t.PlanType, t.AccountStatus, t.TenantName, t.TenantCreateTime, t.CreateTime)
	if err != nil {
		return Tenant{}, err
	}
	return t, nil
}

// SetTenantMeta 只更新租户的展示性元数据（名称、套餐、状态等），用于异步回填。
func (s *Store) SetTenantMeta(id, tenantName, tenantCreateTime, planType, accountStatus string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE oci_user SET
		tenant_name=COALESCE(NULLIF(?,''),tenant_name),
		tenant_create_time=COALESCE(NULLIF(?,''),tenant_create_time),
		plan_type=COALESCE(NULLIF(?,''),plan_type),
		account_status=COALESCE(NULLIF(?,''),account_status)
		WHERE id=?`, tenantName, tenantCreateTime, planType, accountStatus, id)
	return err
}

func (s *Store) DeleteTenant(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.Exec(`DELETE FROM oci_user WHERE id=?`, id); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM oci_create_task WHERE user_id=?`, id)
	return err
}

func (s *Store) CountTenants() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM oci_user`).Scan(&n)
	return n, err
}

/* ------------------------------------------------------------------ 抢机任务 */

func (s *Store) ListTasks() ([]CreateTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT id,COALESCE(user_id,''),COALESCE(oci_region,''),COALESCE(ocpus,0),
		COALESCE(memory,0),COALESCE(disk,0),COALESCE(architecture,''),COALESCE(retry_min,0),COALESCE(retry_max,0),
		COALESCE(create_numbers,0),COALESCE(root_password,''),COALESCE(operation_system,''),
		COALESCE(os_version,''),COALESCE(ssh_public_key,''),COALESCE(instance_name,''),
		COALESCE(availability_domain,''),COALESCE(create_time,''),COALESCE(paused,0)
		FROM oci_create_task ORDER BY create_time`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CreateTask{}
	for rows.Next() {
		var t CreateTask
		if err := rows.Scan(&t.ID, &t.TenantID, &t.Region, &t.Ocpus, &t.Memory, &t.Disk,
			&t.Architecture, &t.RetryMin, &t.RetryMax, &t.CreateNumbers, &t.RootPassword, &t.OperationSystem,
			&t.OSVersion, &t.SSHPublicKey, &t.InstanceName, &t.AvailabilityDomain, &t.CreateTime, &t.Paused); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) GetTask(id string) (CreateTask, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var t CreateTask
	row := s.db.QueryRow(`SELECT id,COALESCE(user_id,''),COALESCE(oci_region,''),COALESCE(ocpus,0),
		COALESCE(memory,0),COALESCE(disk,0),COALESCE(architecture,''),COALESCE(retry_min,0),COALESCE(retry_max,0),
		COALESCE(create_numbers,0),COALESCE(root_password,''),COALESCE(operation_system,''),
		COALESCE(os_version,''),COALESCE(ssh_public_key,''),COALESCE(instance_name,''),
		COALESCE(availability_domain,''),COALESCE(create_time,''),COALESCE(paused,0)
		FROM oci_create_task WHERE id=?`, id)
	err := row.Scan(&t.ID, &t.TenantID, &t.Region, &t.Ocpus, &t.Memory, &t.Disk,
		&t.Architecture, &t.RetryMin, &t.RetryMax, &t.CreateNumbers, &t.RootPassword, &t.OperationSystem,
		&t.OSVersion, &t.SSHPublicKey, &t.InstanceName, &t.AvailabilityDomain, &t.CreateTime, &t.Paused)
	if errors.Is(err, sql.ErrNoRows) {
		return CreateTask{}, false, nil
	}
	if err != nil {
		return CreateTask{}, false, err
	}
	return t, true, nil
}

func (s *Store) UpsertTask(t CreateTask) (CreateTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.ID == "" {
		t.ID = genID("t")
	}
	if t.CreateTime == "" {
		t.CreateTime = nowStr()
	}
	_, err := s.db.Exec(`INSERT INTO oci_create_task
		(id,user_id,oci_region,ocpus,memory,disk,architecture,retry_min,retry_max,create_numbers,root_password,
		 operation_system,os_version,ssh_public_key,instance_name,availability_domain,create_time,paused)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			user_id=excluded.user_id, oci_region=excluded.oci_region, ocpus=excluded.ocpus,
			memory=excluded.memory, disk=excluded.disk, architecture=excluded.architecture,
			retry_min=excluded.retry_min, retry_max=excluded.retry_max,
			create_numbers=excluded.create_numbers,
			root_password=excluded.root_password, operation_system=excluded.operation_system,
			os_version=excluded.os_version, ssh_public_key=excluded.ssh_public_key,
			instance_name=excluded.instance_name, availability_domain=excluded.availability_domain,
			paused=excluded.paused`,
		t.ID, t.TenantID, t.Region, t.Ocpus, t.Memory, t.Disk, t.Architecture, t.RetryMin, t.RetryMax,
		t.CreateNumbers, t.RootPassword, t.OperationSystem, t.OSVersion, t.SSHPublicKey,
		t.InstanceName, t.AvailabilityDomain, t.CreateTime, t.Paused)
	if err != nil {
		return CreateTask{}, err
	}
	return t, nil
}

func (s *Store) SetTaskPaused(id string, paused int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE oci_create_task SET paused=? WHERE id=?`, paused, id)
	return err
}

func (s *Store) SetTaskCreateNumbers(id string, n int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE oci_create_task SET create_numbers=? WHERE id=?`, n, id)
	return err
}

func (s *Store) DeleteTask(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM oci_create_task WHERE id=?`, id)
	return err
}

/* ------------------------------------------------------------------ 键值配置 */

func (s *Store) GetKV(code string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var v string
	err := s.db.QueryRow(`SELECT value FROM oci_kv WHERE code=?`, code).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

func (s *Store) SetKV(code, value, typ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO oci_kv (code,value,type) VALUES (?,?,?)
		ON CONFLICT(code) DO UPDATE SET value=excluded.value, type=excluded.type`, code, value, typ)
	return err
}

/* ------------------------------------------------------------------ 辅助 */

var idSeq int64

// GenID 生成短 ID：前缀 + 时间戳 + 自增序号，避免引入额外依赖。
func GenID(prefix string) string {
	idSeq++
	return fmt.Sprintf("%s%d%03d", prefix, time.Now().UnixNano()/1e6, idSeq%1000)
}

// genID 包内别名。
func genID(prefix string) string { return GenID(prefix) }

// ValidTenant 校验账号必填字段。
func ValidTenant(t Tenant) error {
	var missing []string
	if strings.TrimSpace(t.Name) == "" {
		missing = append(missing, "name")
	}
	if strings.TrimSpace(t.User) == "" {
		missing = append(missing, "user")
	}
	if strings.TrimSpace(t.Fingerprint) == "" {
		missing = append(missing, "fingerprint")
	}
	if strings.TrimSpace(t.Tenancy) == "" {
		missing = append(missing, "tenancy")
	}
	if strings.TrimSpace(t.Region) == "" {
		missing = append(missing, "region")
	}
	if strings.TrimSpace(t.KeyFile) == "" {
		missing = append(missing, "keyFile")
	}
	if len(missing) > 0 {
		return fmt.Errorf("缺少必填字段: %s", strings.Join(missing, ", "))
	}
	return nil
}
