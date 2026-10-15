package web

import (
	"crypto/subtle"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
)

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
