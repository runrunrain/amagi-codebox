package client

// args_test.go — 解析矩阵：旗标形态、位置参数、类型转换、json/stdin 展开、
// `--` 字面量、冲突拒绝、全局旗标任意位置。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"amagi-codebox/internal/agentcli/spec"
)

// echoEnv 跑一条命令并解出 --json 包络中回显的 args（服务端把 req.Args 放在 echo 字段）。
func echoEnv(t *testing.T, argv []string, stdin string) map[string]any {
	t.Helper()
	code, out, errStr := runTest(t, argv, stdin)
	if code != 0 {
		t.Fatalf("argv=%v code=%d out=%q err=%q", argv, code, out, errStr)
	}
	var env spec.Envelope
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &env); err != nil {
		t.Fatalf("stdout 不是包络: %v\n%q", err, out)
	}
	data, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("data 不是对象: %T", env.Data)
	}
	echo, ok := data["echo"].(map[string]any)
	if !ok {
		t.Fatalf("echo 字段缺失: %#v", data)
	}
	return echo
}

func TestParseMatrix(t *testing.T) {
	fs := newFakeServer(t, fakeOptions{})
	fs.useEndpoint(t)

	cases := []struct {
		name  string
		argv  []string
		stdin string
		want  map[string]any
	}{
		{"旗标取值", []string{"session", "list", "--status", "running"}, "", map[string]any{"status": "running"}},
		{"旗标等号", []string{"session", "list", "--status=stopped"}, "", map[string]any{"status": "stopped"}},
		{"位置参数必填", []string{"--yes", "session", "launch", "claudecode"}, "", map[string]any{"appType": "claudecode"}},
		{"最长匹配 config.get", []string{"config", "get", "remote"}, "", map[string]any{"key": "remote"}},
		{"bool 三形态-裸旗标", []string{"--yes", "session", "launch", "claudecode", "--headroom"}, "", map[string]any{"appType": "claudecode", "headroom": true}},
		{"bool 三形态-no", []string{"--yes", "session", "launch", "claudecode", "--no-headroom"}, "", map[string]any{"appType": "claudecode", "headroom": false}},
		{"bool 三形态-等号", []string{"--yes", "session", "launch", "claudecode", "--headroom=false"}, "", map[string]any{"appType": "claudecode", "headroom": false}},
		{"int", []string{"usage", "daily", "--days", "7"}, "", map[string]any{"days": float64(7)}},
		{"int 负数", []string{"usage", "daily", "--days", "-3"}, "", map[string]any{"days": float64(-3)}},
		{"string[] 位置全量", []string{"tags", "a", "b", "c"}, "", map[string]any{"items": []any{"a", "b", "c"}}},
		{"string[] 旗标重复", []string{"tags", "--items", "x", "--items", "y"}, "", map[string]any{"items": []any{"x", "y"}}},
		{"json 字面量", []string{"provider", "save", "--data", `{"a":1}`}, "", map[string]any{"data": map[string]any{"a": float64(1)}}},
		{"json 数组字面量", []string{"provider", "save", "--data", `[1,2]`}, "", map[string]any{"data": []any{float64(1), float64(2)}}},
		{"全局旗标在尾部", []string{"session", "list", "--json", "--status", "running"}, "", map[string]any{"status": "running"}},
		{"--之后全字面量", []string{"config", "get", "--", "--literal"}, "", map[string]any{"key": "--literal"}},
		{"--之后负数字面量", []string{"config", "get", "--", "-5"}, "", map[string]any{"key": "-5"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			argv := append([]string{"--json"}, c.argv...)
			data := echoEnv(t, argv, c.stdin)
			if !reflect.DeepEqual(data, c.want) {
				t.Fatalf("args 不符:\n got %#v\nwant %#v", data, c.want)
			}
		})
	}
}

