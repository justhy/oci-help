// Package config 负责读取 oci-help.ini 并提供进程级配置。
// 账号与任务等持久化数据以 SQLite 为准（见 internal/store），INI 仅用于：
//   - 首次运行导入账号配置
//   - [INSTANCE] 实例模板（全局模板 + 账号级子段）
//   - CLI/面板的全局参数（代理、Telegram、Web 监听等）
package config

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/ini.v1"
)

// DefaultConfigFile 是未指定 -c/-config 时的默认配置文件路径。
const DefaultConfigFile = "./oci-help.ini"

// IPsFilePrefix 是导出实例公共 IP 列表时的文件名前缀。
const IPsFilePrefix = "IPs"

// C 是进程级默认配置，由程序入口加载后赋值；各内部包只读使用。
var C *Config

// Oracle 是 oci-help.ini 账号段中的 API 签名凭证。
type Oracle struct {
	User         string `ini:"user"`
	Fingerprint  string `ini:"fingerprint"`
	Tenancy      string `ini:"tenancy"`
	Region       string `ini:"region"`
	Key_file     string `ini:"key_file"`
	Key_password string `ini:"key_password"`
}

// Instance 是创建实例所用的模板，来自 [INSTANCE] 及其子段。
type Instance struct {
	AvailabilityDomain     string  `ini:"availabilityDomain"`
	SSH_Public_Key         string  `ini:"ssh_authorized_key"`
	VcnDisplayName         string  `ini:"vcnDisplayName"`
	SubnetDisplayName      string  `ini:"subnetDisplayName"`
	Shape                  string  `ini:"shape"`
	OperatingSystem        string  `ini:"OperatingSystem"`
	OperatingSystemVersion string  `ini:"OperatingSystemVersion"`
	InstanceDisplayName    string  `ini:"instanceDisplayName"`
	Ocpus                  float32 `ini:"cpus"`
	MemoryInGBs            float32 `ini:"memoryInGBs"`
	Burstable              string  `ini:"burstable"`
	BootVolumeSizeInGBs    int64   `ini:"bootVolumeSizeInGBs"`
	Sum                    int32   `ini:"sum"`
	Each                   int32   `ini:"each"`
	Retry                  int32   `ini:"retry"`
	CloudInit              string  `ini:"cloud-init"`
	MinTime                int32   `ini:"minTime"`
	MaxTime                int32   `ini:"maxTime"`
	// RetryMinTime / RetryMaxTime 是失败重试的等待区间（秒），随机取值；
	// <=0 时回退到 MinTime/MaxTime。仅作用于失败重试，不影响成功后的连创间隔。
	RetryMinTime int32 `ini:"retryMinTime"`
	RetryMaxTime int32 `ini:"retryMaxTime"`
}

// Config 是进程级配置快照。File 保留原始 INI 句柄，
// 供实例模板等按段读取（INI 即模板的唯一来源）。
type Config struct {
	FilePath string // INI 路径
	DBPath   string // SQLite 路径（入口加载后填充）

	Proxy string // 全局代理
	Cmd   string // 抢到实例后执行的命令（CLI 批量创建用）

	Token   string // Telegram bot token
	ChatID  string // Telegram chat_id
	Each    bool   // 是否逐台发送 Telegram 通知
	SendURL string // sendMessage 接口地址
	EditURL string // editMessageText 接口地址

	UpdateRepo string // 自动更新仓库（owner/name），空表示用默认值
	WebEnabled bool   // INI 是否启用 Web 面板（[web] enabled=1 或默认段 web=1）

	File *ini.File
}

// ResolvePath 未显式指定配置文件时，按 当前目录 → 可执行文件所在目录 的顺序查找，
// 这样把可执行文件与 oci-help.ini 放在一起双击运行也能正常启动。
func ResolvePath(p string) string {
	if p != DefaultConfigFile {
		return p
	}
	if _, err := os.Stat(p); err == nil {
		return p
	}
	exe, err := os.Executable()
	if err != nil {
		return p
	}
	alt := filepath.Join(filepath.Dir(exe), DefaultConfigFile)
	if _, err := os.Stat(alt); err == nil {
		return alt
	}
	return p
}

// Load 读取并解析配置文件。Telegram 配置优先取 [Telegram] 段，
// 为兼容旧配置，段不存在时回退到默认段顶部的 token/chat_id。
func Load(path string) (*Config, error) {
	cfg, err := ini.Load(path)
	if err != nil {
		return nil, err
	}
	c := &Config{FilePath: path, File: cfg, Each: true}

	def := cfg.Section(ini.DefaultSection)
	c.Proxy = def.Key("proxy").Value()
	c.Cmd = def.Key("cmd").Value()

	if tg, e := cfg.GetSection("Telegram"); e == nil {
		c.Token = tg.Key("token").Value()
		c.ChatID = tg.Key("chat_id").Value()
	}
	if c.Token == "" && c.ChatID == "" {
		c.Token = def.Key("token").Value()
		c.ChatID = def.Key("chat_id").Value()
	}
	if def.HasKey("EACH") {
		c.Each, _ = def.Key("EACH").Bool()
	}
	c.SendURL = "https://api.telegram.org/bot" + c.Token + "/sendMessage"
	c.EditURL = "https://api.telegram.org/bot" + c.Token + "/editMessageText"

	if sec := cfg.Section("update"); sec != nil {
		if v := strings.TrimSpace(sec.Key("repo").Value()); v != "" {
			c.UpdateRepo = v
		}
	}

	if sec := cfg.Section("web"); sec != nil {
		if enabled, _ := sec.Key("enabled").Bool(); enabled {
			c.WebEnabled = true
		}
	}
	if def.HasKey("web") {
		if b, err := def.Key("web").Bool(); err == nil && b {
			c.WebEnabled = true
		}
	}
	return c, nil
}

// InstanceTemplates 汇总全局 [INSTANCE] 与指定账号段（iniSec）下的实例模板。
// 只依赖 INI，不需要构造 OCI 客户端；iniSec 为 nil 时只返回全局模板。
func (c *Config) InstanceTemplates(iniSec *ini.Section) []*ini.Section {
	if c == nil || c.File == nil {
		return nil
	}
	var out []*ini.Section
	if base := c.File.Section("INSTANCE"); base != nil {
		out = append(out, base.ChildSections()...)
	}
	if iniSec != nil {
		out = append(out, iniSec.ChildSections()...)
	}
	return out
}
