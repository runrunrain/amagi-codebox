package v2rayn

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"amagi-codebox/internal/aiproxy/subscription"
)

func value(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}
func number(v any) int { n, _ := strconv.Atoi(value(v)); return n }
func first(values ...any) string {
	for _, v := range values {
		if s := value(v); s != "" {
			return s
		}
	}
	return ""
}
func enabled(v any) bool { s := value(v); return s == "1" || strings.EqualFold(s, "true") }
func extras(v any) (map[string]any, error) {
	s := value(v)
	if s == "" {
		return map[string]any{}, nil
	}
	var m map[string]any
	if json.Unmarshal([]byte(s), &m) != nil {
		return nil, errors.New("节点扩展配置无效")
	}
	return m, nil
}

func convert(row map[string]any) (subscription.Node, error) {
	var empty subscription.Node
	proto, err := extras(row["ProtoExtra"])
	if err != nil {
		return empty, err
	}
	tr, err := extras(row["TransportExtra"])
	if err != nil {
		return empty, err
	}
	types := map[int]string{1: "vmess", 3: "ss", 4: "socks5", 5: "vless", 6: "trojan", 7: "hysteria2", 8: "tuic", 10: "http"}
	t := types[number(row["ConfigType"])]
	if t == "" {
		return empty, errors.New("暂不支持此节点类型或策略组")
	}
	for _, k := range []string{"Cert", "CertSha", "EchConfigList", "VerifyPeerCertByName", "Finalmask", "Mldsa65Verify"} {
		if value(row[k]) != "" {
			return empty, fmt.Errorf("暂不支持节点字段 %s", k)
		}
	}
	if number(row["PreSocksPort"]) > 0 {
		return empty, errors.New("暂不支持节点前置代理")
	}
	if enabled(row["MuxEnabled"]) {
		return empty, errors.New("暂不支持此节点的多路复用设置")
	}
	for _, k := range []string{"GeckoMinPacketSize", "GeckoMaxPacketSize", "Hy2RealmUrl", "HttpHeaders"} {
		if value(proto[k]) != "" {
			return empty, fmt.Errorf("暂不支持节点扩展 %s", k)
		}
	}
	if enabled(proto["Uot"]) {
		return empty, errors.New("暂不支持此节点的 UDP over TCP 设置")
	}
	security := value(row["StreamSecurity"])
	if security != "" && security != "none" && security != "tls" && security != "reality" {
		return empty, errors.New("暂不支持此 TLS 类型")
	}
	password := first(row["Password"], row["Id"])
	p := map[string]any{"name": value(row["Remarks"]), "type": t, "server": value(row["Address"]), "port": number(row["Port"]), "password": password}
	switch t {
	case "vless", "vmess":
		p["uuid"] = password
		if t == "vless" {
			enc := first(proto["VlessEncryption"], row["Security"])
			if enc != "" && enc != "none" {
				return empty, errors.New("暂不支持此 VLESS 加密配置")
			}
			p["flow"] = first(proto["Flow"], row["Flow"])
		} else {
			p["cipher"] = first(proto["VmessSecurity"], row["Security"], "auto")
			p["alterId"] = number(first(proto["AlterId"], row["AlterId"]))
		}
	case "ss":
		p["cipher"] = first(proto["SsMethod"], row["Security"])
	case "socks5", "http":
		p["username"] = first(row["Username"], row["Security"])
	case "tuic":
		p["uuid"] = first(row["Username"], row["Id"])
		p["password"] = first(row["Password"], row["Security"])
		p["congestion-controller"] = proto["CongestionControl"]
	case "hysteria2":
		if first(proto["Ports"], row["Ports"]) != "" {
			return empty, errors.New("暂不支持 Hysteria2 端口跳跃")
		}
		if obfs := first(proto["SalamanderPass"], row["Path"]); obfs != "" {
			p["obfs"] = "salamander"
			p["obfs-password"] = obfs
		}
	}
	if security == "tls" || security == "reality" {
		p["tls"] = true
	}
	p["servername"] = row["Sni"]
	p["skip-cert-verify"] = enabled(row["AllowInsecure"])
	p["client-fingerprint"] = row["Fingerprint"]
	if alpn := value(row["Alpn"]); alpn != "" {
		parts := []any{}
		for _, v := range strings.Split(alpn, ",") {
			if v = strings.TrimSpace(v); v != "" {
				parts = append(parts, v)
			}
		}
		p["alpn"] = parts
	}
	if security == "reality" {
		p["reality-opts"] = map[string]any{"public-key": row["PublicKey"], "short-id": row["ShortId"]}
	}
	if t == "vless" || t == "vmess" || t == "trojan" {
		network := value(row["Network"])
		if network == "" || network == "raw" {
			network = "tcp"
		}
		p["network"] = network
		host := first(tr["Host"], row["RequestHost"])
		path := first(tr["Path"], row["Path"])
		switch network {
		case "tcp":
			if header := first(tr["RawHeaderType"], row["HeaderType"]); header != "" && header != "none" {
				return empty, errors.New("暂不支持 TCP HTTP 伪装")
			}
		case "ws":
			p["ws-opts"] = map[string]any{"path": path, "headers": map[string]any{"Host": host}}
		case "grpc":
			if mode := first(tr["GrpcMode"], row["HeaderType"]); mode != "" && mode != "gun" && mode != "none" {
				return empty, errors.New("暂不支持此 gRPC 模式")
			}
			if value(tr["GrpcAuthority"]) != "" {
				return empty, errors.New("暂不支持自定义 gRPC authority")
			}
			p["grpc-opts"] = map[string]any{"grpc-service-name": first(tr["GrpcServiceName"], row["Path"])}
		case "h2", "http":
			p[network+"-opts"] = map[string]any{"host": host, "path": path}
		case "httpupgrade":
			p["http-upgrade-opts"] = map[string]any{"host": host, "path": path}
		default:
			return empty, fmt.Errorf("暂不支持传输 %q", network)
		}
	}
	node, err := subscription.FromClash(p)
	if err != nil {
		return empty, err
	}
	if t == "hysteria2" {
		for from, to := range map[string]string{"UpMbps": "up_mbps", "DownMbps": "down_mbps"} {
			if n := number(proto[from]); n > 0 {
				node.Outbound[to] = n
			}
		}
		return subscription.NewNode(node.Name, node.Outbound)
	}
	return node, nil
}
