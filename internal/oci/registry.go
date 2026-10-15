package oci

import (
	"fmt"
	"sync"

	"gopkg.in/ini.v1"

	"oci-help/internal/config"
	"oci-help/internal/store"
)

/* ------------------------------------------------------------------ 账号注册表 */

// 账号列表与 AccountCtx 缓存。账号配置以数据库为准；
// INI 同名段仅用于关联该账号下的实例模板。
var (
	regMu        sync.Mutex
	tenants      []store.Tenant
	accountCache = map[string]*AccountCtx{}
)

// LoadTenants 从数据库加载账号列表（入口启动时调用一次）。
func LoadTenants() error {
	ts, err := store.DB.ListTenants()
	if err != nil {
		return err
	}
	attachINISections(ts)
	regMu.Lock()
	tenants = ts
	regMu.Unlock()
	return nil
}

// ReloadTenants 从数据库重新加载账号列表，并清空账号上下文缓存。
// 账号增删改后调用，使改动立即生效。
func ReloadTenants() error {
	ts, err := store.DB.ListTenants()
	if err != nil {
		return err
	}
	attachINISections(ts)
	regMu.Lock()
	tenants = ts
	accountCache = map[string]*AccountCtx{}
	regMu.Unlock()
	return nil
}

// Tenants 返回当前账号列表。调用方只读，不要原地修改元素。
func Tenants() []store.Tenant {
	regMu.Lock()
	defer regMu.Unlock()
	return tenants
}

// FindTenant 按名称查找账号配置；name 为空且仅有一个账号时返回该账号。
// 只查内存中的账号列表，不构造 OCI 客户端。
func FindTenant(name string) *store.Tenant {
	ts := Tenants()
	if name == "" {
		if len(ts) == 1 {
			return &ts[0]
		}
		return nil
	}
	for i := range ts {
		if ts[i].Name == name {
			return &ts[i]
		}
	}
	return nil
}

// GetAccount 按名称取账号上下文（带缓存，首次使用时构造 SDK 客户端）。
func GetAccount(name string) (*AccountCtx, error) {
	ts := Tenants()
	if name == "" && len(ts) != 1 {
		return nil, fmt.Errorf("请指定账号")
	}
	if name == "" {
		name = ts[0].Name
	}
	regMu.Lock()
	if ac, ok := accountCache[name]; ok {
		regMu.Unlock()
		return ac, nil
	}
	regMu.Unlock()

	t := FindTenant(name)
	if t == nil {
		return nil, fmt.Errorf("账号不存在: %s", name)
	}
	ac, err := NewAccountCtx(t)
	if err != nil {
		return nil, err
	}
	regMu.Lock()
	accountCache[name] = ac
	regMu.Unlock()
	return ac, nil
}

// attachINISections 为每个租户关联 oci-help.ini 中同名段（用于读取实例模版）。
func attachINISections(ts []store.Tenant) {
	if config.C == nil || config.C.File == nil {
		return
	}
	for i := range ts {
		if sec := config.C.File.Section(ts[i].Name); sec != nil {
			ts[i].IniSec = sec
		}
	}
}

// Templates 汇总全局 [INSTANCE] 与该账号下的实例模板。
func (ac *AccountCtx) Templates() []*ini.Section {
	if ac == nil || ac.Tenant == nil {
		return config.C.InstanceTemplates(nil)
	}
	return config.C.InstanceTemplates(ac.Tenant.IniSec)
}
