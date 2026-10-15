package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oci-help/internal/config"
	"oci-help/internal/job"
	"oci-help/internal/oci"
	"oci-help/internal/store"

	"github.com/oracle/oci-go-sdk/v65/core"
	"gopkg.in/ini.v1"
)

// setupWebTest 准备一个临时数据库与全局状态，供 HTTP 处理器测试使用。
func setupWebTest(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.OpenStore(filepath.Join(dir, "web.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// 生成一个占位私钥文件，供 newAccountCtx 读取。
	keyPath := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(keyPath, []byte("-----BEGIN PRIVATE KEY-----\nMIIB\n-----END PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	store.DB = s
	config.C = &config.Config{FilePath: filepath.Join(dir, "web.db")}

	if _, err := s.UpsertTenant(store.Tenant{
		Name: "test-acct", User: "u", Fingerprint: "f", Tenancy: "t", Region: "r", KeyFile: keyPath,
	}); err != nil {
		t.Fatal(err)
	}
	if err := oci.ReloadTenants(); err != nil {
		t.Fatalf("reloadTenants 失败: %v", err)
	}
}

func doJSON(t *testing.T, h http.HandlerFunc, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, "/", nil)
	} else {
		r = httptest.NewRequest(method, "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func TestHandleVersion(t *testing.T) {
	w := doJSON(t, handleVersion, http.MethodGet, "")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 200, got %d", w.Code)
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if _, ok := resp["version"]; !ok {
		t.Fatal("缺少 version 字段")
	}
}

func TestHandleAccounts(t *testing.T) {
	setupWebTest(t)
	w := doJSON(t, handleAccounts, http.MethodGet, "")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Accounts []accountView `json:"accounts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Accounts) != 1 || resp.Accounts[0].Name != "test-acct" {
		t.Fatalf("账号列表异常: %+v", resp.Accounts)
	}
}

func TestTenantSaveValidation(t *testing.T) {
	setupWebTest(t)
	// 缺少必填字段
	w := doJSON(t, handleTenantSave, http.MethodPost, `{"name":"x"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("缺少字段应返回 400, got %d body=%s", w.Code, w.Body.String())
	}

	// 合法保存
	body := `{"name":"new-acct","user":"u2","fingerprint":"f2","tenancy":"t2","region":"ap-tokyo-1","keyFile":"/tmp/k.pem"}`
	w = doJSON(t, handleTenantSave, http.MethodPost, body)
	if w.Code != http.StatusOK {
		t.Fatalf("保存应成功, got %d body=%s", w.Code, w.Body.String())
	}
	if n, _ := store.DB.CountTenants(); n != 2 {
		t.Fatalf("应有两个账号, got %d", n)
	}

	// 重名应被拒绝
	w = doJSON(t, handleTenantSave, http.MethodPost, body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("重名应返回 400, got %d", w.Code)
	}
}

func TestTasksEndpoint(t *testing.T) {
	setupWebTest(t)
	tenants, _ := store.DB.ListTenants()
	if _, err := store.DB.UpsertTask(store.CreateTask{TenantID: tenants[0].ID, Architecture: "AMD", CreateNumbers: 2, Disk: 47, Interval: 60}); err != nil {
		t.Fatal(err)
	}
	w := doJSON(t, handleTasks, http.MethodGet, "")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 200, got %d", w.Code)
	}
	var resp struct {
		Tasks []store.CreateTask `json:"tasks"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Tasks) != 1 || resp.Tasks[0].CreateNumbers != 2 {
		t.Fatalf("任务列表异常: %+v", resp.Tasks)
	}
}

func TestHandleTemplates(t *testing.T) {
	setupWebTest(t)

	// 方法守卫
	if w := doJSON(t, handleTemplates, http.MethodPost, ""); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST 应返回 405, got %d", w.Code)
	}

	// 账号不存在
	w := httptest.NewRecorder()
	handleTemplates(w, httptest.NewRequest(http.MethodGet, "/?account=nope", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("未知账号应返回 400, got %d", w.Code)
	}

	// 加载一份含实例模版的 INI（全局 [INSTANCE.ARM] + 账号级 [test-acct.INSTANCE.MY]）
	cfg, err := ini.LoadSources(ini.LoadOptions{AllowShadows: true}, []byte(`[INSTANCE]
OperatingSystem=Canonical Ubuntu
OperatingSystemVersion=20.04

[INSTANCE.ARM]
shape=VM.Standard.A1.Flex
cpus=2
memoryInGBs=12
bootVolumeSizeInGBs=100
sum=2
retry=-1

[test-acct]
user=u
fingerprint=f
tenancy=t
region=r
key_file=k

[test-acct.INSTANCE.MY]
shape=VM.Standard.E2.1.Micro
bootVolumeSizeInGBs=50
sum=1
`))
	if err != nil {
		t.Fatal(err)
	}
	config.C.File = cfg
	t.Cleanup(func() { config.C.File = nil })
	if err := oci.ReloadTenants(); err != nil {
		t.Fatalf("reloadTenants 失败: %v", err)
	}

	w = httptest.NewRecorder()
	handleTemplates(w, httptest.NewRequest(http.MethodGet, "/?account=test-acct", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("期望 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Templates []templateView `json:"templates"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Templates) != 2 {
		t.Fatalf("应返回 2 个模版（全局 + 账号级），got %d: %+v", len(resp.Templates), resp.Templates)
	}
	byName := map[string]templateView{}
	for _, tv := range resp.Templates {
		byName[tv.Name] = tv
	}
	arm, ok := byName["INSTANCE.ARM"]
	if !ok {
		t.Fatalf("缺少全局模版 INSTANCE.ARM: %+v", resp.Templates)
	}
	if arm.Shape != "VM.Standard.A1.Flex" || arm.Ocpus != 2 || arm.MemoryInGBs != 12 ||
		arm.BootVolumeSizeInGBs != 100 || arm.Sum != 2 {
		t.Fatalf("ARM 模版字段异常: %+v", arm)
	}
	// 子段应继承父段 [INSTANCE] 的系统信息
	if arm.OperatingSystem != "Canonical Ubuntu" || arm.OperatingSystemVersion != "20.04" {
		t.Fatalf("ARM 模版未继承系统信息: %+v", arm)
	}
	if _, ok := byName["test-acct.INSTANCE.MY"]; !ok {
		t.Fatalf("缺少账号级模版: %+v", resp.Templates)
	}
}

func TestSecurityRulesRequiresVcn(t *testing.T) {
	setupWebTest(t)
	w := doJSON(t, handleSecurityRules, http.MethodGet, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("缺少 vcnId 应返回 400, got %d", w.Code)
	}
}

func TestMethodGuards(t *testing.T) {
	setupWebTest(t)
	// 只允许 POST 的接口用 GET 调用应返回 405
	if w := doJSON(t, handleTenantSave, http.MethodGet, ""); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("期望 405, got %d", w.Code)
	}
	if w := doJSON(t, handleReleaseAllPortsStub, http.MethodGet, ""); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("期望 405, got %d", w.Code)
	}
}

// handleReleaseAllPortsStub 复用真实处理器做方法校验（GET 不应进入业务逻辑）。
func handleReleaseAllPortsStub(w http.ResponseWriter, r *http.Request) {
	handleSecurityRelease(w, r)
}

func TestTaskSaveValidation(t *testing.T) {
	setupWebTest(t)
	// 缺少租户
	w := doJSON(t, handleTaskSave, http.MethodPost, `{"tenantId":"","createNumbers":1}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("缺少租户应返回 400, got %d", w.Code)
	}
	// 数量为 0
	tenants, _ := store.DB.ListTenants()
	body := `{"tenantId":"` + tenants[0].ID + `","createNumbers":0}`
	w = doJSON(t, handleTaskSave, http.MethodPost, body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("数量为 0 应返回 400, got %d", w.Code)
	}
}

/* ------------------------------------------------------------------ 日志分类删除 */

func TestJobsDeleteByType(t *testing.T) {
	setupWebTest(t)
	for _, id := range []string{"t-1", "a-1", "t-2"} {
		job.Remove(id)
	}

	taskJob := job.NewJobWithID("t-1", "task", "acct")
	taskJob.Finish("done")
	actionJob := job.NewJobWithID("a-1", "launch", "acct")
	actionJob.Finish("done")
	_ = job.NewJobWithID("t-2", "task", "acct") // 运行中：任何情况下都不应被删除

	w := doJSON(t, handleJobsDelete, http.MethodPost, `{"status":"","type":"task"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("期望 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Removed int `json:"removed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Removed != 1 {
		t.Fatalf("应删除 1 条 task 日志, got %d", resp.Removed)
	}

	if job.JobByID("t-1") != nil {
		t.Fatal("task 类型日志应被删除")
	}
	if job.JobByID("a-1") == nil {
		t.Fatal("action 类型日志不应被删除")
	}
	if job.JobByID("t-2") == nil {
		t.Fatal("运行中的任务不应被删除")
	}

	// 非法 type
	if w := doJSON(t, handleJobsDelete, http.MethodPost, `{"type":"bogus"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("非法 type 应返回 400, got %d", w.Code)
	}

	// 清空 type=action 应删除 a-1
	w = doJSON(t, handleJobsDelete, http.MethodPost, `{"status":"","type":"action"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("期望 200, got %d", w.Code)
	}
	if job.JobByID("a-1") != nil {
		t.Fatal("action 类型日志应被删除")
	}
}

/* ------------------------------------------------------------------ 网络（VNIC / 子网 / NSG） */

func TestVnicListRequiresInstance(t *testing.T) {
	setupWebTest(t)
	if w := doJSON(t, handleVnicList, http.MethodGet, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("缺少 instanceId 应返回 400, got %d", w.Code)
	}
}

func TestVnicAttachValidation(t *testing.T) {
	setupWebTest(t)
	if w := doJSON(t, handleVnicAttach, http.MethodPost, `{"instanceId":"x"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("缺少 subnetId 应返回 400, got %d", w.Code)
	}
}

func TestSubnetEndpointsValidation(t *testing.T) {
	setupWebTest(t)
	if w := doJSON(t, handleSubnets, http.MethodGet, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("缺少 vcnId 应返回 400, got %d", w.Code)
	}
	if w := doJSON(t, handleSubnetCreate, http.MethodPost, `{"vcnId":"x"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("缺少 cidrBlock 应返回 400, got %d", w.Code)
	}
	if w := doJSON(t, handleSubnetUpdate, http.MethodPost, `{}`); w.Code != http.StatusBadRequest {
		t.Fatalf("缺少 subnetId 应返回 400, got %d", w.Code)
	}
}

func TestNsgEndpointsValidation(t *testing.T) {
	setupWebTest(t)
	if w := doJSON(t, handleNsgRules, http.MethodGet, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("缺少 nsgId 应返回 400, got %d", w.Code)
	}
	if w := doJSON(t, handleNsgRuleRemove, http.MethodPost, `{"nsgId":"x"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("缺少 ruleIds 应返回 400, got %d", w.Code)
	}
	if w := doJSON(t, handleNsgCreate, http.MethodPost, `{"vcnId":"x"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("缺少 name 应返回 400, got %d", w.Code)
	}
}

func TestNetworkMethodGuards(t *testing.T) {
	setupWebTest(t)
	cases := []struct {
		name string
		h    http.HandlerFunc
	}{
		{"vnic/attach", handleVnicAttach},
		{"subnet/create", handleSubnetCreate},
		{"subnet/delete", handleSubnetDelete},
		{"nsg/create", handleNsgCreate},
		{"nsg/rule/add", handleNsgRuleAdd},
	}
	for _, c := range cases {
		if w := doJSON(t, c.h, http.MethodGet, ""); w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s GET 应返回 405, got %d", c.name, w.Code)
		}
	}
}

func TestBuildAddNsgRule(t *testing.T) {
	// 入站 TCP 443
	r, err := oci.BuildAddNsgRule(oci.NsgRuleSpec{Direction: "ingress", Protocol: "tcp", Source: "0.0.0.0/0", PortMin: 443, PortMax: 443})
	if err != nil {
		t.Fatal(err)
	}
	if r.Direction != core.AddSecurityRuleDetailsDirectionIngress {
		t.Fatalf("方向应为 ingress, got %s", r.Direction)
	}
	if r.Protocol == nil || *r.Protocol != "6" {
		t.Fatalf("协议应映射为 6, got %v", r.Protocol)
	}
	if r.Source == nil || *r.Source != "0.0.0.0/0" {
		t.Fatalf("源未设置: %v", r.Source)
	}
	if r.TcpOptions == nil || r.TcpOptions.DestinationPortRange == nil {
		t.Fatal("TCP 端口范围未设置")
	}
	if *r.TcpOptions.DestinationPortRange.Min != 443 || *r.TcpOptions.DestinationPortRange.Max != 443 {
		t.Fatalf("端口范围错误")
	}

	// 出站 all
	e, err := oci.BuildAddNsgRule(oci.NsgRuleSpec{Direction: "egress", Protocol: "all", Destination: "0.0.0.0/0"})
	if err != nil {
		t.Fatal(err)
	}
	if e.Destination == nil || *e.Destination != "0.0.0.0/0" {
		t.Fatalf("出站目标未设置: %v", e.Destination)
	}
	if e.TcpOptions != nil || e.UdpOptions != nil {
		t.Fatal("all 协议不应带端口选项")
	}

	// 未知方向
	if _, err := oci.BuildAddNsgRule(oci.NsgRuleSpec{Direction: "sideways"}); err == nil {
		t.Fatal("未知方向应报错")
	}
}
