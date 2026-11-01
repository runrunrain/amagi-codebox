package main

// app_wsl_unified_test.go — P2-B 统一配置管理的 App 入口级回归：真实 App +
// 真实协调器（假探测面 + 可注入 Home 来源），覆盖任务验收链——启用 → 会话
// 启动供给 → 新增键纳入 → 来源删除移除 → 保留本地冻结 → 换代失效 → 停用
// 停止纳入新项。不安装任何真实 CLI、不改真实用户配置（全部 t.TempDir）。

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/session"
	"amagi-codebox/internal/wslsupply"
)

// ---------------------------------------------------------------------------
// 夹具：假探测面 + 可注入 Home 的完整协调器（S4 适配器 + skills/资源作用域）
// ---------------------------------------------------------------------------

type unifiedTestProbes struct {
	mu       sync.Mutex
	identity platform.WSLUserIdentity
	uncRoot  string
	// Linux chmod 的目标权限不等于宿主 Windows 的 os.FileMode。按文件身份
	// 记录已执行的 chmod；rename 后仍匹配同一文件，未 chmod 的新文件不继承。
	modes []unifiedTestMode
	// failFromCall：第 N 次身份探测起返回失败（模拟 UNC/身份短暂不可达；
	// 0=永不失败）。recovered 置位后恢复——用于验证部分启用失败后的自动续接。
	failFromCall int
	calls        int
	recovered    bool
}

type unifiedTestMode struct {
	info os.FileInfo
	mode string
}

// ident 返回当前身份（带计数与故障注入；并发安全）。
func (p *unifiedTestProbes) ident() platform.WSLUserIdentity {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.failFromCall > 0 && p.calls >= p.failFromCall && !p.recovered {
		return platform.WSLUserIdentity{}
	}
	return p.identity
}

func (p *unifiedTestProbes) setIdent(id platform.WSLUserIdentity) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.identity = id
}

// setIdentFailureFrom 设置从第 N 次调用起的身份故障（N≥1）。
func (p *unifiedTestProbes) setIdentFailureFrom(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failFromCall = n
}

// recoverIdent 恢复身份探测（环境恢复）。
func (p *unifiedTestProbes) recoverIdent() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.recovered = true
}

func (p *unifiedTestProbes) probes() wslsupply.Probes {
	return wslsupply.Probes{
		Identity: func(context.Context, string) platform.WSLUserIdentity { return p.ident() },
		ToUNC: func(_, linuxPath string) string {
			return filepath.Join(p.uncRoot, filepath.FromSlash(strings.TrimPrefix(linuxPath, "/")))
		},
		Chmod: func(_ context.Context, _, mode string, paths ...string) error {
			for _, path := range paths {
				unc := filepath.Join(p.uncRoot, filepath.FromSlash(strings.TrimPrefix(path, "/")))
				info, err := os.Lstat(unc)
				if err != nil {
					return err
				}
				if !info.Mode().IsRegular() && !info.IsDir() {
					return fmt.Errorf("fixture chmod refuses non-regular path: %s", path)
				}
				// Windows FileInfo 延迟读取文件ID；必须在路径尚未rename时固化，
				// 否则稍后 SameFile 会尝试打开已不存在的旧路径。
				if !os.SameFile(info, info) {
					return fmt.Errorf("fixture chmod cannot pin file identity: %s", path)
				}
				p.mu.Lock()
				index := len(p.modes)
				for i, applied := range p.modes {
					if os.SameFile(applied.info, info) {
						index = i
						break
					}
				}
				applied := unifiedTestMode{info: info, mode: mode}
				if index == len(p.modes) {
					p.modes = append(p.modes, applied)
				} else {
					p.modes[index] = applied
				}
				p.mu.Unlock()
			}
			return nil
		},
		StatMode: func(_ context.Context, _, linuxPath string) (string, bool) {
			unc := filepath.Join(p.uncRoot, filepath.FromSlash(strings.TrimPrefix(linuxPath, "/")))
			info, err := os.Stat(unc)
			// 目录和文件都由目标 Linux 权限模型检查，不能拿宿主 Windows 的
			// 0777/0666 位当成 chmod 700/600 没有生效。
			if err != nil || (!info.Mode().IsRegular() && !info.IsDir()) {
				return "", false
			}
			p.mu.Lock()
			defer p.mu.Unlock()
			for _, applied := range p.modes {
				if os.SameFile(applied.info, info) {
					return applied.mode, true
				}
			}
			return fmt.Sprintf("%o", info.Mode().Perm()), true
		},
	}
}

