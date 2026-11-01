package wslsetup

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/platform"
)

// fakeWSL records every runner invocation and dispatches to per-test fake
// behavior. It drives the real InstallTool/UpdateTool/GetStatus flow on ANY OS
// — no wsl.exe, no distro, no user directories touched.
type fakeWSL struct {
	distro string // defaultDistro answer ("" = no usable distro)
	wslVer int

	user   func(f *fakeWSL, script string, timeout time.Duration) (string, error)
	root   func(f *fakeWSL, script string, timeout time.Duration) (string, error)
	login  func(f *fakeWSL, script string) (string, error)
	search func(distro string) platform.WSLSearchTools
	reset  func(distro string)

	mu    sync.Mutex
	calls []fakeCall
}

type fakeCall struct {
	kind    string // "user" | "root" | "login"
	distro  string
	user    string
	script  string
	timeout time.Duration
}

func (f *fakeWSL) record(c fakeCall) {
	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.mu.Unlock()
}

func (f *fakeWSL) recorded() []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeCall(nil), f.calls...)
}

// setDistro / currentDistro are mutex-guarded accessors for the distro answer,
// so tests may flip it mid-flight from another goroutine (race-detector safe).
func (f *fakeWSL) setDistro(d string) {
	f.mu.Lock()
	f.distro = d
	f.mu.Unlock()
}

func (f *fakeWSL) currentDistro() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.distro
}

func (f *fakeWSL) runner() *wslRunner {
	return &wslRunner{
		defaultDistro: func() string { return f.currentDistro() },
		distroVersion: func(string) int { return f.wslVer },
		execUser: func(distro, user, script string, timeout time.Duration) (string, error) {
			f.record(fakeCall{"user", distro, user, script, timeout})
			if f.user != nil {
				return f.user(f, script, timeout)
			}
			return "\n", nil
		},
		execRoot: func(distro, script string, timeout time.Duration) (string, error) {
			f.record(fakeCall{"root", distro, "", script, timeout})
			if f.root != nil {
				return f.root(f, script, timeout)
			}
			return "ok\n", nil
		},
		execLogin: func(distro, script string) (string, error) {
			f.record(fakeCall{"login", distro, "", script, 0})
			if f.login != nil {
				return f.login(f, script)
			}
			return "\n", nil
		},
		searchStatus: func(distro string) platform.WSLSearchTools {
			if f.search != nil {
				return f.search(distro)
			}
			return platform.WSLSearchTools{}
		},
		resetSearchCache: func(distro string) {
			if f.reset != nil {
				f.reset(distro)
			}
		},
	}
}

// injectRunner substitutes wslRunnerImpl for one test (restored afterwards),
// so tests are deterministic on every host OS — including Windows, where init()
// wires the real runner.
func injectRunner(t *testing.T, r *wslRunner) {
	t.Helper()
	prev := wslRunnerImpl
	wslRunnerImpl = r
	t.Cleanup(func() { wslRunnerImpl = prev })
}

func injectFake(t *testing.T, f *fakeWSL) *fakeWSL {
	t.Helper()
	injectRunner(t, f.runner())
	return f
}

// effectiveLogin answers the login probe with a resolution inside the user
// npm-global bin (the effective happy path).
func effectiveLogin(*fakeWSL, string) (string, error) {
	return "/home/u/.npm-global/bin\n/home/u/.npm-global/bin/ok\n", nil
}

