package remote

// lanaddr_test.go — P3-B R1 网卡枚举过滤排序逻辑测试（selectLanAddresses 纯函数）。
//
// 锚定的行为契约：
//   - 过滤：未启用网卡、回环网卡、回环/链路本地/组播/通配/非全局单播 IP、IPv6 全部剔除；
//   - 保留信息：网卡名、掩码长度、私网标记；
//   - VPN/虚拟网卡：点对点接口标志与已知前缀命中者标注 vpnLike，但不硬滤；
//   - 排序：私网优先 → 非疑似 VPN 优先 → 网卡名 → IP（确定序）；
//   - ListLocalLanAddresses 在真实网卡上返回合法 IPv4（回归烟测，不强求非空——
//     CI 容器可能无非回环网卡）。

import (
	"fmt"
	"net"
	"testing"
)

// mustCIDR 构造 *net.IPNet 测试地址。
func mustCIDR(t *testing.T, cidr string) *net.IPNet {
	t.Helper()
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatalf("ParseCIDR %s: %v", cidr, err)
	}
	ipnet.IP = ip
	return ipnet
}

// lanViews 是一组覆盖典型网卡形态的投影输入。
func lanViews(t *testing.T) []ifaceView {
	return []ifaceView{
		{
			// 回环接口：整体剔除（即使其地址是公网形态也不该出现）。
			Name: "lo0", Up: true, Loopback: true,
			Addrs: []net.Addr{mustCIDR(t, "127.0.0.1/8")},
		},
		{
			// 未启用网卡：剔除。
			Name: "en5", Up: false,
			Addrs: []net.Addr{mustCIDR(t, "192.168.99.9/24")},
		},
		{
			// 物理网卡：私网 IPv4 + IPv6（IPv6 应被剔除）+ 链路本地（剔除）。
			Name: "en0", Up: true,
			Addrs: []net.Addr{
				mustCIDR(t, "192.168.31.55/24"),
				mustCIDR(t, "169.254.100.7/16"),
				mustCIDR(t, "fd00::a1/64"),
			},
		},
		{
			// 第二块物理网卡：10/8 私网。
			Name: "en1", Up: true,
			Addrs: []net.Addr{mustCIDR(t, "10.1.2.3/8")},
		},
		{
			// VPN 点对点接口（utun 携带 IPv4 私网）：标注不硬滤。
			Name: "utun3", Up: true, PointToPoint: true,
			Addrs: []net.Addr{mustCIDR(t, "10.8.0.2/24")},
		},
		{
			// 虚拟网桥（前缀命中 vmnet，非点对点）：标注不硬滤。
			Name: "Vmnet1", Up: true,
			Addrs: []net.Addr{mustCIDR(t, "192.168.84.1/24")},
		},
		{
			// 公网 IPv4（直连公网场景）：保留但排在私网之后。
			Name: "en2", Up: true,
			Addrs: []net.Addr{mustCIDR(t, "203.0.113.10/24")},
		},
	}
}

func TestSelectLanAddresses_FiltersLoopbackLinkLocalAndIPv6(t *testing.T) {
	got := selectLanAddresses(lanViews(t))
	seen := map[string]bool{}
	for _, info := range got {
		seen[info.IP] = true
	}
	for _, banned := range []string{"127.0.0.1", "169.254.100.7", "192.168.99.9"} {
		if seen[banned] {
			t.Fatalf("banned address %q survived filtering: %+v", banned, got)
		}
	}
	if seen["fd00::a1"] {
		t.Fatal("IPv6 address must be excluded (IPv4-only enumeration)")
	}
	for _, want := range []string{"192.168.31.55", "10.1.2.3", "10.8.0.2", "192.168.84.1", "203.0.113.10"} {
		if !seen[want] {
			t.Fatalf("expected candidate %q missing: %+v", want, got)
		}
	}
}

func TestSelectLanAddresses_CarriesInterfacePrefixAndPrivateFlags(t *testing.T) {
	got := selectLanAddresses(lanViews(t))
	byIP := map[string]LanAddressInfo{}
	for _, info := range got {
		byIP[info.IP] = info
	}
	en0 := byIP["192.168.31.55"]
	if en0.Interface != "en0" || en0.PrefixLen != 24 || !en0.Private || en0.VPNLike {
		t.Fatalf("en0 candidate metadata wrong: %+v", en0)
	}
	pub := byIP["203.0.113.10"]
	if pub.Private {
		t.Fatalf("public IPv4 must not be flagged private: %+v", pub)
	}
	vpn := byIP["10.8.0.2"]
	if !vpn.VPNLike || !vpn.Private {
		t.Fatalf("point-to-point tunnel address must be private+vpnLike: %+v", vpn)
	}
	bridge := byIP["192.168.84.1"]
	if !bridge.VPNLike {
		t.Fatalf("vmnet-prefixed virtual NIC must be marked vpnLike (case-insensitive): %+v", bridge)
	}
}

func TestSelectLanAddresses_OrdersPrivatePhysicalFirstDeterministically(t *testing.T) {
	got := selectLanAddresses(lanViews(t))
	if len(got) != 5 {
		t.Fatalf("candidate count = %d want 5: %+v", len(got), got)
	}
	// 期望序：en0(192.168.31.55) / en1(10.1.2.3) 私网物理 → Vmnet1 / utun3 私网虚拟 → en2 公网。
	want := []string{"192.168.31.55", "10.1.2.3", "192.168.84.1", "10.8.0.2", "203.0.113.10"}
	for i, w := range want {
		if got[i].IP != w {
			t.Fatalf("order[%d] = %s want %s (full: %+v)", i, got[i].IP, w, got)
		}
	}
	// 确定序：同一输入二次枚举结果一致。
	again := selectLanAddresses(lanViews(t))
	if fmt.Sprint(again) != fmt.Sprint(got) {
		t.Fatalf("ordering not deterministic:\n%+v\nvs\n%+v", again, got)
	}
}

func TestSelectLanAddresses_EmptyWhenOnlyLoopback(t *testing.T) {
	got := selectLanAddresses([]ifaceView{{
		Name: "lo0", Up: true, Loopback: true,
		Addrs: []net.Addr{mustCIDR(t, "127.0.0.1/8")},
	}})
	if len(got) != 0 {
		t.Fatalf("loopback-only host must yield no candidates: %+v", got)
	}
}

func TestListLocalLanAddresses_RealInterfacesSmoke(t *testing.T) {
	got, err := ListLocalLanAddresses()
	if err != nil {
		t.Skipf("net.Interfaces unavailable in this environment: %v", err)
	}
	for _, info := range got {
		ip := net.ParseIP(info.IP)
		if ip == nil || ip.To4() == nil {
			t.Fatalf("candidate %q not IPv4: %+v", info.IP, info)
		}
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
			t.Fatalf("candidate %q survived filtering: %+v", info.IP, info)
		}
		if info.Interface == "" || info.PrefixLen < 0 || info.PrefixLen > 32 {
			t.Fatalf("candidate metadata incomplete: %+v", info)
		}
	}
}
