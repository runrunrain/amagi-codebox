package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/agentcli/spec"
)

// gitAvailable 跳过无 git 的环境（CI 两个平台均有 git，本地兜底）。
func gitAvailable(t *testing.T) bool {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 不在 PATH，跳过 git 域集成断言")
		return false
	}
	return true
}

func runTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	args = append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func newTestGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runTestGit(t, dir, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, dir, "add", ".")
	runTestGit(t, dir, "commit", "-m", "init")
	return dir
}

func TestAgentCLIGitRepoInfo(t *testing.T) {
	if !gitAvailable(t) {
		return
	}
	h, _, _ := newAgentCLIHostFixture(t)
	ctx := context.Background()

	repo := newTestGitRepo(t)
	data, err := h.handleGitRepoInfo(ctx, spec.Args{"dir": repo})
	if err != nil {
		t.Fatalf("repo-info: %v", err)
	}
	out := mustJSON(t, data)
	for _, want := range []string{`"isGitRepo":true`, `"branch":"main"`, `"workDir"`} {
		if !strings.Contains(out, want) {
			t.Errorf("repo-info 缺少 %s: %s", want, out)
		}
	}
	// 未跟踪文件计数。
	if err := os.WriteFile(filepath.Join(repo, "b.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	data2, err := h.handleGitRepoInfo(ctx, spec.Args{"dir": repo})
	if err != nil {
		t.Fatalf("repo-info 2: %v", err)
	}
	view := data2.(agentCLIRepoInfoView)
	if view.Untracked != 1 {
		t.Errorf("untracked = %d, want 1", view.Untracked)
	}

	// 非 git 目录：不报错，返回 isGitRepo=false。
	plain := t.TempDir()
	data3, err := h.handleGitRepoInfo(ctx, spec.Args{"dir": plain})
	if err != nil {
		t.Fatalf("repo-info non-git: %v", err)
	}
	if data3.(agentCLIRepoInfoView).IsGitRepo {
		t.Errorf("非 git 目录 isGitRepo 应为 false")
	}

	// 缺参 → bad_args。
	if _, err := h.handleGitRepoInfo(ctx, spec.Args{}); specErrCode(t, err) != spec.ErrBadArgs {
		t.Errorf("缺 dir 应 bad_args: %v", err)
	}
}

func TestAgentCLIGitBranches(t *testing.T) {
	if !gitAvailable(t) {
		return
	}
	h, _, _ := newAgentCLIHostFixture(t)
	ctx := context.Background()

	repo := newTestGitRepo(t)
	runTestGit(t, repo, "branch", "feature-x")

	data, err := h.handleGitBranches(ctx, spec.Args{"dir": repo})
	if err != nil {
		t.Fatalf("branches: %v", err)
	}
	m := data.(map[string]any)
	branchesJSON := mustJSON(t, m["branches"])
	if !strings.Contains(branchesJSON, `"name":"main"`) || !strings.Contains(branchesJSON, `"name":"feature-x"`) {
		t.Errorf("branches 缺分支: %s", branchesJSON)
	}
	if m["count"] != 2 {
		t.Errorf("count = %v", m["count"])
	}

	// 非 git 目录 → not_found。
	if _, err := h.handleGitBranches(ctx, spec.Args{"dir": t.TempDir()}); specErrCode(t, err) != spec.ErrNotFound {
		t.Errorf("非 git 目录 branches 应 not_found: %v", err)
	}
	if _, err := h.handleGitBranches(ctx, spec.Args{}); specErrCode(t, err) != spec.ErrBadArgs {
		t.Errorf("缺 dir 应 bad_args: %v", err)
	}
}

// TestAgentCLIGitHandlersCanceledContext D-06 回归：git 域 Handler 入口检查
// ctx 取消（gitassist 无 ctx 接口，入口检查是 CLI 侧取消边界）。修复前对已
// 取消 ctx 仍真实执行 git 并返回成功。
func TestAgentCLIGitHandlersCanceledContext(t *testing.T) {
	if !gitAvailable(t) {
		return
	}
	h, _, _ := newAgentCLIHostFixture(t)
	repo := newTestGitRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.handleGitBranches(ctx, spec.Args{"dir": repo}); specErrCode(t, err) != spec.ErrNotReady {
		t.Errorf("已取消 ctx 的 git branches 应 not_ready，实际：%v", err)
	}
	if _, err := h.handleGitRepoInfo(ctx, spec.Args{"dir": repo}); specErrCode(t, err) != spec.ErrNotReady {
		t.Errorf("已取消 ctx 的 git repo-info 应 not_ready，实际：%v", err)
	}
}
