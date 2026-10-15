package web

import (
	"fmt"
	"net/http"
	"oci-help/internal/buildinfo"
	"oci-help/internal/oci"
	"strings"

	"github.com/oracle/oci-go-sdk/v65/core"
)

/* ------------------------------------------------------------------ 版本 */

// handleVersion 返回构建信息，供界面在程序名下方展示版本号。
func handleVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"version":   buildinfo.Version,
		"commit":    buildinfo.Commit,
		"buildTime": buildinfo.BuildTime,
	})
}

/* ------------------------------------------------------------------ 账号 */

func handleAccounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ts := oci.Tenants()
	list := make([]accountView, 0, len(ts))
	for i := range ts {
		t := &ts[i]
		list = append(list, accountView{
			ID:            t.ID,
			Name:          t.Name,
			Region:        t.Region,
			User:          t.User,
			Tenancy:       t.Tenancy,
			KeyFile:       t.KeyFile,
			Proxy:         t.Proxy,
			PlanType:      t.PlanType,
			AccountStatus: t.AccountStatus,
			TenantName:    t.TenantName,
		})
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
	ac, err := oci.GetAccount(r.URL.Query().Get("account"))
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
		id := oci.S(ins.Id)
		out = append(out, instanceView{
			ID:                 id,
			DisplayName:        oci.S(ins.DisplayName),
			State:              oci.InstanceStateName(ins.LifecycleState),
			StateRaw:           string(ins.LifecycleState),
			Shape:              oci.S(ins.Shape),
			Ocpus:              oci.ShapeOcpus(ins.ShapeConfig),
			MemoryInGBs:        oci.ShapeMemoryInGBs(ins.ShapeConfig),
			AvailabilityDomain: oci.S(ins.AvailabilityDomain),
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
	ac, err := oci.GetAccount(r.URL.Query().Get("account"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 id")
		return
	}
	ins, err := ac.GetInstance(&id)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "获取实例详情失败: "+err.Error())
		return
	}
	vnics, err := ac.GetInstanceVnics(&id)
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
			ID:                 oci.S(ins.Id),
			DisplayName:        oci.S(ins.DisplayName),
			State:              oci.InstanceStateName(ins.LifecycleState),
			StateRaw:           string(ins.LifecycleState),
			Shape:              oci.S(ins.Shape),
			Ocpus:              oci.ShapeOcpus(ins.ShapeConfig),
			MemoryInGBs:        oci.ShapeMemoryInGBs(ins.ShapeConfig),
			AvailabilityDomain: oci.S(ins.AvailabilityDomain),
			PublicIps:          publicIps,
		},
		PrivateIps: privateIps,
	}
	detail.NetworkingBandwidthInGbps = oci.ShapeBandwidth(ins.ShapeConfig)
	if ins.AgentConfig != nil {
		detail.IsMonitoringDisabled = ins.AgentConfig.IsMonitoringDisabled != nil && *ins.AgentConfig.IsMonitoringDisabled
		detail.IsManagementDisabled = ins.AgentConfig.IsManagementDisabled != nil && *ins.AgentConfig.IsManagementDisabled
		detail.AreAllPluginsDisabled = ins.AgentConfig.AreAllPluginsDisabled != nil && *ins.AgentConfig.AreAllPluginsDisabled
		for _, p := range ins.AgentConfig.PluginsConfig {
			detail.Plugins = append(detail.Plugins, pluginView{Name: oci.S(p.Name), DesiredState: string(p.DesiredState)})
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
	ac, err := oci.GetAccount(req.Account)
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
func doInstanceAction(ac *oci.AccountCtx, id, action string) (string, error) {
	switch strings.ToLower(action) {
	case "start":
		if _, err := ac.InstanceAction(&id, core.InstanceActionActionStart); err != nil {
			return "", fmt.Errorf("启动实例失败: %w", err)
		}
		return "正在启动实例，请稍后刷新查看状态", nil
	case "stop":
		if _, err := ac.InstanceAction(&id, core.InstanceActionActionSoftstop); err != nil {
			return "", fmt.Errorf("停止实例失败: %w", err)
		}
		return "正在停止实例，请稍后刷新查看状态", nil
	case "restart":
		if _, err := ac.InstanceAction(&id, core.InstanceActionActionSoftreset); err != nil {
			return "", fmt.Errorf("重启实例失败: %w", err)
		}
		return "正在重启实例，请稍后刷新查看状态", nil
	case "terminate":
		if err := ac.TerminateInstance(&id); err != nil {
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
	ac, err := oci.GetAccount(req.Account)
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
		name := oci.S(ins.DisplayName)
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
	ac, err := oci.GetAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	vnics, err := ac.GetInstanceVnics(&req.ID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "获取实例VNIC失败: "+err.Error())
		return
	}
	if len(vnics) == 0 {
		writeErr(w, http.StatusBadGateway, "实例已终止或获取实例VNIC失败，请稍后重试")
		return
	}
	publicIp, err := ac.ChangePublicIp(vnics)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "更换实例公共IP失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "更换公共IP成功", "ip": oci.S(publicIp.IpAddress)})
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
	ac, err := oci.GetAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	var plugins []core.InstanceAgentPluginConfigDetails
	if req.AgentDisable != nil {
		ins, err := ac.GetInstance(&req.ID)
		if err != nil {
			writeErr(w, http.StatusBadGateway, "获取实例详情失败: "+err.Error())
			return
		}
		if ins.AgentConfig != nil {
			plugins = ins.AgentConfig.PluginsConfig
		}
	}

	if _, err := ac.UpdateInstance(&req.ID, req.DisplayName, req.Ocpus, req.MemoryInGBs, plugins, req.AgentDisable); err != nil {
		writeErr(w, http.StatusBadGateway, "更新实例失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "更新实例成功"})
}