// 假目标权限随真实文件身份迁移，且必须由 Chmod 明确设置；不能硬编码安全位
// 来绕过敏感提交检查，也不能把旧路径的权限授给同名替换文件。
func TestWSLUnifiedProbeModesFollowFileIdentity(t *testing.T) {
	p := &unifiedTestProbes{uncRoot: t.TempDir()}
	probe := p.probes()
	ctx := context.Background()
	const source = "/home/u/source.json"
	const moved = "/home/u/moved.json"
	unc := probe.ToUNC("Ubuntu", source)
	writeUnifiedFile(t, unc, "fixture")
	if mode, ok := probe.StatMode(ctx, "Ubuntu", source); !ok || mode == "640" {
		t.Fatalf("unmodified file must not inherit synthetic chmod, mode=%q ok=%v", mode, ok)
	}
	if err := probe.Chmod(ctx, "Ubuntu", "640", source); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(unc, probe.ToUNC("Ubuntu", moved)); err != nil {
		t.Fatal(err)
	}
	if mode, ok := probe.StatMode(ctx, "Ubuntu", moved); !ok || mode != "640" {
		t.Fatalf("chmod must survive rename, mode=%q ok=%v", mode, ok)
	}
	writeUnifiedFile(t, unc, "replacement")
	if mode, ok := probe.StatMode(ctx, "Ubuntu", source); !ok || mode == "640" {
		t.Fatalf("replacement must not inherit renamed file's mode, mode=%q ok=%v", mode, ok)
	}
	if err := probe.Chmod(ctx, "Ubuntu", "644", source); err != nil {
		t.Fatal(err)
	}
	if mode, ok := probe.StatMode(ctx, "Ubuntu", moved); !ok || mode != "640" {
		t.Fatalf("chmod of replacement must not erase moved file's mode, mode=%q ok=%v", mode, ok)
	}
	if err := probe.Chmod(ctx, "Ubuntu", "600", moved); err != nil {
		t.Fatal(err)
	}
	if mode, ok := probe.StatMode(ctx, "Ubuntu", moved); !ok || mode != "600" {
		t.Fatalf("latest chmod after rename must replace prior mode, mode=%q ok=%v", mode, ok)
	}
	if mode, ok := probe.StatMode(ctx, "Ubuntu", source); !ok || mode == "640" || mode == "600" {
		t.Fatalf("new file at old path must not inherit prior chmod, mode=%q ok=%v", mode, ok)
	}
	if err := probe.Chmod(ctx, "Ubuntu", "710", "/home/u"); err != nil {
		t.Fatal(err)
	}
	if mode, ok := probe.StatMode(ctx, "Ubuntu", "/home/u"); !ok || mode != "710" {
		t.Fatalf("directory must report the applied Linux mode, mode=%q ok=%v", mode, ok)
	}
}

// recordingCoordinator 包装真实协调器并记录 RunUnits 调用（嵌入提升全部
// 能力面，App 的接口断言照常命中）。
type recordingCoordinator struct {
	*wslsupply.Coordinator
	mu      sync.Mutex
	calls   []string
	rescans int
}

func (r *recordingCoordinator) RunUnits(ctx context.Context, distro string, cli wslsupply.CLI, reason wslsupply.Reason, units []wslsupply.UnitID) (wslsupply.RunResult, error) {
	r.mu.Lock()
	r.calls = append(r.calls, fmt.Sprintf("%s|%s|%d", cli, reason, len(units)))
	r.mu.Unlock()
	return r.Coordinator.RunUnits(ctx, distro, cli, reason, units)
}

// RescanSkills 计数后台循环触发的技能重扫（F1 有节制调度断言）。
func (r *recordingCoordinator) RescanSkills() bool {
	r.mu.Lock()
	r.rescans++
	r.mu.Unlock()
	return r.Coordinator.RescanSkills()
}

func (r *recordingCoordinator) rescanCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rescans
}

