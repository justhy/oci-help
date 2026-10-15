// Command oci-help 是甲骨文云（OCI）实例管理工具。
//
// 单个静态链接的 Go 二进制，内含两种形态：
//   - CLI 交互菜单（默认）
//   - 内嵌 Web 管理面板（-web / -addr，或配置 [web] enabled=1）
//
// 包结构与依赖关系见 doc.go。
package main

import (
	"flag"
	"fmt"
	"path/filepath"

	"oci-help/internal/applog"
	"oci-help/internal/cli"
	"oci-help/internal/config"
	"oci-help/internal/oci"
	"oci-help/internal/store"
	"oci-help/internal/task"
	"oci-help/internal/web"
)

func main() {
	var (
		configFilePath string
		dbFilePath     string
		flagWeb        bool
		webAddr        string
		noBrowser      bool
	)
	flag.StringVar(&configFilePath, "config", config.DefaultConfigFile, "配置文件路径")
	flag.StringVar(&configFilePath, "c", config.DefaultConfigFile, "配置文件路径")
	flag.StringVar(&dbFilePath, "db", "", "本地数据库路径 (默认与配置文件同目录的 oci-help.db)")
	flag.BoolVar(&flagWeb, "web", false, "启动 Web 管理界面")
	flag.BoolVar(&flagWeb, "w", false, "启动 Web 管理界面")
	flag.StringVar(&webAddr, "addr", "", "Web 监听地址, 例如 :8080 (默认取配置文件或 127.0.0.1:8080)")
	flag.BoolVar(&noBrowser, "no-browser", false, "启动 Web 界面时不自动打开浏览器")
	flag.Parse()

	// 未显式指定配置文件时，当前目录找不到就回退到可执行文件所在目录，
	// 这样把 exe 与 oci-help.ini 放在一起双击运行也能正常启动。
	configFilePath = config.ResolvePath(configFilePath)

	cfg, err := config.Load(configFilePath)
	if err != nil {
		applog.ErrExit("配置文件读取失败", err.Error()+"；可用 -c 指定配置文件路径")
	}
	config.C = cfg

	// 打开本地数据库（账号配置与抢机任务都持久化在此）。
	if dbFilePath == "" {
		dbFilePath = filepath.Join(filepath.Dir(configFilePath), "oci-help.db")
	}
	cfg.DBPath = dbFilePath
	st, err := store.OpenStore(dbFilePath)
	if err != nil {
		applog.ErrExit("数据库打开失败", err.Error()+"；可用 -db 指定数据库路径")
	}
	store.DB = st

	// 首次运行：把 oci-help.ini 中的账号导入数据库（之后以数据库为准）。
	if err := importAccountsFromINI(); err != nil {
		applog.ErrExit("导入账号配置失败", err.Error())
	}

	if err := oci.LoadTenants(); err != nil {
		applog.ErrExit("读取账号配置失败", err.Error())
	}
	if len(oci.Tenants()) == 0 {
		applog.ErrExit("未找到正确的配置信息",
			"请在 "+configFilePath+" 中至少配置一个账号的 user/fingerprint/tenancy/region/key_file，"+
				"或在 Web 界面的「租户管理」中添加账号")
	}

	// 恢复本地数据库中持久化的抢机任务（断点续抢）。
	task.StartTaskManager()

	// 启动 Web：-web / -addr 优先，其次配置文件中的 web=1 / [web] enabled=1。
	web.Addr = webAddr
	web.NoBrowser = noBrowser
	if flagWeb || cfg.WebEnabled || webAddr != "" {
		if err := web.Run(); err != nil {
			applog.Errf("Web 服务启动失败", err.Error())
		}
		return
	}

	cli.Run()
}

// importAccountsFromINI 在数据库为空时，把 oci-help.ini 中所有合法的账号段导入数据库。
// 导入后数据库即成为账号配置的唯一来源；再次运行不会覆盖已修改的配置。
func importAccountsFromINI() error {
	n, err := store.DB.CountTenants()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	imported := 0
	for _, sec := range config.C.File.Sections() {
		if len(sec.ParentKeys()) != 0 {
			continue
		}
		user := sec.Key("user").Value()
		fingerprint := sec.Key("fingerprint").Value()
		tenancy := sec.Key("tenancy").Value()
		region := sec.Key("region").Value()
		keyFile := sec.Key("key_file").Value()
		if user == "" || fingerprint == "" || tenancy == "" || region == "" || keyFile == "" {
			continue
		}
		t := store.Tenant{
			Name:        sec.Name(),
			User:        user,
			Fingerprint: fingerprint,
			Tenancy:     tenancy,
			Region:      region,
			KeyFile:     keyFile,
			KeyPassword: sec.Key("key_password").Value(),
			Proxy:       sec.Key("proxy").Value(),
		}
		if _, err := store.DB.UpsertTenant(t); err != nil {
			return err
		}
		imported++
	}
	if imported > 0 {
		fmt.Printf("已从 %s 导入 %d 个账号配置到数据库 %s\n", config.C.FilePath, imported, config.C.DBPath)
	}
	return nil
}
