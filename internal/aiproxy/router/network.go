package router

import (
	"fmt"
	"net"
	"strings"
)

// Interface describes only network metadata; addresses never enter the UI.
type Interface struct {
	Name       string `json:"name"`
	Up         bool   `json:"up"`
	Selectable bool   `json:"selectable"`
}

func physicalInterface(n net.Interface) bool {
	if n.Flags&(net.FlagLoopback|net.FlagPointToPoint) != 0 {
		return false
	}
	name := strings.ToLower(n.Name)
	for _, prefix := range []string{"utun", "tun", "tap", "wg", "tailscale", "docker", "veth", "bridge", "vmnet", "awdl", "llw", "gif", "stf", "anpi", "ap", "ipsec", "ppp", "sing-box", "v2ray", "clash", "mihomo", "nekoray"} {
		if strings.HasPrefix(name, prefix) {
			return false
		}
	}
	return true
}

func Interfaces() []Interface {
	result := []Interface{}
	all, _ := net.Interfaces()
	for _, n := range all {
		up := n.Flags&net.FlagUp != 0
		result = append(result, Interface{Name: n.Name, Up: up, Selectable: up && physicalInterface(n) && hasAddress(n)})
	}
	return result
}
func ValidateInterface(name string) error {
	n, err := net.InterfaceByName(name)
	if err != nil || n.Flags&net.FlagUp == 0 || !physicalInterface(*n) || !hasAddress(*n) {
		return fmt.Errorf("出口网卡 %q 不可用，请选择已连接的物理网卡", name)
	}
	return nil
}

func hasAddress(n net.Interface) bool {
	addresses, _ := n.Addrs()
	for _, address := range addresses {
		ip, _, err := net.ParseCIDR(address.String())
		if err == nil && ip.IsGlobalUnicast() {
			return true
		}
	}
	return false
}