func (r *recordingCoordinator) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func (r *recordingCoordinator) hasCall(prefix string) bool {
	for _, c := range r.snapshot() {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// unifiedFixture 是一轮入口级测试的完整装配。
type unifiedFixture struct {
	app     *App
	coord   *recordingCoordinator
	probes  *unifiedTestProbes
	home    string // Windows 侧 Home（来源文件根）
	uncRoot string // distro UNC 落点根（目标 = uncRoot + identity.Home）
	target  string // distro 用户 Home 的 UNC 落点（随身份变化重算）
	state   string // 协调器状态清单路径（降级断言直读）
}

// setIdentity 模拟目标身份变化（machine-id/Home 变化 → 新代；可选新 HOME），
// 并重算目标落点。
func (f *unifiedFixture) setIdentity(machineID, home string) {
	f.probes.setIdent(platform.WSLUserIdentity{UID: "1000", Home: home, MachineID: machineID, OK: true})
	f.target = filepath.Join(f.uncRoot, filepath.FromSlash(strings.TrimPrefix(home, "/")))
}

func writeUnifiedFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newUnifiedFixture(t *testing.T) *unifiedFixture {
	t.Helper()
	home := t.TempDir()
	target := t.TempDir()
	probes := &unifiedTestProbes{
		identity: platform.WSLUserIdentity{UID: "1000", Home: "/home/u", MachineID: "machine-unified", OK: true},
		uncRoot:  target,
	}
	// 来源：claude mcp（远程 URL，无需转换事实）+ settings 白名单 + codex TOML。
	writeUnifiedFile(t, filepath.Join(home, ".claude.json"),
		`{"mcpServers": {"web": {"url": "https://mcp.example/sse"}}, "projects": {"/win": {}}}`)
	writeUnifiedFile(t, filepath.Join(home, ".claude", "settings.json"), `{"model": "sonnet", "permissions": {"allow": ["Bash"]}}`)
	writeUnifiedFile(t, filepath.Join(home, ".codex", "config.toml"),
		"[mcp_servers.web]\nurl = \"https://mcp.example/sse\"\n\n[profiles.fast]\nmodel = \"gpt-5\"\n\n[trust]\ntrusted = [\"C:/proj\"]\n")
	// 资源与技能。
	writeUnifiedFile(t, filepath.Join(home, ".claude", "CLAUDE.md"), "# claude global")
	writeUnifiedFile(t, filepath.Join(home, ".claude", "commands", "review.md"), "# review")
	writeUnifiedFile(t, filepath.Join(home, ".agents", "skills", "alpha", "SKILL.md"), "# alpha")

	origHome, origDistro, origRunning := wslS4HomeDirFn, wslDefaultDistroFn, wslDistroRunningFn
	t.Cleanup(func() {
		wslS4HomeDirFn = origHome
		wslDefaultDistroFn = origDistro
		wslDistroRunningFn = origRunning
	})
	wslS4HomeDirFn = func() (string, error) { return home, nil }
	wslDefaultDistroFn = func([]string) string { return "Ubuntu" }
	wslDistroRunningFn = func(string) bool { return true }
	// 确定性收尾：先停统一管理后台循环（join 后不再有循环侧调度），再排空
	// 本夹具调度的全部后台推送，最后还原全局接缝与临时目录（t.Cleanup 后进
	// 先出——LIFO 保证下述顺序）。
	t.Cleanup(waitForWSLSupplyPushIdleForTest)

	app := newTestApp(t)
	t.Cleanup(app.stopWSLUnifiedReconcileLoop)
	stateDir := t.TempDir()
	coord := &recordingCoordinator{Coordinator: wslsupply.NewCoordinator(stateDir, nil, probes.probes())}
	registerS4Adapters(coord.Coordinator, app)
	coord.RegisterSkills(wslsupply.NewSkillsScope(filepath.Join(home, ".agents", "skills"), wslsupply.DefaultSkillsScanLimits()))
	app.WSLSupply = coord
	// 与生产装配（newWSLSupplyCoordinator）一致：扫描完成回调触发统一对账推送。
	coord.SetScanCompletionCallback(app.wslUnifiedOnScanDone)
	return &unifiedFixture{app: app, coord: coord, probes: probes, home: home, uncRoot: target, target: filepath.Join(target, "home", "u"), state: filepath.Join(stateDir, wslsupply.StateFileName)}
}

// waitUntil 有界轮询（后台推送/扫描完成是异步的；确定性终态 + 超时兜底）。
func waitUntil(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timeout: " + msg)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func readUnifiedTargetJSON(t *testing.T, f *unifiedFixture, rel string) map[string]any {
	t.Helper()
	out, ok := unifiedTargetJSONOk(f, rel)
	if !ok {
		t.Fatalf("read target %s: missing or unparsable", rel)
	}
	return out
}

// unifiedTargetJSONOk 是轮询友好的非致命读（缺失/未就绪返回 false）。
func unifiedTargetJSONOk(f *unifiedFixture, rel string) (map[string]any, bool) {
	data, err := os.ReadFile(filepath.Join(f.target, filepath.FromSlash(rel)))
	if err != nil {
		return nil, false
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, false
	}
	return out, true
}

func unifiedTargetExists(f *unifiedFixture, rel string) bool {
	_, err := os.Stat(filepath.Join(f.target, filepath.FromSlash(rel)))
	return err == nil
}

// mcpServerURL 是轮询友好的非致命取值。
func mcpServerURL(f *unifiedFixture, name string) (string, bool) {
	root, ok := unifiedTargetJSONOk(f, ".claude.json")
	if !ok {
		return "", false
	}
	servers, _ := root["mcpServers"].(map[string]any)
	web, _ := servers[name].(map[string]any)
	if web == nil {
		return "", false
	}
	url, _ := web["url"].(string)
	return url, url != ""
}

// ---------------------------------------------------------------------------
// 主链路：启用 → 供给 → 新增键 → 来源删除 → 保留本地
// ---------------------------------------------------------------------------

func TestWSLUnifiedSupplyEndToEnd(t *testing.T) {
	f := newUnifiedFixture(t)

	// 启用前：状态面显示未启用；claude 启动补漏不调 RunUnits（无授权）。
	if st := f.app.GetWSLConfigSupplyStatus(); st.Unified {
		t.Fatal("unified must default to false")
	}
	spec := platform.ResolvedLaunchSpec{BootstrapMode: platform.BootstrapWSL, AppType: "claudecode"}
	if err := runWSLSharedFilesForSpec(context.Background(), f.coord, spec); err != nil {
		t.Fatalf("launch before consent: %v", err)
	}
	if f.coord.hasCall("claude|") {
		t.Fatal("launch must not run claude units before consent (no awaiting-consent spam)")
	}

	// 启用：一次授权 → 扫描启动 + 推送调度。
	outcomes, err := f.app.EnableWSLUnifiedSupply()
	if err != nil {
		t.Fatalf("EnableWSLUnifiedSupply: %v (outcomes=%+v)", err, outcomes)
	}
	if len(outcomes) == 0 {
		t.Fatal("enable must return per-unit outcomes")
	}
	waitUntil(t, func() bool { return f.app.GetWSLConfigSupplyStatus().Unified }, "unified flag must be visible")

	// 供给收敛：claude mcp/settings、codex config、skills、资源全部写入。
	waitUntil(t, func() bool {
		_, ok := mcpServerURL(f, "web")
		return ok
	}, "claude mcp server web must be supplied")
	waitUntil(t, func() bool {
		settings, ok := unifiedTargetJSONOk(f, ".claude/settings.json")
		return ok && settings["model"] == "sonnet"
	}, "claude settings whitelist field must be supplied")
	waitUntil(t, func() bool {
		data, err := os.ReadFile(filepath.Join(f.target, ".codex", "config.toml"))
		return err == nil && strings.Contains(string(data), "mcp_servers.web") && strings.Contains(string(data), "profiles.fast")
	}, "codex managed sections must be supplied")
	waitUntil(t, func() bool {
		return unifiedTargetExists(f, ".agents/skills/alpha/SKILL.md")
	}, "shared skill alpha must be supplied by scan-completion reconcile")
	waitUntil(t, func() bool {
		return unifiedTargetExists(f, ".claude/CLAUDE.md")
	}, "claude resource CLAUDE.md must be supplied")
	waitUntil(t, func() bool {
		return unifiedTargetExists(f, ".claude/commands/review.md")
	}, "claude commands resource must be supplied")
	// LocalOnly 防线：projects/trust/permissions 绝不跨界。
	if root := readUnifiedTargetJSON(t, f, ".claude.json"); root["projects"] != nil {
		t.Fatal("claude projects must never cross the boundary")
	}
	if settings := readUnifiedTargetJSON(t, f, ".claude/settings.json"); settings["permissions"] != nil {
		t.Fatal("claude permissions must never cross the boundary")
	}
	codexBytes, err := os.ReadFile(filepath.Join(f.target, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("read codex target: %v", err)
	}
	if strings.Contains(string(codexBytes), "[trust]") {
		t.Fatal("codex trust section is LocalOnly and must never be written to the target")
	}

	// 会话启动供给（lazy）：已授权单元随启动重跑（Fresh 幂等）。
	if err := runWSLSharedFilesForSpec(context.Background(), f.coord, spec); err != nil {
		t.Fatalf("launch after consent: %v", err)
	}
	waitUntil(t, func() bool { return f.coord.hasCall("claude|launch|") }, "claude units must run at launch")

	// Windows 侧新增键：下一次推送自动纳入（显式键物化，无通配持久化）。
	writeUnifiedFile(t, filepath.Join(f.home, ".claude.json"),
		`{"mcpServers": {"web": {"url": "https://mcp.example/sse"}, "extra": {"url": "https://mcp2.example/sse"}}}`)
	f.app.doPushWSLSupply()
	waitUntil(t, func() bool {
		_, ok := mcpServerURL(f, "extra")
		return ok
	}, "new Windows-side key must be imported on next push")

	// 来源删除：托管且未手改的键随下次推送移除。
	writeUnifiedFile(t, filepath.Join(f.home, ".claude.json"),
		`{"mcpServers": {"extra": {"url": "https://mcp2.example/sse"}}}`)
	f.app.doPushWSLSupply()
	waitUntil(t, func() bool {
		root, ok := unifiedTargetJSONOk(f, ".claude.json")
		if !ok {
			return false
		}
		servers, _ := root["mcpServers"].(map[string]any)
		_, stillThere := servers["web"]
		return !stillThere
	}, "source-deleted managed key must be removed on next push")

	// 保留本地（keep-local）：单元冻结，来源变化不再覆盖 WSL。
	if err := f.app.KeepWSLCliConfigUnitLocal("claude", "claude/settings"); err != nil {
		t.Fatalf("keep local: %v", err)
	}
	writeUnifiedFile(t, filepath.Join(f.home, ".claude", "settings.json"), `{"model": "opus"}`)
	f.app.doPushWSLSupply()
	time.Sleep(150 * time.Millisecond) // 推送同步执行；短暂让渡确保无迟到写入
	if settings := readUnifiedTargetJSON(t, f, ".claude/settings.json"); settings["model"] != "sonnet" {
		t.Fatalf("keep-local unit must stay frozen, got %v", settings["model"])
	}

	// 状态清单永不持久化通配键（旧版本降级安全）。
	stateBytes, err := os.ReadFile(f.state)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if strings.Contains(string(stateBytes), `"*"`) {
		t.Fatal("state manifest must never persist a wildcard selection key (downgrade safety)")
	}
}

// ---------------------------------------------------------------------------
// 换代失效与停用
// ---------------------------------------------------------------------------

func TestWSLUnifiedSupplyGenerationChangeAndDisable(t *testing.T) {
	f := newUnifiedFixture(t)
	if _, err := f.app.EnableWSLUnifiedSupply(); err != nil {
		t.Fatalf("enable: %v", err)
	}
	waitUntil(t, func() bool {
		_, ok := mcpServerURL(f, "web")
		return ok
	}, "claude mcp must be supplied after enable")

	// 停用：新技能不再自动纳入；自动化停止，既有副本保持现状。
	writeUnifiedFile(t, filepath.Join(f.home, ".agents", "skills", "beta", "SKILL.md"), "# beta")
	if err := f.app.DisableWSLUnifiedSupply(); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if st := f.app.GetWSLConfigSupplyStatus(); st.Unified {
		t.Fatal("unified must be off after disable")
	}
	// 强制重扫（扫描完成回调触发，但未启用统一管理 → 不再对账纳入新项）。
	f.coord.RescanSkills()
	waitUntil(t, func() bool {
		for _, m := range f.coord.SkillsSnapshot().Skills {
			if m.Name == "beta" {
				return true
			}
		}
		return false
	}, "rescan must see the new skill")
	f.app.doPushWSLSupply()
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(f.target, ".agents", "skills", "beta", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("new skill must not be auto-imported after disable")
	}
	// 停用后自动化停止：web 来源更新不再自动落地（副本保持现状；手动面仍可用）。
	writeUnifiedFile(t, filepath.Join(f.home, ".claude.json"),
		`{"mcpServers": {"web": {"url": "https://v2.example/sse"}}}`)
	f.app.doPushWSLSupply()
	time.Sleep(300 * time.Millisecond)
	if url, ok := mcpServerURL(f, "web"); ok && url == "https://v2.example/sse" {
		t.Fatal("disable must stop automatic refresh of managed units (copies stay as-is)")
	}

	// 换代（machine-id 变化 = 重装/换身份）：Unified 失效；旧代授权不写新身份。
	f.setIdentity("machine-reinstalled", "/home/u")
	if st := f.app.GetWSLConfigSupplyStatus(); st.Unified {
		t.Fatal("unified must not survive generation change (SA-02)")
	}
	writeUnifiedFile(t, filepath.Join(f.home, ".claude.json"),
		`{"mcpServers": {"web": {"url": "https://v3.example/sse"}}}`)
	f.app.doPushWSLSupply()
	time.Sleep(300 * time.Millisecond)
	if url, ok := mcpServerURL(f, "web"); ok && url == "https://v3.example/sse" {
		t.Fatal("old-generation authorization must not write to the new identity")
	}
	// 模拟重装后的新 HOME（目标侧空），重新启用后恢复供给。
	f.setIdentity("machine-reinstalled", "/home/u2")
	// 重新启用（新代）后恢复。
	if _, err := f.app.EnableWSLUnifiedSupply(); err != nil {
		t.Fatalf("re-enable on new generation: %v", err)
	}
	waitUntil(t, func() bool {
		url, ok := mcpServerURL(f, "web")
		return ok && url == "https://v3.example/sse"
	}, "re-enable must resume supply on the new generation")
}

// ---------------------------------------------------------------------------
// 启动补漏：无授权不调 RunUnits；授权后 lazy 重跑且失败不阻断
// ---------------------------------------------------------------------------

func TestWSLCliConfigAtLaunchRequiresConsent(t *testing.T) {
	f := newUnifiedFixture(t)
	// 未启用统一管理、无授权：codex 启动不写 awaiting-consent、不调 RunUnits。
	spec := platform.ResolvedLaunchSpec{BootstrapMode: platform.BootstrapWSL, AppType: "codex"}
	if err := runWSLSharedFilesForSpec(context.Background(), f.coord, spec); err != nil {
		t.Fatalf("launch: %v", err)
	}
	if f.coord.hasCall("codex|") {
		t.Fatal("codex units must not run at launch without consent")
	}
	// 启用后再启动：授权单元 lazy 运行。
	if _, err := f.app.EnableWSLUnifiedSupply(); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return f.coord.hasCall("codex|save|") }, "enable push must cover codex units")
	if err := runWSLSharedFilesForSpec(context.Background(), f.coord, spec); err != nil {
		t.Fatalf("launch after enable: %v", err)
	}
	waitUntil(t, func() bool { return f.coord.hasCall("codex|launch|") }, "codex units must run at launch after consent")
	// 非 claude/codex appType（pi）：统一启动补漏不越面。
	piSpec := platform.ResolvedLaunchSpec{BootstrapMode: platform.BootstrapWSL, AppType: "pi"}
	if err := runWSLSharedFilesForSpec(context.Background(), f.coord, piSpec); err != nil {
		t.Fatalf("pi launch: %v", err)
	}
	if f.coord.hasCall("pi|launch|") {
		t.Fatal("pi units are handled by their own launch gate, not the unified hook")
	}
}

// ---------------------------------------------------------------------------
// 状态面：Unified 标记与扫描完成回调驱动的对账
// ---------------------------------------------------------------------------

func TestWSLUnifiedScanCompletionTriggersReconcile(t *testing.T) {
	f := newUnifiedFixture(t)
	// 启用后扫描是异步的：技能写入必须等到「扫描完成回调 → 推送」之后发生，
	// 而不是启用时那轮早于扫描结束的空转。
	if _, err := f.app.EnableWSLUnifiedSupply(); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool {
		_, err := os.Stat(filepath.Join(f.target, ".agents", "skills", "alpha", "SKILL.md"))
		return err == nil
	}, "skill must be supplied after scan-completion-triggered reconcile")
	// 扫描快照确实完成（非 Running）。
	if snap := f.coord.SkillsSnapshot(); snap.Running || !snap.Started {
		t.Fatalf("skills scan must have completed: %+v", snap.Progress)
	}
}

