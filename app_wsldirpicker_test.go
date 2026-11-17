package main

// app_wsldirpicker_test.go — SessionListDirectories facade 单测
// （app_wsldirpicker.go）：会话权威上下文判定（WSL → distro 列表 / 非 WSL →
// 原生委托）、Windows 盘符 root 的实测转换与失败语义、空 root 透传
// （distro $HOME 由后端展开）。WSL 探测经包级接缝注入假实现；原生委托用
// 真实临时目录验证与 PathsService.ListDirectories 同路。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/paths"
	"amagi-codebox/internal/processcap"
	"amagi-codebox/internal/session"
)

// mirrorWindowsIsLinuxStyle 镜像 platform.IsLinuxStylePath 的 Windows 语义，
// 供非 Windows 宿主测试驱动同一条纯逻辑。
func mirrorWindowsIsLinuxStyle(p string) bool {
	w := strings.TrimSpace(p)
	return strings.HasPrefix(w, "/") || strings.HasPrefix(w, "~/") || w == "~"
}

type pickerSeams struct {
	listCalled   []string // 每次 "distro|root"
	listResponse string
	listErr      error
}

func injectPickerSeams(t *testing.T, wsl bool, distro string, translate map[string]string) *pickerSeams {
	t.Helper()
	seams := &pickerSeams{}
	prevTarget, prevStyle, prevTranslate, prevList := sessionWSLTargetFn, isLinuxStylePathFn, wslTranslateWindowsRootFn, listWSLDirectoriesFn
	sessionWSLTargetFn = func(_ *App, _ string, _ launchplan.Mode) (bool, string, error) { return wsl, distro, nil }
	isLinuxStylePathFn = mirrorWindowsIsLinuxStyle
	wslTranslateWindowsRootFn = func(d string, windowsPaths ...string) map[string]string { return translate }
	listWSLDirectoriesFn = func(svc *paths.PathsService, d, root string) (string, error) {
		seams.listCalled = append(seams.listCalled, d+"|"+root)
		if seams.listErr != nil {
			return "", seams.listErr
		}
		if seams.listResponse != "" {
			return seams.listResponse, nil
		}
		return fmt.Sprintf(`{"root":%q,"parent":null,"dirs":[],"truncated":false}`, root), nil
	}
	t.Cleanup(func() {
		sessionWSLTargetFn, isLinuxStylePathFn, wslTranslateWindowsRootFn, listWSLDirectoriesFn = prevTarget, prevStyle, prevTranslate, prevList
	})
	return seams
}

// activatePickerSession 在 manager 里建一个已激活的权威会话并返回其 ID。
func activatePickerSession(t *testing.T, manager *session.Manager, id string, mode launchplan.Mode, shellRef string) {
	t.Helper()
	reservation, err := manager.ReserveCreate(session.CreateSpec{
		RequestedID: id, AppType: session.AppTypePi, Origin: launchplan.OriginDesktop,
		Mode: mode, Workdir: "/home/u/repo",
	})
	if err != nil {
		t.Fatalf("ReserveCreate: %v", err)
	}
	binding := processcap.BindingID{Kind: processcap.BackendPTY, Owner: 12, Generation: 1}
	token, err := manager.PrepareActivation(reservation, session.PreparedAuthorityActivation{
		Recipe:    launchplan.StableRecipe{Workdir: "/home/u/repo", ShellRef: shellRef},
		BindingID: binding, PID: 1000, RunRevision: 1,
		StartedAt: time.Unix(300, 0), LastActivityAt: time.Unix(300, 0),
	})
	if err != nil {
		t.Fatalf("PrepareActivation: %v", err)
	}
	if _, err := manager.CommitPreparedActivation(token, func() {}); err != nil {
		t.Fatalf("CommitPreparedActivation: %v", err)
	}
}

