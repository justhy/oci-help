package web

import (
	"embed"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"oci-help/internal/config"
	"oci-help/internal/oci"
	"os/exec"
	"runtime"
	"strings"
)

//go:embed web
var webAssets embed.FS

// Addr / NoBrowser 由程序入口（main）在启动时赋值，供 Run 读取。
var (
	Addr      string // Web 监听地址，空表示取配置或 127.0.0.1:8080
	NoBrowser bool   // 启动 Web 界面时不自动打开浏览器
)

type webConfig struct {
	Addr        string
	Username    string
	Password    string
	Path        string // 访问路径前缀，例如 /my-secret；为空表示根路径
	OpenBrowser bool
}

var webCfg webConfig

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
// 只填充 webCfg，是否启动 Web 由入口根据 flag / 配置决定。
func loadWebConfig() error {
	webCfg.Addr = "127.0.0.1:8080"
	webCfg.OpenBrowser = true
	if config.C != nil && config.C.File != nil && config.C.File.Section("web") != nil {
		sec := config.C.File.Section("web")
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
	}
	if Addr != "" {
		webCfg.Addr = Addr
	}
	// 只配置了 username/password 其中之一时拒绝启动，
	// 否则会出现“用户名为 admin、密码为空”这种伪鉴权（空密码即视为正确密码）。
	if (webCfg.Username == "") != (webCfg.Password == "") {
		return fmt.Errorf("[web] username 与 password 必须同时配置，当前只配置了其中一个")
	}
	return nil
}

// authEnabled 仅在 username 与 password 都已配置时才启用鉴权。
// 这是鉴权开关的唯一判定入口，避免各处条件不一致。
func authEnabled() bool {
	return webCfg.Username != "" && webCfg.Password != ""
}

