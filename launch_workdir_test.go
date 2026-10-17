package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// launch_workdir_test.go 覆盖启动工作目录校验/回退链（launch_workdir.go）。
// Windows ConPTY 报 "The directory name is invalid"（ERROR_DIRECTORY 267）的
// 防御逻辑无法在 macOS 复现，故全部以代码层表驱动用例覆盖。

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustWriteFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("plain file"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustWd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return wd
}

type recordedFallback struct {
	requested string
	fallback  string
	reason    string
}

func TestResolveLaunchWorkDirChain(t *testing.T) {
	base := t.TempDir()
	defaultDir := filepath.Join(base, "default")
	mustMkdir(t, defaultDir)
	homeDir := filepath.Join(base, "home")
	mustMkdir(t, homeDir)
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	existingDir := t.TempDir()
	nested := filepath.Join(existingDir, "proj")
	mustMkdir(t, nested)
	filePath := filepath.Join(base, "plain-file")
	mustWriteFile(t, filePath)
	relToExisting, err := filepath.Rel(mustWd(t), existingDir)
	if err != nil {
		t.Fatalf("rel: %v", err)
	}

	tests := []struct {
		name          string
		requested     string
		defaultPath   string
		want          string
		wantFallbacks int
	}{
		{"absolute existing directory is kept", existingDir, "", existingDir, 0},
		{"relative path resolves against process cwd", relToExisting, "", filepath.Clean(existingDir), 0},
		{"nonexistent path falls back to default", filepath.Join(base, "missing"), defaultDir, defaultDir, 1},
		{"file instead of directory falls back", filePath, defaultDir, defaultDir, 1},
		{"quoted path falls back", `"` + existingDir + `"`, defaultDir, defaultDir, 1},
		{"trailing space falls back", nested + " ", defaultDir, defaultDir, 1},
		{"invalid requested and invalid default fall back to home", filepath.Join(base, "missing"), filepath.Join(base, "missing-default"), homeDir, 2},
		{"empty requested uses default", "", defaultDir, defaultDir, 0},
		{"empty requested and empty default use home", "", "", homeDir, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fallbacks []recordedFallback
			got, err := resolveLaunchWorkDirChain(tt.requested, tt.defaultPath, func(requested, fallback, reason string) {
				fallbacks = append(fallbacks, recordedFallback{requested: requested, fallback: fallback, reason: reason})
			})
			if err != nil {
				t.Fatalf("resolveLaunchWorkDirChain(%q, %q) error: %v", tt.requested, tt.defaultPath, err)
			}
			if got != tt.want {
				t.Fatalf("resolved = %q, want %q", got, tt.want)
			}
			if len(fallbacks) != tt.wantFallbacks {
				t.Fatalf("fallback callbacks = %d (%v), want %d", len(fallbacks), fallbacks, tt.wantFallbacks)
			}
			for _, fb := range fallbacks {
				if fb.fallback != got {
					t.Fatalf("fallback target %q != resolved %q", fb.fallback, got)
				}
				if fb.reason == "" {
					t.Fatalf("fallback for %q has empty reason", fb.requested)
				}
			}
		})
	}
}

func TestResolveLaunchWorkDirChain_FileFallbackReasonMentionsNotADirectory(t *testing.T) {
	base := t.TempDir()
	defaultDir := filepath.Join(base, "default")
	mustMkdir(t, defaultDir)
	filePath := filepath.Join(base, "some-file")
	mustWriteFile(t, filePath)

	var reasons []string
	got, err := resolveLaunchWorkDirChain(filePath, defaultDir, func(requested, fallback, reason string) {
		reasons = append(reasons, reason)
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got != defaultDir {
		t.Fatalf("resolved = %q, want %q", got, defaultDir)
	}
	if len(reasons) != 1 || !strings.Contains(reasons[0], "not a directory") {
		t.Fatalf("reasons = %v, want one entry containing \"not a directory\"", reasons)
	}
}

func TestResolveLaunchWorkDirChain_AllInvalidReturnsError(t *testing.T) {
	base := t.TempDir()
	missingHome := filepath.Join(base, "missing-home")
	t.Setenv("HOME", missingHome)
	t.Setenv("USERPROFILE", missingHome)

	requested := filepath.Join(base, "gone")
	_, err := resolveLaunchWorkDirChain(requested, filepath.Join(base, "gone-default"), nil)
	if err == nil {
		t.Fatal("expected error when every candidate is unusable")
	}
	for _, want := range []string{"no usable working directory", requested} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not contain %q", err.Error(), want)
		}
	}
}

