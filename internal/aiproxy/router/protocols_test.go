package router

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"amagi-codebox/internal/aiproxy/subscription"
)

func TestImportedProtocolsPassCoreCheck(t *testing.T) {
	core := os.Getenv("SING_BOX_TEST_BINARY")
	if core == "" {
		t.Skip("set SING_BOX_TEST_BINARY to validate protocol translations")
	}
	uuid := "c066503f-5bd3-4c00-92ef-56083b3422f3"
	v := base64.StdEncoding.EncodeToString([]byte(`{"v":"2","ps":"VM","add":"vm.example","port":"443","id":"` + uuid + `","aid":"0","net":"ws","path":"/test","host":"sni.example","tls":"tls"}`))
	cases := []string{
		"vless://" + uuid + "@node.example:443?security=reality&sni=example.com&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&sid=01&fp=chrome&flow=xtls-rprx-vision",
		"vmess://" + v,
		"trojan://pass@node.example:443?type=grpc&serviceName=proxy&sni=example.com",
		"ss://aes-128-gcm:pass@node.example:8388",
		"hy2://pass@node.example:443?sni=example.com&obfs=salamander&obfs-password=secret",
		"tuic://" + uuid + ":pass@node.example:443?alpn=h3&congestion_control=bbr",
		"socks5://user:pass@127.0.0.1:1080",
		"https://user:pass@node.example:443",
		"proxies:\n  - name: Reality\n    type: vless\n    server: node.example\n    port: 443\n    uuid: " + uuid + "\n    tls: true\n    flow: xtls-rprx-vision\n    servername: example.com\n    reality-opts:\n      public-key: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\n      short-id: '01'\n",
	}
	for i, content := range cases {
		nodes, warnings, err := subscription.Parse([]byte(content))
		if err != nil || len(warnings) > 0 {
			t.Fatal(i, err, warnings)
		}
		s := Defaults()
		s.Rules[0].Node = "c"
		s.Rules[1].Node = "o"
		config, err := Build(s, map[string]subscription.Node{"c": nodes[0], "o": nodes[0]})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "config.json")
		if err = os.WriteFile(path, config, 0600); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		out, err := exec.CommandContext(ctx, core, "check", "-c", path).CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("protocol sample %d failed: %v %s", i, err, out)
		}
	}
}
func TestCredentialRedactionWithEscapes(t *testing.T) {
	config := []byte(`{"outbounds":[{"password":"secret\"quoted\npassword","uuid":"private-id"}]}`)
	if got := redact("secret\"quoted\npassword private-id", config); got != "[redacted] [redacted]" {
		t.Fatal(got)
	}
}