// cliFlow answers the spine's script sequence for one CLI key: type -P
// results come from typeP FIFO ("" = absent), node is native at the floor,
// npm/verify succeed. Mirrors the historical windows-only script runners.
func (f *fakeWSL) cliFlow(key, version string, typeP []string) {
	f.user = func(_ *fakeWSL, script string, _ time.Duration) (string, error) {
		switch {
		case strings.Contains(script, "type -P '"+key+"'"):
			if len(typeP) == 0 {
				return "\n", nil
			}
			out := typeP[0]
			typeP = typeP[1:]
			return out + "\n", nil
		case strings.Contains(script, "type -P 'node'"):
			return "/usr/bin/node\n", nil
		case strings.Contains(script, "node -v"):
			return "v22.19.0\n", nil
		case strings.Contains(script, key+" --version"):
			return version + "\n", nil
		case strings.Contains(script, "prefix-ok"), strings.Contains(script, "npm config set prefix"):
			return "prefix-ok\n", nil
		case strings.Contains(script, "npm i -g"):
			return "added 1 package\n", nil
		default:
			return "\n", nil
		}
	}
}

// assertNoAgentTreeSeeding fails when any captured script touches the distro
// agent tree or performs a recursive copy (P3-S0：ensurePiConfig 整树播种停用；
// 首装不得带入 sessions/auth/plugin 实体)。
func assertNoAgentTreeSeeding(t *testing.T, scripts []string) {
	t.Helper()
	for _, script := range scripts {
		if strings.Contains(script, "cp -r") || strings.Contains(script, ".pi/agent") || strings.Contains(script, ".pi\\agent") {
			t.Fatalf("agent-tree seeding leaked into install script:\n%s", script)
		}
	}
}

// scriptsOf extracts recorded scripts of one kind.
func scriptsOf(calls []fakeCall, kind string) []string {
	var out []string
	for _, c := range calls {
		if c.kind == kind {
			out = append(out, c.script)
		}
	}
	return out
}

// TestInstallToolPiDoesNotSeedAgentTree（P3-S0 验收 1，移植为可移植测试）：pi
// 首装不再 cp -r Windows ~/.pi/agent 整树——任何阶段的脚本都不得触碰 distro
// agent 目录；CLI 安装成功语义保留（Success + 版本）。
func TestInstallToolPiDoesNotSeedAgentTree(t *testing.T) {
	f := injectFake(t, &fakeWSL{distro: "Ubuntu-Test"})
	f.cliFlow("pi", "1.1.0", []string{"", "/home/u/.npm-global/bin/pi"})
	f.login = func(*fakeWSL, string) (string, error) {
		return "/home/u/.npm-global/bin\n/home/u/.npm-global/bin/pi\n", nil
	}

	res, err := NewService(nil).InstallTool("pi")
	if err != nil {
		t.Fatalf("InstallTool(pi): %v", err)
	}
	if !res.Success || res.Version != "1.1.0" {
		t.Fatalf("install success semantics must hold: Success=%v Version=%q Error=%q", res.Success, res.Version, res.Error)
	}
	if strings.Contains(res.Log, "[pi-config]") || strings.Contains(res.Log, "seed") {
		t.Fatalf("install log still mentions config seeding:\n%s", res.Log)
	}
	assertNoAgentTreeSeeding(t, scriptsOf(f.recorded(), "user"))
}

// TestInstallToolPiAlreadyOKIdempotent（P3-S0 验收 2，移植）：重复
// InstallTool(pi) 幂等——第二次 AlreadyOK 短路成功，且不触发播种副作用。
func TestInstallToolPiAlreadyOKIdempotent(t *testing.T) {
	f := injectFake(t, &fakeWSL{distro: "Ubuntu-Test"})
	f.cliFlow("pi", "1.1.0", []string{"/home/u/.npm-global/bin/pi", "/home/u/.npm-global/bin/pi"})
	f.login = func(*fakeWSL, string) (string, error) {
		return "/home/u/.npm-global/bin\n/home/u/.npm-global/bin/pi\n", nil
	}

	svc := NewService(nil)
	first, err := svc.InstallTool("pi")
	if err != nil {
		t.Fatalf("first InstallTool: %v", err)
	}
	if !first.Success {
		t.Fatalf("first install failed: %q", first.Error)
	}
	second, err := svc.InstallTool("pi")
	if err != nil {
		t.Fatalf("second InstallTool: %v", err)
	}
	if !second.AlreadyOK || !second.Success {
		t.Fatalf("second install must be AlreadyOK success, got AlreadyOK=%v Success=%v Error=%q", second.AlreadyOK, second.Success, second.Error)
	}
	assertNoAgentTreeSeeding(t, scriptsOf(f.recorded(), "user"))
}