func TestResolveLaunchWorkDirChain_NoCandidatesReturnsError(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")

	_, err := resolveLaunchWorkDirChain("", "", nil)
	if err == nil {
		t.Fatal("expected error when no candidate is provided")
	}
	if !strings.Contains(err.Error(), "all empty") {
		t.Fatalf("error %q should mention that all candidates are empty", err.Error())
	}
}

func TestAppResolveLaunchWorkDirFallsBackToDefaultPath(t *testing.T) {
	isolatedHome(t)
	app, _ := newTestAppWithConfigDir(t)
	if err := app.Paths.Load(); err != nil {
		t.Fatalf("load paths: %v", err)
	}
	def := t.TempDir()
	if err := app.Paths.SetDefaultPath(def); err != nil {
		t.Fatalf("set default path: %v", err)
	}

	got, err := app.resolveLaunchWorkDir(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatalf("resolveLaunchWorkDir error: %v", err)
	}
	if want := filepath.Clean(def); got != want {
		t.Fatalf("resolved = %q, want %q", got, want)
	}
}

// TestResolveLaunchWorkDirForSpec：解析后定谳（纯目标判据 bootstrapWSL，
// 零探测）。isLinuxStyleWorkDirFn 注入镜像 platform wsl_form_windows.go 语义
// 的假实现（生产默认在非 Windows 宿主恒 false，无法在本机驱动该分支）。
func TestResolveLaunchWorkDirForSpec(t *testing.T) {
	base := t.TempDir()
	defaultDir := filepath.Join(base, "default")
	mustMkdir(t, defaultDir)
	winProj := filepath.Join(base, `C:\工程 空格`)
	mustMkdir(t, winProj)

	origPath := isLinuxStyleWorkDirFn
	isLinuxStyleWorkDirFn = func(p string) bool {
		w := strings.TrimSpace(p)
		return strings.HasPrefix(w, "/") || w == "~" || strings.HasPrefix(w, "~/")
	}
	t.Cleanup(func() { isLinuxStyleWorkDirFn = origPath })

	noPreserve := func(string) {}

	// WSL 目标 + 显式 Linux 形态：原样保留（TrimSpace），不触发宿主校验。
	for in, want := range map[string]string{
		"/home/u/我的 工程": "/home/u/我的 工程",
		"~":             "~",
		"~/proj":        "~/proj",
		"  /home/u/x  ": "/home/u/x",
	} {
		got, err := resolveLaunchWorkDirForSpec(true, in, defaultDir, "", noPreserve)
		if err != nil || got != want {
			t.Fatalf("resolveLaunchWorkDirForSpec(true, %q) = (%q, %v), want (%q, nil)", in, got, err, want)
		}
	}
	var preserved []string
	if _, err := resolveLaunchWorkDirForSpec(true, "~/proj", defaultDir, "", func(p string) { preserved = append(preserved, p) }); err != nil || len(preserved) != 1 || preserved[0] != "~/proj" {
		t.Fatalf("preserve callback = %v, err = %v", preserved, err)
	}

	// WSL 目标 + 显式 Windows 形态：存在→Clean+Abs 保留；不存在→拒绝启动
	//（不静默换工程）。
	if got, err := resolveLaunchWorkDirForSpec(true, winProj, defaultDir, "", noPreserve); err != nil || got != winProj {
		t.Fatalf("explicit valid windows form: got (%q, %v), want (%q, nil)", got, err, winProj)
	}
	if _, err := resolveLaunchWorkDirForSpec(true, `C:\missing\目录`, defaultDir, "", noPreserve); err == nil || !strings.Contains(err.Error(), "拒绝启动") {
		t.Fatalf("explicit invalid windows form must fail the launch, got %v", err)
	}

	// WSL 目标 + 空请求：默认目录为 Linux 形态时同样保留，否则维持宿主链值。
	if got, err := resolveLaunchWorkDirForSpec(true, "", defaultDir, "~/默认 工程", noPreserve); err != nil || got != "~/默认 工程" {
		t.Fatalf("linux-form default must be preserved, got (%q, %v)", got, err)
	}
	if got, err := resolveLaunchWorkDirForSpec(true, "", defaultDir, defaultDir, noPreserve); err != nil || got != defaultDir {
		t.Fatalf("windows-form default keeps host chain value, got (%q, %v)", got, err)
	}

	// 非 WSL 目标：一律返回宿主链产物（原生回退语义不变；即便请求是
	// Linux 形态/无效路径也不在上层拦截）。
	for _, raw := range []string{"/home/u/proj", `C:\missing`, ""} {
		if got, err := resolveLaunchWorkDirForSpec(false, raw, defaultDir, "~/默认 工程", noPreserve); err != nil || got != defaultDir {
			t.Fatalf("native target raw=%q: got (%q, %v), want host resolved", raw, got, err)
		}
	}
}
