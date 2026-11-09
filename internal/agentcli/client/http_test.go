package client

// http_test.go — 退出码映射、--json 包络、tableHash 偏差警告、超时、
// 端点缺失/损坏/陈旧/非回环、探活不可达。

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"amagi-codebox/internal/agentcli/spec"
)

func TestExitCodeMapping(t *testing.T) {
	cases := []struct {
		code     string
		wantExit int
	}{
		{spec.ErrUnauthorized, 3},
		{spec.ErrForbidden, 3},
		{spec.ErrDisabled, 3},
		{spec.ErrBadArgs, 2},
		{spec.ErrUnknownCommand, 2},
		{spec.ErrForbiddenCommand, 2},
		{spec.ErrConfirmationRequired, 4},
		{spec.ErrNotFound, 1},
		{spec.ErrConflict, 1},
		{spec.ErrNotReady, 1},
		{spec.ErrInternal, 1},
	}
	for _, c := range cases {
		t.Run(c.code, func(t *testing.T) {
			fs := newFakeServer(t, fakeOptions{
				handler: func(name string, req spec.Request) (any, *spec.Error) {
					return nil, spec.Errorf(c.code, "业务错误 %s", c.code)
				},
			})
			fs.useEndpoint(t)
			code, _, errStr := runTest(t, []string{"status"}, "")
			if code != c.wantExit {
				t.Fatalf("%s: code=%d want %d err=%q", c.code, code, c.wantExit, errStr)
			}
			if !strings.Contains(errStr, "业务错误") {
				t.Fatalf("%s: 错误信息未透出: %q", c.code, errStr)
			}
			// --json 模式同一映射。
			code, out, _ := runTest(t, []string{"--json", "status"}, "")
			if code != c.wantExit {
				t.Fatalf("%s --json: code=%d want %d", c.code, code, c.wantExit)
			}
			var env spec.Envelope
			if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &env); err != nil || env.OK || env.Error == nil || env.Error.Code != c.code {
				t.Fatalf("%s --json 包络: %v %+v", c.code, err, env)
			}
		})
	}
}

func TestExitCodeForLocal(t *testing.T) {
	cases := map[string]int{
		spec.ErrBadArgs: 2, spec.ErrUnknownCommand: 2, spec.ErrForbiddenCommand: 2,
		spec.ErrUnauthorized: 3, spec.ErrForbidden: 3, spec.ErrDisabled: 3,
		spec.ErrConfirmationRequired: 4,
		spec.ErrNotFound:             1, spec.ErrConflict: 1, spec.ErrNotReady: 1, spec.ErrInternal: 1,
		"anything_else": 1, "": 1,
	}
	for code, want := range cases {
		if got := exitCodeFor(code); got != want {
			t.Fatalf("exitCodeFor(%q)=%d want %d", code, got, want)
		}
	}
}

func TestJSONEnvelopeSuccess(t *testing.T) {
	fs := newFakeServer(t, fakeOptions{
		handler: func(name string, req spec.Request) (any, *spec.Error) {
			return []any{map[string]any{"名称": "甲", "count": float64(3)}}, nil
		},
	})
	fs.useEndpoint(t)
	code, out, errStr := runTest(t, []string{"--json", "status"}, "")
	if code != 0 {
		t.Fatalf("code=%d err=%q", code, errStr)
	}
	// stdout 仅一个 JSON 对象（单行）。
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], `{"ok":true`) {
		t.Fatalf("stdout 应为单个 Envelope JSON: %q", out)
	}
	if errStr != "" {
		t.Fatalf("hash 一致时不应有 stderr 警告: %q", errStr)
	}
}

func TestTableHashMismatchWarning(t *testing.T) {
	fs := newFakeServer(t, fakeOptions{tableHash: "deadbeefdeadbeef"})
	fs.useEndpoint(t)
	code, _, errStr := runTest(t, []string{"status"}, "")
	if code != 0 {
		t.Fatalf("偏差仅警告不改变退出码: code=%d err=%q", code, errStr)
	}
	if !strings.Contains(errStr, "警告") || !strings.Contains(errStr, "deadbeefdeadbeef") {
		t.Fatalf("缺 tableHash 偏差警告: %q", errStr)
	}
}

func TestCommandTimeout(t *testing.T) {
	fs := newFakeServer(t, fakeOptions{delay: 2500 * time.Millisecond})
	fs.useEndpoint(t)
	// --timeout 1 覆盖默认：命令 2.5s 未返回 → 客户端超时 → 3。
	code, _, errStr := runTest(t, []string{"--timeout", "1", "status"}, "")
	if code != 3 {
		t.Fatalf("--timeout 1: code=%d err=%q", code, errStr)
	}
	// 超时提示语义见 TestCommandTimeoutHintMentionsEffect。
	// 探活固定 5s：ping 不受 delay 影响（delay 仅命令路径）。
}

