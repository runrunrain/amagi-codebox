//go:build windows

package wslsetup

import (
	"strings"
	"testing"

	"amagi-codebox/internal/platform"
)

// stubWSLExec replaces the wslExec / wslExecLogin package vars for one test and
// restores them afterwards, so other tests (and GetStatus) keep the real
// executors.
func stubWSLExec(t *testing.T, execFn, loginFn func(distro, script string) (string, error)) {
	t.Helper()
	prevExec, prevLogin := wslExec, wslExecLogin
	wslExec = func(_, _, script string) (string, error) {
		return execFn("", script)
	}
	if loginFn != nil {
		wslExecLogin = loginFn
	}
	t.Cleanup(func() {
		wslExec, wslExecLogin = prevExec, prevLogin
	})
}

// TestEnsureUserNpmPrefixUsesExactLineGuard pins the C6① guard: the append
// condition must match the exact standard line with grep -qF, not the old weak
// `grep -q "npm-global/bin"` substring guard that dirty PATH-snapshot lines
// defeated.
func TestEnsureUserNpmPrefixUsesExactLineGuard(t *testing.T) {
	var captured string
	stubWSLExec(t, func(_, script string) (string, error) {
		captured = script
		return "prefix-ok\n", nil
	}, nil)

	svc := NewService(nil)
	if _, err := svc.ensureUserNpmPrefix("Ubuntu-Test"); err != nil {
		t.Fatalf("ensureUserNpmPrefix: %v", err)
	}

	exactGuard := `grep -qF 'export PATH="$HOME/.npm-global/bin:$PATH"'`
	for _, target := range []string{`"$HOME/.bashrc"`, `"$HOME/.profile"`} {
		if !strings.Contains(captured, exactGuard+" "+target) {
			t.Errorf("script missing exact-line guard for %s:\n%s", target, captured)
		}
	}
	if strings.Contains(captured, `grep -q "npm-global/bin"`) {
		t.Errorf("script still contains the old weak substring guard:\n%s", captured)
	}
	// The appended line must be the standard line itself (C6③: appending the
	// exact line after a dirty snapshot repairs it — later PATH-prepends win).
	if got := strings.Count(captured, `'export PATH="$HOME/.npm-global/bin:$PATH"' >> `); got != 2 {
		t.Errorf("script appends the standard line %d times, want 2 (.bashrc + .profile):\n%s", got, captured)
	}
}

func TestSplitLoginResolutionProbe(t *testing.T) {
	cases := []struct {
		name     string
		out      string
		userBin  string
		resolved string
	}{
		{"effective", "/home/u/.npm-global/bin\n/home/u/.npm-global/bin/claude\n", "/home/u/.npm-global/bin", "/home/u/.npm-global/bin/claude"},
		{"windows passthrough", "/home/u/.npm-global/bin\r\n/mnt/c/Users/u/AppData/Roaming/npm/claude\r\n", "/home/u/.npm-global/bin", "/mnt/c/Users/u/AppData/Roaming/npm/claude"},
		{"not found", "/home/u/.npm-global/bin\n", "/home/u/.npm-global/bin", ""},
		{"empty output", "", "", ""},
		{"garbage single line", "something went wrong\n", "something went wrong", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			userBin, resolved := splitLoginResolutionProbe(c.out)
			if userBin != c.userBin || resolved != c.resolved {
				t.Errorf("splitLoginResolutionProbe(%q) = (%q, %q), want (%q, %q)", c.out, userBin, resolved, c.userBin, c.resolved)
			}
		})
	}
}

