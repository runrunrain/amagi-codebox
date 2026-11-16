package pty

import (
	"context"
	"errors"
	"strings"
	"testing"

	"amagi-codebox/internal/platform"
)

func TestWSLStartupUsesTheExactResolvedDirectory(t *testing.T) {
	previous := resolveWSLStartupDirectory
	t.Cleanup(func() { resolveWSLStartupDirectory = previous })
	resolved := "/home/custom home "
	resolveWSLStartupDirectory = func(_ context.Context, distro, root string) (string, error) {
		if distro != "Captured" || root != "" {
			t.Fatalf("wrong resolve input: %q %q", distro, root)
		}
		return resolved, nil
	}
	spec, err := prepareWSLStartupSpec(platform.ResolvedLaunchSpec{BootstrapMode: platform.BootstrapWSL}, "Captured")
	if err != nil || spec.WorkDir != resolved {
		t.Fatalf("prepared dir=%q err=%v", spec.WorkDir, err)
	}
	command := buildWSLCommandLineForDistro(spec, nil, "Captured")
	if !strings.Contains(command, "--cd "+windowsQuoteArg(resolved)) {
		t.Fatalf("validated/executed directory diverged: %s", command)
	}
	command = buildWSLCommandLineForDistro(platform.ResolvedLaunchSpec{}, nil, "Captured")
	if !strings.Contains(command, "--cd "+windowsQuoteArg("~")) {
		t.Fatalf("empty directory inherited an unvalidated cwd: %s", command)
	}
	resolveWSLStartupDirectory = func(context.Context, string, string) (string, error) { return "", errors.New("unavailable") }
	if _, err := prepareWSLStartupSpec(platform.ResolvedLaunchSpec{WorkDir: "/Users/stale"}, "Captured"); err == nil || !strings.Contains(err.Error(), "不回退换目录") {
		t.Fatalf("invalid directory error=%v", err)
	}
}

func TestSessionWSLTargetUsesCapturedProcess(t *testing.T) {
	service := &Service{sessions: map[string]*PtySession{
		"wsl":            {bootstrapMode: platform.BootstrapWSL, wslDistro: "Ubuntu-recorded"},
		"native":         {bootstrapMode: platform.BootstrapShellInline},
		"unknown-target": {bootstrapMode: platform.BootstrapWSL},
	}}
	wsl, distro, err := service.SessionWSLTarget("wsl")
	if err != nil || !wsl || distro != "Ubuntu-recorded" {
		t.Fatalf("target=%v,%q err=%v", wsl, distro, err)
	}
	wsl, distro, err = service.SessionWSLTarget("native")
	if err != nil || wsl || distro != "" {
		t.Fatalf("native target=%v,%q err=%v", wsl, distro, err)
	}
	for _, id := range []string{"missing", "unknown-target"} {
		if _, _, err := service.SessionWSLTarget(id); err == nil {
			t.Fatalf("unverifiable target accepted: %s", id)
		}
	}
}

func TestWSLRuntimeCommandUsesCapturedTargetAndExec(t *testing.T) {
	for _, tc := range []struct {
		appType, startup string
		wantRuntime      bool
	}{
		{"pi", "", true},
		{"pi", "env -u OPENAI_API_KEY 'pi' '--model' 'login'; exec bash -li", true},
		{"codex", "", false},
	} {
		spec := platform.ResolvedLaunchSpec{
			AppType: tc.appType, BootstrapMode: platform.BootstrapWSL,
			WorkDir: "/home/user/project", StartupCommand: tc.startup,
			CLI: platform.ResolvedCLI{Path: tc.appType, Args: []string{"--model", "literal $HOME"}},
		}
		command := buildWSLCommandLineForDistro(spec, nil, "Captured-Distro")
		if !strings.HasPrefix(command, "wsl.exe -d Captured-Distro ") || !strings.Contains(command, " --exec bash -lic ") {
			t.Fatalf("target/exec boundary lost: %s", command)
		}
		count := strings.Count(command, "AMAGI_DATA_DIR:=")
		if (count == 1) != tc.wantRuntime || count > 1 {
			t.Fatalf("runtime prelude count=%d, want=%v", count, tc.wantRuntime)
		}
		if tc.startup != "" && !strings.Contains(command, tc.startup) {
			t.Fatalf("login credential scrubber lost: %s", command)
		}
	}
}
