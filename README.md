# 甲骨文实例管理工具使用教程

## 功能特性

在原有「抢机 / 实例管理 / 引导卷管理」基础上，新增 Web 管理面板与以下能力：

- **实例**：启动 / 停止 / 重启 / 终止、修改名称、升级降级、插件配置、更换公共IP、**一键附加 IPv6**
- **引导卷**：列表、改性能、改大小、分离、终止
- **网络**：**安全列表**查询与增删规则、**一键放行所有端口**
- **子网管理**：列 / 建 / 改（名称、路由表、安全列表）/ 删
- **网络安全组（NSG）**：列 / 建 / 删、规则的增删改查、查看已绑定的 VNIC
- **VNIC 管理**：查看实例全部 VNIC（主/辅、MAC、私网/公网 IP、NSG、路由表）、附加 / 分离辅助 VNIC、改 VNIC 属性、辅助私有 IP 增删改、公网 IP 保留 / 释放
- **免费 AMD 实例**：**一键开启 / 关闭下行 500Mbps**（NAT 网关 + 路由表 + 网络负载均衡器）
- **一键自动救援 / 缩小硬盘**（默认 47GB，原引导卷先备份）
- **实时流量统计**（分钟级别，基于 OCI Monitoring）
- **配额查询**（OCI Limits，含已用 / 可用量）
- **租户管理**：租户信息、用户列表、API 密钥与 MFA 设备管理
- **抢机任务**：多租户批量抢机，**配置与任务持久化到本地数据库，服务重启自动续抢（断点续抢）**；任务日志按「抢机任务 / 操作类」分类查看，支持按分类 + 状态批量删除（运行中的任务始终保留）
- **一键自动更新**：检查 GitHub Release 并替换当前二进制后重启

> 账号配置与抢机任务保存在本地 SQLite 数据库（默认 `oci-help.db`，与配置文件同目录）。
> 首次启动会自动把 `oci-help.ini` 中的账号导入数据库，之后以数据库为准，可在 Web 界面「租户」页维护。

## 获取程序
- 如果需要在本地电脑运行该程序，根据自己电脑的CPU架构类型，打开下面的 `下载地址` 下载对应的文件即可。如果需要在服务器上运行该程序，那么根据服务器的CPU架构类型选择对应的文件下载到服务器即可。

- 下载完成后解压压缩包，可以得到可执行程序 (Windows系统: `oci-help.exe` , 其他操作系统: `oci-help` ) 和 配置文件 `oci-help.ini` 。

> 下载地址: https://github.com/justhy/oci-help/releases/latest


## 获取配置信息
![image](https://github.com/justhy/oci-help/raw/main/doc/1.png)
![image](https://github.com/justhy/oci-help/raw/main/doc/2.png)
![image](https://github.com/justhy/oci-help/raw/main/doc/3.png)
![image](https://github.com/justhy/oci-help/raw/main/doc/4.png)
![image](https://github.com/justhy/oci-help/raw/main/doc/5.png)
![image](https://github.com/justhy/oci-help/raw/main/doc/6.png)


## 编辑配置文件
用文本编辑器打开在第一步获取到的 `oci-help.ini` 文件，进行如下配置:

![image](https://github.com/justhy/oci-help/raw/main/doc/7.png)
![image](https://github.com/justhy/oci-help/raw/main/doc/8.png)

## Telegram 消息提醒配置
![image](https://github.com/justhy/oci-help/raw/main/doc/9.png)

> BotFather: https://t.me/BotFather    
> IDBot: https://t.me/myidbot


## 运行程序
```bash
# 前台运行程序
./oci-help

# 前台运行需要一直开着终端窗口，可以在 Screen 中运行程序，以实现断开终端窗口后一直运行。
# 创建 Screen 终端
screen -S oci-help 
# 在 Screen 中运行程序
./oci-help
# 离开 Screen 终端
按下 Ctrl 键不松，依次按字母 A 键和 D 键。或者直接关闭终端窗口也可以。
# 查看已创建的 Screen 终端
screen -ls
# 重新连接 Screen 终端
screen -r oci-help
```


## 🎉 感谢赞助

本项目 CDN 加速及安全防护由 [Tencent EdgeOne](https://edgeone.ai/zh?from=github "Tencent EdgeOne") 赞助

[![Tencent EdgeOne](https://edgeone.ai/media/34fe3a45-492d-4ea4-ae5d-ea1087ca7b4b.png)](https://edgeone.ai/zh?from=github)