// Run 启动 Web 管理界面（阻塞直至进程退出）。
// 启动前自行读取 [web] 段配置并做鉴权参数成对校验。
func Run() error {
	if err := loadWebConfig(); err != nil {
		return err
	}
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

	// 租户（账号）配置
	mux.HandleFunc("/api/tenants/save", handleTenantSave)
	mux.HandleFunc("/api/tenants/delete", handleTenantDelete)
	// 抢机任务
	mux.HandleFunc("/api/tasks", handleTasks)
	mux.HandleFunc("/api/task/save", handleTaskSave)
	mux.HandleFunc("/api/task/pause", handleTaskPause)
	mux.HandleFunc("/api/task/resume", handleTaskResume)
	mux.HandleFunc("/api/task/delete", handleTaskDelete)
	// 网络：VCN / IPv6 / 安全列表 / 500Mbps / 救援
	mux.HandleFunc("/api/vcns", handleVcns)
	mux.HandleFunc("/api/ipv6/attach", handleIpv6Attach)
	mux.HandleFunc("/api/security/rules", handleSecurityRules)
	mux.HandleFunc("/api/security/rule/add", handleSecurityRuleAdd)
	mux.HandleFunc("/api/security/rule/remove", handleSecurityRuleRemove)
	mux.HandleFunc("/api/security/release", handleSecurityRelease)
	mux.HandleFunc("/api/instance/500m", handle500MEnable)
	mux.HandleFunc("/api/instance/500m/disable", handle500MDisable)
	mux.HandleFunc("/api/instance/rescue", handleRescue)
	// VNIC 管理
	mux.HandleFunc("/api/vnics", handleVnicList)
	mux.HandleFunc("/api/vnic/attach", handleVnicAttach)
	mux.HandleFunc("/api/vnic/detach", handleVnicDetach)
	mux.HandleFunc("/api/vnic/update", handleVnicUpdate)
	mux.HandleFunc("/api/vnic/private-ips", handleVnicPrivateIps)
	mux.HandleFunc("/api/vnic/private-ip/add", handleVnicPrivateIpAdd)
	mux.HandleFunc("/api/vnic/private-ip/delete", handleVnicPrivateIpDelete)
	mux.HandleFunc("/api/vnic/private-ip/update", handleVnicPrivateIpUpdate)
	mux.HandleFunc("/api/vnic/public-ip/reserve", handleVnicPublicIpReserve)
	mux.HandleFunc("/api/vnic/public-ip/release", handleVnicPublicIpRelease)
	// 子网管理
	mux.HandleFunc("/api/subnets", handleSubnets)
	mux.HandleFunc("/api/subnet/create", handleSubnetCreate)
	mux.HandleFunc("/api/subnet/update", handleSubnetUpdate)
	mux.HandleFunc("/api/subnet/delete", handleSubnetDelete)
	// 网络安全组（NSG）
	mux.HandleFunc("/api/nsgs", handleNsgs)
	mux.HandleFunc("/api/nsg/rules", handleNsgRules)
	mux.HandleFunc("/api/nsg/vnics", handleNsgVnics)
	mux.HandleFunc("/api/nsg/create", handleNsgCreate)
	mux.HandleFunc("/api/nsg/delete", handleNsgDelete)
	mux.HandleFunc("/api/nsg/rule/add", handleNsgRuleAdd)
	mux.HandleFunc("/api/nsg/rule/update", handleNsgRuleUpdate)
	mux.HandleFunc("/api/nsg/rule/remove", handleNsgRuleRemove)
	// 路由表 / 安全列表清单
	mux.HandleFunc("/api/route-tables", handleRouteTables)
	mux.HandleFunc("/api/security-lists", handleSecurityLists)
	// 流量 / 配额 / 租户信息
	mux.HandleFunc("/api/traffic/vnics", handleTrafficVnics)
	mux.HandleFunc("/api/traffic", handleTraffic)
	mux.HandleFunc("/api/limits/services", handleLimitServices)
	mux.HandleFunc("/api/limits", handleLimits)
	mux.HandleFunc("/api/tenant", handleTenantInfo)
	mux.HandleFunc("/api/tenant/apikeys", handleTenantApiKeys)
	mux.HandleFunc("/api/tenant/mfa", handleTenantMfa)
	mux.HandleFunc("/api/tenant/apikey/delete", handleTenantDeleteApiKey)
	mux.HandleFunc("/api/tenant/mfa/delete", handleTenantDeleteMfa)
	mux.HandleFunc("/api/tenant/user/delete", handleTenantDeleteUser)
	mux.HandleFunc("/api/tenant/reset-password", handleTenantResetPassword)
	mux.HandleFunc("/api/tenant/user/update", handleTenantUpdateUser)
	// 一键自动更新
	mux.HandleFunc("/api/update/check", handleUpdateCheck)
	mux.HandleFunc("/api/update/apply", handleUpdateApply)

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
	fmt.Printf("共加载 %d 个甲骨文账号\n", len(oci.Tenants()))
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
	return webCfg.OpenBrowser && !NoBrowser
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

// isWildcardHost 判断监听主机是否为通配地址（即未指定具体网卡）。
func isWildcardHost(h string) bool {
	h = strings.Trim(h, "[]")
	return h == "" || h == "0.0.0.0" || h == "::"
}

// forceLoopback 在未启用鉴权时强制绑定到回环地址，避免实例被任意访问/操作。
func forceLoopback(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "127.0.0.1:8080"
	}
	if isWildcardHost(host) {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

// detectLocalIP 探测本机对外通信所用的 IP，用于把访问地址展示成实际可达的地址。
// 探测失败返回空串。
func detectLocalIP() string {
	// 1) UDP "连接" 一个外部地址即可拿到出口网卡 IP（UDP 无握手，不会真的发包）。
	//    比遍历网卡更准：多虚拟网卡的机器上能选中真正的出口网卡。
	if conn, err := net.Dial("udp", "8.8.8.8:80"); err == nil {
		if ua, ok := conn.LocalAddr().(*net.UDPAddr); ok && ua.IP != nil {
			ip := ua.IP
			_ = conn.Close()
			if !ip.IsLoopback() && !ip.IsUnspecified() {
				if v4 := ip.To4(); v4 != nil {
					return v4.String()
				}
				return ip.String()
			}
		} else {
			_ = conn.Close()
		}
	}
	// 2) 回退：遍历网卡，取第一个非回环、非链路本地的全局单播 IPv4
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok || ipnet.IP.IsLoopback() || ipnet.IP.IsLinkLocalUnicast() {
				continue
			}
			if v4 := ipnet.IP.To4(); v4 != nil {
				return v4.String()
			}
		}
	}
	return ""
}

// displayAddr 决定对外展示的访问地址：
// 配置了具体网卡/域名就原样使用；配置的是通配地址时，
// 探测本机实际 IP（如 192.168.x.x），探测不到再退回 127.0.0.1。
func displayAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if isWildcardHost(host) {
		if ip := detectLocalIP(); ip != "" {
			return net.JoinHostPort(ip, port)
		}
		return net.JoinHostPort("127.0.0.1", port)
	}
	return net.JoinHostPort(strings.Trim(host, "[]"), port)
}
