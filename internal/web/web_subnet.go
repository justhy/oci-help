package web

import (
	"net/http"
	"oci-help/internal/job"
	"oci-help/internal/oci"
)

/* ------------------------------------------------------------------ 子网 */

func handleSubnets(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodGet) {
		return
	}
	vcnID := r.URL.Query().Get("vcnId")
	if vcnID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 vcnId")
		return
	}
	ac, err := oci.GetAccount(r.URL.Query().Get("account"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	subnets, err := ac.ListSubnetViews(vcnID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "获取子网失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"subnets": subnets})
}

func handleSubnetCreate(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodPost) {
		return
	}
	var req struct {
		Account                 string   `json:"account"`
		VcnID                   string   `json:"vcnId"`
		DisplayName             string   `json:"displayName"`
		CidrBlock               string   `json:"cidrBlock"`
		DnsLabel                string   `json:"dnsLabel"`
		AvailabilityDomain      string   `json:"availabilityDomain"`
		ProhibitInternetIngress bool     `json:"prohibitInternetIngress"`
		RouteTableID            string   `json:"routeTableId"`
		SecurityListIds         []string `json:"securityListIds"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.VcnID == "" || req.CidrBlock == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 vcnId 或 cidrBlock")
		return
	}
	ac, err := oci.GetAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	sn, err := ac.CreateSubnet(oci.SubnetCreateOptions{
		VcnID:                   req.VcnID,
		DisplayName:             req.DisplayName,
		CidrBlock:               req.CidrBlock,
		DnsLabel:                req.DnsLabel,
		AvailabilityDomain:      req.AvailabilityDomain,
		ProhibitInternetIngress: req.ProhibitInternetIngress,
		RouteTableID:            req.RouteTableID,
		SecurityListIds:         req.SecurityListIds,
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"subnet": sn})
}

func handleSubnetUpdate(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodPost) {
		return
	}
	var req struct {
		Account         string   `json:"account"`
		SubnetID        string   `json:"subnetId"`
		DisplayName     string   `json:"displayName"`
		RouteTableID    string   `json:"routeTableId"`
		SecurityListIds []string `json:"securityListIds"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.SubnetID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 subnetId")
		return
	}
	ac, err := oci.GetAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := ac.UpdateSubnetAttrs(oci.SubnetUpdateOptions{
		SubnetID:        req.SubnetID,
		DisplayName:     req.DisplayName,
		RouteTableID:    req.RouteTableID,
		SecurityListIds: req.SecurityListIds,
	}); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "子网已更新"})
}

func handleSubnetDelete(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodPost) {
		return
	}
	var req struct {
		Account  string `json:"account"`
		SubnetID string `json:"subnetId"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.SubnetID == "" {
		writeErr(w, http.StatusBadRequest, "缺少参数 subnetId")
		return
	}
	ac, err := oci.GetAccount(req.Account)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	job := startActionJob("subnet-delete", ac.Name, ac, func(jb *job.Job, ac *oci.AccountCtx) error {
		jb.Log("正在删除子网 %s ...", req.SubnetID)
		return ac.DeleteSubnet(req.SubnetID)
	})
	writeJSON(w, http.StatusOK, map[string]string{"jobId": job.ID})
}