// ---------------------------------------------------------------------------
// P4 修复回归：F1 周期重扫跟随 / F2 会话阻挡延期补推 / F3 部分启用失败
// 自动续接 / 直接换代不继承
// ---------------------------------------------------------------------------

// setWSLUnifiedLoopIntervalsForTest 注入统一管理后台循环节拍（测试缩短；
// 注册还原清理）。必须在 Enable/启动对账之前调用（循环启动时快照节拍）。
func setWSLUnifiedLoopIntervalsForTest(t *testing.T, pendingTick, rescan time.Duration) {
	t.Helper()
	origPending, origRescan := wslUnifiedPendingTickInterval, wslUnifiedRescanInterval
	wslUnifiedPendingTickInterval, wslUnifiedRescanInterval = pendingTick, rescan
	t.Cleanup(func() {
		wslUnifiedPendingTickInterval, wslUnifiedRescanInterval = origPending, origRescan
	})
}

// fakeUnifiedVisionSource 是固定分组 #2（视觉清单共享）的测试内来源
// （不读真实导出文件；字节由测试提供）。
type fakeUnifiedVisionSource struct{ bytes []byte }

func (s fakeUnifiedVisionSource) VisionExportBytes() ([]byte, error) { return s.bytes, nil }
func (s fakeUnifiedVisionSource) ModalityKBBytes() ([]byte, error)   { return nil, nil }

