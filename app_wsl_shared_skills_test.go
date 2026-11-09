package main

import (
	"strings"
	"sync"
	"testing"
	"time"

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
	face := wslSharedSkillsFace("Ubuntu")
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
	face := wslSharedSkillsFace("Ubuntu")
	if face.State != "host-missing" {
		t.Fatalf("host-missing face must be its own state, got %+v", face)
	}
	// R1-F05：其它 distro 无缓存时不串用本 distro 结果。
	if face := wslSharedSkillsFace("Debian"); face.State != "unchecked" || face.LastCheck != "" {
		t.Fatalf("other distro must stay unchecked, got %+v", face)
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


// TestWSLSharedSkillsFunnelWaitsForInFlight（R2-F09 正式时序回归）：同 distro
// 第二个首次启动（漏斗/显式检查）必须等待在途轮次真正完成并拿到真实结果，
// 不得在链接就绪前提前返回，也不得把「正在执行」当结果。
func TestWSLSharedSkillsFunnelWaitsForInFlight(t *testing.T) {
	app := newSharedSkillsTestApp(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	orig := wslSharedSkillsLinkFn
	wslSharedSkillsLinkFn = func(distro, host string) platform.WSLSharedSkillsLinkResult {
		select {
		case <-entered:
		default:
			close(entered)
		}
		<-release // 阻塞在途轮次，模拟建链尚未完成
		return platform.WSLSharedSkillsLinkResult{State: platform.WSLSkillsLinked, Target: "/mnt/c/Users/u/.agents/skills", Detail: "已建立指向 Windows 技能根的符号链接"}
	}
	defer func() { wslSharedSkillsLinkFn = orig }()
	// 清空进程内缓存，保证两路都是「首次」。
	wslSharedSkillsRootMu.Lock()
	wslSharedSkillsRoots = map[string]wslSharedSkillsRootRecord{}
	wslSharedSkillsRootMu.Unlock()

	// A：会话漏斗（首轮，进入 seam 后阻塞）。
	aDone := make(chan struct{})
	go func() { defer close(aDone); app.wslEnsureSharedAgentsSkillsFunnel("Ubuntu") }()
	<-entered // A 已进入真实供给且未完成。

	// B：同 distro 第二个首次启动（漏斗）——必须等待，不得提前返回。
	bDone := make(chan struct{})
	go func() { defer close(bDone); app.wslEnsureSharedAgentsSkillsFunnel("Ubuntu") }()
	select {
	case <-bDone:
		t.Fatal("second funnel returned before the in-flight link round finished (R2-F09 regression)")
	case <-time.After(150 * time.Millisecond):
	}
	// C：显式检查同样等待在途轮次（拿真实结果，不冒充）。
	cDone := make(chan struct{})
	var cRes WSLSharedSkillsRootStatus
	go func() { defer close(cDone); cRes = app.EnsureWSLSharedSkillsRoot() }()
	select {
	case <-cDone:
		t.Fatal("explicit ensure returned before the in-flight round finished")
	case <-time.After(150 * time.Millisecond):
	}

	close(release)
	<-aDone
	<-bDone
	<-cDone
	// 真实供给只发生一次；B/C 拿到的是本轮真实结果而非「正在执行」。
	if cRes.State != string(platform.WSLSkillsLinked) {
		t.Fatalf("explicit ensure must return the real in-flight result, got %+v", cRes)
	}
	rec, ok := lookupWSLSharedSkillsRoot("Ubuntu")
	if !ok || rec.Res.State != platform.WSLSkillsLinked {
		t.Fatalf("cache must hold the completed round result, got %+v", rec)
	}
	face := wslSharedSkillsFace("Ubuntu")
	if face.State != "synced" {
		t.Fatalf("face must reflect the completed round, got %+v", face)
	}
}