func TestStdinExpansion(t *testing.T) {
	fs := newFakeServer(t, fakeOptions{})
	fs.useEndpoint(t)

	// Stdin 字符串参数：`-` 读 stdin（去尾部换行，保留内部内容）。
	data := echoEnv(t, []string{"--json", "key", "set", "prov1", "-"}, "  secret-value\n")
	if data["value"] != "  secret-value" {
		t.Fatalf("stdin 字符串: %#v", data)
	}
	// json 参数 `-` 从 stdin。
	data = echoEnv(t, []string{"--json", "provider", "save", "--data", "-"}, `{"k":"v"}`)
	if !reflect.DeepEqual(data["data"], map[string]any{"k": "v"}) {
		t.Fatalf("json stdin: %#v", data)
	}
	// json 参数 @file。
	file := filepath.Join(t.TempDir(), "p.json")
	if err := os.WriteFile(file, []byte(`{"f":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	data = echoEnv(t, []string{"--json", "provider", "save", "--data", "@" + file}, "")
	if !reflect.DeepEqual(data["data"], map[string]any{"f": float64(1)}) {
		t.Fatalf("json @file: %#v", data)
	}
	// session send 的 text 参数为 Stdin：`-` 读 stdin。
	data = echoEnv(t, []string{"--json", "--yes", "session", "send", "s1", "-"}, "hello\n")
	if data["id"] != "s1" || data["text"] != "hello" {
		t.Fatalf("session send stdin: %#v", data)
	}
}

func TestParseErrors(t *testing.T) {
	fs := newFakeServer(t, fakeOptions{})
	fs.useEndpoint(t)

	cases := []struct {
		name    string
		argv    []string
		wantMsg string
	}{
		{"枚举违规", []string{"session", "list", "--status", "bogus"}, "取值必须是"},
		{"未知旗标", []string{"session", "list", "--bogus"}, "未知旗标"},
		{"未知旗标建议", []string{"session", "list", "--statuz"}, "你是不是想输入"},
		{"位置与旗标冲突", []string{"session", "launch", "claudecode", "--app-type", "pi"}, "不能同时以位置参数和旗标"},
		{"旗标缺值", []string{"session", "list", "--status"}, "缺少值"},
		{"旗标缺值-后随旗标", []string{"session", "launch", "--mode", "--headroom"}, "缺少值"},
		{"标量旗标重复", []string{"usage", "daily", "--days", "1", "--days", "2"}, "重复给出"},
		{"int 非法", []string{"usage", "daily", "--days", "x"}, "整数"},
		{"bool 非法", []string{"session", "launch", "claudecode", "--headroom=x"}, "布尔"},
		{"json 非法", []string{"provider", "save", "--data", "{bad"}, "JSON"},
		{"json 文件缺失", []string{"provider", "save", "--data", "@/nonexistent.json"}, "读取文件"},
		{"多余位置参数", []string{"status", "extra"}, "多余"},
		{"no- 用于非 bool", []string{"usage", "daily", "--no-days"}, "仅适用于 bool"},
		{"string[] 位置与旗标冲突", []string{"tags", "a", "--items", "b"}, "不能同时以位置参数和旗标"},
		{"单破折线词拒作位置参数", []string{"config", "get", "-5"}, "未知旗标"},
		{"缺必填位置参数", []string{"key", "set", "name"}, "缺少必填位置参数"},
		{"单破折线未知旗标", []string{"session", "list", "-x"}, "未知旗标"},
		{"--timeout 非法", []string{"--timeout", "abc", "status"}, "正整数"},
		{"--json 不接受值", []string{"--json=true", "status"}, "不接受值"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, _, errStr := runTest(t, c.argv, "")
			if code != 2 {
				t.Fatalf("argv=%v code=%d err=%q", c.argv, code, errStr)
			}
			if !strings.Contains(errStr, c.wantMsg) {
				t.Fatalf("argv=%v\n err=%q\n期望包含 %q", c.argv, errStr, c.wantMsg)
			}
		})
	}
}

// TestSecretStdinParamMustUseDash — Secret+Stdin 参数只能以 `-`（stdin）
// 传入（评审 D-07）：明文 argv 值一律本地用法错误（exit 2）、不发出请求、
// 错误不回显值并给出 stdin 用法示例。
func TestSecretStdinParamMustUseDash(t *testing.T) {
	fs := newFakeServer(t, fakeOptions{})
	fs.useEndpoint(t)

	for _, argv := range [][]string{
		{"key", "set", "prov1", "SUPER-SECRET-LITERAL-9"}, // 位置参数明文
		{"key", "set", "prov1", "--value", "SUPER-SECRET-LITERAL-9"}, // 旗标明文
		{"key", "set", "--value=SUPER-SECRET-LITERAL-9", "prov1"}, // 等号形态
	} {
		code, out, errStr := runTest(t, argv, "")
		if code != spec.ExitUsage {
			t.Fatalf("argv=%v code=%d err=%q：Secret 参数明文应本地拒绝 exit 2", argv, code, errStr)
		}
		if strings.Contains(out+errStr, "SUPER-SECRET-LITERAL-9") {
			t.Fatalf("argv=%v：错误不得回显明文值 out=%q err=%q", argv, out, errStr)
		}
		if !strings.Contains(errStr, "stdin") || !strings.Contains(errStr, "-") {
			t.Fatalf("argv=%v：错误应给出 `-`（stdin）用法示例 err=%q", argv, errStr)
		}
	}
	if n := fs.requestCount(); n != 0 {
		t.Fatalf("明文 Secret 应在本地拒绝，实际发出 %d 个请求", n)
	}
	// 合法路径不受影响：`-` 从 stdin 读。
	code, _, errStr := runTest(t, []string{"key", "set", "prov1", "-"}, "the-key")
	if code != 0 {
		t.Fatalf("`-` 传入应正常执行: code=%d err=%q", code, errStr)
	}
}

// TestParamFlagShadowsGlobalFlag 命令词之后与全局旗标同名的参数旗标（真实表
// provider.save 的 --json <json>）必须按参数旗标处理——值不被全局 --json
// 吞掉、不报“多余的参数”；命令无同名参数或旗标在命令词之前时仍是全局
// 包络模式。回归锚定：table-aware extractGlobals（C2 集成测试发现的跨组件缺陷）。
func TestParamFlagShadowsGlobalFlag(t *testing.T) {
	fs := newFakeServer(t, fakeOptions{})
	fs.useEndpoint(t)

	// 参数 --json 携带 JSON 值：按参数旗标解析，与全局 --json 包络共存。
	data := echoEnv(t, []string{"--json", "provider", "save", "--data", `{"a":1}`, "--json", `{"b":2}`}, "")
	if !reflect.DeepEqual(data["data"], map[string]any{"a": float64(1)}) {
		t.Fatalf("--data 参数: %#v", data)
	}
	if !reflect.DeepEqual(data["json"], map[string]any{"b": float64(2)}) {
		t.Fatalf("同名参数旗标 --json 应按参数解析: %#v", data)
	}
	// 等号形态同样按参数旗标处理。
	data = echoEnv(t, []string{"--json", "provider", "save", "--data", `{}`, "--json={\"c\":3}"}, "")
	if !reflect.DeepEqual(data["json"], map[string]any{"c": float64(3)}) {
		t.Fatalf("--json=值 形态: %#v", data)
	}
	// 命令无同名参数（session list）：命令词之后的 --json 仍是全局旗标
	//（若被误当参数旗标，session list 无 json 参数会报“未知旗标”退出 2）。
	code, out, errStr := runTest(t, []string{"session", "list", "--json"}, "")
	if code != 0 || !strings.Contains(out, `"ok":true`) {
		t.Fatalf("session list --json 应是全局包络模式：code=%d out=%q err=%q", code, out, errStr)
	}
}