// TestWSLUnifiedPeriodicRescanFollowsSourceChanges（F1）：首次扫描完成后，
// Windows 侧修改 + 新增技能与 Claude 资源——不点任何显式「检查」，仅靠真实
// 后台对账周期重扫来源自动跟随；来源删除仍按既有保护（不自动删目标副本）；
// 调度有节制（重扫只由定时器驱动，无「扫描完成→推送→再扫描」循环）。
func TestWSLUnifiedPeriodicRescanFollowsSourceChanges(t *testing.T) {
	f := newUnifiedFixture(t)
	setWSLUnifiedLoopIntervalsForTest(t, 10*time.Minute, 200*time.Millisecond)
	if _, err := f.app.EnableWSLUnifiedSupply(); err != nil {
		t.Fatalf("enable: %v", err)
	}
	waitUntil(t, func() bool { return unifiedTargetExists(f, ".agents/skills/alpha/SKILL.md") },
		"initial scan-completion reconcile must supply alpha")

	writeUnifiedFile(t, filepath.Join(f.home, ".agents", "skills", "alpha", "SKILL.md"), "# alpha v2")
	writeUnifiedFile(t, filepath.Join(f.home, ".agents", "skills", "beta", "SKILL.md"), "# beta")
	writeUnifiedFile(t, filepath.Join(f.home, ".claude", "CLAUDE.md"), "# claude global v2")
	waitUntil(t, func() bool {
		data, err := os.ReadFile(filepath.Join(f.target, ".agents", "skills", "alpha", "SKILL.md"))
		return err == nil && string(data) == "# alpha v2"
	}, "modified skill must follow via the automatic rescan cycle (no explicit check)")
	waitUntil(t, func() bool { return unifiedTargetExists(f, ".agents/skills/beta/SKILL.md") },
		"new skill must be auto-imported by the automatic rescan cycle")
	waitUntil(t, func() bool {
		data, err := os.ReadFile(filepath.Join(f.target, ".claude", "CLAUDE.md"))
		return err == nil && string(data) == "# claude global v2"
	}, "modified claude resource must follow via the automatic rescan cycle")

	// 来源删除：快照刷新后自动对账只遍历新快照——已托管旧单元不自动删除
	//（source-deleted 删除需确认，既有保护不变）。
	if err := os.RemoveAll(filepath.Join(f.home, ".agents", "skills", "beta")); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool {
		for _, m := range f.coord.SkillsSnapshot().Skills {
			if m.Name == "beta" {
				return false
			}
		}
		return true
	}, "automatic rescan must refresh the snapshot (beta dropped after source deletion)")
	time.Sleep(600 * time.Millisecond) // 覆盖至少两个重扫周期后的对账
	if !unifiedTargetExists(f, ".agents/skills/beta/SKILL.md") {
		t.Fatal("source-deleted managed skill must stay in WSL (deletion needs confirmation)")
	}

	// 有节制调度：收敛后重扫计数只按定时器节拍增长（若推送触发重扫形成
	// 循环，计数会爆炸）。
	base := f.coord.rescanCount()
	time.Sleep(900 * time.Millisecond)
	got := f.coord.rescanCount()
	if got < base+1 {
		t.Fatalf("periodic rescan must keep running (real automatic cycle): base=%d got=%d", base, got)
	}
	if got > base+6 {
		t.Fatalf("rescan cadence must stay timer-bounded (no scan→push→scan loop): base=%d got=%d", base, got)
	}
}

