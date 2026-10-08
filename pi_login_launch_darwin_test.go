//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/processcap"
	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/settings"
	"amagi-codebox/internal/webui"
)

// Real desktop entry -> production resolver -> real PTY. The executable is an
// explicitly synthetic Pi CLI fixture: no provider network call or real token.
func TestPiLoginDesktopRealPTY(t *testing.T) {
	home := isolatedHome(t)
	app, configDir := newTestAppWithConfigDir(t)
	app.Settings = settings.NewService(configDir)
	if err := app.Settings.Load(); err != nil {
		t.Fatal(err)
	}
	var defaultsErr error
	app.remoteDefaults, defaultsErr = launchplan.NewDefaultStore(app.Settings)
	if defaultsErr != nil {
		t.Fatal(defaultsErr)
	}
	app.ctx = t.Context()
	app.Capabilities = platform.CurrentCapabilities()
	app.processRegistry = processcap.NewRegistry()
	wireTestControl(t, app)
	app.WebUI = webui.NewService(app.Log, t.TempDir())
	preset, key, auth := piLoginFixture(t, app.Config)
	before, _ := os.ReadFile(auth)
	bin := t.TempDir()
	trace := filepath.Join(bin, "trace")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$PI_LOGIN_FIXTURE_TRACE\"\nprintf 'agent=%s key=%s\\n' \"$PI_CODING_AGENT_DIR\" \"$OPENAI_API_KEY\" >> \"$PI_LOGIN_FIXTURE_TRACE\"\nexec /bin/sleep 30\n"
	if err := os.WriteFile(filepath.Join(bin, "pi"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PI_LOGIN_FIXTURE_TRACE", trace)
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "fixture-inherited-key")
	app.CLIResolver = platform.NewCLIResolver(app.Capabilities)
	sid, err := app.LaunchPiSession(key, config.PiLoginProviderID(preset.Provider), "embedded", bin, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Pty.Close(sid) })
	deadline := time.Now().Add(5 * time.Second)
	var body []byte
	for time.Now().Before(deadline) {
		body, err = os.ReadFile(trace)
		if err == nil && strings.Contains(string(body), "agent=") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	want := "--provider\nopenai-codex\n--model\nnative-model\n--thinking\nmax\nagent=" + filepath.Join(home, ".pi", "agent") + " key=\n"
	if string(body) != want {
		t.Fatalf("real CLI fixture received %q, want %q", body, want)
	}
	after, _ := os.ReadFile(auth)
	if string(before) != string(after) {
		t.Fatal("real launch modified auth")
	}
	if _, err := os.Stat(filepath.Join(defaultPiAgentDir(), "models.json")); !os.IsNotExist(err) {
		t.Fatal("real login launch wrote managed models")
	}
	if !app.Pty.IsRunning(sid) {
		t.Fatal("Pi fixture process not active")
	}
	recipe, ok := app.remoteDefaults.HostDefaultRefs(contract.CLITypePi)
	if !ok || recipe.PresetRef != key || recipe.ProviderRef != config.PiLoginProviderID(preset.Provider) {
		t.Fatalf("desktop discarded stable login recipe: %+v", recipe)
	}
	planner := newAppLaunchPlanner(app.Config, app.Secrets, app.remoteDefaults, fakeCLIResolver{}, fakePlatformCaps(), app.Paths, app.EnvVars, filepath.Dir(filepath.Dir(defaultPiAgentDir())))
	plan, failure := planner.BuildPlan(t.Context(), launchplan.BuildRequest{CLIType: contract.CLITypePi, Origin: launchplan.OriginRestart})
	if failure != nil {
		t.Fatalf("restart of actual desktop recipe: %+v", failure)
	}
	defer plan.Secrets.Dispose()
	if plan.Recipe.ModelRef != key {
		t.Fatalf("restart lost native login preset: %+v", plan.Recipe)
	}
}

// R2+R3 真实边界回归：zsh startup 文件重导出 PI_CODING_AGENT_DIR/API 凭据，
// 同时 EnvVars 面板把 HOME 改到另一目录。登录分支必须在最终 CLI 执行边界
// 钉住 canonical agent root 并剥离凭据（env 钉定 + shell 初始化后的 env 前缀）。
// 全部使用临时 HOME/合成凭据；不读取真实 auth、不访问任何提供商网络。
func TestPiLoginDesktopRealShellStartupReAnchor(t *testing.T) {
	home := isolatedHome(t)
	canonical := filepath.Join(home, ".pi", "agent")
	alt := t.TempDir()   // .zshenv 试图把 Pi 导向的另一 agent root
	homeB := t.TempDir() // EnvVars 面板把子进程 HOME 改到 B（R2）
	if err := os.MkdirAll(alt, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".zshenv"),
		[]byte("export PI_CODING_AGENT_DIR='"+alt+"'\nexport OPENAI_API_KEY='fixture-shell-startup-key'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// ZDOTDIR 显式指向 A：无论子进程 HOME 是 A 还是 B，zsh 都会加载我们的 fixture 启动文件。
	t.Setenv("ZDOTDIR", home)

	app, configDir := newTestAppWithConfigDir(t)
	app.Settings = settings.NewService(configDir)
	if err := app.Settings.Load(); err != nil {
		t.Fatal(err)
	}
	var defaultsErr error
	app.remoteDefaults, defaultsErr = launchplan.NewDefaultStore(app.Settings)
	if defaultsErr != nil {
		t.Fatal(defaultsErr)
	}
	if err := app.EnvVars.Set("HOME", homeB); err != nil {
		t.Fatal(err)
	}
	app.ctx = t.Context()
	app.Capabilities = platform.CurrentCapabilities()
	app.processRegistry = processcap.NewRegistry()
	wireTestControl(t, app)
	app.WebUI = webui.NewService(app.Log, t.TempDir())
	preset, key, auth := piLoginFixture(t, app.Config)
	before, _ := os.ReadFile(auth)

	bin := t.TempDir()
	trace := filepath.Join(bin, "trace")
	// fixture 直接观测最终进程环境：agent root、被剥离的凭据、argv。
	script := "#!/bin/sh\nprintf '%s\n' \"$@\" > \"$PI_LOGIN_FIXTURE_TRACE\"\nprintf 'agent=%s key=%s\n' \"$PI_CODING_AGENT_DIR\" \"${OPENAI_API_KEY-unset}\" >> \"$PI_LOGIN_FIXTURE_TRACE\"\nexec /bin/sleep 30\n"
	if err := os.WriteFile(filepath.Join(bin, "pi"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PI_LOGIN_FIXTURE_TRACE", trace)
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir()) // CodeBox 进程自身的旧覆盖也必须被钉定值替换
	t.Setenv("OPENAI_API_KEY", "fixture-inherited-key")
	app.CLIResolver = platform.NewCLIResolver(app.Capabilities)

	sid, err := app.LaunchPiSession(key, config.PiLoginProviderID(preset.Provider), "embedded", bin, "/bin/zsh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Pty.Close(sid) })
	deadline := time.Now().Add(8 * time.Second)
	var body []byte
	for time.Now().Before(deadline) {
		body, err = os.ReadFile(trace)
		if err == nil && strings.Contains(string(body), "agent=") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	want := "--provider\nopenai-codex\n--model\nnative-model\n--thinking\nmax\nagent=" + canonical + " key=unset\n"
	if string(body) != want {
		t.Fatalf("shell-startup CLI fixture received %q, want %q (alt=%s homeB=%s)", body, want, alt, homeB)
	}
	after, _ := os.ReadFile(auth)
	if string(before) != string(after) {
		t.Fatal("real launch modified auth")
	}
	if _, err := os.Stat(filepath.Join(canonical, "models.json")); !os.IsNotExist(err) {
		t.Fatal("real login launch wrote managed models")
	}
	if !app.Pty.IsRunning(sid) {
		t.Fatal("Pi fixture process not active")
	}
}
