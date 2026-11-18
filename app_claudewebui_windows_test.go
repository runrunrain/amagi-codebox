package main

import (
	"path/filepath"
	"testing"

	"amagi-codebox/internal/appmeta/claude"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/webui"
)

// TestSessionJSONLPathInRoot_WSLUNCRootAndLinuxWorkDir：UNC 转录根 + Linux
// workDir 的编码落位——tailer 侧 resolvePath 直接拼接（使用 Windows 宿主的 UNC 语义，不访问 WSL）。
func TestSessionJSONLPathInRoot_WSLUNCRootAndLinuxWorkDir(t *testing.T) {
	root, err := platform.WSLToUNCPath("Ubuntu-24.04", "/home/amagi/.claude")
	if err != nil {
		t.Fatalf("WSLToUNCPath: %v", err)
	}
	got := claude.SessionJSONLPathInRoot(root, "/mnt/d/WorkPace/amagi-codebox", "1bdc84ba-0000")
	want := `\\wsl.localhost\Ubuntu-24.04\home\amagi\.claude\projects\-mnt-d-WorkPace-amagi-codebox\1bdc84ba-0000.jsonl`
	if got != want || !filepath.IsAbs(got) {
		t.Fatalf("jsonl path = %q, want %q", got, want)
	}
}

// TestStartClaudeWebPlane_WSLSpec_UsesCrossBoundaryRoot：WSL 目标的会话走跨界
// 转录根解析（2026-11 扩展）：PTY 实际目标经 claudePlaneWSLTargetFn 接缝注入；
// CLAUDE_CONFIG_DIR 为宿主原生路径（Windows 盘符）时原样透传——fixture 可直读，
// 平面起且 tracker 注册。Windows 临时目录是盘符路径，不访问真实 WSL。
func TestStartClaudeWebPlane_WSLSpec_UsesCrossBoundaryRoot(t *testing.T) {
	app := newTestApp(t)
	webuiSvc, _ := wireClaudeWebPlane(t, app)

	origTarget := claudePlaneWSLTargetFn
	claudePlaneWSLTargetFn = func(*App, string) (bool, string, error) {
		return true, "Ubuntu-24.04", nil
	}
	t.Cleanup(func() { claudePlaneWSLTargetFn = origTarget })

	workDir := "/mnt/d/WorkPace/amagi-codebox"
	configDirRoot := t.TempDir()
	const claudeSID = "claude-sid-wsl-0001"
	const sessID = "sess-plane-wsl"
	const fixtureText = "wsl cross-boundary root record"
	writeClaudePlaneFixture(t, configDirRoot, workDir, claudeSID, fixtureText)

	port, err := webui.AllocateFreePort()
	if err != nil {
		t.Fatalf("allocate port: %v", err)
	}
	spec := platform.ResolvedLaunchSpec{
		BootstrapMode: platform.BootstrapWSL,
		WorkDir:       workDir,
		Env:           platform.ResolvedEnv{Variables: []string{"CLAUDE_CONFIG_DIR=" + configDirRoot}},
	}
	app.startClaudeWebPlane(sessID, claudeSID, spec, 222222, port, "test-plane-token-wsl")

	// 宿主无关断言：平面已启动并注册（tracker probing → available）。
	st := webuiSvc.GetWebUIStatus(sessID)
	if st.State != webui.StateProbing && st.State != webui.StateAvailable {
		t.Fatalf("WSL 会话平面应已注册，got state=%s", st.State)
	}
	claudePlaneWaitFor(t, "history 读到跨界根下 fixture", func() bool {
		var h claudePlaneHistory
		if err := claudePlaneGet(t, port, "test-plane-token-wsl", "/api/history", &h); err != nil {
			return false
		}
		return len(h.Items) == 1 && h.Items[0].Text == fixtureText
	})
	app.stopClaudeWebPlane(sessID)
}