// TestWSLUnifiedPushDeferredUntilActiveSessionsEnd（F2）：任一内嵌会话 running
// 时，启用推送与扫描完成推送都被守卫挡下且不写目标；会话退出后不触发任何
// 新的保存，延期对账由后台心跳自动补齐收敛。
func TestWSLUnifiedPushDeferredUntilActiveSessionsEnd(t *testing.T) {
	f := newUnifiedFixture(t)
	setWSLUnifiedLoopIntervalsForTest(t, 80*time.Millisecond, 10*time.Minute)
	sess := f.app.Sessions.Create(session.AppTypeClaudeCode, "p", "preset", "m", session.ModeEmbedded, t.TempDir())
	if _, err := f.app.EnableWSLUnifiedSupply(); err != nil {
		t.Fatalf("enable: %v", err)
	}
	waitUntil(t, func() bool {
		snap := f.coord.SkillsSnapshot()
		return snap.Started && !snap.Running
	}, "background scans must complete while the session is running")
	waitForWSLSupplyPushIdleForTest()  // 启用推送 + 扫描完成推送均已执行并被挡下
	time.Sleep(200 * time.Millisecond) // pending 心跳空转窗口：活动会话仍不得写入
	for _, rel := range []string{".agents/skills/alpha/SKILL.md", ".claude/CLAUDE.md"} {
		if unifiedTargetExists(f, rel) {
			t.Fatalf("active session must block background writes: %s", rel)
		}
	}
	if _, ok := mcpServerURL(f, "web"); ok {
		t.Fatal("active session must block claude keyed-unit writes")
	}
	// 会话退出：无需另一次保存，延期对账自动补齐。
	f.app.Sessions.MarkExited(sess.ID)
	waitUntil(t, func() bool { return unifiedTargetExists(f, ".agents/skills/alpha/SKILL.md") },
		"deferred reconcile must supply skills after session end without another save")
	waitUntil(t, func() bool { _, ok := mcpServerURL(f, "web"); return ok },
		"deferred reconcile must supply keyed units after session end")
}

