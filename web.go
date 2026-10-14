package main

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/oracle/oci-go-sdk/v54/core"
	"gopkg.in/ini.v1"
)

//go:embed web
var webAssets embed.FS

var (
	webMode   bool
	webAddr   string
	noBrowser bool
	webCfg    webConfig
)

type webConfig struct {
	Addr        string
	Username    string
	Password    string
	Path        string // 访问路径前缀，例如 /my-secret；为空表示根路径
	OpenBrowser bool
}

// normalizeWebPath 规整访问路径：确保以 "/" 开头、不以 "/" 结尾。
// 空字符串或 "/" 返回空串（表示根路径，不启用路径防护）。
func normalizeWebPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || p == "/" {
		return ""
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	p = strings.TrimRight(p, "/")
	if p == "" {
		return ""
	}
	return p
}

// loadWebConfig 读取 [web] 段配置；-addr 参数优先。未配置鉴权时仅监听本机。
func loadWebConfig(cfg *ini.File) error {
	webCfg.Addr = "127.0.0.1:8080"
	webCfg.OpenBrowser = true
	if sec := cfg.Section("web"); sec != nil {
		if v := strings.TrimSpace(sec.Key("addr").Value()); v != "" {
			webCfg.Addr = v
		}
		webCfg.Username = strings.TrimSpace(sec.Key("username").Value())
		webCfg.Password = strings.TrimSpace(sec.Key("password").Value())
		webCfg.Path = normalizeWebPath(sec.Key("path").Value())
		if sec.HasKey("openBrowser") {
			if v, err := sec.Key("openBrowser").Bool(); err == nil {
				webCfg.OpenBrowser = v
			}
		}
		if enabled, _ := sec.Key("enabled").Bool(); enabled {
			webMode = true
		}
	}
	// 默认段 web=1 也可启用
	if defSec := cfg.Section(ini.DefaultSection); defSec.HasKey("web") {
		if b, err := defSec.Key("web").Bool(); err == nil && b {
			webMode = true
		}
	}
	// 显式指定监听地址即视为启用 Web
	if webAddr != "" {
		webCfg.Addr = webAddr
		webMode = true
	}
	// 只配置了 username/password 其中之一时拒绝启动，
	// 否则会出现“用户名为 admin、密码为空”这种伪鉴权（空密码即视为正确密码）。
	if webMode && (webCfg.Username == "") != (webCfg.Password == "") {
		return fmt.Errorf("[web] username 与 password 必须同时配置，当前只配置了其中一个")
	}
	return nil
}

// authEnabled 仅在 username 与 password 都已配置时才启用鉴权。
// 这是鉴权开关的唯一判定入口，避免各处条件不一致。
func authEnabled() bool {
	return webCfg.Username != "" && webCfg.Password != ""
}

func runWebServer() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/version", handleVersion)
	mux.HandleFunc("/api/accounts", handleAccounts)
	mux.HandleFunc("/api/instances", handleInstances)
	mux.HandleFunc("/api/instance", handleInstance)
	mux.HandleFunc("/api/instance/action", handleInstanceAction)
	mux.HandleFunc("/api/instances/action-all", handleInstanceActionAll)
	mux.HandleFunc("/api/instance/public-ip", handleChangePublicIp)
	mux.HandleFunc("/api/instance/update", handleInstanceUpdate)
	mux.HandleFunc("/api/boot-volumes", handleBootVolumes)
	mux.HandleFunc("/api/boot-volume", handleBootVolume)
	mux.HandleFunc("/api/boot-volume/update", handleBootVolumeUpdate)
	mux.HandleFunc("/api/boot-volume/detach", handleBootVolumeDetach)
	mux.HandleFunc("/api/boot-volume/delete", handleBootVolumeDelete)
	mux.HandleFunc("/api/templates", handleTemplates)
	mux.HandleFunc("/api/launch", handleLaunch)
	mux.HandleFunc("/api/jobs", handleJobs)
	mux.HandleFunc("/api/job", handleJob)
	mux.HandleFunc("/api/job/cancel", handleJobCancel)
	mux.HandleFunc("/api/job/delete", handleJobDelete)
	mux.HandleFunc("/api/jobs/delete", handleJobsDelete)
	mux.HandleFunc("/api/export-ips", handleExportIps)

	sub, err := fs.Sub(webAssets, "web")
	if err != nil {
		return err
	}
	fileServer := http.FileServer(http.FS(sub))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 静态资源随二进制一起更新，禁止强缓存，避免升级后浏览器仍加载旧界面。
		w.Header().Set("Cache-Control", "no-cache")
		fileServer.ServeHTTP(w, r)
	}))

	addr := webCfg.Addr
	if !authEnabled() {
		addr = forceLoopback(addr)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		host = ""
	}
	// 仅在回环监听时严格校验 Host（阻断 DNS rebinding）；
	// 对外监听时用户可能通过域名/网卡地址访问，不做 Host 白名单。
	strictHost := isLoopbackHost(host)
	serve := buildWebHandler(withGuards(withAuth(mux), strictHost, configuredHost()))

	accessURL := "http://" + displayAddr(ln.Addr().String())
	if webCfg.Path != "" {
		accessURL += webCfg.Path + "/"
	} else {
		accessURL += "/"
	}

	fmt.Printf("\033[1;32mWeb 管理界面已启动:\033[0m %s\n", accessURL)
	if authEnabled() {
		fmt.Printf("已启用登录鉴权 (用户名: %s)\n", webCfg.Username)
	} else {
		fmt.Println("未配置 [web] username/password，已限制仅本机访问")
	}
	if webCfg.Path != "" {
		fmt.Printf("已启用访问路径保护: %s\n", webCfg.Path)
	}
	if strictHost {
		fmt.Println("已校验 Host 请求头，仅接受通过本机地址访问")
	}
	fmt.Printf("共加载 %d 个甲骨文账号\n", len(oracleSections))
	if shouldOpenBrowser() {
		openBrowser(accessURL)
	}
	return http.Serve(ln, serve)
}

