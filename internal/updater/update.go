// Package updater 通过 GitHub Release 实现自更新：检查新版本、
// 下载对应平台的发布包并原地替换可执行文件后重启。
package updater

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"oci-help/internal/buildinfo"
	"oci-help/internal/config"
	"oci-help/internal/proxy"
)

// defaultUpdateRepo 是未在配置中指定 update.repo 时的默认仓库。
const defaultUpdateRepo = "justhy/oci-help"

const updateUserAgent = "oci-help-updater"

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

type releaseInfo struct {
	TagName string         `json:"tag_name"`
	Name    string         `json:"name"`
	HTMLURL string         `json:"html_url"`
	Body    string         `json:"body"`
	Assets  []releaseAsset `json:"assets"`
}

// UpdateCheck 描述一次更新检查的结果。
type UpdateCheck struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	HasUpdate bool   `json:"hasUpdate"`
	URL       string `json:"url"`
	Notes     string `json:"notes"`
	Asset     string `json:"asset"`
}

// repo 返回配置的更新仓库，未配置时用默认值。
func repo() string {
	if config.C != nil && config.C.UpdateRepo != "" {
		return config.C.UpdateRepo
	}
	return defaultUpdateRepo
}

func updateHTTPClient() *http.Client {
	proxyStr := ""
	if config.C != nil {
		proxyStr = config.C.Proxy
	}
	return &http.Client{
		Timeout:   10 * time.Minute,
		Transport: proxy.Transport(proxyStr),
	}
}

// fetchLatestRelease 查询仓库最新的 Release。
func fetchLatestRelease() (releaseInfo, error) {
	var info releaseInfo
	api := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo())
	req, err := http.NewRequest(http.MethodGet, api, nil)
	if err != nil {
		return info, err
	}
	req.Header.Set("User-Agent", updateUserAgent)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := updateHTTPClient().Do(req)
	if err != nil {
		return info, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return info, fmt.Errorf("GitHub API 返回 %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return info, err
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return info, err
	}
	return info, nil
}

// pickAsset 选择与当前平台匹配的 Release 资产。
func pickAsset(assets []releaseAsset) (releaseAsset, bool) {
	suffix := fmt.Sprintf("-%s-%s-", runtime.GOOS, runtime.GOARCH)
	for _, a := range assets {
		if strings.Contains(a.Name, suffix) && strings.HasSuffix(strings.ToLower(a.Name), ".zip") {
			return a, true
		}
	}
	return releaseAsset{}, false
}

// parseVersion 把版本号解析为数字段，忽略前导 v、预发布/构建后缀与非数字段。
func parseVersion(v string) []int {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			break
		}
		out = append(out, n)
	}
	return out
}

// isNewerVersion 判断 latest 是否比 current 新。按数字段逐位比较，避免字符串
// 字典序导致 v1.0.10 < v1.0.9 之类的误判（含降级提示）。
func isNewerVersion(latest, current string) bool {
	l, c := parseVersion(latest), parseVersion(current)
	for i := 0; i < len(l) || i < len(c); i++ {
		var lv, cv int
		if i < len(l) {
			lv = l[i]
		}
		if i < len(c) {
			cv = c[i]
		}
		if lv != cv {
			return lv > cv
		}
	}
	return false
}

// CheckUpdate 检查是否有新版本。
func CheckUpdate() (UpdateCheck, error) {
	res := UpdateCheck{Current: buildinfo.Version}
	info, err := fetchLatestRelease()
	if err != nil {
		return res, err
	}
	res.Latest = info.TagName
	res.URL = info.HTMLURL
	res.Notes = info.Body
	if asset, ok := pickAsset(info.Assets); ok {
		res.Asset = asset.Name
	}
	res.HasUpdate = info.TagName != "" && isNewerVersion(info.TagName, buildinfo.Version)
	return res, nil
}

// ApplyUpdate 下载并安装最新版本，随后重启进程。
func ApplyUpdate() error {
	info, err := fetchLatestRelease()
	if err != nil {
		return err
	}
	if info.TagName == "" {
		return fmt.Errorf("未获取到最新版本号")
	}
	if !isNewerVersion(info.TagName, buildinfo.Version) {
		return fmt.Errorf("当前已是最新版本 %s", buildinfo.Version)
	}
	asset, ok := pickAsset(info.Assets)
	if !ok {
		return fmt.Errorf("未找到适配当前平台（%s/%s）的发布包", runtime.GOOS, runtime.GOARCH)
	}

	req, err := http.NewRequest(http.MethodGet, asset.BrowserDownloadURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", updateUserAgent)
	resp, err := updateHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载失败: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 128<<20))
	if err != nil {
		return fmt.Errorf("读取下载内容失败: %w", err)
	}
	bin, err := extractBinaryFromZip(data)
	if err != nil {
		return err
	}
	if len(bin) < 1<<20 {
		return fmt.Errorf("下载的二进制体积异常（%d 字节），已中止替换", len(bin))
	}
	return installAndRestart(bin)
}

// extractBinaryFromZip 从发布包中取出当前平台的可执行文件。
func extractBinaryFromZip(data []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("解析发布包失败: %w", err)
	}
	want := "oci-help"
	if runtime.GOOS == "windows" {
		want = "oci-help.exe"
	}
	for _, f := range zr.File {
		name := strings.ToLower(f.Name)
		if name == want || strings.HasSuffix(name, "/"+want) {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(io.LimitReader(rc, 256<<20))
		}
	}
	return nil, fmt.Errorf("发布包中未找到可执行文件 %s", want)
}
