package subscription

import (
	"fmt"
	"strings"
)

// FromClash converts an in-memory node using the same validation as YAML imports.
func FromClash(p map[string]any) (Node, error) {
	out, err := clashNode(p)
	if err != nil {
		return Node{}, err
	}
	return NewNode(str(p["name"]), out)
}

func clashNode(p map[string]any) (map[string]any, error) {
	t := str(p["type"])
	if t == "ss" {
		t = "shadowsocks"
	}
	if t == "socks5" {
		t = "socks"
	}
	if t == "hy2" {
		t = "hysteria2"
	}
	for _, k := range []string{"plugin", "dialer-proxy", "smux", "reality-opts", "ws-opts", "grpc-opts", "h2-opts", "http-opts", "http-upgrade-opts"} {
		if k == "plugin" || k == "dialer-proxy" || k == "smux" {
			if v, ok := p[k]; ok && v != nil && str(v) != "" {
				return nil, fmt.Errorf("暂不支持 Clash 字段 %q", k)
			}
		}
	}
	o := map[string]any{"type": t, "server": str(p["server"]), "server_port": integer(p["port"])}
	switch t {
	case "vless", "vmess":
		o["uuid"] = str(p["uuid"])
		if t == "vless" {
			if f := str(p["flow"]); f != "" {
				o["flow"] = f
			}
		} else {
			o["security"] = first(str(p["cipher"]), "auto")
			o["alter_id"] = integer(p["alterId"])
		}
	case "trojan", "hysteria2":
		o["password"] = str(p["password"])
	case "shadowsocks":
		o["method"] = str(p["cipher"])
		o["password"] = str(p["password"])
	case "socks", "http":
		if user := str(p["username"]); user != "" {
			o["username"] = user
			o["password"] = str(p["password"])
		}
		if t == "socks" {
			o["version"] = "5"
		}
	case "tuic":
		o["uuid"] = str(p["uuid"])
		o["password"] = str(p["password"])
	default:
		return nil, fmt.Errorf("不支持的 Clash 协议 %q", t)
	}
	if truth(str(p["tls"])) || t == "trojan" || t == "hysteria2" || t == "tuic" {
		tls := map[string]any{"enabled": true}
		if sni := first(str(p["servername"]), str(p["sni"])); sni != "" {
			tls["server_name"] = sni
		}
		if truth(str(p["skip-cert-verify"])) {
			tls["insecure"] = true
		}
		if a, ok := p["alpn"].([]any); ok {
			tls["alpn"] = a
		}
		fp := str(p["client-fingerprint"])
		if r, ok := p["reality-opts"].(map[string]any); ok {
			tls["reality"] = map[string]any{"enabled": true, "public_key": str(r["public-key"]), "short_id": str(r["short-id"])}
			if fp == "" {
				fp = "chrome"
			}
		}
		if fp != "" {
			tls["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
		}
		o["tls"] = tls
	} else if p["reality-opts"] != nil {
		return nil, fmt.Errorf("Reality 节点必须启用 TLS")
	}
	if t == "hysteria2" {
		if v := str(p["obfs"]); v != "" {
			o["obfs"] = map[string]any{"type": v, "password": str(p["obfs-password"])}
		}
	}
	if t == "tuic" {
		for from, to := range map[string]string{"congestion-controller": "congestion_control", "udp-relay-mode": "udp_relay_mode"} {
			if v := str(p[from]); v != "" {
				o[to] = v
			}
		}
	}
	if t == "vless" || t == "vmess" || t == "trojan" {
		net := first(str(p["network"]), "tcp")
		var options map[string]any
		switch net {
		case "ws":
			options, _ = p["ws-opts"].(map[string]any)
		case "grpc":
			options, _ = p["grpc-opts"].(map[string]any)
		case "h2":
			options, _ = p["h2-opts"].(map[string]any)
		case "http":
			options, _ = p["http-opts"].(map[string]any)
		case "httpupgrade":
			options, _ = p["http-upgrade-opts"].(map[string]any)
		}
		host := str(options["host"])
		if h, ok := options["host"].([]any); ok {
			parts := []string{}
			for _, v := range h {
				parts = append(parts, str(v))
			}
			host = strings.Join(parts, ",")
		}
		headers, _ := options["headers"].(map[string]any)
		if h := str(headers["Host"]); h != "" {
			host = h
		}
		tr, err := transport(net, host, str(options["path"]), str(options["grpc-service-name"]), "")
		if err != nil {
			return nil, err
		}
		if tr != nil {
			if net == "ws" && len(headers) > 0 {
				tr["headers"] = headers
			}
			if net == "ws" && integer(options["max-early-data"]) > 0 {
				tr["max_early_data"] = integer(options["max-early-data"])
				tr["early_data_header_name"] = first(str(options["early-data-header-name"]), "Sec-WebSocket-Protocol")
			}
			o["transport"] = tr
		}
	}
	return o, nil
}