// TestCommandTimeoutHintMentionsEffect — 客户端超时错误必须写明“操作可能
// 已生效，请先查询状态再重试”（评审 D-06）：服务端不强行中止 Handler，
// 不可取消的命令会在客户端超时后继续完成，直接重试有重复副作用风险。
func TestCommandTimeoutHintMentionsEffect(t *testing.T) {
	fs := newFakeServer(t, fakeOptions{delay: 2500 * time.Millisecond})
	fs.useEndpoint(t)
	code, _, errStr := runTest(t, []string{"--timeout", "1", "status"}, "")
	if code != 3 {
		t.Fatalf("--timeout 1: code=%d err=%q", code, errStr)
	}
	if !strings.Contains(errStr, "操作可能已生效") || !strings.Contains(errStr, "查询状态") {
		t.Fatalf("超时提示应写明『操作可能已生效，请先查询状态再重试』: %q", errStr)
	}
}

func TestCommandTimeoutFieldUsed(t *testing.T) {
	// usage daily 的 Timeout=30 不影响本测试；通过对照确认 --timeout 优先级：
	// delay 100ms < --timeout 5 → 成功。
	fs := newFakeServer(t, fakeOptions{delay: 100 * time.Millisecond})
	fs.useEndpoint(t)
	code, _, _ := runTest(t, []string{"--timeout", "5", "usage", "daily"}, "")
	if code != 0 {
		t.Fatalf("--timeout 5 应成功: code=%d", code)
	}
}

// ---- 端点状态 ----

func TestEndpointMissing(t *testing.T) {
	t.Setenv("AMAGI_CODEBOX_HOME", t.TempDir())
	code, _, errStr := runTest(t, []string{"status"}, "")
	if code != 3 {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(errStr, "应用未运行或命令行访问未开启（设置→命令行访问）") {
		t.Fatalf("缺标准提示: %q", errStr)
	}
	// --json 包络。
	code, out, _ := runTest(t, []string{"--json", "status"}, "")
	if code != 3 {
		t.Fatalf("--json code=%d", code)
	}
	var env spec.Envelope
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &env); err != nil || env.OK || env.Error == nil {
		t.Fatalf("--json 包络: %v %+v", err, env)
	}
}

func TestEndpointCorrupt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AMAGI_CODEBOX_HOME", home)
	if err := os.MkdirAll(home+"/run", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(home+"/run/endpoint.json", []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errStr := runTest(t, []string{"status"}, "")
	if code != 3 || !strings.Contains(errStr, "端点文件损坏") {
		t.Fatalf("code=%d err=%q", code, errStr)
	}
}

func TestEndpointStalePID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AMAGI_CODEBOX_HOME", home)
	// 起一个立刻退出的子进程取“已死 pid”。
	cmd := exec.Command("sleep", "0.05")
	if err := cmd.Start(); err != nil {
		t.Skipf("无法启动子进程: %v", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Wait()
	writeEndpointFile(t, home, "http://127.0.0.1:1", pid, "some-token")
	code, _, errStr := runTest(t, []string{"status"}, "")
	if code != 3 || !strings.Contains(errStr, "已过期") {
		t.Fatalf("code=%d err=%q", code, errStr)
	}
}

func TestEndpointNonLoopback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AMAGI_CODEBOX_HOME", home)
	writeEndpointFile(t, home, "http://example.com:8080", os.Getpid(), "some-token")
	code, _, errStr := runTest(t, []string{"status"}, "")
	if code != 3 || !strings.Contains(errStr, "回环") {
		t.Fatalf("code=%d err=%q", code, errStr)
	}
}

func TestPingUnreachable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AMAGI_CODEBOX_HOME", home)
	// 占一个端口再关掉：endpoint 指向无人监听的回环端口。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	writeEndpointFile(t, home, "http://"+addr, os.Getpid(), "some-token")
	code, _, errStr := runTest(t, []string{"status"}, "")
	if code != 3 || !strings.Contains(errStr, "探活失败") {
		t.Fatalf("code=%d err=%q", code, errStr)
	}
}

func TestPingWrongToken(t *testing.T) {
	fs := newFakeServer(t, fakeOptions{})
	home := t.TempDir()
	t.Setenv("AMAGI_CODEBOX_HOME", home)
	writeEndpointFile(t, home, fs.URL, os.Getpid(), "wrong-token")
	code, _, errStr := runTest(t, []string{"status"}, "")
	if code != 3 {
		t.Fatalf("code=%d err=%q", code, errStr)
	}
}
