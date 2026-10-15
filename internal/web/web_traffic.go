package web

import (
	"net/http"
	"oci-help/internal/oci"
	"strconv"
	"time"
)

/* ------------------------------------------------------------------ 流量统计 */

func handleTrafficVnics(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodGet) {
		return
	}
	ac, err := oci.GetAccount(r.URL.Query().Get("account"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	instanceID := r.URL.Query().Get("instanceId")
	if instanceID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 instanceId")
		return
	}
	vnics, err := ac.GetInstanceVnics(&instanceID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "获取 VNIC 失败: "+err.Error())
		return
	}
	type vnicView struct {
		ID          string `json:"id"`
		DisplayName string `json:"displayName"`
		PublicIP    string `json:"publicIp"`
		PrivateIP   string `json:"privateIp"`
	}
	out := make([]vnicView, 0, len(vnics))
	for i := range vnics {
		out = append(out, vnicView{
			ID:          oci.S(vnics[i].Id),
			DisplayName: oci.S(vnics[i].DisplayName),
			PublicIP:    oci.S(vnics[i].PublicIp),
			PrivateIP:   oci.S(vnics[i].PrivateIp),
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"vnics": out})
}

func handleTraffic(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodGet) {
		return
	}
	ac, err := oci.GetAccount(r.URL.Query().Get("account"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	vnicID := r.URL.Query().Get("vnicId")
	if vnicID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 vnicId")
		return
	}
	minutes, _ := strconv.Atoi(r.URL.Query().Get("minutes"))
	if minutes <= 0 {
		minutes = 60
	}
	end := time.Now()
	start := end.Add(-time.Duration(minutes) * time.Minute)
	res, err := ac.GetTraffic(vnicID, start, end)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "获取流量失败: "+err.Error())
		return
	}
	in, out, _ := ac.MonthTraffic(vnicID)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"traffic":       res,
		"monthInbound":  in,
		"monthOutbound": out,
		"monthInText":   oci.FormatBytes(in),
		"monthOutText":  oci.FormatBytes(out),
	})
}
