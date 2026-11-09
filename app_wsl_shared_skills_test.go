package main

import (
	"strings"
	"sync"
	"testing"

	"amagi-codebox/internal/platform"
)

// newSharedSkillsTestApp 返回接好缝的测试 App（共享技能根供给面）。
func newSharedSkillsTestApp(t *testing.T) *App {
	t.Helper()
	app := newTestApp(t)
	origDistro := wslDefaultDistroFn
	wslDefaultDistroFn = func([]string) string { return "Ubuntu" }
	t.Cleanup(func() { wslDefaultDistroFn = origDistro })
	return app
}

// TestEnsureWSLSharedSkillsRootHappyPath 正向：显式检查返回链接终态并登记
// 缓存；状态卡片读取同一份事实（含备份路径）。
func TestEnsureWSLSharedSkillsRootHappyPath(t *testing.T) {
	app := newSharedSkillsTestApp(t)
	var mu sync.Mutex
	calls := 0
	orig := wslSharedSkillsLinkFn
	wslSharedSkillsLinkFn = func(distro, host string) platform.WSLSharedSkillsLinkResult {
		mu.Lock()
		calls++
		mu.Unlock()
		if distro != "Ubuntu" {
			t.Errorf("distro mismatch: %s", distro)
		}
		return platform.WSLSharedSkillsLinkResult{
			State: platform.WSLSkillsBackedUp,
			Target: "/mnt/c/Users/u/.agents/skills",
			Backup: "/home/u/.agents/skills.pre-shared.20260101",
			Detail: "既有目录已备份迁移",
		}
	}
	defer func() { wslSharedSkillsLinkFn = orig }()

	st := app.EnsureWSLSharedSkillsRoot()
	if !st.Checked || st.State != string(platform.WSLSkillsBackedUp) {
		t.Fatalf("ensure must report backed-up, got %+v", st)
	}
	if st.Backup == "" || st.Target != "/mnt/c/Users/u/.agents/skills" {
		t.Fatalf("backup/target must surface, got %+v", st)
	}
	face := wslSharedSkillsFace()
	if face.Key != "skills" || face.State != "synced" {
		t.Fatalf("face must be synced, got %+v", face)
	}
	if !strings.Contains(face.Detail, "符号链接") || !strings.Contains(face.Detail, "旧目录已备份") {
		t.Fatalf("face detail must mention link + backup: %+v", face)
	}
	// funnel 缓存命中：后续会话漏斗不再触达 runner。
	app.wslEnsureSharedAgentsSkillsFunnel("Ubuntu")
	mu.Lock()
	got := calls
	mu.Unlock()
	if got != 1 {
		t.Fatalf("funnel must hit cache (1 real call expected), got %d", got)
	}
}

// TestEnsureWSLSharedSkillsRootNegative 负向：宿主根不可达如实返回
// host-missing，不冒充成功；无 distro 时 unchecked。
func TestEnsureWSLSharedSkillsRootNegative(t *testing.T) {
	app := newSharedSkillsTestApp(t)
	orig := wslSharedSkillsLinkFn
	wslSharedSkillsLinkFn = func(string, string) platform.WSLSharedSkillsLinkResult {
		return platform.WSLSharedSkillsLinkResult{State: platform.WSLSkillsHostMissing, Detail: "Windows 技能根在 distro 内不可达（不存在或未挂载）"}
	}
	defer func() { wslSharedSkillsLinkFn = orig }()

	st := app.EnsureWSLSharedSkillsRoot()
	if !st.Checked || st.State != string(platform.WSLSkillsHostMissing) {
		t.Fatalf("host-missing must surface honestly, got %+v", st)
	}
	face := wslSharedSkillsFace()
	if face.State != "unchecked" {
		t.Fatalf("host-missing face must be unchecked, got %+v", face)
	}

	// 无 distro：未检查，不触达 runner。
	origDistro := wslDefaultDistroFn
	wslDefaultDistroFn = func([]string) string { return "" }
	defer func() { wslDefaultDistroFn = origDistro }()
	st = app.EnsureWSLSharedSkillsRoot()
	if st.Checked || st.State != "unchecked" {
		t.Fatalf("no-distro must stay unchecked, got %+v", st)
	}
}

