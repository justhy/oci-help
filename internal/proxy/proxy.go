// Package proxy 为 OCI SDK 客户端与普通 HTTP 客户端统一设置代理。
// proxyStr 为空表示直连。代理值来自配置文件，由调用方按需读取 config.C.Proxy。
package proxy

import (
	"net/http"
	"net/url"

	"github.com/oracle/oci-go-sdk/v65/common"
)

// Apply 为 SDK BaseClient 设置代理；proxyStr 为空表示直连。
func Apply(client *common.BaseClient, proxyStr string) {
	if client == nil || proxyStr == "" {
		return
	}
	u, err := url.Parse(proxyStr)
	if err != nil {
		return
	}
	client.HTTPClient = &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(u)}}
}

// Transport 返回配置了代理的 http.Transport；proxyStr 为空时使用默认传输。
func Transport(proxyStr string) *http.Transport {
	t := &http.Transport{}
	if proxyStr == "" {
		return t
	}
	if u, err := url.Parse(proxyStr); err == nil {
		t.Proxy = http.ProxyURL(u)
	}
	return t
}
