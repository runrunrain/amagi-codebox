package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/agentcli/spec"
	"amagi-codebox/internal/aiproxy"
	"amagi-codebox/internal/piplugin"
	"amagi-codebox/internal/sharedmarket"
	"amagi-codebox/internal/sharedmcp"
	"amagi-codebox/internal/sharedskills"
	"amagi-codebox/internal/wslsupply"
)

func TestAgentCLIRecentExtensions(t *testing.T) {
	f := newAgentCLIE2E(t)
	home := t.TempDir()
	f.app.SharedSkills = sharedskills.New(home, f.app.Log)
	f.app.SharedMcp = sharedmcp.New(home, f.app.configDir, nil, f.app.Log)
	check := func(want int, stdin string, argv ...string) spec.Envelope {
		t.Helper()
		status, env, _ := f.runJSON(t, stdin, argv...)
		if status != want || env.OK != (want == spec.ExitOK) {
			t.Fatalf("%v: exit=%d envelope=%+v, want exit=%d", argv, status, env, want)
		}
		return env
	}

	skill := filepath.Join(t.TempDir(), "reviewer")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: reviewer\ndescription: Reviews fixtures.\n---\nReview a fixture.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	check(spec.ExitOK, "", "shared", "skill", "import", skill)
	disabled := filepath.Join(home, ".agents", "skills-disabled", "reviewer", "SKILL.md")
	if _, err := os.Stat(disabled); err != nil {
		t.Fatalf("new skill should be disabled: %v", err)
	}
	check(spec.ExitOK, "", "shared", "skill", "enable", "reviewer")
	check(spec.ExitConfirm, "", "shared", "skill", "delete", "reviewer")
	if f.recorder.count("shared.skill.delete") != 0 {
		t.Fatal("unconfirmed delete reached the handler")
	}
	check(spec.ExitOK, "", "shared", "skill", "delete", "reviewer", "--yes")

	const credential = "fixture-not-a-real-credential-6789"
	const config = `{"mcpServers":{"fixture":{"type":"stdio","command":"fixture","env":{"KEY":"` + credential + `"}}}}`
	check(spec.ExitUsage, "", "shared", "mcp", "import", "--config", config)
	check(spec.ExitOK, config, "shared", "mcp", "preview", "--config", "-")
	registry := filepath.Join(home, ".agents", ".mcp.json")
	if _, err := os.Stat(registry); !os.IsNotExist(err) {
		t.Fatalf("preview changed registry: %v", err)
	}
	check(spec.ExitOK, config, "shared", "mcp", "import", "--config", "-")
	check(spec.ExitConfirm, "", "shared", "mcp", "distribute", "fixture", "claude")
	check(spec.ExitOK, "", "shared", "mcp", "distribute", "fixture", "claude", "--yes")
	body, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil || !strings.Contains(string(body), credential) {
		t.Fatalf("MCP was not distributed: %v", err)
	}
	view := check(spec.ExitOK, "", "shared", "mcp", "list")
	encoded, err := json.Marshal(view.Data)
	if err != nil || !strings.Contains(string(encoded), `"status":"distributed"`) {
		t.Fatalf("missing actual distribution status: %s (%v)", encoded, err)
	}
	if strings.Contains(f.logs.String(), credential) {
		t.Fatal("CLI echoed input credentials")
	}

	piHome := t.TempDir()
	writePiPackagesFixture(t, piHome, "npm:@foo/bar@1.0.0")
	f.app.PiPlugins = piplugin.NewService(piHome, f.app.Log)
	check(spec.ExitOK, "", "plugin", "disable", "pi", "npm:@foo/bar@1.0.0")
	packages, err := f.app.PiPlugins.ListInstalledPackages()
	if err != nil || len(packages) != 1 || packages[0].Enabled {
		t.Fatalf("Pi package was not disabled in place: %+v (%v)", packages, err)
	}
	wrongEngine := check(spec.ExitUsage, "", "plugin", "install", "codex", "x@y", "--target", "wsl", "--yes")
	if wrongEngine.Error == nil || !strings.Contains(wrongEngine.Error.Message, "仅适用于 Pi") {
		t.Fatalf("install must parse the positional package and reject non-Pi targets: %+v", wrongEngine)
	}
	check(spec.ExitBusiness, "", "wsl", "config", "preview", "claude")
}

func TestAgentCLILaunchPreservesWSLWorkdir(t *testing.T) {
	originalPathCheck, originalTargetCheck := isLinuxStyleWorkDirFn, launchTargetsWSLFn
	t.Cleanup(func() {
		isLinuxStyleWorkDirFn, launchTargetsWSLFn = originalPathCheck, originalTargetCheck
	})
	isLinuxStyleWorkDirFn = func(path string) bool { return strings.HasPrefix(path, "/home/") }
	launchTargetsWSLFn = func(mode, shell string) bool { return mode == "embedded" && shell == "wsl.exe" }
	params := agentLaunchParams{AppType: "pi", Model: "test-model", Shell: "wsl.exe", WorkDir: "/home/example/project"}
	got, err := normalizeAgentLaunchParams(params)
	if err != nil || got.WorkDir != params.WorkDir {
		t.Fatalf("WSL path was changed before the launch preflight: %+v (%v)", got, err)
	}
	params.Shell = "powershell.exe"
	if _, err := normalizeAgentLaunchParams(params); err == nil || err.Code != spec.ErrBadArgs {
		t.Fatalf("Linux path unexpectedly accepted for the native target: %v", err)
	}
}

