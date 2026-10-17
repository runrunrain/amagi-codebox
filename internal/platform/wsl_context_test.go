package platform

// wsl_context_test.go — WSL 探测原语 ctx 变体的语义回归（G0）：
//   - ctx 真实贯通：默认 runner 为 exec.CommandContext，已取消的 ctx 在
//     Start 前即失败（不执行任何 wsl.exe）；
//   - 入口 ctx 检查：已取消的 ctx 不触发 runner（各探测面零调用）；
//   - 解析语义与旧无 ctx 函数一致（fail-closed：输出异常 → 零值/false）。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWSLContextRunnerHonorsCanceledContext(t *testing.T) {
	// 默认生产 runner 必须是 exec.CommandContext：PATH 上放一个真实可执行
	// 的 wsl.exe 替身（LookPath 必须命中，否则 macOS 上路径错误先于 ctx 检
	// 查，无法证明取消语义），已取消 ctx 下 Start 直接返回 ctx 错误——
	// Go 运行时保证不会真正执行替身。
	fakeBin := t.TempDir()
	script := filepath.Join(fakeBin, "wsl.exe")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(filepath.ListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := wslScriptRunnerContext(ctx, "Ubuntu", "id -u")
	if err == nil {
		t.Fatal("canceled context must fail the runner")
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("err = %v, want context cancellation error", err)
	}
}

func TestWSLContextProbesCheckCtxBeforeRunner(t *testing.T) {
	// 已取消 ctx 下各探测入口必须在调用 runner 前短路（runner 计数为零）。
	orig := wslScriptRunnerContext
	runnerCalls := 0
	wslScriptRunnerContext = func(context.Context, string, string) ([]byte, error) {
		runnerCalls++
		return []byte("1000\n/home/u\nmachine\n"), nil
	}
	t.Cleanup(func() { wslScriptRunnerContext = orig })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if id := WSLTargetIdentityContext(ctx, "Ubuntu"); id.OK {
		t.Fatal("canceled ctx must yield not-OK identity")
	}
	if _, ok := wslFileMTimeContextForOS(ctx, "windows", "Ubuntu", "/home/u/x"); ok {
		t.Fatal("canceled ctx must yield mtime ok=false")
	}
	if _, ok := WSLFileModeContext(ctx, "Ubuntu", "/home/u/x"); ok {
		t.Fatal("canceled ctx must yield mode ok=false")
	}
	if err := WSLChmodContext(ctx, "Ubuntu", "600", "/home/u/x"); err == nil {
		t.Fatal("canceled ctx must fail chmod")
	}
	if runnerCalls != 0 {
		t.Fatalf("canceled ctx must not invoke the runner, calls=%d", runnerCalls)
	}
}

func TestWSLContextProbesParseLikeLegacy(t *testing.T) {
	orig := wslScriptRunnerContext
	t.Cleanup(func() { wslScriptRunnerContext = orig })
	ctx := context.Background()

	t.Run("identity parses uid/home/machine-id", func(t *testing.T) {
		wslScriptRunnerContext = func(_ context.Context, distro, script string) ([]byte, error) {
			if distro != "Ubuntu" || !strings.Contains(script, "machine-id") {
				t.Errorf("unexpected probe: distro=%q script=%q", distro, script)
			}
			return []byte("1000\n/home/wslu\nb0bb\n"), nil
		}
		id := WSLTargetIdentityContext(ctx, "Ubuntu")
		if !id.OK || id.UID != "1000" || id.Home != "/home/wslu" || id.MachineID != "b0bb" {
			t.Fatalf("identity = %+v", id)
		}
	})

	t.Run("identity fails closed on malformed output", func(t *testing.T) {
		wslScriptRunnerContext = func(context.Context, string, string) ([]byte, error) {
			return []byte("not-a-uid\nrelative\n"), nil
		}
		if id := WSLTargetIdentityContext(ctx, "Ubuntu"); id.OK {
			t.Fatalf("malformed output must not be OK: %+v", id)
		}
	})

	t.Run("mtime parses epoch seconds", func(t *testing.T) {
		wslScriptRunnerContext = func(context.Context, string, string) ([]byte, error) {
			return []byte("1792992000\r\n"), nil
		}
		sec, ok := wslFileMTimeContextForOS(ctx, "windows", "Ubuntu", "/home/u/auth.json")
		if !ok || sec != 1792992000 {
			t.Fatalf("mtime = (%d, %v)", sec, ok)
		}
	})

	t.Run("mtime non-windows host stays false without runner", func(t *testing.T) {
		wslScriptRunnerContext = func(context.Context, string, string) ([]byte, error) {
			t.Fatal("runner must not be called for non-windows host core")
			return nil, nil
		}
		if _, ok := wslFileMTimeContextForOS(ctx, "darwin", "Ubuntu", "/home/u/auth.json"); ok {
			t.Fatal("non-windows host must yield ok=false")
		}
	})

	t.Run("mode parses octal and fails on noise", func(t *testing.T) {
		wslScriptRunnerContext = func(context.Context, string, string) ([]byte, error) {
			return []byte("600\n"), nil
		}
		if m, ok := wslFileModeContextForOS(ctx, "windows", "Ubuntu", "/home/u/auth.json"); !ok || m != "600" {
			t.Fatalf("mode = (%q, %v)", m, ok)
		}
		wslScriptRunnerContext = func(context.Context, string, string) ([]byte, error) {
			return []byte("rw-r--r--"), nil
		}
		if _, ok := wslFileModeContextForOS(ctx, "windows", "Ubuntu", "/home/u/auth.json"); ok {
			t.Fatal("symbolic mode noise must fail closed")
		}
	})

	t.Run("chmod builds same script as legacy", func(t *testing.T) {
		var gotScript string
		wslScriptRunnerContext = func(_ context.Context, distro, script string) ([]byte, error) {
			if distro != "Ubuntu" {
				t.Errorf("distro = %q", distro)
			}
			gotScript = script
			return nil, nil
		}
		if err := WSLChmodContext(ctx, "Ubuntu", "600", "/home/u/a b/'quote'.json"); err != nil {
			t.Fatal(err)
		}
		if gotScript != "chmod 600 '/home/u/a b/'\\''quote'\\''.json'" {
			t.Fatalf("script = %q, want legacy-identical quoting", gotScript)
		}
	})

	t.Run("chmod rejects invalid mode without runner", func(t *testing.T) {
		runnerHit := false
		wslScriptRunnerContext = func(context.Context, string, string) ([]byte, error) {
			runnerHit = true
			return nil, nil
		}
		if err := WSLChmodContext(ctx, "Ubuntu", "600; rm -rf /", "/home/u/x"); err == nil {
			t.Fatal("invalid mode must error")
		}
		if runnerHit {
			t.Fatal("invalid mode must not reach the runner")
		}
	})

	t.Run("runner error propagates", func(t *testing.T) {
		wslScriptRunnerContext = func(context.Context, string, string) ([]byte, error) {
			return nil, context.DeadlineExceeded
		}
		ctxDeadline, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		if err := WSLChmodContext(ctxDeadline, "Ubuntu", "600", "/home/u/x"); err == nil {
			t.Fatal("runner error must propagate")
		}
	})
}
