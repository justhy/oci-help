package oci

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/monitoring"
)

const (
	trafficNamespace = "oci_vcn"
	// 与参考项目一致：按 VNIC 维度统计进出流量，分钟级聚合。
	trafficQueryIn  = `VnicToNetworkBytes[%s]{resourceId = "%s"}.sum()`
	trafficQueryOut = `VnicFromNetworkBytes[%s]{resourceId = "%s"}.sum()`
)

// TrafficResult 是某个 VNIC 在指定时间窗内的流量序列（单位：字节）。
type TrafficResult struct {
	Times    []string  `json:"times"`
	Inbound  []float64 `json:"inbound"`
	Outbound []float64 `json:"outbound"`
}

type metricPoint struct {
	ts    time.Time
	value float64
}

// summarizeMetric 调用 Monitoring 的 summarizeMetricsData。
func (ac *AccountCtx) summarizeMetric(query string, start, end time.Time, resolution string) ([]metricPoint, error) {
	resp, err := ac.monitorClient.SummarizeMetricsData(Ctx, monitoring.SummarizeMetricsDataRequest{
		CompartmentId: common.String(ac.Oracle.Tenancy),
		SummarizeMetricsDataDetails: monitoring.SummarizeMetricsDataDetails{
			Namespace:  common.String(trafficNamespace),
			Query:      common.String(query),
			StartTime:  &common.SDKTime{Time: start},
			EndTime:    &common.SDKTime{Time: end},
			Resolution: common.String(resolution),
		},
		RequestMetadata: getCustomRequestMetadataWithRetryPolicy(),
	})
	if err != nil {
		return nil, err
	}
	var points []metricPoint
	for _, item := range resp.Items {
		for _, dp := range item.AggregatedDatapoints {
			if dp.Timestamp == nil || dp.Value == nil {
				continue
			}
			points = append(points, metricPoint{ts: dp.Timestamp.Time, value: *dp.Value})
		}
	}
	sort.Slice(points, func(i, j int) bool { return points[i].ts.Before(points[j].ts) })
	return points, nil
}

// GetTraffic 返回 VNIC 在 [start, end] 的分钟级进出流量序列。
func (ac *AccountCtx) GetTraffic(vnicID string, start, end time.Time) (TrafficResult, error) {
	inPoints, err := ac.summarizeMetric(fmt.Sprintf(trafficQueryIn, "1m", vnicID), start, end, "1m")
	if err != nil {
		return TrafficResult{}, err
	}
	outPoints, err := ac.summarizeMetric(fmt.Sprintf(trafficQueryOut, "1m", vnicID), start, end, "1m")
	if err != nil {
		return TrafficResult{}, err
	}

	const layout = "2006-01-02 15:04"
	inMap := make(map[string]float64, len(inPoints))
	outMap := make(map[string]float64, len(outPoints))
	seen := map[string]bool{}
	for _, p := range inPoints {
		k := p.ts.Format(layout)
		inMap[k] = p.value
		seen[k] = true
	}
	for _, p := range outPoints {
		k := p.ts.Format(layout)
		outMap[k] = p.value
		seen[k] = true
	}
	times := make([]string, 0, len(seen))
	for k := range seen {
		times = append(times, k)
	}
	sort.Strings(times)

	res := TrafficResult{Times: times, Inbound: make([]float64, len(times)), Outbound: make([]float64, len(times))}
	for i, k := range times {
		res.Inbound[i] = inMap[k]
		res.Outbound[i] = outMap[k]
	}
	return res, nil
}

// MonthTraffic 返回 VNIC 本月的进出流量合计（字节）。
func (ac *AccountCtx) MonthTraffic(vnicID string) (in, out float64, err error) {
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	inPoints, err := ac.summarizeMetric(fmt.Sprintf(trafficQueryIn, "1d", vnicID), start, now, "1d")
	if err != nil {
		return 0, 0, err
	}
	outPoints, err := ac.summarizeMetric(fmt.Sprintf(trafficQueryOut, "1d", vnicID), start, now, "1d")
	if err != nil {
		return 0, 0, err
	}
	for _, p := range inPoints {
		in += p.value
	}
	for _, p := range outPoints {
		out += p.value
	}
	return in, out, nil
}

// FormatBytes 把字节数格式化为易读字符串。
func FormatBytes(b float64) string {
	const unit = 1024.0
	if b < unit {
		return strconv.FormatFloat(b, 'f', 0, 64) + " B"
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	v := b
	for i := 0; i < len(units); i++ {
		v /= unit
		if v < unit || i == len(units)-1 {
			return strconv.FormatFloat(v, 'f', 2, 64) + " " + units[i]
		}
	}
	return strconv.FormatFloat(v, 'f', 2, 64) + " EB"
}
