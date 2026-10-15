package web

import (
	"testing"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/core"
)

func TestCollectInstanceIps(t *testing.T) {
	vnics := []core.Vnic{
		{
			PublicIp:      common.String("132.226.174.35"),
			PrivateIp:     common.String("10.0.0.214"),
			Ipv6Addresses: []string{"2603:c024:4502:8d00::1"},
		},
		{
			// 辅助 VNIC：无公网 IP、私网 IP 是空串（应跳过）、IPv6 里混了一个空串（应跳过）
			PrivateIp:     common.String(""),
			Ipv6Addresses: []string{"2603:c024:4502:8d01::5", ""},
		},
	}
	pub, priv, v6 := collectInstanceIps(vnics)
	if len(pub) != 1 || pub[0] != "132.226.174.35" {
		t.Fatalf("publicIps = %v", pub)
	}
	if len(priv) != 1 || priv[0] != "10.0.0.214" {
		t.Fatalf("privateIps = %v", priv)
	}
	if len(v6) != 2 || v6[0] != "2603:c024:4502:8d00::1" || v6[1] != "2603:c024:4502:8d01::5" {
		t.Fatalf("ipv6Addrs = %v", v6)
	}

	// 没有 IPv6 的实例：IPv6 为空（前端显示 "-"），其余照常
	pub, priv, v6 = collectInstanceIps([]core.Vnic{{PublicIp: common.String("1.2.3.4")}})
	if len(pub) != 1 || len(priv) != 0 || len(v6) != 0 {
		t.Fatalf("无 IPv6 场景: pub=%v priv=%v v6=%v", pub, priv, v6)
	}

	// 空输入不应 panic，且三项都为 nil
	p, q, r := collectInstanceIps(nil)
	if p != nil || q != nil || r != nil {
		t.Fatalf("nil 输入应返回 nil: %v %v %v", p, q, r)
	}
}
