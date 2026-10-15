// Package subscription translates standalone proxy nodes into sing-box outbounds.
package subscription

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const MaxBytes = 8 << 20

type Node struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Type     string         `json:"type"`
	Server   string         `json:"server"`
	Port     int            `json:"port"`
	Outbound map[string]any `json:"outbound"`
}

func DecodeBase64(s string) ([]byte, error) {
	s = strings.Join(strings.Fields(s), "")
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("invalid base64")
}

// Parse accepts URI lists, base64 URI subscriptions, Clash YAML and standalone
// sing-box JSON outbounds. Unsupported nodes are reported, never downgraded.
func Parse(data []byte) ([]Node, []string, error) {
	if len(data) > MaxBytes {
		return nil, nil, errors.New("订阅超过 8 MiB")
	}
	s := strings.TrimSpace(strings.TrimPrefix(string(data), "\ufeff"))
	if s == "" {
		return nil, nil, errors.New("订阅为空")
	}
	if !strings.Contains(s, "://") && !strings.HasPrefix(s, "{") && !strings.HasPrefix(s, "[") && !strings.Contains(s, "proxies:") {
		if decoded, err := DecodeBase64(s); err == nil {
			s = strings.TrimSpace(string(decoded))
		}
	}
	var nodes []Node
	var warnings []string
	add := func(name string, out map[string]any, err error, position int) {
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("第 %d 个节点：%v", position, err))
			return
		}
		n, err := NewNode(name, out)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("第 %d 个节点：%v", position, err))
			return
		}
		nodes = append(nodes, n)
	}
	if strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[") {
		var raw json.RawMessage
		if err := json.Unmarshal([]byte(s), &raw); err != nil {
			return nil, nil, errors.New("JSON 格式错误")
		}
		var outs []map[string]any
		if strings.HasPrefix(s, "[") {
			_ = json.Unmarshal(raw, &outs)
		} else {
			var wrapper struct {
				Outbounds []map[string]any `json:"outbounds"`
			}
			_ = json.Unmarshal(raw, &wrapper)
			outs = wrapper.Outbounds
		}
		for i, o := range outs {
			add(str(o["tag"]), o, nil, i+1)
		}
	} else if strings.Contains(s, "proxies:") {
		var wrapper struct {
			Proxies []map[string]any `yaml:"proxies"`
		}
		if err := yaml.Unmarshal([]byte(s), &wrapper); err != nil {
			return nil, nil, errors.New("Clash YAML 格式错误")
		}
		for i, p := range wrapper.Proxies {
			out, err := clashNode(p)
			add(str(p["name"]), out, err, i+1)
		}
	} else {
		for i, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			name, out, err := parseURI(line)
			add(name, out, err, i+1)
		}
	}
	seen := map[string]bool{}
	unique := nodes[:0]
	for _, n := range nodes {
		if !seen[n.ID] {
			unique = append(unique, n)
			seen[n.ID] = true
		}
	}
	if len(unique) == 0 {
		return nil, warnings, errors.New("没有可用节点；支持 URI/Base64、Clash YAML 和 sing-box outbounds JSON")
	}
	return unique, warnings, nil
}

func NewNode(name string, out map[string]any) (Node, error) {
	var n Node
	t := str(out["type"])
	switch t {
	case "socks", "http", "shadowsocks", "vmess", "vless", "trojan", "hysteria2", "tuic":
	default:
		return n, fmt.Errorf("不支持的协议 %q", t)
	}
	// A subscription may only supply standalone endpoints, not arbitrary local
	// files, external processes, routing chains or interface changes.
	allowed := map[string]bool{}
	for _, k := range strings.Fields("type tag server server_port username password version method uuid security alter_id flow tls transport network packet_encoding obfs up_mbps down_mbps ports hop_interval congestion_control udp_relay_mode zero_rtt_handshake heartbeat") {
		allowed[k] = true
	}
	for k := range out {
		if !allowed[k] {
			return n, fmt.Errorf("不支持的节点字段 %q", k)
		}
	}
	if err := checkNested(out); err != nil {
		return n, err
	}
	server := str(out["server"])
	port := integer(out["server_port"])
	if server == "" || port < 1 || port > 65535 {
		return n, errors.New("服务器地址或端口无效")
	}
	copyOut := make(map[string]any, len(out))
	for k, v := range out {
		if k != "tag" {
			copyOut[k] = v
		}
	}
	if name == "" {
		name = fmt.Sprintf("%s %s:%d", t, server, port)
	}
	b, _ := json.Marshal(copyOut)
	h := sha256.Sum256(b)
	n = Node{ID: hex.EncodeToString(h[:12]), Name: name, Type: t, Server: server, Port: port, Outbound: copyOut}
	return n, nil
}

