package session

// authority_launchcontext_test.go — LaunchContextByID 权威启动上下文投影的
// 单测（authority.go）：pending 隐藏、提交后 mode/workdir/ShellRef 可读、
// ShellRef 缺省为空、未知 ID 报 ErrAuthorityNotFound。

import (
	"errors"
	"testing"
	"time"

	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/processcap"
	"amagi-codebox/internal/remote/contract"
)

func TestLaunchContextByID(t *testing.T) {
	manager := NewManager()
	reservation := reserveEmbedded(t, manager, "ctx-probe")

	// pending（未激活）不对外暴露。
	if _, err := manager.LaunchContextByID("ctx-probe"); !errors.Is(err, ErrAuthorityNotFound) {
		t.Fatalf("pending entry must be hidden, got %v", err)
	}

	binding := processcap.BindingID{Kind: processcap.BackendPTY, Owner: 11, Generation: 7}
	token, err := manager.PrepareActivation(reservation, PreparedAuthorityActivation{
		Recipe: launchplan.StableRecipe{
			CLIType:  contract.CLITypePi,
			Workdir:  "/home/u/repo",
			ShellRef: `C:\Windows\System32\wsl.exe`,
		},
		BindingID: binding, PID: 4242, RunRevision: 7,
		StartedAt: time.Unix(200, 0), LastActivityAt: time.Unix(200, 0),
	})
	if err != nil {
		t.Fatalf("PrepareActivation: %v", err)
	}
	if _, err := manager.CommitPreparedActivation(token, func() {}); err != nil {
		t.Fatalf("CommitPreparedActivation: %v", err)
	}

	ctx, err := manager.LaunchContextByID("ctx-probe")
	if err != nil {
		t.Fatalf("LaunchContextByID: %v", err)
	}
	// CLIType 取权威会话记录的 AppType（CreateSpec 值），非 recipe 副本。
	if ctx.SessionID != "ctx-probe" || ctx.CLIType != contract.CLITypeClaudeCode {
		t.Fatalf("identity = %q/%v", ctx.SessionID, ctx.CLIType)
	}
	if ctx.Mode != launchplan.ModeEmbedded {
		t.Fatalf("mode = %v, want embedded", ctx.Mode)
	}
	// 权威 workdir 是 authority 会话记录（CreateSpec）值；recipe.Workdir 是
	// 启动配方内副本，投影不暴露（消费方只需目标事实，不需要配方全量）。
	if ctx.Workdir != "/work" {
		t.Fatalf("workdir = %q", ctx.Workdir)
	}
	if ctx.ShellRef != `C:\Windows\System32\wsl.exe` {
		t.Fatalf("shellRef = %q", ctx.ShellRef)
	}
	if ctx.State != AuthorityRunning {
		t.Fatalf("state = %v", ctx.State)
	}
}

func TestLaunchContextByIDShellRefDefaultsEmpty(t *testing.T) {
	manager := NewManager()
	reservation := reserveEmbedded(t, manager, "ctx-noshell")
	activateEmbedded(t, manager, reservation, 3) // 该助手提交的 recipe 无 ShellRef
	ctx, err := manager.LaunchContextByID("ctx-noshell")
	if err != nil {
		t.Fatalf("LaunchContextByID: %v", err)
	}
	if ctx.ShellRef != "" {
		t.Fatalf("shellRef = %q, want empty", ctx.ShellRef)
	}
}

func TestLaunchContextByIDUnknown(t *testing.T) {
	manager := NewManager()
	if _, err := manager.LaunchContextByID("nope"); !errors.Is(err, ErrAuthorityNotFound) {
		t.Fatalf("unknown id must be ErrAuthorityNotFound, got %v", err)
	}
}