func TestCheckLoginResolution(t *testing.T) {
	svc := NewService(nil)

	stubWSLExec(t, nil, func(_, _ string) (string, error) {
		return "/home/u/.npm-global/bin\n/home/u/.npm-global/bin/claude\n", nil
	})
	if diag := svc.checkLoginResolution("Ubuntu-Test", "claude"); diag != "" {
		t.Errorf("effective resolution produced diagnostic: %s", diag)
	}

	stubWSLExec(t, nil, func(_, _ string) (string, error) {
		return "/home/u/.npm-global/bin\n/mnt/c/Users/u/AppData/Roaming/npm/claude\n", nil
	})
	diag := svc.checkLoginResolution("Ubuntu-Test", "claude")
	if diag == "" || !strings.Contains(diag, "/mnt/c/Users/u/AppData/Roaming/npm/claude") || !strings.Contains(diag, "Windows passthrough") {
		t.Errorf("passthrough resolution diagnostic missing expected content: %q", diag)
	}

	stubWSLExec(t, nil, func(_, _ string) (string, error) {
		return "/home/u/.npm-global/bin\n", nil
	})
	if diag = svc.checkLoginResolution("Ubuntu-Test", "claude"); diag == "" || !strings.Contains(diag, "found nothing on PATH") {
		t.Errorf("unresolved diagnostic missing expected content: %q", diag)
	}

	// A probe error is inconclusive: it must not fail an otherwise completed
	// install, so no diagnostic is produced.
	stubWSLExec(t, nil, func(_, _ string) (string, error) {
		return "boom", errFakeWSL
	})
	if diag = svc.checkLoginResolution("Ubuntu-Test", "claude"); diag != "" {
		t.Errorf("probe error must be inconclusive, got diagnostic: %s", diag)
	}
}

type fakeWSLError struct{}

func (fakeWSLError) Error() string { return "fake wsl.exe failure" }

var errFakeWSL = fakeWSLError{}

// wslInstallScriptRunner fakes wslExec for the scripted InstallTool flow tests:
// the CLI is present in the user npm-global bin (so the artificial-PATH probes
// succeed), node is native and at the floor, and the prefix script reports ok.
func wslInstallScriptRunner(typePClaude []string) func(distro, script string) (string, error) {
	return func(_, script string) (string, error) {
		switch {
		case strings.Contains(script, "type -P 'claude'"):
			if len(typePClaude) == 0 {
				return "\n", nil
			}
			out := typePClaude[0]
			typePClaude = typePClaude[1:]
			return out + "\n", nil
		case strings.Contains(script, "type -P 'node'"):
			return "/usr/bin/node\n", nil
		case strings.Contains(script, "node -v"):
			return "v22.19.0\n", nil
		case strings.Contains(script, "claude --version"):
			return "1.2.3\n", nil
		case strings.Contains(script, "prefix-ok"), strings.Contains(script, "npm config set prefix"):
			return "prefix-ok\n", nil
		case strings.Contains(script, "npm i -g"):
			return "added 1 package\n", nil
		default:
			return "\n", nil
		}
	}
}

// skipWithoutWSLDistro skips tests whose code path calls
// platform.DefaultWSLDistro (no injection seam) on machines without WSL.
func skipWithoutWSLDistro(t *testing.T) {
	t.Helper()
	if platform.DefaultWSLDistro(nil) == "" {
		t.Skip("no usable WSL distro on this machine; skipping scripted install-flow test")
	}
}

// TestInstallToolAlreadyOKRepairsDirtySnapshot covers C6②③ on the AlreadyOK
// short-circuit: the binary only resolves through the artificial probe PATH,
// while the login shell still hits the /mnt/c Windows shim (dirty PATH
// snapshot). The repaired prefix script (exact-line guard) is re-run, and the
// re-probe must confirm the fix; the result surfaces the repair in Message.
func TestInstallToolAlreadyOKRepairsDirtySnapshot(t *testing.T) {
	skipWithoutWSLDistro(t)

	loginOutputs := []string{
		"/home/u/.npm-global/bin\n/mnt/c/Users/u/AppData/Roaming/npm/claude\n", // dirty snapshot
		"/home/u/.npm-global/bin\n/home/u/.npm-global/bin/claude\n",            // repaired
	}
	stubWSLExec(t,
		wslInstallScriptRunner([]string{"/home/u/.npm-global/bin/claude"}),
		func(_, _ string) (string, error) {
			if len(loginOutputs) == 0 {
				t.Fatal("unexpected extra login probe")
			}
			out := loginOutputs[0]
			loginOutputs = loginOutputs[1:]
			return out, nil
		})

	res, err := NewService(nil).InstallTool("claude")
	if err != nil {
		t.Fatalf("InstallTool: %v", err)
	}
	if !res.AlreadyOK || !res.Success {
		t.Fatalf("AlreadyOK=%v Success=%v Error=%q, want already-ok success", res.AlreadyOK, res.Success, res.Error)
	}
	if !strings.Contains(res.Message, "repaired login PATH") {
		t.Errorf("Message should mention the repaired login PATH: %q", res.Message)
	}
	if !strings.Contains(res.Log, "[npm-prefix-repair]") {
		t.Errorf("Log should contain the repair block:\n%s", res.Log)
	}
}