func TestAgentCLIE2EWSLConfigSelection(t *testing.T) {
	f := newAgentCLIE2E(t)
	app, coord, uncRoot := newS4TestApp(t)
	if app.Log != nil {
		t.Cleanup(app.Log.Close)
	}
	f.app.WSLSupply = coord
	home := t.TempDir()
	previousHome, previousDistro := wslS4HomeDirFn, wslDefaultDistroFn
	t.Cleanup(func() { wslS4HomeDirFn, wslDefaultDistroFn = previousHome, previousDistro })
	wslS4HomeDirFn = func() (string, error) { return home, nil }
	wslDefaultDistroFn = func([]string) string { return "Ubuntu" }
	registerS4Adapters(coord, f.app)
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"mcpServers":{"web":{"url":"https://one.example/mcp"},"notes":{"url":"https://two.example/mcp"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) spec.Envelope {
		t.Helper()
		exit, env, _ := f.runJSON(t, "", append([]string{"wsl", "config"}, args...)...)
		if exit != spec.ExitOK || !env.OK {
			t.Fatalf("config %v: %+v (exit %d)", args, env, exit)
		}
		return env
	}
	target := filepath.Join(uncRoot, "home", "u", ".claude.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	const local = `{"projects":{"local-only":{}}}`
	if err := os.WriteFile(target, []byte(local), 0o600); err != nil {
		t.Fatal(err)
	}
	run("preview", "claude")
	if data, err := os.ReadFile(target); err != nil || string(data) != local {
		t.Fatalf("preview changed target: %q (%v)", data, err)
	}
	unit := string(wslsupply.UnitClaudeMCP)
	run("select", "claude", unit, "--key", "web", "--key", "notes", "--yes")
	data, err := os.ReadFile(target)
	if err != nil || !strings.Contains(string(data), "one.example") || !strings.Contains(string(data), "two.example") {
		t.Fatalf("repeated --key selection was not applied: %q (%v)", data, err)
	}
	run("select", "claude", unit, "--yes")
	if data, err = os.ReadFile(target); err != nil || !strings.Contains(string(data), "local-only") || strings.Contains(string(data), "one.example") || strings.Contains(string(data), "two.example") {
		t.Fatalf("clearing selection did not retract the managed entries: %q (%v)", data, err)
	}
}

func TestAgentCLIE2EProxySettingsAndTraffic(t *testing.T) {
	f := newAgentCLIE2E(t)
	f.app.aiProxy = aiproxy.New(t.TempDir())
	t.Cleanup(func() { _ = f.app.aiProxy.Close() })
	run := func(stdin string, argv ...string) spec.Envelope {
		t.Helper()
		exit, env, _ := f.runJSON(t, stdin, append([]string{"proxy"}, argv...)...)
		if exit != spec.ExitOK || !env.OK {
			t.Fatalf("proxy %v: %+v (exit %d)", argv, env, exit)
		}
		return env
	}
	settings := run("", "status").Data.(map[string]any)["settings"].(map[string]any)
	routing := settings["routing"].(map[string]any)
	const password = "fixture-proxy-password-only"
	routing["fetch_proxy"] = "http://fixture:" + password + "@localhost:8080"
	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	run(string(data), "save", "--settings", "-")
	status := run("", "status").Data.(map[string]any)
	if status["enabled"] != false || !strings.Contains(f.app.aiProxy.State().Settings.Routing.FetchProxy, "已保存") {
		t.Fatal("settings were not saved and masked while the proxy remained stopped")
	}
	if strings.Contains(f.logs.String(), password) {
		t.Fatal("CLI echoed proxy credentials")
	}
	traffic := run("", "traffic", "--verdict", "mismatch", "--offset", "0")
	if traffic.Data.(map[string]any)["total"] != float64(0) {
		t.Fatalf("unexpected traffic from the stopped fixture: %+v", traffic)
	}
}

func TestAgentCLIMarketUpdatePluginThroughCLI(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	f := newAgentCLIE2E(t)
	home := t.TempDir()
	f.app.SharedMarkets = sharedmarket.New(home, f.app.Log)
	repo := filepath.Join(t.TempDir(), "fixture-market")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if body, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, body)
		}
	}
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(message string) {
		git("add", "--all")
		git("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--quiet", "-m", message)
	}
	run := func(args ...string) spec.Envelope {
		t.Helper()
		exit, env, _ := f.runJSON(t, "", append([]string{"shared", "market"}, args...)...)
		if exit != spec.ExitOK || !env.OK {
			t.Fatalf("market %v: %+v (exit %d)", args, env, exit)
		}
		return env
	}
	git("init", "--quiet")
	write(".claude-plugin/marketplace.json", `{"name":"fixture-market","plugins":[{"name":"alpha","source":"./alpha"}]}`)
	write("alpha/SKILL.md", "# version 1\n")
	commit("Initial fixture")
	run("add", repo, "--yes")
	run("deploy", "fixture-market", "alpha")
	disabled := filepath.Join(home, ".agents", "skills-disabled", "alpha")
	if err := os.Rename(filepath.Join(home, ".agents", "skills", "alpha"), disabled); err != nil {
		t.Fatal(err)
	}
	write("alpha/SKILL.md", "# version 2\n")
	commit("Update fixture")
	updated := run("update-plugin", "fixture-market", "alpha", "--yes")
	if got := updated.Data.(map[string]any)["updatedSkills"].([]any); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("unexpected updated skills: %v", got)
	}
	if data, err := os.ReadFile(filepath.Join(disabled, "SKILL.md")); err != nil || !strings.Contains(string(data), "version 2") {
		t.Fatalf("disabled skill did not update: %q (%v)", data, err)
	}
}
