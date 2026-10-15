package subscription

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

func TestSubscriptionFormats(t *testing.T) {
	vmess := base64.StdEncoding.EncodeToString([]byte(`{"v":"2","ps":"VM","add":"vm.example","port":"443","id":"c066503f-5bd3-4c00-92ef-56083b3422f3","aid":"0","net":"ws","path":"/test","host":"sni.example","tls":"tls"}`))
	cases := []struct{ name, content, protocol string }{
		{"VLESS Reality", "vless://c066503f-5bd3-4c00-92ef-56083b3422f3@node.example:443?security=reality&sni=example.com&pbk=abc&sid=01&fp=chrome&flow=xtls-rprx-vision#US", "vless"},
		{"VMess", "vmess://" + vmess, "vmess"},
		{"Trojan", "trojan://my-password@node.example:443?sni=example.com#Tokyo", "trojan"},
		{"SS SIP002", "ss://" + base64.RawURLEncoding.EncodeToString([]byte("aes-128-gcm:pass:word")) + "@node.example:8388#SS", "shadowsocks"},
		{"SS legacy", "ss://" + base64.StdEncoding.EncodeToString([]byte("aes-128-gcm:pass@node.example:8388")) + "#SS", "shadowsocks"},
		{"Hysteria2", "hy2://pass@node.example:443?sni=example.com&obfs=salamander&obfs-password=obfs#HY", "hysteria2"},
		{"TUIC", "tuic://c066503f-5bd3-4c00-92ef-56083b3422f3:pass@node.example:443?alpn=h3&congestion_control=bbr#TUIC", "tuic"},
		{"Base64 subscription", base64.RawURLEncoding.EncodeToString([]byte("trojan://password@node.example:443#Tokyo")), "trojan"},
		{"Clash YAML", "proxies:\n  - name: US\n    type: vless\n    server: node.example\n    port: 443\n    uuid: c066503f-5bd3-4c00-92ef-56083b3422f3\n    tls: true\n    network: ws\n    ws-opts:\n      path: /proxy\n      headers:\n        Host: example.com\n", "vless"},
		{"sing-box JSON", `{"outbounds":[{"tag":"US","type":"socks","server":"127.0.0.1","server_port":1080,"version":"5"}]}`, "socks"},
		{"IPv6", "socks5://user:pass@[::1]:1080#IPv6", "socks"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			nodes, warnings, err := Parse([]byte(c.content))
			if err != nil || len(warnings) > 0 || len(nodes) != 1 {
				t.Fatalf("nodes=%d warnings=%v err=%v", len(nodes), warnings, err)
			}
			if nodes[0].Type != c.protocol {
				t.Fatal(nodes[0])
			}
			again, _, _ := Parse([]byte(c.content))
			if nodes[0].ID != again[0].ID {
				t.Fatal("unstable node identity")
			}
		})
	}
}
func TestUnsupportedNodesDoNotDowngrade(t *testing.T) {
	for _, bad := range []string{
		"vless://uuid@node.example:443?type=xhttp&security=tls",
		"vless://uuid@node.example:443?headerType=http",
		"ss://aes-128-gcm:pass@node.example:8388?plugin=v2ray-plugin",
		`{"outbounds":[{"type":"socks","server":"127.0.0.1","server_port":1080,"detour":"elsewhere"}]}`,
		`{"outbounds":[{"type":"trojan","server":"node.example","server_port":443,"password":"pass","tls":{"enabled":true,"certificate_path":"/tmp/private"}}]}`,
	} {
		t.Run(fmt.Sprint(len(bad), bad[:8]), func(t *testing.T) {
			nodes, warnings, err := Parse([]byte(bad))
			if err == nil || len(nodes) != 0 || len(warnings) == 0 {
				t.Fatalf("must reject: %v %v", err, warnings)
			}
		})
	}
	nodes, warnings, err := Parse([]byte("unsupported://node.example:443\ntrojan://password@node.example:443#valid"))
	if err != nil || len(nodes) != 1 || len(warnings) != 1 {
		t.Fatalf("mixed subscription failed: %v", err)
	}
	if strings.Contains(warnings[0], "password") {
		t.Fatal("credentials in diagnostics")
	}
}
func TestDeduplicateAndSize(t *testing.T) {
	uri := "socks5://127.0.0.1:1080#one"
	nodes, _, err := Parse([]byte(uri + "\n" + uri))
	if err != nil || len(nodes) != 1 {
		t.Fatal(err)
	}
	if _, _, err = Parse(make([]byte, MaxBytes+1)); err == nil {
		t.Fatal("oversize accepted")
	}
}
