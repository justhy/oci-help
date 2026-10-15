package web

import (
	"encoding/json"
	"io"
	"net/http"
)

/* ------------------------------------------------------------------ 通用响应 */

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v interface{}) error {
	defer r.Body.Close()
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v)
}

/* ------------------------------------------------------------------ 视图结构 */

type accountView struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Region        string `json:"region"`
	User          string `json:"user"`
	Tenancy       string `json:"tenancy"`
	KeyFile       string `json:"keyFile"`
	Proxy         string `json:"proxy"`
	PlanType      string `json:"planType"`
	AccountStatus string `json:"accountStatus"`
	TenantName    string `json:"tenantName"`
}

type instanceView struct {
	ID                 string   `json:"id"`
	DisplayName        string   `json:"displayName"`
	State              string   `json:"state"`
	StateRaw           string   `json:"stateRaw"`
	Shape              string   `json:"shape"`
	Ocpus              float32  `json:"ocpus"`
	MemoryInGBs        float32  `json:"memoryInGBs"`
	AvailabilityDomain string   `json:"availabilityDomain"`
	PublicIps          []string `json:"publicIps"`
}

type pluginView struct {
	Name         string `json:"name"`
	DesiredState string `json:"desiredState"`
}

type instanceDetailView struct {
	instanceView
	NetworkingBandwidthInGbps float32      `json:"networkingBandwidthInGbps"`
	PrivateIps                []string     `json:"privateIps"`
	IsMonitoringDisabled      bool         `json:"isMonitoringDisabled"`
	IsManagementDisabled      bool         `json:"isManagementDisabled"`
	AreAllPluginsDisabled     bool         `json:"areAllPluginsDisabled"`
	Plugins                   []pluginView `json:"plugins"`
}

type bootVolumeView struct {
	ID                 string `json:"id"`
	DisplayName        string `json:"displayName"`
	State              string `json:"state"`
	SizeInGBs          int64  `json:"sizeInGBs"`
	VpusPerGB          int64  `json:"vpusPerGB"`
	AvailabilityDomain string `json:"availabilityDomain"`
}

type bootVolumeAttachmentView struct {
	ID           string `json:"id"`
	InstanceId   string `json:"instanceId"`
	InstanceName string `json:"instanceName"`
	State        string `json:"state"`
}

type templateView struct {
	Name                   string  `json:"name"`
	Shape                  string  `json:"shape"`
	Ocpus                  float32 `json:"ocpus"`
	MemoryInGBs            float32 `json:"memoryInGBs"`
	BootVolumeSizeInGBs    int64   `json:"bootVolumeSizeInGBs"`
	Sum                    int32   `json:"sum"`
	Each                   int32   `json:"each"`
	Retry                  int32   `json:"retry"`
	AvailabilityDomain     string  `json:"availabilityDomain"`
	OperatingSystem        string  `json:"operatingSystem"`
	OperatingSystemVersion string  `json:"operatingSystemVersion"`
}