// TestWSLUnifiedPartialEnableFailureAutoContinues（F3）：SetTargetUnified 已提交
// 后第二固定分组（视觉清单）身份短暂失败 → 部分启用失败但 Unified 标记保持；
// 环境恢复后不重新启用、不额外敏感授权，固定单元随自动对账幂等续接收敛；
// detached 单元不被续接复活。
func TestWSLUnifiedPartialEnableFailureAutoContinues(t *testing.T) {
	f := newUnifiedFixture(t)
	f.coord.RegisterProjection(wslsupply.NewVisionExportAdapter(
		fakeUnifiedVisionSource{bytes: []byte(`{"schemaVersion":1,"providers":[]}`)}))
	setWSLUnifiedLoopIntervalsForTest(t, 80*time.Millisecond, 200*time.Millisecond)
	// 探测 #1（SetTargetUnified）成功；从 #2 起（固定分组授权）身份不可达。
	f.probes.setIdentFailureFrom(2)
	outcomes, err := f.app.EnableWSLUnifiedSupply()
	if err == nil || !strings.Contains(err.Error(), "部分授权失败") {
		t.Fatalf("partial enable must fail loudly, err=%v", err)
	}
	if !f.app.GetWSLConfigSupplyStatus().Unified {
		t.Fatal("unified flag must stay persisted after partial failure (no destructive rollback)")
	}
	if len(outcomes) == 0 {
		t.Fatal("partial enable must still return per-unit outcomes")
	}
	// 环境恢复：自动续接（无需重新启用/重新授权）。
	f.probes.recoverIdent()
	waitUntil(t, func() bool {
		data, err := os.ReadFile(filepath.Join(f.target, ".agents", "amagi-media-models.json"))
		return err == nil && strings.Contains(string(data), "schemaVersion")
	}, "fixed group #2 (vision) must be auto-continued after recovery without re-enable")
	waitUntil(t, func() bool { _, ok := mcpServerURL(f, "web"); return ok },
		"later fixed groups (claude) must be auto-continued after recovery")
	waitUntil(t, func() bool {
		data, err := os.ReadFile(filepath.Join(f.target, ".codex", "config.toml"))
		return err == nil && strings.Contains(string(data), "mcp_servers.web")
	}, "codex fixed group must be auto-continued after recovery")
	waitUntil(t, func() bool { return unifiedTargetExists(f, ".agents/skills/alpha/SKILL.md") },
		"tree reconcile must resume after recovery")
	// 续接不复活 detached：保留本地的单元在后续自动周期中不被重新授权/改写。
	if err := f.app.KeepWSLCliConfigUnitLocal("claude", "claude/settings"); err != nil {
		t.Fatalf("keep local: %v", err)
	}
	writeUnifiedFile(t, filepath.Join(f.home, ".claude", "settings.json"), `{"model": "opus"}`)
	time.Sleep(700 * time.Millisecond) // 覆盖多个自动周期
	if settings := readUnifiedTargetJSON(t, f, ".claude/settings.json"); settings["model"] != "sonnet" {
		t.Fatalf("detached unit must stay frozen through automatic continuation, got %v", settings["model"])
	}
	detached := false
	for _, u := range f.app.GetWSLCliSupplyStatus("claude").Units {
		if u.Unit == "claude/settings" && u.State == "detached" {
			detached = true
		}
	}
	if !detached {
		t.Fatal("claude/settings must remain detached (not revived by continuation)")
	}
}