// TestInstallToolAlreadyOKRepairsDirtySnapshot covers C6②③ on the AlreadyOK
// short-circuit: the binary only resolves through the artificial probe PATH,
// while the login shell still hits the /mnt/c Windows shim (dirty PATH
// snapshot). The repaired prefix script (exact-line guard) is re-run, and the
// re-probe must confirm the fix; the result surfaces the repair in Message.
func TestInstallToolAlreadyOKRepairsDirtySnapshot(t *testing.T) {
	f := injectFake(t, &fakeWSL{distro: "Ubuntu-Test"})
	f.cliFlow("claude", "1.2.3", []string{"/home/u/.npm-global/bin/claude"})
	loginOutputs := []string{
		"/home/u/.npm-global/bin\n/mnt/c/Users/u/AppData/Roaming/npm/claude\n", // dirty snapshot
		"/home/u/.npm-global/bin\n/home/u/.npm-global/bin/claude\n",            // repaired
	}
	f.login = func(_ *fakeWSL, _ string) (string, error) {
		if len(loginOutputs) == 0 {
			t.Fatal("unexpected extra login probe")
			return "", nil
		}
		out := loginOutputs[0]
		loginOutputs = loginOutputs[1:]
		return out, nil
	}

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
	f := injectFake(t, &fakeWSL{distro: "Ubuntu-Test"})
	f.cliFlow("claude", "1.2.3", []string{"/home/u/.npm-global/bin/claude"})
	dirty := "/home/u/.npm-global/bin\n/mnt/c/Users/u/AppData/Roaming/npm/claude\n"
	f.login = func(*fakeWSL, string) (string, error) { return dirty, nil }

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
	f := injectFake(t, &fakeWSL{distro: "Ubuntu-Test"})
	// First probe (short-circuit check): not installed yet. Verify probe
	// after npm i -g: present in the user bin.
	f.cliFlow("claude", "1.2.3", []string{"", "/home/u/.npm-global/bin/claude"})
	f.login = func(*fakeWSL, string) (string, error) {
		return "/home/u/.npm-global/bin\n/mnt/c/Users/u/AppData/Roaming/npm/claude\n", nil
	}

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
// success with no repair and no error, and no npm step runs.
func TestInstallToolEffectiveShortCircuitUnchanged(t *testing.T) {
	f := injectFake(t, &fakeWSL{distro: "Ubuntu-Test"})
	f.cliFlow("claude", "1.2.3", []string{"/home/u/.npm-global/bin/claude"})
	f.login = func(*fakeWSL, string) (string, error) {
		return "/home/u/.npm-global/bin\n/home/u/.npm-global/bin/claude\n", nil
	}

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
	for _, script := range scriptsOf(f.recorded(), "user") {
		if strings.Contains(script, "npm i -g") {
			t.Errorf("AlreadyOK short-circuit must not run npm, got:\n%s", script)
		}
	}
}

// TestEnsureUserNpmPrefixUsesExactLineGuard pins the C6① guard: the append
// condition must match the exact standard line with grep -qF, not the old weak
// `grep -q "npm-global/bin"` substring guard that dirty PATH-snapshot lines
// defeated.
func TestEnsureUserNpmPrefixUsesExactLineGuard(t *testing.T) {
	f := injectFake(t, &fakeWSL{distro: "Ubuntu-Test"})
	var captured string
	f.user = func(_ *fakeWSL, script string, _ time.Duration) (string, error) {
		captured = script
		return "prefix-ok\n", nil
	}
	injectFake(t, f)

	if _, err := NewService(nil).ensureUserNpmPrefix(f.runner(), "Ubuntu-Test"); err != nil {
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
	f := &fakeWSL{distro: "Ubuntu-Test"}
	r := f.runner()

	f.login = func(*fakeWSL, string) (string, error) {
		return "/home/u/.npm-global/bin\n/home/u/.npm-global/bin/claude\n", nil
	}
	if diag := svc.checkLoginResolution(r, "Ubuntu-Test", "claude"); diag != "" {
		t.Errorf("effective resolution produced diagnostic: %s", diag)
	}

	f.login = func(*fakeWSL, string) (string, error) {
		return "/home/u/.npm-global/bin\n/mnt/c/Users/u/AppData/Roaming/npm/claude\n", nil
	}
	diag := svc.checkLoginResolution(r, "Ubuntu-Test", "claude")
	if diag == "" || !strings.Contains(diag, "/mnt/c/Users/u/AppData/Roaming/npm/claude") || !strings.Contains(diag, "Windows passthrough") {
		t.Errorf("passthrough resolution diagnostic missing expected content: %q", diag)
	}

	f.login = func(*fakeWSL, string) (string, error) {
		return "/home/u/.npm-global/bin\n", nil
	}
	if diag = svc.checkLoginResolution(r, "Ubuntu-Test", "claude"); diag == "" || !strings.Contains(diag, "found nothing on PATH") {
		t.Errorf("unresolved diagnostic missing expected content: %q", diag)
	}

	// A probe error is inconclusive: it must not fail an otherwise completed
	// install, so no diagnostic is produced.
	f.login = func(*fakeWSL, string) (string, error) {
		return "boom", errors.New("fake wsl.exe failure")
	}
	if diag = svc.checkLoginResolution(r, "Ubuntu-Test", "claude"); diag != "" {
		t.Errorf("probe error must be inconclusive, got diagnostic: %s", diag)
	}
}

// TestUpdateToolUpgradesInstalledCLI pins the new core semantics: an installed
// CLI is NOT short-circuited — npm runs with @latest — AlreadyOK stays false,
// and the verify chain (native resolution + login effectiveness) still runs.
func TestUpdateToolUpgradesInstalledCLI(t *testing.T) {
	f := injectFake(t, &fakeWSL{distro: "Ubuntu-Test"})
	f.cliFlow("codex", "2.0.1", []string{"/home/u/.npm-global/bin/codex", "/home/u/.npm-global/bin/codex"})
	base := f.user
	var npmScripts []string
	f.user = func(f *fakeWSL, script string, timeout time.Duration) (string, error) {
		if strings.Contains(script, "npm i -g") {
			npmScripts = append(npmScripts, script)
		}
		return base(f, script, timeout)
	}
	f.login = func(*fakeWSL, string) (string, error) {
		return "/home/u/.npm-global/bin\n/home/u/.npm-global/bin/codex\n", nil
	}

	res, err := NewService(nil).UpdateTool("codex")
	if err != nil {
		t.Fatalf("UpdateTool(codex): %v", err)
	}
	if !res.Success || res.AlreadyOK {
		t.Fatalf("upgrade must run and succeed: Success=%v AlreadyOK=%v Error=%q", res.Success, res.AlreadyOK, res.Error)
	}
	if res.Version != "2.0.1" || !strings.Contains(res.Message, "updated") {
		t.Errorf("result = %+v, want version kept + updated message", res)
	}
	if len(npmScripts) != 1 || !strings.Contains(npmScripts[0], `'@openai/codex@latest'`) {
		t.Fatalf("npm step must run exactly once with @latest, got %v", npmScripts)
	}
	if strings.Contains(npmScripts[0], `'@openai/codex' `) || strings.HasSuffix(strings.TrimSpace(npmScripts[0]), "'@openai/codex'") {
		t.Errorf("npm step must not target the plain package: %s", npmScripts[0])
	}
	if !strings.Contains(res.Log, "[update]") {
		t.Errorf("log should carry the [update] block:\n%s", res.Log)
	}
}

// TestUpdateToolInstallsMissingCLI covers 未装可安装：update on a missing CLI
// performs the full ensure chain and the same @latest install; AlreadyOK stays
// false and success semantics match a fresh install.
func TestUpdateToolInstallsMissingCLI(t *testing.T) {
	f := injectFake(t, &fakeWSL{distro: "Ubuntu-Test"})
	// Update mode does not pre-probe the CLI; the single type -P hit below is
	// the post-npm verification.
	f.cliFlow("pi", "1.5.0", []string{"/home/u/.npm-global/bin/pi"})
	f.login = func(*fakeWSL, string) (string, error) {
		return "/home/u/.npm-global/bin\n/home/u/.npm-global/bin/pi\n", nil
	}

	res, err := NewService(nil).UpdateTool("pi")
	if err != nil {
		t.Fatalf("UpdateTool(pi): %v", err)
	}
	if !res.Success || res.AlreadyOK {
		t.Fatalf("missing CLI must be installed by update: Success=%v AlreadyOK=%v Error=%q", res.Success, res.AlreadyOK, res.Error)
	}
	var sawNpm, sawPrefix, sawNode bool
	for _, c := range f.recorded() {
		if strings.Contains(c.script, "npm i -g") && strings.Contains(c.script, "@earendil-works/pi-coding-agent@latest") {
			sawNpm = true
		}
		if strings.Contains(c.script, "npm config set prefix") {
			sawPrefix = true
		}
		if strings.Contains(c.script, "node -v") {
			sawNode = true
		}
	}
	if !sawNpm || !sawPrefix || !sawNode {
		t.Errorf("update must reuse the ensure chain: npm=%v prefix=%v node=%v", sawNpm, sawPrefix, sawNode)
	}
}

// TestUpdateToolValidationAndDistroFailure pins the error contracts: unknown
// tools return a Go error (validated before the runner is even consulted);
// no usable distro yields a structured failure with zero distro side effects.
func TestUpdateToolValidationAndDistroFailure(t *testing.T) {
	f := injectFake(t, &fakeWSL{distro: ""}) // no usable distro
	injectFake(t, f)
	svc := NewService(nil)

	if _, err := svc.UpdateTool("nope"); err == nil {
		t.Error("UpdateTool(nope) must return an error for unknown tools")
	}
	res, err := svc.UpdateTool("omp")
	if err != nil {
		t.Fatalf("UpdateTool(omp): %v", err)
	}
	if res.Success || res.Error != "no usable WSL distro installed" || res.Tool != "omp" || res.Package != "@oh-my-pi/pi-coding-agent" {
		t.Errorf("result = %+v, want structured no-distro failure for omp", res)
	}
	if calls := f.recorded(); len(calls) != 0 {
		t.Errorf("no distro means no side effects, got %d runner calls", len(calls))
	}
}

// TestCommandFailuresReported keeps real errors surfacing: an npm failure and
// a NodeSource failure must produce Success=false with the underlying cause in
// Error (never a fake success), for both install and update modes.
func TestCommandFailuresReported(t *testing.T) {
	t.Run("npm install failure", func(t *testing.T) {
		f := injectFake(t, &fakeWSL{distro: "Ubuntu-Test"})
		f.cliFlow("claude", "1.2.3", []string{"", "/home/u/.npm-global/bin/claude"})
		base := f.user
		f.user = func(f *fakeWSL, script string, timeout time.Duration) (string, error) {
			if strings.Contains(script, "npm i -g") {
				return "npm ERR! network\n", errors.New("exit status 1")
			}
			return base(f, script, timeout)
		}
		f.login = effectiveLogin

		res, err := NewService(nil).InstallTool("claude")
		if err != nil {
			t.Fatalf("InstallTool: %v", err)
		}
		if res.Success || !strings.Contains(res.Error, "npm install failed") || !strings.Contains(res.Error, "exit status 1") {
			t.Fatalf("result = %+v, want npm install failure with cause", res)
		}
		if !strings.Contains(res.Log, "npm ERR! network") {
			t.Errorf("log should retain npm output:\n%s", res.Log)
		}
	})

	t.Run("npm upgrade failure", func(t *testing.T) {
		f := injectFake(t, &fakeWSL{distro: "Ubuntu-Test"})
		f.cliFlow("omp", "1.0.0", []string{"/home/u/.npm-global/bin/omp"})
		base := f.user
		f.user = func(f *fakeWSL, script string, timeout time.Duration) (string, error) {
			if strings.Contains(script, "npm i -g") {
				return "npm ERR! EACCES\n", errors.New("exit status 1")
			}
			return base(f, script, timeout)
		}
		f.login = effectiveLogin

		res, err := NewService(nil).UpdateTool("omp")
		if err != nil {
			t.Fatalf("UpdateTool: %v", err)
		}
		if res.Success || !strings.Contains(res.Error, "npm upgrade failed") || !strings.Contains(res.Error, "exit status 1") {
			t.Fatalf("result = %+v, want npm upgrade failure with cause", res)
		}
	})

	t.Run("node install failure", func(t *testing.T) {
		f := injectFake(t, &fakeWSL{distro: "Ubuntu-Test"})
		f.user = func(_ *fakeWSL, script string, _ time.Duration) (string, error) {
			switch {
			case strings.Contains(script, "type -P 'pi'"):
				return "\n", nil
			case strings.Contains(script, "type -P 'node'"):
				return "\n", nil // native node absent → root install path
			case strings.Contains(script, "prefix-ok"), strings.Contains(script, "npm config set prefix"):
				return "prefix-ok\n", nil
			default:
				return "\n", nil
			}
		}
		f.root = func(_ *fakeWSL, _ string, _ time.Duration) (string, error) {
			return "E: Unable to fetch some archival archives\n", errors.New("exit status 100")
		}
		injectFake(t, f)

		res, err := NewService(nil).InstallTool("pi")
		if err != nil {
			t.Fatalf("InstallTool: %v", err)
		}
		if res.Success || !strings.Contains(res.Error, "install node in WSL") || !strings.Contains(res.Error, "exit status 100") {
			t.Fatalf("result = %+v, want node install failure surfaced", res)
		}
	})
}

// TestLongStepsBoundedByTimeouts pins the timeout wiring (the historical
// unbounded-hang problem, now on the upgrade path too): the npm step runs
// under npmOpTimeout, the NodeSource chain under nodeInstallTimeout, while
// probes stay unbounded (fast commands that fail on their own).
func TestLongStepsBoundedByTimeouts(t *testing.T) {
	f := injectFake(t, &fakeWSL{distro: "Ubuntu-Test"})
	nodeTypeP := []string{"", "/usr/bin/node"} // absent → root install; resolvable after
	var npmTimeout, rootTimeout time.Duration
	f.user = func(_ *fakeWSL, script string, timeout time.Duration) (string, error) {
		switch {
		case strings.Contains(script, "type -P 'pi'"):
			return "/home/u/.npm-global/bin/pi\n", nil
		case strings.Contains(script, "pi --version"):
			return "1.0.0\n", nil
		case strings.Contains(script, "type -P 'node'"):
			out := nodeTypeP[0]
			nodeTypeP = nodeTypeP[1:]
			return out + "\n", nil
		case strings.Contains(script, "npm i -g"):
			npmTimeout = timeout
			return "added 1 package\n", nil
		case strings.Contains(script, "prefix-ok"), strings.Contains(script, "npm config set prefix"):
			return "prefix-ok\n", nil
		default:
			return "\n", nil
		}
	}
	f.root = func(_ *fakeWSL, script string, timeout time.Duration) (string, error) {
		rootTimeout = timeout
		if !strings.Contains(script, "apt-get install -y nodejs") {
			return "", errors.New("unexpected root script: " + script)
		}
		return "v22.19.0\n", nil
	}
	f.login = effectiveLogin
	injectFake(t, f)

	res, err := NewService(nil).UpdateTool("pi")
	if err != nil {
		t.Fatalf("UpdateTool: %v", err)
	}
	if !res.Success {
		t.Fatalf("flow should succeed: %+v", res)
	}
	if npmTimeout != npmOpTimeout {
		t.Errorf("npm step timeout = %v, want %v", npmTimeout, npmOpTimeout)
	}
	if rootTimeout != nodeInstallTimeout {
		t.Errorf("node install timeout = %v, want %v", rootTimeout, nodeInstallTimeout)
	}
	for _, c := range f.recorded() {
		if c.timeout != 0 && !(c.kind == "root" || strings.Contains(c.script, "npm i -g")) {
			t.Errorf("unexpected bounded command (%s):\n%s", c.kind, c.script)
		}
	}
}

// TestGetStatusReportsFiveCLIs pins the Status surface incl. omp: five tool
// rows in canonical order, per-tool install detection via native PATH probe,
// version probe, node version and WSL generation badge.
func TestGetStatusReportsFiveCLIs(t *testing.T) {
	f := injectFake(t, &fakeWSL{distro: "Ubuntu-Test", wslVer: 2})
	installed := map[string]bool{"claude": true, "omp": true}
	f.user = func(_ *fakeWSL, script string, _ time.Duration) (string, error) {
		for key := range cliPackages {
			if strings.Contains(script, "type -P '"+key+"'") {
				if installed[key] {
					return "/home/u/.npm-global/bin/" + key + "\n", nil
				}
				return "\n", nil
			}
		}
		switch {
		case strings.Contains(script, "type -P 'node'"):
			return "/usr/bin/node\n", nil
		case strings.Contains(script, "node -v"):
			return "v22.23.2\n", nil
		case strings.Contains(script, "claude --version"):
			return "1.0.0\n", nil
		case strings.Contains(script, "omp --version"):
			return "9.9.9\n", nil
		}
		return "\n", nil
	}

	st := NewService(nil).GetStatus()
	if !st.Available || st.Distro != "Ubuntu-Test" || st.DistroWSLVersion != 2 {
		t.Fatalf("status = %+v, want available Ubuntu-Test @WSL2", st)
	}
	if st.NodeVersion != "v22.23.2" {
		t.Errorf("NodeVersion = %q", st.NodeVersion)
	}
	if len(st.Tools) != len(cliToolOrder) {
		t.Fatalf("Tools has %d entries, want %d", len(st.Tools), len(cliToolOrder))
	}
	byTool := map[string]ToolStatus{}
	for i, ts := range st.Tools {
		if ts.Tool != cliToolOrder[i] {
			t.Errorf("Tools[%d] = %q, want canonical order %q", i, ts.Tool, cliToolOrder[i])
		}
		byTool[ts.Tool] = ts
	}
	for _, key := range cliToolOrder {
		wantPkg, _ := packageForTool(key)
		if byTool[key].Package != wantPkg {
			t.Errorf("%s package = %q, want %q", key, byTool[key].Package, wantPkg)
		}
	}
	ompTs := byTool["omp"]
	if !ompTs.Installed || ompTs.Version != "9.9.9" || ompTs.ExecutablePath != "/home/u/.npm-global/bin/omp" {
		t.Errorf("omp status = %+v, want installed with version and path", ompTs)
	}
	if byTool["pi"].Installed {
		t.Errorf("pi must report not installed:\n%+v", byTool["pi"])
	}
	if byTool["claude"].Version != "1.0.0" {
		t.Errorf("claude version probe not surfaced: %+v", byTool["claude"])
	}
}
