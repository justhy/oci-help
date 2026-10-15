// Command oci-help 的源码布局说明（实现见 main.go）。
//
// # 多包结构（依赖为有向无环）
//
//	cmd/oci-help          薄入口：解析 flag、加载配置与数据库、决定 CLI/Web 形态
//	  │
//	internal/applog       控制台输出助手（带时间戳打印、错误高亮、双击启动检测）
//	internal/buildinfo    构建信息（-ldflags 注入 Version/Commit/BuildTime）
//	internal/config       oci-help.ini 读取、进程级配置 config.C、实例模板段
//	internal/proxy        叶子包：为 SDK 客户端与普通 HTTP 客户端统一设置代理
//	internal/store        纯 Go SQLite 持久化（租户 / 抢机任务 / KV）
//	  │
//	internal/notify       Telegram 通知（读 config.C，自带代理）
//	internal/updater      GitHub Release 自更新（按平台分文件）
//	internal/oci          账号注册表 + AccountCtx 全部 OCI 操作
//	  │
//	internal/job          异步任务（Job）与注册表，不依赖 Web
//	  │
//	internal/task         断点续抢任务管理器（重启从库恢复、暂停/恢复）
//	  │
//	internal/cli          CLI 交互菜单
//	internal/web          内嵌 Web 面板（HTTP handler + go:embed 静态资源）
//
// oci → notify 而 notify 不反向依赖 oci：stopped 通知所需的代理设置被抽到
// internal/proxy，凭据与参数统一来自 config.C，从而打破循环依赖。
//
// # 各包文件
//
//	internal/applog/applog.go            通用输出
//	internal/applog/console_windows.go   Windows 双击启动检测
//	internal/applog/console_other.go     非 Windows 空实现
//
//	internal/config/config.go            INI 解析、Config、实例模板汇总
//	internal/proxy/proxy.go              Apply / Transport
//	internal/store/store.go              Store、Tenant、CreateTask、KV
//
//	internal/oci/oci.go                  包级上下文 Ctx、指针安全取值、状态名映射、
//	                                     时长格式化、重试策略、随机等待
//	internal/oci/registry.go             账号注册表（LoadTenants/ReloadTenants/Tenants/
//	                                     FindTenant/GetAccount、INI 段关联）
//	internal/oci/account.go              AccountCtx 结构、构造、Provider、可用性域缓存
//	internal/oci/oci_instance.go         实例列表/详情/改配/启停/删除
//	internal/oci/oci_ip.go               公共 IP / 私有 IP / VNIC 小改
//	internal/oci/oci_bootvolume.go       引导卷列表/改配/挂载/删除
//	internal/oci/oci_infra.go            VCN / 子网 / InternetGateway / 路由表 基础建设
//	internal/oci/oci_image.go            镜像 / Shape 查询
//	internal/oci/oci_launch.go           创建实例（含抢机重试循环）、导出 IP
//	internal/oci/oci_network.go          VCN/子网/VNIC 基础查询
//	internal/oci/oci_subnet.go           子网管理（列/建/改/删）
//	internal/oci/oci_vnic.go             VNIC 管理（附加/分离、属性、私有/公网 IP）
//	internal/oci/oci_seclists.go         安全列表规则增删查、一键放行端口
//	internal/oci/oci_nsg.go              网络安全组（NSG）与规则管理
//	internal/oci/oci_refs.go             路由表 / 安全列表清单（供 UI 下拉）
//	internal/oci/oci_ipv6.go             一键附加 IPv6
//	internal/oci/oci_500m.go             一键开启/关闭免费 AMD 实例下行 500Mbps
//	internal/oci/oci_rescue.go           自动救援 / 缩小引导卷
//	internal/oci/oci_traffic.go          实时流量统计（Monitoring）
//	internal/oci/oci_limits.go           配额查询（Limits）
//	internal/oci/oci_tenant.go           租户信息与用户/APIKey/MFA/重置密码管理
//
//	internal/job/job.go                  Job、注册表、创建实例任务、注册表访问器
//	internal/task/tasks.go               TaskManager、TaskToInstance、NormalizeOS
//
//	internal/notify/notify.go            Telegram SendMessage / EditMessage
//	internal/updater/update.go           版本检查、下载、校验
//	internal/updater/update_windows.go   Windows 批处理延迟替换自身
//	internal/updater/update_unix.go       Unix rename + syscall.Exec 重启
//
//	internal/cli/cli.go                  账号选择与主菜单
//	internal/cli/cli_instance.go         实例菜单
//	internal/cli/cli_launch.go           模版选择、批量创建、导出 IP
//	internal/cli/cli_volume.go           引导卷菜单
//
//	internal/web/web.go                  服务启动、内嵌静态资源、路由注册、地址展示
//	internal/web/web_auth.go             鉴权与请求守卫中间件
//	internal/web/web_util.go             JSON 响应与视图结构体
//	internal/web/web_job.go              任务查询/取消/删除/导出 IP handlers
//	internal/web/web_instance.go         版本/账号/实例 handlers
//	internal/web/web_volume.go           引导卷 handlers
//	internal/web/web_launch.go           模板查询与一次性创建
//	internal/web/web_task.go             抢机任务 handlers
//	internal/web/web_oci.go              操作类任务框架（startActionJob / methodGuard）
//	internal/web/web_vcn.go              VCN / IPv6 handlers
//	internal/web/web_subnet.go           子网 handlers
//	internal/web/web_vnic.go             VNIC handlers
//	internal/web/web_seclist.go          安全列表 handlers
//	internal/web/web_nsg.go              NSG handlers
//	internal/web/web_refs.go             路由表 / 安全列表清单 handlers
//	internal/web/web_500m.go             500Mbps handlers
//	internal/web/web_rescue.go           救援 handler
//	internal/web/web_traffic.go          流量 handlers
//	internal/web/web_limits.go           配额 handlers
//	internal/web/web_tenant.go           账号配置 CRUD handlers
//	internal/web/web_tenant_info.go      租户信息 / 用户管理 handlers
//	internal/web/web_update.go           一键自动更新 handlers
//
// 前端静态资源在 internal/web/web/（index.html / app.js / style.css），
// 经 go:embed 内嵌进二进制；改动前端后必须重新 go build 才会生效。
//
// # 测试
//
// 单测与源码同目录（Go 约定，文件以 _test.go 结尾）：
//
//	internal/store/store_test.go          持久化层单测
//	internal/task/task_test.go            抢机任务配置转换单测
//	internal/updater/update_test.go       版本比较单测
//	internal/oci/oci_launch_test.go       抢机重试决策单测
//	internal/web/web_smoke_test.go        HTTP 接口与规则构造器 smoke 测试
//
// 运行：go test ./...。.test/ 为本地 E2E 夹具与脚本（已被 .gitignore 忽略，不入库）。
package main