func checkNested(v any) error {
	switch x := v.(type) {
	case map[string]any:
		for k, value := range x {
			if strings.Contains(k, "path") && k != "path" || k == "certificate" || k == "key" || k == "detour" {
				return fmt.Errorf("不支持的本地资源字段 %q", k)
			}
			if err := checkNested(value); err != nil {
				return err
			}
		}
	case []any:
		for _, value := range x {
			if err := checkNested(value); err != nil {
				return err
			}
		}
	}
	return nil
}

func parseURI(line string) (string, map[string]any, error) {
	if strings.HasPrefix(line, "vmess://") {
		return vmessURI(strings.TrimPrefix(line, "vmess://"))
	}
	if strings.HasPrefix(line, "ss://") {
		return shadowsocksURI(line)
	}
	u, err := url.Parse(line)
	if err != nil {
		return "", nil, errors.New("节点链接格式错误")
	}
	q := u.Query()
	t := strings.ToLower(u.Scheme)
	if t == "hy2" {
		t = "hysteria2"
	}
	if t == "socks5" || t == "socks5h" {
		t = "socks"
	}
	if t == "https" {
		t = "http"
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		return "", nil, errors.New("节点链接缺少有效端口")
	}
	o := map[string]any{"type": t, "server": u.Hostname(), "server_port": port}
	user := ""
	pass := ""
	if u.User != nil {
		user = u.User.Username()
		pass, _ = u.User.Password()
	}
	switch t {
	case "vless":
		if user == "" {
			return "", nil, errors.New("VLESS 缺少 UUID")
		}
		if enc := q.Get("encryption"); enc != "" && enc != "none" {
			return "", nil, errors.New("暂不支持此 VLESS encryption")
		}
		o["uuid"] = user
		if flow := q.Get("flow"); flow != "" {
			o["flow"] = flow
		}
	case "trojan":
		o["password"] = user
	case "hysteria2":
		if pass != "" {
			user += ":" + pass
		}
		o["password"] = user
		if q.Get("obfs") != "" {
			o["obfs"] = map[string]any{"type": q.Get("obfs"), "password": q.Get("obfs-password")}
		}
	case "tuic":
		o["uuid"] = user
		o["password"] = pass
		if v := q.Get("congestion_control"); v != "" {
			o["congestion_control"] = v
		}
		if v := q.Get("udp_relay_mode"); v != "" {
			o["udp_relay_mode"] = v
		}
	case "socks", "http":
		if user != "" {
			o["username"] = user
			o["password"] = pass
		}
		if t == "socks" {
			o["version"] = "5"
		}
	default:
		return "", nil, fmt.Errorf("不支持的协议 %q", t)
	}
	tlsEnabled := q.Get("security") == "tls" || q.Get("security") == "reality" || t == "trojan" || t == "hysteria2" || t == "tuic" || u.Scheme == "https"
	if sec := q.Get("security"); sec != "" && sec != "none" && sec != "tls" && sec != "reality" {
		return "", nil, fmt.Errorf("不支持的 security %q", sec)
	}
	if tlsEnabled {
		tls := map[string]any{"enabled": true}
		if sni := first(q.Get("sni"), q.Get("peer")); sni != "" {
			tls["server_name"] = sni
		}
		if truth(first(q.Get("allowInsecure"), q.Get("insecure"))) {
			tls["insecure"] = true
		}
		if alpn := q.Get("alpn"); alpn != "" {
			tls["alpn"] = strings.Split(alpn, ",")
		}
		if fp := q.Get("fp"); fp != "" {
			tls["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
		}
		if q.Get("security") == "reality" {
			tls["reality"] = map[string]any{"enabled": true, "public_key": q.Get("pbk"), "short_id": q.Get("sid")}
			if q.Get("fp") == "" {
				tls["utls"] = map[string]any{"enabled": true, "fingerprint": "chrome"}
			}
		}
		o["tls"] = tls
	}
	if t == "vless" || t == "trojan" {
		tr, err := transport(first(q.Get("type"), "tcp"), q.Get("host"), q.Get("path"), q.Get("serviceName"), q.Get("headerType"))
		if err != nil {
			return "", nil, err
		}
		if tr != nil {
			o["transport"] = tr
		}
	}
	return u.Fragment, o, nil
}

func transport(t, host, path, service, header string) (map[string]any, error) {
	switch t {
	case "", "tcp":
		if header != "" && header != "none" {
			return nil, errors.New("暂不支持 TCP HTTP 伪装")
		}
		return nil, nil
	case "ws":
		o := map[string]any{"type": "ws", "path": first(path, "/")}
		if host != "" {
			o["headers"] = map[string]any{"Host": host}
		}
		return o, nil
	case "grpc":
		return map[string]any{"type": "grpc", "service_name": service}, nil
	case "http", "h2":
		o := map[string]any{"type": "http", "path": first(path, "/")}
		if host != "" {
			o["host"] = strings.Split(host, ",")
		}
		return o, nil
	case "httpupgrade":
		return map[string]any{"type": "httpupgrade", "host": host, "path": first(path, "/")}, nil
	default:
		return nil, fmt.Errorf("暂不支持传输 %q（例如 XHTTP 需要其他内核）", t)
	}
}

func vmessURI(s string) (string, map[string]any, error) {
	b, err := DecodeBase64(s)
	if err != nil {
		return "", nil, errors.New("VMess base64 无效")
	}
	var v map[string]any
	if json.Unmarshal(b, &v) != nil {
		return "", nil, errors.New("VMess JSON 无效")
	}
	o := map[string]any{"type": "vmess", "server": str(v["add"]), "server_port": integer(v["port"]), "uuid": str(v["id"]), "security": first(str(v["scy"]), "auto"), "alter_id": integer(v["aid"])}
	tr, err := transport(first(str(v["net"]), "tcp"), str(v["host"]), str(v["path"]), str(v["path"]), str(v["type"]))
	if err != nil {
		return "", nil, err
	}
	if tr != nil {
		o["transport"] = tr
	}
	if str(v["tls"]) == "tls" {
		tls := map[string]any{"enabled": true}
		if sni := first(str(v["sni"]), str(v["host"])); sni != "" {
			tls["server_name"] = sni
		}
		if fp := str(v["fp"]); fp != "" {
			tls["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
		}
		if a := str(v["alpn"]); a != "" {
			tls["alpn"] = strings.Split(a, ",")
		}
		o["tls"] = tls
	}
	return str(v["ps"]), o, nil
}

func shadowsocksURI(line string) (string, map[string]any, error) {
	s := strings.TrimPrefix(line, "ss://")
	// Old format base64(method:password@host:port), with an optional fragment.
	if !strings.Contains(strings.SplitN(s, "#", 2)[0], "@") {
		parts := strings.SplitN(s, "#", 2)
		b, err := DecodeBase64(parts[0])
		if err != nil {
			return "", nil, errors.New("SS base64 无效")
		}
		line = "ss://" + string(b)
		if len(parts) == 2 {
			line += "#" + parts[1]
		}
	}
	u, err := url.Parse(line)
	if err != nil || u.User == nil {
		return "", nil, errors.New("SS 链接格式错误")
	}
	if u.Query().Get("plugin") != "" {
		return "", nil, errors.New("暂不支持 SS 插件")
	}
	method := u.User.Username()
	password, hasPass := u.User.Password()
	if !hasPass {
		b, e := DecodeBase64(method)
		if e != nil {
			return "", nil, errors.New("SS 用户信息无效")
		}
		pair := strings.SplitN(string(b), ":", 2)
		if len(pair) != 2 {
			return "", nil, errors.New("SS 缺少密码")
		}
		method, password = pair[0], pair[1]
	}
	port, _ := strconv.Atoi(u.Port())
	return u.Fragment, map[string]any{"type": "shadowsocks", "server": u.Hostname(), "server_port": port, "method": method, "password": password}, nil
}

func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}
func integer(v any) int { i, _ := strconv.Atoi(str(v)); return i }
func first(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
func truth(s string) bool { return s == "1" || strings.EqualFold(s, "true") }