// TestWSLUnifiedGenerationChangeWhileEnabledDoesNotInherit：授权仍开启时直接
// 换代（未先停用，重装后新 HOME）：旧代授权不写新身份——自动周期与手动应用
// 都不继承；换代后重新启用能重启后台周期（自然退出的旧循环不占启动槽）并
// 仅靠自动周期跟随；Disable 后循环同步收敛退出（防泄漏）。
func TestWSLUnifiedGenerationChangeWhileEnabledDoesNotInherit(t *testing.T) {
	f := newUnifiedFixture(t)
	setWSLUnifiedLoopIntervalsForTest(t, 100*time.Millisecond, 200*time.Millisecond)
	if _, err := f.app.EnableWSLUnifiedSupply(); err != nil {
		t.Fatalf("enable: %v", err)
	}
	waitUntil(t, func() bool { _, ok := mcpServerURL(f, "web"); return ok },
		"claude mcp must be supplied while enabled")
	waitUntil(t, func() bool { return unifiedTargetExists(f, ".agents/skills/alpha/SKILL.md") },
		"tree units must be supplied while enabled before the flip")
	oldTarget := f.target
	oldMCPRel := filepath.FromSlash(".claude.json")
	// 直接换代（machine-id 变化 = 重装/换身份，新 HOME，不先停用）。
	f.setIdentity("machine-direct-flip", "/home/u2")
	waitUntil(t, func() bool { return f.target != oldTarget }, "fixture target must track the new home")
	waitForWSLSupplyPushIdleForTest() // 排空换代前在途推送（其已按旧代解析）
	// 来源继续变化：自动周期仍在跑，但新身份无同代授权 → 旧目标不被改写、
	// 新目标不教写入。
	writeUnifiedFile(t, filepath.Join(f.home, ".claude.json"),
		`{"mcpServers": {"web": {"url": "https://direct-flip.example/sse"}}}`)
	writeUnifiedFile(t, filepath.Join(f.home, ".agents", "skills", "alpha", "SKILL.md"), "# alpha flipped")
	time.Sleep(700 * time.Millisecond)
	oldData, rerr := os.ReadFile(filepath.Join(oldTarget, oldMCPRel))
	if rerr != nil || !strings.Contains(string(oldData), "https://mcp.example/sse") {
		t.Fatalf("old-generation consent must not follow source changes after generation flip: err=%v data=%s", rerr, oldData)
	}
	if unifiedTargetExists(f, ".claude.json") {
		t.Fatal("new identity must not receive writes without same-generation consent")
	}
	// 手动应用同样不继承旧代授权（新代 awaiting-consent，不写）。
	if out := f.app.ApplyWSLCliConfigSupply("claude"); len(out) == 0 {
		t.Fatal("manual apply should report per-unit outcomes")
	}
	if unifiedTargetExists(f, ".claude.json") {
		t.Fatal("manual apply on the new generation must not write without fresh consent")
	}
	// 换代后重新启用：自然退出的旧循环不得占据启动槽——新周期必须跑起来，
	// 且后续来源变化仅靠自动周期跟随（无任何手动保存/检查）。
	if _, err := f.app.EnableWSLUnifiedSupply(); err != nil {
		t.Fatalf("re-enable on new generation: %v", err)
	}
	waitUntil(t, func() bool {
		f.app.wslUnifiedLoop.mu.Lock()
		defer f.app.wslUnifiedLoop.mu.Unlock()
		return f.app.wslUnifiedLoop.cancel != nil
	}, "re-enable must restart the reconcile loop after natural exit")
	writeUnifiedFile(t, filepath.Join(f.home, ".claude.json"),
		`{"mcpServers": {"web": {"url": "https://reenabled.example/sse"}}}`)
	waitUntil(t, func() bool {
		url, ok := mcpServerURL(f, "web")
		return ok && url == "https://reenabled.example/sse"
	}, "re-enabled automatic cycle must follow source changes on the new generation")
	waitUntil(t, func() bool {
		data, err := os.ReadFile(filepath.Join(f.target, ".agents", "skills", "alpha", "SKILL.md"))
		return err == nil && string(data) == "# alpha flipped"
	}, "re-enabled automatic cycle must follow tree units on the new generation")
	// 自然退出后重新启用（steer 回归）：统一标记关闭（不经 App.Disable 的
	// 显式停止）→ 循环自行退出并按 done 令牌清槽；重新 enable 必须能再起新
	// 循环，且后续来源变化仅靠新周期跟随。
	if err := f.coord.SetTargetUnified(context.Background(), "Ubuntu", false); err != nil {
		t.Fatalf("toggle unified off: %v", err)
	}
	waitUntil(t, func() bool {
		f.app.wslUnifiedLoop.mu.Lock()
		defer f.app.wslUnifiedLoop.mu.Unlock()
		return f.app.wslUnifiedLoop.cancel == nil
	}, "loop must self-exit and retire its slots when unified turns off")
	if _, err := f.app.EnableWSLUnifiedSupply(); err != nil {
		t.Fatalf("re-enable after natural exit: %v", err)
	}
	waitUntil(t, func() bool {
		f.app.wslUnifiedLoop.mu.Lock()
		defer f.app.wslUnifiedLoop.mu.Unlock()
		return f.app.wslUnifiedLoop.cancel != nil
	}, "re-enable after natural exit must start a fresh loop (stale slot would block it)")
	// 启用后的下一笔来源变化：enable 的即时推送已过去，只有新周期的重扫→
	// 推送链路能交付（证明周期真正继续运行）。
	writeUnifiedFile(t, filepath.Join(f.home, ".claude.json"),
		`{"mcpServers": {"web": {"url": "https://natural-exit.example/sse"}}}`)
	waitUntil(t, func() bool {
		url, ok := mcpServerURL(f, "web")
		return ok && url == "https://natural-exit.example/sse"
	}, "restarted loop must resume the automatic cycle after natural exit (no stale slot)")
	// 停用路径收敛：Disable 同步停止后台循环（cancel/done 槽清空）。
	if err := f.app.DisableWSLUnifiedSupply(); err != nil {
		t.Fatalf("disable: %v", err)
	}
	f.app.wslUnifiedLoop.mu.Lock()
	stopped := f.app.wslUnifiedLoop.cancel == nil
	f.app.wslUnifiedLoop.mu.Unlock()
	if !stopped {
		t.Fatal("disable must stop the reconcile loop synchronously (no goroutine leak)")
	}
}
