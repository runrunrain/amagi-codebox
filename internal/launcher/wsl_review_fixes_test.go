package launcher

// wsl_review_fixes_test.go — wsl-goal-closure 审核 PR02 修复的 launcher 侧
// 回归：零写入路径权限探测不可用（ok=false）不再视为安全（先收紧后复验，
// 仍不可证则 fail closed）；A2 各降级消费 fallback（mtime 不可判/回写失败）
// 统一复核 distro 副本权限，不可证 600 不得降级放行。

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// TestReviewFixPR02RetightenUnknownModeFailsClosed：权限探测不可用时零写入
// 路径不再静默放行——chmod 后复验仍不可证 → 报错（此前 !ok 直接 return nil）。
func TestReviewFixPR02RetightenUnknownModeFailsClosed(t *testing.T) {
	ft := installFakeWSLTarget(t)
	agentDir := "/home/wslu/.pi/agent"
	if err := os.MkdirAll(ft.uncPath(agentDir), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{"providers": map[string]any{"amagi-glm": map[string]any{"baseUrl": "https://x.example", "apiKey": "sk"}}}
	if _, err := WriteWSLPiAgentConfig("Ubuntu", cfg); err != nil {
		t.Fatalf("first write: %v", err)
	}
	before, err := os.ReadFile(ft.uncPath(agentDir + "/models.json"))
	if err != nil {
		t.Fatal(err)
	}
	// 探测面整体不可用（wsl.exe stat 故障形态）：零写入路径必须 fail closed。
	origStatMode := wslStatModeFn
	wslStatModeFn = func(context.Context, string, string) (string, bool) { return "", false }
	t.Cleanup(func() { wslStatModeFn = origStatMode })
	if _, err := WriteWSLPiAgentConfig("Ubuntu", cfg); err == nil {
		t.Fatal("unknown permission state must not pass the zero-write path")
	}
	after, err := os.ReadFile(ft.uncPath(agentDir + "/models.json"))
	if err != nil || string(before) != string(after) {
		t.Fatalf("identical bytes must not be rewritten, got %q err=%v", after, err)
	}
}

// TestReviewFixPR02RetightenLooseModeVerified：改宽副本被收紧并复验（探测面
// 记录到 600），零字节重写保持。
func TestReviewFixPR02RetightenLooseModeVerified(t *testing.T) {
	ft := installFakeWSLTarget(t)
	agentDir := "/home/wslu/.pi/agent"
	if err := os.MkdirAll(ft.uncPath(agentDir), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{"providers": map[string]any{"amagi-glm": map[string]any{"baseUrl": "https://x.example", "apiKey": "sk"}}}
	if _, err := WriteWSLPiAgentConfig("Ubuntu", cfg); err != nil {
		t.Fatalf("first write: %v", err)
	}
	// 副本被改宽（仅探测面形态——真实文件位由 9P 语义决定，这里钉复核链）。
	ft.modes[agentDir+"/models.json"] = "644"
	if _, err := WriteWSLPiAgentConfig("Ubuntu", cfg); err != nil {
		t.Fatalf("retighten must recover the loose copy: %v", err)
	}
	if m, ok := ft.modes[agentDir+"/models.json"]; !ok || m != "600" {
		t.Fatalf("loose copy must be re-tightened and verified, got %q ok=%v", m, ok)
	}
}

// TestReviewFixPR02A2FallbackVerifiesCopyPerms：A2 降级消费（mtime 不可判 →
// DistroHasCopy）前复核 distro 副本权限：健康 0600 → 降级放行；宽松且收紧
// 失败 → fail closed（NoAuth + errWSLAuthLooseLeftover）。
func TestReviewFixPR02A2FallbackVerifiesCopyPerms(t *testing.T) {
	t.Run("healthy 600 copy degrades", func(t *testing.T) {
		fakeWindowsAuth(t, `{"newer":{"type":"oauth"}}`)
		root, _ := fakeWSLForSync(t, nil)
		writeDistroAuth(t, root, `{"old":{"type":"oauth"}}`, time.Now().Add(-time.Hour))
		// fakeWSLForSync 默认 mtime 探测 (0,false)：内容不同 → mtime 不可判分支。
		outcome, err := SyncWSLPiLoginAuth("Ubuntu")
		if err == nil {
			t.Fatal("indeterminable mtime must report an error")
		}
		if outcome != SyncWSLPiLoginAuthDistroHasCopy {
			t.Fatalf("healthy 600 copy must degrade to DistroHasCopy, got %v (err=%v)", outcome, err)
		}
	})
	t.Run("loose copy with failing retighten fails closed", func(t *testing.T) {
		fakeWindowsAuth(t, `{"newer":{"type":"oauth"}}`)
		root, _ := fakeWSLForSync(t, errors.New("chmod dead"))
		writeDistroAuth(t, root, `{"old":{"type":"oauth"}}`, time.Now().Add(-time.Hour))
		if err := os.Chmod(distroAuthPath(root), 0o644); err != nil {
			t.Fatal(err)
		}
		outcome, err := SyncWSLPiLoginAuth("Ubuntu")
		if err == nil || !errors.Is(err, errWSLAuthLooseLeftover) {
			t.Fatalf("loose-perm copy with broken retighten must fail closed, got err=%v", err)
		}
		if outcome != SyncWSLPiLoginAuthDistroNoAuth {
			t.Fatalf("loose-perm copy must not degrade-pass, got %v", outcome)
		}
	})
	t.Run("reverse-sync failure with loose copy fails closed", func(t *testing.T) {
		fakeWindowsAuth(t, `{"older":{"type":"oauth"}}`)
		root, _ := fakeWSLForSync(t, nil)
		writeDistroAuth(t, root, `{"newer":{"type":"oauth"}}`, time.Now())
		stubDistroMTime(t, time.Now().Add(time.Hour)) // distro 新 → 回写 Windows
		if err := os.Chmod(distroAuthPath(root), 0o644); err != nil {
			t.Fatal(err)
		}
		// 使 Windows 侧回写失败（G0：宿主私有化接缝注入故障）→ 降级分支 → 副本权限复核。
		origEnsureDir := hostEnsurePrivateDirFn
		hostEnsurePrivateDirFn = func(string) error { return os.ErrPermission }
		t.Cleanup(func() { hostEnsurePrivateDirFn = origEnsureDir })
		// 探测面不认识该副本（modes 为空、回退真实位 0644）→ 收紧动作可执行
		// 且复验成功 → 允许降级（权限已修复为可证安全）。
		outcome, err := SyncWSLPiLoginAuth("Ubuntu")
		if err == nil {
			t.Fatal("reverse-sync failure must surface an error")
		}
		if outcome != SyncWSLPiLoginAuthDistroHasCopy {
			t.Fatalf("retightened copy must allow degradation, got %v (err=%v)", outcome, err)
		}
		if m, ok := wslStatModeFn(context.Background(), "Ubuntu", "/home/wslu/.pi/agent/auth.json"); !ok || m != "600" {
			t.Fatalf("loose copy must be retightened before degradation, got %q ok=%v", m, ok)
		}
	})
}

// TestReviewFixPR02A2FallbackLooseAndUnfixableFailsClosed：回写失败 + 副本
// 宽松 + 收紧不可复验 → fail closed（不降级）。
func TestReviewFixPR02A2FallbackLooseAndUnfixableFailsClosed(t *testing.T) {
	fakeWindowsAuth(t, `{"older":{"type":"oauth"}}`)
	root, _ := fakeWSLForSync(t, nil)
	writeDistroAuth(t, root, `{"newer":{"type":"oauth"}}`, time.Now())
	stubDistroMTime(t, time.Now().Add(time.Hour))
	if err := os.Chmod(distroAuthPath(root), 0o644); err != nil {
		t.Fatal(err)
	}
	// 使 Windows 侧回写失败（G0：宿主私有化接缝注入故障）。
	origEnsureDir := hostEnsurePrivateDirFn
	hostEnsurePrivateDirFn = func(string) error { return os.ErrPermission }
	t.Cleanup(func() { hostEnsurePrivateDirFn = origEnsureDir })
	// chmod 永远失败 → 收紧不可复验。
	origChmod := wslChmodFn
	wslChmodFn = func(context.Context, string, string, ...string) error { return errors.New("chmod dead") }
	t.Cleanup(func() { wslChmodFn = origChmod })
	outcome, err := SyncWSLPiLoginAuth("Ubuntu")
	if err == nil || !errors.Is(err, errWSLAuthLooseLeftover) {
		t.Fatalf("unfixable loose copy must fail closed on degradation, got err=%v", err)
	}
	if outcome != SyncWSLPiLoginAuthDistroNoAuth {
		t.Fatalf("unfixable loose copy must not degrade, got %v", outcome)
	}
	if !strings.Contains(err.Error(), "auth.json") {
		t.Fatalf("error must identify the affected secret file, got %v", err)
	}
}