// TestInstallToolAlreadyOKUnrepairableReportsFailure keeps Success=false with
// the effectiveness diagnostic when even the repaired prefix cannot make the
// login shell resolve the CLI into the user bin.
func TestInstallToolAlreadyOKUnrepairableReportsFailure(t *testing.T) {
	skipWithoutWSLDistro(t)

	dirty := "/home/u/.npm-global/bin\n/mnt/c/Users/u/AppData/Roaming/npm/claude\n"
	stubWSLExec(t,
		wslInstallScriptRunner([]string{"/home/u/.npm-global/bin/claude"}),
		func(_, _ string) (string, error) { return dirty, nil })

	res, err := NewService(nil).InstallTool("claude")
	if err != nil {
		t.Fatalf("InstallTool: %v", err)
	}
	if res.Success {
		t.Fatalf("Success=true, want false with effectiveness error")
	}
	if res.Error == "" || !strings.Contains(res.Error, "not effective in the WSL login shell") {
		t.Errorf("Error should carry the effectiveness diagnostic: %q", res.Error)
	}
}

// TestInstallToolEffectivenessFailureReported covers the fresh-install path:
// npm i -g succeeds and the artificial-PATH verify passes, but the login-shell
// effectiveness check finds the /mnt/c passthrough — the install result must
// report failure with the diagnostic instead of silently succeeding.
func TestInstallToolEffectivenessFailureReported(t *testing.T) {
	skipWithoutWSLDistro(t)

	stubWSLExec(t,
		// First probe (short-circuit check): not installed yet. Verify probe
		// after npm i -g: present in the user bin.
		wslInstallScriptRunner([]string{"", "/home/u/.npm-global/bin/claude"}),
		func(_, _ string) (string, error) {
			return "/home/u/.npm-global/bin\n/mnt/c/Users/u/AppData/Roaming/npm/claude\n", nil
		})

	res, err := NewService(nil).InstallTool("claude")
	if err != nil {
		t.Fatalf("InstallTool: %v", err)
	}
	if res.Success {
		t.Fatalf("Success=true, want false: login shell resolves the Windows passthrough")
	}
	if res.Error == "" || !strings.Contains(res.Error, "/mnt/c/Users/u/AppData/Roaming/npm/claude") {
		t.Errorf("Error should carry the resolved passthrough path: %q", res.Error)
	}
	if res.Version != "1.2.3" {
		t.Errorf("Version = %q, want the installed version retained for diagnostics", res.Version)
	}
}

// TestInstallToolEffectiveShortCircuitUnchanged pins the happy path: when the
// login shell already resolves into the user bin, AlreadyOK stays a plain
// success with no repair and no error.
func TestInstallToolEffectiveShortCircuitUnchanged(t *testing.T) {
	skipWithoutWSLDistro(t)

	stubWSLExec(t,
		wslInstallScriptRunner([]string{"/home/u/.npm-global/bin/claude"}),
		func(_, _ string) (string, error) {
			return "/home/u/.npm-global/bin\n/home/u/.npm-global/bin/claude\n", nil
		})

	res, err := NewService(nil).InstallTool("claude")
	if err != nil {
		t.Fatalf("InstallTool: %v", err)
	}
	if !res.AlreadyOK || !res.Success || res.Error != "" {
		t.Fatalf("AlreadyOK=%v Success=%v Error=%q, want plain already-ok success", res.AlreadyOK, res.Success, res.Error)
	}
	if strings.Contains(res.Message, "repaired") {
		t.Errorf("no repair expected on effective resolution: %q", res.Message)
	}
}