// buildWebHandler 在已构建好的 handler 外层套上访问路径前缀。
// 配置了 [web] path 时，仅在 <path>/ 下提供服务，其他路径（含根路径）一律 404。
func buildWebHandler(handler http.Handler) http.Handler {
	if webCfg.Path == "" {
		return handler
	}
	outer := http.NewServeMux()
	outer.Handle(webCfg.Path+"/", http.StripPrefix(webCfg.Path, handler))
	return outer
}

// shouldOpenBrowser 是否在启动后自动打开浏览器（-no-browser 优先于配置）。
func shouldOpenBrowser() bool {
	return webCfg.OpenBrowser && !noBrowser
}

// openBrowser 用系统默认浏览器打开地址；失败只提示，不影响服务。
func openBrowser(u string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	case "darwin":
		cmd = exec.Command("open", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	if err := cmd.Start(); err != nil {
		fmt.Printf("未能自动打开浏览器，请手动访问: %s\n", u)
		return
	}
	go func() { _ = cmd.Wait() }()
}

// forceLoopback 在未启用鉴权时强制绑定到回环地址，避免实例被任意访问/操作。
func forceLoopback(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "127.0.0.1:8080"
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

func displayAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "::" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

// withAuth 仅对 /api/ 接口做鉴权。
// 静态资源（HTML/CSS/JS）不含任何敏感数据，放行后由前端渲染自定义登录框；
// 否则浏览器直接访问根路径会看到纯文本 "Unauthorized" 页面。
// publicAPIPaths 是不需要鉴权的接口（版本信息非敏感，登录页也需展示）。
var publicAPIPaths = map[string]bool{
	"/api/version": true,
}

func withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !authEnabled() || !strings.HasPrefix(r.URL.Path, "/api/") || publicAPIPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		user, pass, ok := r.BasicAuth()
		if !ok ||
			subtle.ConstantTimeCompare([]byte(user), []byte(webCfg.Username)) != 1 ||
			subtle.ConstantTimeCompare([]byte(pass), []byte(webCfg.Password)) != 1 {
			// realm 前缀加 x 避免浏览器触发原生 Basic Auth 弹窗，由前端接管登录交互。
			w.Header().Set("WWW-Authenticate", `xBasic realm="oci-help"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// withGuards 在鉴权之前做请求防护：
//  1. Host 校验：仅回环监听时启用，阻断 DNS rebinding 读写本机服务；
//  2. 写请求必须是同源且 Content-Type 为 application/json，
//     使浏览器必须发起 CORS 预检，从而阻断表单类 CSRF。
func withGuards(next http.Handler, strictHost bool, extraHost string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strictHost && !hostAllowed(r.Host, extraHost) {
			http.Error(w, "Forbidden: invalid Host header", http.StatusForbidden)
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if !isJSONContentType(r.Header.Get("Content-Type")) {
				writeErr(w, http.StatusUnsupportedMediaType, "请求头必须为 Content-Type: application/json")
				return
			}
			if !sameSiteRequest(r) {
				writeErr(w, http.StatusForbidden, "已拒绝跨站请求")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// hostAllowed 仅接受回环地址、localhost，以及配置中显式写出的主机名（例如 addr=panel.local:8080）。
func hostAllowed(hostport, extraHost string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if isLoopbackHost(host) {
		return true
	}
	return extraHost != "" && strings.EqualFold(host, strings.Trim(extraHost, "[]"))
}

// configuredHost 返回 [web] addr / -addr 中显式配置的主机名，通配地址与端口除外。
func configuredHost() string {
	h, _, err := net.SplitHostPort(webCfg.Addr)
	if err != nil {
		return ""
	}
	switch h {
	case "", "0.0.0.0", "::":
		return ""
	}
	return h
}

// isLoopbackHost 判断主机名或 IP 是否为回环地址。
func isLoopbackHost(host string) bool {
	if host == "" {
		return false
	}
	host = strings.Trim(host, "[]")
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return strings.EqualFold(host, "localhost")
}

func isJSONContentType(v string) bool {
	mt, _, err := mime.ParseMediaType(v)
	if err != nil {
		return false
	}
	return strings.EqualFold(mt, "application/json")
}

// sameSiteRequest 判断写请求是否来自同源页面：
// 优先使用 Sec-Fetch-Site（现代浏览器），缺失时回退比较 Origin 与 Host。
func sameSiteRequest(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "":
		// 老浏览器或命令行工具，继续用 Origin 判断
	default:
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

/* ------------------------------------------------------------------ 账号缓存 */

var (
	accountCacheMu sync.Mutex
	accountCache   = map[string]*AccountCtx{}
)

func getAccount(name string) (*AccountCtx, error) {
	if name == "" {
		if len(oracleSections) == 1 {
			name = oracleSections[0].Name()
		} else {
			return nil, fmt.Errorf("请指定账号")
		}
	}
	accountCacheMu.Lock()
	if ac, ok := accountCache[name]; ok {
		accountCacheMu.Unlock()
		return ac, nil
	}
	accountCacheMu.Unlock()

	var sec *ini.Section
	for _, s := range oracleSections {
		if s.Name() == name {
			sec = s
			break
		}
	}
	if sec == nil {
		return nil, fmt.Errorf("账号不存在: %s", name)
	}
	ac, err := newAccountCtx(sec)
	if err != nil {
		return nil, err
	}
	accountCacheMu.Lock()
	accountCache[name] = ac
	accountCacheMu.Unlock()
	return ac, nil
}

/* ------------------------------------------------------------------ 通用响应 */

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v interface{}) error {
	defer r.Body.Close()
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v)
}

func s(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func f32(v *float32) float32 {
	if v == nil {
		return 0
	}
	return *v
}

func i64(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

func bval(v *bool) bool {
	return v != nil && *v
}

// shapeOcpus / shapeMemoryInGBs 安全读取实例规格。
// SDK 中 Instance.ShapeConfig 本身是可选的指针字段，可能为 nil。
func shapeOcpus(sc *core.InstanceShapeConfig) float32 {
	if sc == nil {
		return 0
	}
	return f32(sc.Ocpus)
}

func shapeMemoryInGBs(sc *core.InstanceShapeConfig) float32 {
	if sc == nil {
		return 0
	}
	return f32(sc.MemoryInGBs)
}

func shapeBandwidth(sc *core.InstanceShapeConfig) float32 {
	if sc == nil {
		return 0
	}
	return f32(sc.NetworkingBandwidthInGbps)
}

/* ------------------------------------------------------------------ 视图结构 */

type accountView struct {
	Name   string `json:"name"`
	Region string `json:"region"`
}

type instanceView struct {
	ID                 string   `json:"id"`
	DisplayName        string   `json:"displayName"`
	State              string   `json:"state"`
	StateRaw           string   `json:"stateRaw"`
	Shape              string   `json:"shape"`
	Ocpus              float32  `json:"ocpus"`
	MemoryInGBs        float32  `json:"memoryInGBs"`
	AvailabilityDomain string   `json:"availabilityDomain"`
	PublicIps          []string `json:"publicIps"`
}

type pluginView struct {
	Name         string `json:"name"`
	DesiredState string `json:"desiredState"`
}

type instanceDetailView struct {
	instanceView
	NetworkingBandwidthInGbps float32      `json:"networkingBandwidthInGbps"`
	PrivateIps                []string     `json:"privateIps"`
	IsMonitoringDisabled      bool         `json:"isMonitoringDisabled"`
	IsManagementDisabled      bool         `json:"isManagementDisabled"`
	AreAllPluginsDisabled     bool         `json:"areAllPluginsDisabled"`
	Plugins                   []pluginView `json:"plugins"`
}

type bootVolumeView struct {
	ID                 string `json:"id"`
	DisplayName        string `json:"displayName"`
	State              string `json:"state"`
	SizeInGBs          int64  `json:"sizeInGBs"`
	VpusPerGB          int64  `json:"vpusPerGB"`
	AvailabilityDomain string `json:"availabilityDomain"`
}

type bootVolumeAttachmentView struct {
	ID           string `json:"id"`
	InstanceId   string `json:"instanceId"`
	InstanceName string `json:"instanceName"`
	State        string `json:"state"`
}

type templateView struct {
	Name                   string  `json:"name"`
	Shape                  string  `json:"shape"`
	Ocpus                  float32 `json:"ocpus"`
	MemoryInGBs            float32 `json:"memoryInGBs"`
	BootVolumeSizeInGBs    int64   `json:"bootVolumeSizeInGBs"`
	Sum                    int32   `json:"sum"`
	Each                   int32   `json:"each"`
	Retry                  int32   `json:"retry"`
	AvailabilityDomain     string  `json:"availabilityDomain"`
	OperatingSystem        string  `json:"operatingSystem"`
	OperatingSystemVersion string  `json:"operatingSystemVersion"`
}

/* ------------------------------------------------------------------ 版本 */

// handleVersion 返回构建信息，供界面在程序名下方展示版本号。
func handleVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"version":   Version,
		"commit":    Commit,
		"buildTime": BuildTime,
	})
}

/* ------------------------------------------------------------------ 账号 */

func handleAccounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	list := make([]accountView, 0, len(oracleSections))
	for _, sec := range oracleSections {
		list = append(list, accountView{Name: sec.Name(), Region: sec.Key("region").Value()})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"accounts":   list,
		"authEnable": authEnabled(),
	})
}

/* ------------------------------------------------------------------ 实例 */

func handleInstances(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ac, err := getAccount(r.URL.Query().Get("account"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	instances, ips, err := ac.ListInstancesWithIps()
	if err != nil {
		writeErr(w, http.StatusBadGateway, "获取实例失败: "+err.Error())
		return
	}
	out := make([]instanceView, 0, len(instances))
	for i := range instances {
		ins := instances[i]
		id := s(ins.Id)
		out = append(out, instanceView{
			ID:                 id,
			DisplayName:        s(ins.DisplayName),
			State:              getInstanceState(ins.LifecycleState),
			StateRaw:           string(ins.LifecycleState),
			Shape:              s(ins.Shape),
			Ocpus:              shapeOcpus(ins.ShapeConfig),
			MemoryInGBs:        shapeMemoryInGBs(ins.ShapeConfig),
			AvailabilityDomain: s(ins.AvailabilityDomain),
			PublicIps:          ips[id],
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"instances": out})
}

func handleInstance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ac, err := getAccount(r.URL.Query().Get("account"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 id")
		return
	}
	ins, err := ac.getInstance(&id)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "获取实例详情失败: "+err.Error())
		return
	}
	vnics, err := ac.getInstanceVnics(&id)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "获取实例VNIC失败: "+err.Error())
		return
	}
	var publicIps, privateIps []string
	for _, v := range vnics {
		if v.PublicIp != nil && *v.PublicIp != "" {
			publicIps = append(publicIps, *v.PublicIp)
		}
		if v.PrivateIp != nil && *v.PrivateIp != "" {
			privateIps = append(privateIps, *v.PrivateIp)
		}
	}

	detail := instanceDetailView{
		instanceView: instanceView{
			ID:                 s(ins.Id),
			DisplayName:        s(ins.DisplayName),
			State:              getInstanceState(ins.LifecycleState),
			StateRaw:           string(ins.LifecycleState),
			Shape:              s(ins.Shape),
			Ocpus:              shapeOcpus(ins.ShapeConfig),
			MemoryInGBs:        shapeMemoryInGBs(ins.ShapeConfig),
			AvailabilityDomain: s(ins.AvailabilityDomain),
			PublicIps:          publicIps,
		},
		PrivateIps: privateIps,
	}
	detail.NetworkingBandwidthInGbps = shapeBandwidth(ins.ShapeConfig)
	if ins.AgentConfig != nil {
		detail.IsMonitoringDisabled = ins.AgentConfig.IsMonitoringDisabled != nil && *ins.AgentConfig.IsMonitoringDisabled
		detail.IsManagementDisabled = ins.AgentConfig.IsManagementDisabled != nil && *ins.AgentConfig.IsManagementDisabled
		detail.AreAllPluginsDisabled = ins.AgentConfig.AreAllPluginsDisabled != nil && *ins.AgentConfig.AreAllPluginsDisabled
		for _, p := range ins.AgentConfig.PluginsConfig {
			detail.Plugins = append(detail.Plugins, pluginView{Name: s(p.Name), DesiredState: string(p.DesiredState)})
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"instance": detail})
}

type actionRequest struct {
	Account string `json:"account"`
	ID      string `json:"id"`
	Action  string `json:"action"`
}

func handleInstanceAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req actionRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.ID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 id")
		return
	}
	ac, err := getAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	msg, err := doInstanceAction(ac, req.ID, req.Action)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": msg})
}

// doInstanceAction 执行单个实例操作，返回可读结果。
func doInstanceAction(ac *AccountCtx, id, action string) (string, error) {
	switch strings.ToLower(action) {
	case "start":
		if _, err := ac.instanceAction(&id, core.InstanceActionActionStart); err != nil {
			return "", fmt.Errorf("启动实例失败: %w", err)
		}
		return "正在启动实例，请稍后刷新查看状态", nil
	case "stop":
		if _, err := ac.instanceAction(&id, core.InstanceActionActionSoftstop); err != nil {
			return "", fmt.Errorf("停止实例失败: %w", err)
		}
		return "正在停止实例，请稍后刷新查看状态", nil
	case "restart":
		if _, err := ac.instanceAction(&id, core.InstanceActionActionSoftreset); err != nil {
			return "", fmt.Errorf("重启实例失败: %w", err)
		}
		return "正在重启实例，请稍后刷新查看状态", nil
	case "terminate":
		if err := ac.terminateInstance(&id); err != nil {
			return "", fmt.Errorf("终止实例失败: %w", err)
		}
		return "正在终止实例，请稍后刷新查看状态", nil
	default:
		return "", fmt.Errorf("不支持的操作: %s", action)
	}
}

func handleInstanceActionAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req actionRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	ac, err := getAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	instances, err := ac.ListAllInstances()
	if err != nil {
		writeErr(w, http.StatusBadGateway, "获取实例失败: "+err.Error())
		return
	}
	type result struct {
		Name  string `json:"name"`
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	results := make([]result, 0, len(instances))
	for i := range instances {
		ins := instances[i]
		name := s(ins.DisplayName)
		if ins.Id == nil {
			continue
		}
		_, err := doInstanceAction(ac, *ins.Id, req.Action)
		if err != nil {
			results = append(results, result{Name: name, OK: false, Error: err.Error()})
		} else {
			results = append(results, result{Name: name, OK: true})
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"results": results})
}

func handleChangePublicIp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req actionRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.ID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 id")
		return
	}
	ac, err := getAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	vnics, err := ac.getInstanceVnics(&req.ID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "获取实例VNIC失败: "+err.Error())
		return
	}
	if len(vnics) == 0 {
		writeErr(w, http.StatusBadGateway, "实例已终止或获取实例VNIC失败，请稍后重试")
		return
	}
	publicIp, err := ac.changePublicIp(vnics)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "更换实例公共IP失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "更换公共IP成功", "ip": s(publicIp.IpAddress)})
}

type instanceUpdateRequest struct {
	Account      string   `json:"account"`
	ID           string   `json:"id"`
	DisplayName  *string  `json:"displayName"`
	Ocpus        *float32 `json:"ocpus"`
	MemoryInGBs  *float32 `json:"memoryInGBs"`
	AgentDisable *bool    `json:"agentDisable"`
}

func handleInstanceUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req instanceUpdateRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.ID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 id")
		return
	}
	ac, err := getAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	var plugins []core.InstanceAgentPluginConfigDetails
	if req.AgentDisable != nil {
		ins, err := ac.getInstance(&req.ID)
		if err != nil {
			writeErr(w, http.StatusBadGateway, "获取实例详情失败: "+err.Error())
			return
		}
		if ins.AgentConfig != nil {
			plugins = ins.AgentConfig.PluginsConfig
		}
	}

	if _, err := ac.updateInstance(&req.ID, req.DisplayName, req.Ocpus, req.MemoryInGBs, plugins, req.AgentDisable); err != nil {
		writeErr(w, http.StatusBadGateway, "更新实例失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "更新实例成功"})
}

/* ------------------------------------------------------------------ 引导卷 */

func handleBootVolumes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ac, err := getAccount(r.URL.Query().Get("account"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	volumes, err := ac.ListAllBootVolumes()
	if err != nil {
		writeErr(w, http.StatusBadGateway, "获取引导卷失败: "+err.Error())
		return
	}
	out := make([]bootVolumeView, 0, len(volumes))
	for i := range volumes {
		v := volumes[i]
		out = append(out, bootVolumeView{
			ID:                 s(v.Id),
			DisplayName:        s(v.DisplayName),
			State:              getBootVolumeState(v.LifecycleState),
			SizeInGBs:          i64(v.SizeInGBs),
			VpusPerGB:          i64(v.VpusPerGB),
			AvailabilityDomain: s(v.AvailabilityDomain),
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"bootVolumes": out})
}

func handleBootVolume(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ac, err := getAccount(r.URL.Query().Get("account"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 id")
		return
	}
	bv, err := ac.getBootVolume(&id)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "获取引导卷详情失败: "+err.Error())
		return
	}
	attachments, _ := ac.listBootVolumeAttachments(bv.AvailabilityDomain, bv.CompartmentId, bv.Id)
	attViews := make([]bootVolumeAttachmentView, 0, len(attachments))
	for _, att := range attachments {
		name := ""
		if att.InstanceId != nil {
			if ins, err := ac.getInstance(att.InstanceId); err == nil {
				name = s(ins.DisplayName)
			}
		}
		attViews = append(attViews, bootVolumeAttachmentView{
			ID:           s(att.Id),
			InstanceId:   s(att.InstanceId),
			InstanceName: name,
			State:        string(att.LifecycleState),
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"bootVolume": bootVolumeView{
			ID:                 s(bv.Id),
			DisplayName:        s(bv.DisplayName),
			State:              getBootVolumeState(bv.LifecycleState),
			SizeInGBs:          i64(bv.SizeInGBs),
			VpusPerGB:          i64(bv.VpusPerGB),
			AvailabilityDomain: s(bv.AvailabilityDomain),
		},
		"attachments": attViews,
	})
}

type bootVolumeUpdateRequest struct {
	Account   string `json:"account"`
	ID        string `json:"id"`
	SizeInGBs *int64 `json:"sizeInGBs"`
	VpusPerGB *int64 `json:"vpusPerGB"`
}

func handleBootVolumeUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req bootVolumeUpdateRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.ID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 id")
		return
	}
	if req.SizeInGBs == nil && req.VpusPerGB == nil {
		writeErr(w, http.StatusBadRequest, "请提供 sizeInGBs 或 vpusPerGB")
		return
	}
	ac, err := getAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := ac.updateBootVolume(&req.ID, req.SizeInGBs, req.VpusPerGB); err != nil {
		writeErr(w, http.StatusBadGateway, "修改引导卷失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "修改引导卷成功，请稍后查看状态"})
}

func handleBootVolumeDetach(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req bootVolumeUpdateRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.ID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 id")
		return
	}
	ac, err := getAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	bv, err := ac.getBootVolume(&req.ID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "获取引导卷详情失败: "+err.Error())
		return
	}
	attachments, err := ac.listBootVolumeAttachments(bv.AvailabilityDomain, bv.CompartmentId, bv.Id)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "获取引导卷附件失败: "+err.Error())
		return
	}
	if len(attachments) == 0 {
		writeJSON(w, http.StatusOK, map[string]string{"message": "引导卷未附加到任何实例"})
		return
	}
	for _, att := range attachments {
		if _, err := ac.detachBootVolume(att.Id); err != nil {
			writeErr(w, http.StatusBadGateway, "分离引导卷失败: "+err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "分离引导卷成功，请稍后查看状态"})
}

func handleBootVolumeDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req bootVolumeUpdateRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.ID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 id")
		return
	}
	ac, err := getAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := ac.deleteBootVolume(&req.ID); err != nil {
		writeErr(w, http.StatusBadGateway, "终止引导卷失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "终止引导卷成功，请稍后查看状态"})
}

/* ------------------------------------------------------------------ 模板 / 创建实例 */

func handleTemplates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ac, err := getAccount(r.URL.Query().Get("account"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	sections := instanceTemplates(ac)
	out := make([]templateView, 0, len(sections))
	for _, sec := range sections {
		inst := Instance{}
		if err := sec.MapTo(&inst); err != nil {
			continue
		}
		out = append(out, templateView{
			Name:                   sec.Name(),
			Shape:                  inst.Shape,
			Ocpus:                  inst.Ocpus,
			MemoryInGBs:            inst.MemoryInGBs,
			BootVolumeSizeInGBs:    inst.BootVolumeSizeInGBs,
			Sum:                    inst.Sum,
			Each:                   inst.Each,
			Retry:                  inst.Retry,
			AvailabilityDomain:     inst.AvailabilityDomain,
			OperatingSystem:        inst.OperatingSystem,
			OperatingSystemVersion: inst.OperatingSystemVersion,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"templates": out})
}

type launchRequest struct {
	Account             string   `json:"account"`
	Template            string   `json:"template"`
	Sum                 *int32   `json:"sum"`
	Each                *int32   `json:"each"`
	Retry               *int32   `json:"retry"`
	Ocpus               *float32 `json:"ocpus"`
	MemoryInGBs         *float32 `json:"memoryInGBs"`
	BootVolumeSizeInGBs *int64   `json:"bootVolumeSizeInGBs"`
	AvailabilityDomain  *string  `json:"availabilityDomain"`
	InstanceDisplayName *string  `json:"instanceDisplayName"`
	SSHPublicKey        *string  `json:"sshPublicKey"`
}

func handleLaunch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req launchRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	ac, err := getAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var target *ini.Section
	for _, sec := range instanceTemplates(ac) {
		if sec.Name() == req.Template {
			target = sec
			break
		}
	}
	if target == nil {
		writeErr(w, http.StatusBadRequest, "实例模版不存在: "+req.Template)
		return
	}
	inst := Instance{}
	if err := target.MapTo(&inst); err != nil {
		writeErr(w, http.StatusBadRequest, "解析实例模版失败: "+err.Error())
		return
	}
	// 应用前端覆盖参数
	if req.Sum != nil {
		inst.Sum = *req.Sum
	}
	if req.Each != nil {
		inst.Each = *req.Each
	}
	if req.Retry != nil {
		inst.Retry = *req.Retry
	}
	if req.Ocpus != nil {
		inst.Ocpus = *req.Ocpus
	}
	if req.MemoryInGBs != nil {
		inst.MemoryInGBs = *req.MemoryInGBs
	}
	if req.BootVolumeSizeInGBs != nil {
		inst.BootVolumeSizeInGBs = *req.BootVolumeSizeInGBs
	}
	if req.AvailabilityDomain != nil {
		inst.AvailabilityDomain = *req.AvailabilityDomain
	}
	if req.InstanceDisplayName != nil {
		inst.InstanceDisplayName = *req.InstanceDisplayName
	}
	if req.SSHPublicKey != nil && *req.SSHPublicKey != "" {
		inst.SSH_Public_Key = *req.SSHPublicKey
	}

	// 校验创建个数，避免 sum/each 为 0 或负数时任务"成功"却什么都没创建。
	if inst.Sum < 0 || inst.Each < 0 {
		writeErr(w, http.StatusBadRequest, "sum 与 each 不能为负数")
		return
	}
	if inst.Sum == 0 && inst.Each == 0 {
		writeErr(w, http.StatusBadRequest, "创建个数必须大于 0：请设置 sum（总个数）或 each（每个可用性域个数）")
		return
	}
	if inst.Retry < -1 {
		writeErr(w, http.StatusBadRequest, "retry 不能小于 -1（-1 表示一直重试）")
		return
	}

	job := startLaunchJob(ac, inst)
	writeJSON(w, http.StatusOK, map[string]interface{}{"jobId": job.ID})
}

/* ------------------------------------------------------------------ 异步任务 */

var (
	jobsMu sync.Mutex
	jobs   = map[string]*Job{}
	jobSeq int64
)

// jobSummary 是任务列表用的轻量视图，不含日志，避免每次轮询都传输大量文本。
type jobSummary struct {
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

// jobDetail 是任务详情视图，只返回 since 之后的增量日志。
type jobDetail struct {
	jobSummary
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

func newJob(typ, account string) *Job {
	jobsMu.Lock()
	jobSeq++
	id := fmt.Sprintf("%s-%d-%d", typ, time.Now().Unix(), jobSeq)
	j := &Job{ID: id, Type: typ, Account: account, Status: "running", Started: time.Now()}
	jobs[id] = j
	jobsMu.Unlock()
	return j
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func (j *Job) log(format string, a ...interface{}) {
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

func (j *Job) setProgress(stage string, done, total int) {
	j.mu.Lock()
	j.Stage = stage
	j.Done = done
	j.Total = total
	j.mu.Unlock()
}

// finishWith 以指定状态结束任务。
func (j *Job) finishWith(status, msg string) {
	j.mu.Lock()
	j.Status = status
	j.Ended = time.Now()
	if msg != "" {
		j.Logs = append(j.Logs, msg)
	}
	j.mu.Unlock()
}

func (j *Job) finish(msg string) {
	j.finishWith("success", msg)
}

func (j *Job) fail(msg string) {
	j.mu.Lock()
	j.Status = "failed"
	j.Error = msg
	j.Ended = time.Now()
	j.Logs = append(j.Logs, "❌ "+msg)
	j.mu.Unlock()
}

// canceled 标记任务被用户中止。
func (j *Job) canceled(msg string) {
	j.mu.Lock()
	j.Status = "canceled"
	j.Ended = time.Now()
	if msg != "" {
		j.Logs = append(j.Logs, msg)
	}
	j.mu.Unlock()
}

// stop 请求停止任务；正在运行的创建循环会在下一次检查时退出。
func (j *Job) stop() {
	atomic.StoreInt32(&j.stopped, 1)
}

func (j *Job) isStopped() bool {
	return atomic.LoadInt32(&j.stopped) == 1
}

// currentStatus 安全读取任务状态。
func (j *Job) currentStatus() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.Status
}

func (j *Job) summary() jobSummary {
	j.mu.Lock()
	defer j.mu.Unlock()
	return jobSummary{
		ID: j.ID, Type: j.Type, Account: j.Account, Status: j.Status,
		Stage: j.Stage, Done: j.Done, Total: j.Total, Error: j.Error,
		Started: j.Started, Ended: j.Ended,
		Stopping: j.isStopped(), LogCount: j.logDropped + len(j.Logs),
	}
}

// detailSince 返回序号 since 之后的日志。日志超出上限被裁剪时，
// 返回的 LogFrom 会大于请求的 since，客户端据此丢弃本地旧日志。
func (j *Job) detailSince(since int) jobDetail {
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
	return jobDetail{
		jobSummary: jobSummary{
			ID: j.ID, Type: j.Type, Account: j.Account, Status: j.Status,
			Stage: j.Stage, Done: j.Done, Total: j.Total, Error: j.Error,
			Started: j.Started, Ended: j.Ended,
			Stopping: j.isStopped(), LogCount: j.logDropped + len(j.Logs),
		},
		Logs:    logs,
		LogFrom: j.logDropped + start,
		LogNext: j.logDropped + len(j.Logs),
	}
}

// startLaunchJob 在后台执行创建实例任务，日志与进度写入 Job。
func startLaunchJob(ac *AccountCtx, inst Instance) *Job {
	job := newJob("launch", ac.Name)
	go runLaunchJob(job, ac, inst)
	return job
}

func runLaunchJob(job *Job, ac *AccountCtx, inst Instance) {
	defer func() {
		if rec := recover(); rec != nil {
			job.fail(fmt.Sprintf("任务异常: %v", rec))
		}
	}()
	ads, err := ac.ensureAvailabilityDomains()
	if err != nil {
		job.fail("获取可用性域失败: " + err.Error())
		return
	}
	// 使用独立副本，避免污染缓存中的 AccountCtx
	jc := *ac
	jc.Logger = job.log
	jc.Progress = job.setProgress
	jc.Stopped = job.isStopped
	sum, num := jc.LaunchInstances(ads, inst)
	msg := fmt.Sprintf("创建总数: %d, 成功: %d, 失败: %d", sum, num, sum-num)
	if job.isStopped() {
		job.canceled("任务已被中止。" + msg)
		return
	}
	job.finishWith(launchStatus(sum, num), "任务结束。"+msg)
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

func handleJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	jobsMu.Lock()
	list := make([]jobSummary, 0, len(jobs))
	for _, j := range jobs {
		list = append(list, j.summary())
	}
	jobsMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]interface{}{"jobs": list})
}

func handleJob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := r.URL.Query().Get("id")
	since, _ := strconv.Atoi(r.URL.Query().Get("since"))
	jobsMu.Lock()
	j, ok := jobs[id]
	jobsMu.Unlock()
	if !ok {
		writeErr(w, http.StatusNotFound, "任务不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"job": j.detailSince(since)})
}

// handleJobCancel 请求停止一个正在运行的任务。
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
	jobsMu.Lock()
	j, ok := jobs[req.ID]
	jobsMu.Unlock()
	if !ok {
		writeErr(w, http.StatusNotFound, "任务不存在")
		return
	}
	j.stop()
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
	jobsMu.Lock()
	j, ok := jobs[req.ID]
	if !ok {
		jobsMu.Unlock()
		writeErr(w, http.StatusNotFound, "任务不存在")
		return
	}
	if j.currentStatus() == "running" {
		jobsMu.Unlock()
		writeErr(w, http.StatusBadRequest, "运行中的任务不能删除，请先停止")
		return
	}
	delete(jobs, req.ID)
	jobsMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"message": "任务已删除"})
}

// handleJobsDelete 批量删除任务。
// body.status 为空 → 删除全部非运行中任务；否则只删除该状态的任务。
// 运行中的任务永远不会被删除。
func handleJobsDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Status string `json:"status"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.Status == "running" {
		writeErr(w, http.StatusBadRequest, "不能删除运行中的任务")
		return
	}
	removed := 0
	jobsMu.Lock()
	for id, j := range jobs {
		st := j.currentStatus()
		if st == "running" {
			continue
		}
		if req.Status == "" || st == req.Status {
			delete(jobs, id)
			removed++
		}
	}
	jobsMu.Unlock()
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
	ac, err := getAccount(req.Account)
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
	filename := IPsFilePrefix + "-" + time.Now().Format("2006-01-02-150405") + ".txt"
	writeJSON(w, http.StatusOK, map[string]string{
		"filename": filepath.Base(filename),
		"content":  string(content),
	})
}
