package remote

// lanaddr.go — 本机局域网 IPv4 候选地址枚举（P3-B R1）。
//
// 供 App.ListLocalLanAddresses 绑定（配对卡 addressRequired 兜底候选列表与
// 自检卡展示）。与 server_security.go 的 selectPairingAdvertiseIP（通配监听时
// 自动选一个可通告 IP）同域：这里面向用户展示"全部候选"，那里是自动择一。
//
// 枚举与过滤排序分离：selectLanAddresses 为纯函数（消费 ifaceView 投影），
// 可脱离真实网卡单测；ListLocalLanAddresses 负责真实枚举。

import (
	"net"
	"sort"
	"strings"
)

// LanAddressInfo 是一个可枚举的本机 IPv4 候选地址。
type LanAddressInfo struct {
	// IP 为点分十进制 IPv4（已过滤回环/链路本地/组播/通配与非单播地址）。
	IP string `json:"ip"`
	// Interface 为操作系统网卡名（en0 / 以太网 / utun3 …）。
	Interface string `json:"interface"`
	// PrefixLen 为掩码长度（CIDR 前缀位数，如 24）。
	PrefixLen int `json:"prefixLen"`
	// Private 标记是否私网地址（net.IP.IsPrivate：RFC1918 私网段与 RFC4193 IPv6 ULA；
	// 不含 CGNAT 100.64/10 共享段——该段在 Go 标准库非 IsPrivate 语义，diting Minor-1 勘误）。
	Private bool `json:"private"`
	// VPNLike 按启发式标注疑似 VPN/虚拟网卡（点对点接口标志或常见隧道/虚拟
	// 网卡名前缀）；只标注、不硬滤——VPN 内网地址在远程场景可能同样可达。
	VPNLike bool `json:"vpnLike"`
}

// ifaceView 是 net.Interface 的可测试投影：ListLocalLanAddresses 负责真实
// 枚举并填充，selectLanAddresses 只消费该结构，使过滤与排序逻辑可单测。
type ifaceView struct {
	Name         string
	Up           bool
	Loopback     bool
	PointToPoint bool
	Addrs        []net.Addr
}

// vpnNamePrefixes 收录常见 VPN 隧道与虚拟网卡名前缀（小写比较）。点对点
// 接口标志已覆盖大多数隧道（utun/ppp/wg 等），前缀仅补充确实以广播多路
// 访问（非点对点）形式出现的虚拟交换/桥接网卡。
var vpnNamePrefixes = []string{
	"vmnet",     // VMware / Parallels 共享网络
	"vethernet", // Windows Hyper-V 虚拟交换机
	"veth",      // Linux veth pair
	"virbr",     // libvirt 桥接
	"docker",    // Docker 桥接
	"br-",       // Docker 自定义网桥
	"zt",        // ZeroTier
}

// ListLocalLanAddresses 枚举本机网卡并返回排序后的 IPv4 候选列表。
// 单块网卡地址枚举失败不影响整体（跳过该网卡继续）；net.Interfaces 本身
// 失败时返回错误，由调用方决定降级路径（前端兜底回手动输入）。
func ListLocalLanAddresses() ([]LanAddressInfo, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	views := make([]ifaceView, 0, len(ifaces))
	for _, ifi := range ifaces {
		addrs, aerr := ifi.Addrs()
		if aerr != nil {
			continue
		}
		views = append(views, ifaceView{
			Name:         ifi.Name,
			Up:           ifi.Flags&net.FlagUp != 0,
			Loopback:     ifi.Flags&net.FlagLoopback != 0,
			PointToPoint: ifi.Flags&net.FlagPointToPoint != 0,
			Addrs:        addrs,
		})
	}
	return selectLanAddresses(views), nil
}

// selectLanAddresses 过滤并排序候选地址（纯函数）：
//   - 过滤：未启用网卡、回环网卡、回环/链路本地/组播/通配/非全局单播 IP、IPv6；
//   - 排序（稳定确定序）：私网优先 → 非疑似 VPN 优先 → 网卡名 → IP 字符串。
func selectLanAddresses(views []ifaceView) []LanAddressInfo {
	out := []LanAddressInfo{}
	for _, ifv := range views {
		if !ifv.Up || ifv.Loopback {
			continue
		}
		vpn := ifv.PointToPoint || hasVPNNamePrefix(ifv.Name)
		for _, addr := range ifv.Addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil {
				continue // 仅 IPv4：配对兜底面向 LAN 扫码直连，IPv6 ULA 直连性不可靠
			}
			if ip4.IsLoopback() || ip4.IsLinkLocalUnicast() || ip4.IsLinkLocalMulticast() ||
				ip4.IsMulticast() || ip4.IsUnspecified() || !ip4.IsGlobalUnicast() {
				continue
			}
			prefixLen, _ := ipnet.Mask.Size()
			out = append(out, LanAddressInfo{
				IP:        ip4.String(),
				Interface: ifv.Name,
				PrefixLen: prefixLen,
				Private:   ip4.IsPrivate(),
				VPNLike:   vpn,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Private != b.Private {
			return a.Private // 私网优先
		}
		if a.VPNLike != b.VPNLike {
			return !a.VPNLike // 物理网卡优先于疑似 VPN/虚拟网卡
		}
		if a.Interface != b.Interface {
			return a.Interface < b.Interface
		}
		return a.IP < b.IP
	})
	return out
}

// hasVPNNamePrefix 报告网卡名是否命中已知 VPN/虚拟网卡前缀（大小写不敏感）。
func hasVPNNamePrefix(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	for _, p := range vpnNamePrefixes {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}