func TestSessionListDirectoriesNativeDelegatesUnchanged(t *testing.T) {
	injectPickerSeams(t, false, "", nil)
	base := t.TempDir()
	for _, name := range []string{"alpha", "Beta"} {
		if err := os.MkdirAll(filepath.Join(base, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	app := &App{Sessions: session.NewManager(), Paths: paths.NewPathsService(t.TempDir())}
	activatePickerSession(t, app.Sessions, "native-1", launchplan.ModeExternal, "")

	raw, err := app.SessionListDirectories("native-1", base)
	if err != nil {
		t.Fatalf("SessionListDirectories: %v", err)
	}
	var payload struct {
		Root string                  `json:"root"`
		Dirs []struct{ Name string } `json:"dirs"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if payload.Root != base || len(payload.Dirs) != 2 || payload.Dirs[0].Name != "alpha" {
		t.Fatalf("native delegation payload mismatch: %s", raw)
	}
}

func TestSessionListDirectoriesWSLFlow(t *testing.T) {
	translate := map[string]string{`c:\work\proj`: "/mnt/c/Work/proj"}
	seams := injectPickerSeams(t, true, "Ubuntu", translate)
	app := &App{Sessions: session.NewManager(), Paths: paths.NewPathsService(t.TempDir())}
	activatePickerSession(t, app.Sessions, "wsl-1", launchplan.ModeEmbedded, `C:\Windows\System32\wsl.exe`)

	// Windows 盘符 root：经实测转换后进入 distro 列表。
	if _, err := app.SessionListDirectories("wsl-1", `C:\Work\proj`); err != nil {
		t.Fatalf("windows root: %v", err)
	}
	// Linux 形态 / 空 root：原样透传（$HOME 展开在后端 ListWSLDirectories）。
	for _, root := range []string{"", "/home/u", "~/proj"} {
		if _, err := app.SessionListDirectories("wsl-1", root); err != nil {
			t.Fatalf("root %q: %v", root, err)
		}
	}
	want := []string{"Ubuntu|/mnt/c/Work/proj", "Ubuntu|", "Ubuntu|/home/u", "Ubuntu|~/proj"}
	if strings.Join(seams.listCalled, ",") != strings.Join(want, ",") {
		t.Fatalf("distro list calls = %#v, want %#v", seams.listCalled, want)
	}
}

func TestSessionListDirectoriesWSLTranslateMissIsExplicit(t *testing.T) {
	injectPickerSeams(t, true, "Ubuntu", map[string]string{}) // 转换无命中
	app := &App{Sessions: session.NewManager(), Paths: paths.NewPathsService(t.TempDir())}
	activatePickerSession(t, app.Sessions, "wsl-2", launchplan.ModeEmbedded, "wsl.exe")

	_, err := app.SessionListDirectories("wsl-2", `X:\Gone\Dir`)
	if err == nil {
		t.Fatal("unmappable windows root must fail explicitly")
	}
	if !strings.Contains(err.Error(), "从主目录开始") || !strings.Contains(err.Error(), "X:\\Gone\\Dir") {
		t.Fatalf("error must guide back to distro home with the offending path, got: %v", err)
	}
}

func TestSessionListDirectoriesWSLBadFormRejected(t *testing.T) {
	seams := injectPickerSeams(t, true, "Ubuntu", nil)
	app := &App{Sessions: session.NewManager(), Paths: paths.NewPathsService(t.TempDir())}
	activatePickerSession(t, app.Sessions, "wsl-3", launchplan.ModeEmbedded, "wsl.exe")

	for _, root := range []string{"relative/path"} {
		_, err := app.SessionListDirectories("wsl-3", root)
		if err == nil || !strings.Contains(err.Error(), "Linux 路径") {
			t.Fatalf("root %q must be rejected with form guidance, got: %v", root, err)
		}
	}
	if len(seams.listCalled) != 0 {
		t.Fatalf("rejected roots must not reach distro listing: %#v", seams.listCalled)
	}
}

func TestSessionListDirectoriesWSLNoDistro(t *testing.T) {
	injectPickerSeams(t, true, "", nil)
	app := &App{Sessions: session.NewManager(), Paths: paths.NewPathsService(t.TempDir())}
	activatePickerSession(t, app.Sessions, "wsl-4", launchplan.ModeEmbedded, "wsl.exe")

	_, err := app.SessionListDirectories("wsl-4", "/home/u")
	if err == nil || !strings.Contains(err.Error(), "未找到可用发行版") {
		t.Fatalf("missing distro must be explicit, got: %v", err)
	}
}

func TestSessionListDirectoriesUnknownSessionAndArgs(t *testing.T) {
	injectPickerSeams(t, true, "Ubuntu", nil)
	app := &App{Sessions: session.NewManager(), Paths: paths.NewPathsService(t.TempDir())}

	if _, err := app.SessionListDirectories("ghost", "/home/u"); err == nil || !strings.Contains(err.Error(), "不存在或已移除") {
		t.Fatalf("unknown session must be explicit, got: %v", err)
	}
	if _, err := app.SessionListDirectories("  ", "/home/u"); err == nil {
		t.Fatal("empty session id must fail")
	}
	if _, err := (&App{}).SessionListDirectories("x", ""); err == nil {
		t.Fatal("nil services must fail")
	}
}

func TestWslPickerRootNormalizeKey(t *testing.T) {
	if got := normalizeWindowsRootKey(`C:/Work/proj\`); got != `c:\work\proj` {
		t.Fatalf("normalizeWindowsRootKey = %q", got)
	}
}

func TestSessionListDirectoriesMissingRuntimeFailsClosed(t *testing.T) {
	app := &App{Sessions: session.NewManager(), Paths: paths.NewPathsService(t.TempDir())}
	activatePickerSession(t, app.Sessions, "missing-pty", launchplan.ModeEmbedded, "wsl")
	if _, err := app.SessionListDirectories("missing-pty", ""); err == nil || !strings.Contains(err.Error(), sessionDirectoryErrorMarker) {
		t.Fatalf("missing runtime must not fall back to a native tree: %v", err)
	}
}

func TestSessionListDirectoriesActualTargetOverridesRequestedShell(t *testing.T) {
	injectPickerSeams(t, false, "", nil)
	base := t.TempDir()
	app := &App{Sessions: session.NewManager(), Paths: paths.NewPathsService(t.TempDir())}
	activatePickerSession(t, app.Sessions, "resolved-native", launchplan.ModeEmbedded, "wsl")
	if _, err := app.SessionListDirectories("resolved-native", base); err != nil {
		t.Fatalf("must use captured native process, not the requested wsl recipe: %v", err)
	}
}

func TestWslPickerRootPreservesLinuxTrailingSpaces(t *testing.T) {
	seams := injectPickerSeams(t, true, "Captured", nil)
	app := &App{Sessions: session.NewManager(), Paths: paths.NewPathsService(t.TempDir())}
	activatePickerSession(t, app.Sessions, "space-root", launchplan.ModeEmbedded, "wsl")
	if _, err := app.SessionListDirectories("space-root", "/home/u/project "); err != nil {
		t.Fatal(err)
	}
	if len(seams.listCalled) != 1 || seams.listCalled[0] != "Captured|/home/u/project " {
		t.Fatalf("root altered: %#v", seams.listCalled)
	}
}

func TestWslPickerUNCAndNUL(t *testing.T) {
	injectPickerSeams(t, true, "Captured", map[string]string{`\\wsl$\captured\home\u`: "/home/u"})
	if got, err := wslPickerRoot("Captured", `\\wsl$\Captured\home\u`); err != nil || got != "/home/u" {
		t.Fatalf("UNC resolution=%q err=%v", got, err)
	}
	if _, err := wslPickerRoot("Captured", "/home/u\x00fake"); err == nil {
		t.Fatal("NUL path accepted")
	}
}
