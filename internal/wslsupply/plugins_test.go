package wslsupply

// plugins_test.go — P3-S5 插件重放的行为钉子（全部经注入假执行面/假探测面
// 运行，不触达真实 wsl.exe）。覆盖设计 §四六条硬规则：目标原生重放（不复制
// 实体）、版本/ref 固定、重放成功才写登记、本地项不动、脏仓库冻结、离线/
// 超时/取消结构化错误与幂等；以及插件单元不进 RunUnits 词汇表的结构性保证
//（启动不自动安装）。

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/platform"
)

// ---------------------------------------------------------------------------
// 测试桩
// ---------------------------------------------------------------------------

// testPluginParser 复刻 piplugin parseSource 的测试面子集（npm/git/local 三
// 型 + name/ref/host/path 拆分；解析语义本身的钉子在 piplugin/target_test.go）。
func testPluginParser(raw string) PluginSourceSpec {
	s := strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(s, "npm:"):
		spec := strings.TrimPrefix(s, "npm:")
		name, ref := splitTestNpmSpec(spec)
		return PluginSourceSpec{Type: "npm", Name: name, Ref: ref}
	case strings.HasPrefix(s, "git:"):
		rest := strings.TrimPrefix(s, "git:")
		repo, ref := splitTestRef(rest)
		host, path := splitTestRepo(repo)
		return PluginSourceSpec{Type: "git", Host: host, Path: path, Ref: ref}
	default:
		return PluginSourceSpec{Type: "local", LocalPath: s}
	}
}

func splitTestNpmSpec(spec string) (name, ref string) {
	if i := strings.LastIndex(spec, "@"); i > 0 {
		return spec[:i], spec[i+1:]
	}
	return spec, ""
}

func splitTestRef(s string) (repo, ref string) {
	if i := strings.LastIndex(s, "@"); i > 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

func splitTestRepo(repo string) (host, path string) {
	if i := strings.Index(repo, "/"); i > 0 {
		return repo[:i], repo[i+1:]
	}
	return "", ""
}

var testRefCheck = PluginRefChecker{
	ExactNpmVersion: func(ref string) bool {
		parts := strings.Split(ref, ".")
		return len(parts) == 3 && !strings.ContainsAny(ref, "^~* ><|")
	},
	FullCommitSHA: isFullCommitSHALocal,
}

// fakePluginDistro 模拟 distro 侧文件系统（经假 UNC 落 temp 目录）与 pi CLI
// 行为：pi install 写 settings.json packages[] + 建实体；pi remove 删登记
// （实体保留，pi 语义）。runner 记录全部脚本供断言。
type fakePluginDistro struct {
	mu      sync.Mutex
	uncRoot string
	scripts []string
	// piErr 非空时 pi 子命令失败（模拟重放失败）。
	piErr error
	// piErrKind 为 timeout/canceled 时把 piErr 包装为对应 ctx 错误。
	piErrKind string
	// npmViewVer 是 npm view 返回值；npmViewErr 非空时失败（离线/包消失）。
	npmViewVer string
	npmViewErr error
	// lsRemoteSHA 是 git ls-remote 返回值；lsRemoteErr 非空时失败。
	lsRemoteSHA string
	lsRemoteErr error
	// gitStatusOut 是 git status --porcelain 输出（空=干净）；gitStatusErr 非空失败。
	gitStatusOut string
	gitStatusErr error
	// piMissing 为 true 时 command -v pi 失败。
	piMissing bool
}

func newFakePluginDistro(t *testing.T) *fakePluginDistro {
	t.Helper()
	d := &fakePluginDistro{uncRoot: t.TempDir(), npmViewVer: "1.3.0", lsRemoteSHA: strings.Repeat("a1", 20)}
	t.Cleanup(func() {})
	return d
}

func (d *fakePluginDistro) agentRoot() string {
	// 假 ToUNC 把 /home/u/.pi/agent 映射到 uncRoot/home/u/.pi/agent
	//（与 coordinator_test 的 fakeProbes 同构），实体/登记必须落在同一路径。
	return filepath.Join(d.uncRoot, "home", "u", ".pi", "agent")
}

func (d *fakePluginDistro) settingsPath() string {
	return filepath.Join(d.agentRoot(), "settings.json")
}

func (d *fakePluginDistro) writeSettings(t *testing.T, sources []string) {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	d.writeSettingsLocked(sources)
}

func (d *fakePluginDistro) writeSettingsLocked(sources []string) {
	pkgs := make([]any, 0, len(sources))
	for _, s := range sources {
		pkgs = append(pkgs, s)
	}
	data, _ := json.Marshal(map[string]any{"packages": pkgs})
	_ = os.MkdirAll(d.agentRoot(), 0o755)
	_ = os.WriteFile(d.settingsPath(), data, 0o644)
}

func (d *fakePluginDistro) readSettingsSources(t *testing.T) []string {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.readSettingsSourcesLocked()
}

func (d *fakePluginDistro) readSettingsSourcesLocked() []string {
	data, err := os.ReadFile(d.settingsPath())
	if err != nil {
		return nil
	}
	var root struct {
		Packages []string `json:"packages"`
	}
	_ = json.Unmarshal(data, &root)
	return root.Packages
}

// writeNpmEntity 建 npm 实体（package.json version）。
func (d *fakePluginDistro) writeNpmEntity(t *testing.T, name, version string) {
	t.Helper()
	root := filepath.Join(d.agentRoot(), filepath.FromSlash("npm/node_modules/"+name))
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(map[string]any{"name": name, "version": version})
	if err := os.WriteFile(filepath.Join(root, "package.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeGitEntity 建 git 实体（package.json + .git/HEAD 指向 commit）。
func (d *fakePluginDistro) writeGitEntity(t *testing.T, host, userRepo, commit string) {
	t.Helper()
	root := filepath.Join(d.agentRoot(), "git", host, filepath.FromSlash(userRepo))
	if err := os.MkdirAll(filepath.Join(root, ".git", "refs", "heads"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(map[string]any{"name": userRepo})
	_ = os.WriteFile(filepath.Join(root, "package.json"), manifest, 0o644)
	_ = os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, ".git", "refs", "heads", "main"), []byte(commit+"\n"), 0o644)
}

// run 模拟 wsl.exe 执行面：按脚本前缀分发。
func (d *fakePluginDistro) run(_ context.Context, _, script string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.scripts = append(d.scripts, script)
	trim := strings.TrimSpace(script)
	switch {
	case trim == "command -v pi":
		if d.piMissing {
			return "", errors.New("exit status 127")
		}
		return "/home/u/.npm-global/bin/pi", nil
	case strings.HasPrefix(trim, "npm view "):
		if d.npmViewErr != nil {
			return "npm ERR! network", d.npmViewErr
		}
		return d.npmViewVer, nil
	case strings.HasPrefix(trim, "git ls-remote "):
		if d.lsRemoteErr != nil {
			return "", d.lsRemoteErr
		}
		// 真实脚本经 `| head -1 | cut -f1`；fake 即 shell，直接给 post-pipe 形态。
		return d.lsRemoteSHA + "\n", nil
	case strings.HasPrefix(trim, "git -C "):
		if d.gitStatusErr != nil {
			return "", d.gitStatusErr
		}
		return d.gitStatusOut, nil
	case strings.HasPrefix(trim, "pi install "):
		if d.piErr != nil {
			return "pi: install failed", d.piErrFor()
		}
		src := unquoteTail(trim, "pi install ")
		spec := testPluginParser(src)
		identity := pluginIdentity(spec)
		next := d.readSettingsSourcesLocked()
		replaced := false
		for i, s := range next {
			if pluginIdentity(testPluginParser(s)) == identity {
				next[i] = src
				replaced = true
			}
		}
		if !replaced {
			next = append(next, src)
		}
		d.writeSettingsLocked(next)
		switch spec.Type {
		case "npm":
			d.writeNpmEntityLocked(spec.Name, spec.Ref)
		case "git":
			d.writeGitEntityLocked(spec.Host, spec.Path, spec.Ref)
		}
		return "installed " + src, nil
	case strings.HasPrefix(trim, "pi remove "):
		if d.piErr != nil {
			return "pi: remove failed", d.piErrFor()
		}
		src := unquoteTail(trim, "pi remove ")
		spec := testPluginParser(src)
		identity := pluginIdentity(spec)
		next := []string{}
		for _, s := range d.readSettingsSourcesLocked() {
			if pluginIdentity(testPluginParser(s)) != identity {
				next = append(next, s)
			}
		}
		d.writeSettingsLocked(next) // 实体保留（pi 语义）
		return "removed " + src, nil
	default:
		return "", errors.New("unexpected script: " + trim)
	}
}

func (d *fakePluginDistro) piErrFor() error {
	switch d.piErrKind {
	case "timeout":
		return context.DeadlineExceeded
	case "canceled":
		return context.Canceled
	default:
		return d.piErr
	}
}

func (d *fakePluginDistro) writeNpmEntityLocked(name, version string) {
	root := filepath.Join(d.agentRoot(), filepath.FromSlash("npm/node_modules/"+name))
	_ = os.MkdirAll(root, 0o755)
	manifest, _ := json.Marshal(map[string]any{"name": name, "version": version})
	_ = os.WriteFile(filepath.Join(root, "package.json"), manifest, 0o644)
}

func (d *fakePluginDistro) writeGitEntityLocked(host, path, commit string) {
	root := filepath.Join(d.agentRoot(), "git", host, filepath.FromSlash(path))
	_ = os.MkdirAll(filepath.Join(root, ".git", "refs", "heads"), 0o755)
	manifest, _ := json.Marshal(map[string]any{"name": path})
	_ = os.WriteFile(filepath.Join(root, "package.json"), manifest, 0o644)
	_ = os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)
	if commit == "" {
		commit = strings.Repeat("b2", 20)
	}
	_ = os.WriteFile(filepath.Join(root, ".git", "refs", "heads", "main"), []byte(commit+"\n"), 0o644)
}

// unquoteTail 去掉单引号包裹的尾参。
func unquoteTail(script, prefix string) string {
	tail := strings.TrimPrefix(script, prefix)
	tail = strings.TrimSpace(tail)
	return strings.Trim(tail, "'")
}

func (d *fakePluginDistro) recorded() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.scripts...)
}

func (d *fakePluginDistro) hasScript(substr string) bool {
	for _, s := range d.recorded() {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}

// newPluginTestCoordinator 构造注入假执行面的协调器与目标。
func newPluginTestCoordinator(t *testing.T, distro *fakePluginDistro) (*Coordinator, TargetRef) {
	t.Helper()
	c := NewCoordinator(t.TempDir(), nil, Probes{
		Identity: func(string) platform.WSLUserIdentity {
			return platform.WSLUserIdentity{UID: "1000", Home: "/home/u", MachineID: "m-s5", OK: true}
		},
		ToUNC: func(_, linuxPath string) string {
			return filepath.Join(distro.uncRoot, filepath.FromSlash(strings.TrimPrefix(linuxPath, "/")))
		},
		Chmod: func(_, _ string, _ ...string) error { return nil },
	})
	c.SetPluginCommandRunner(distro.run)
	c.SetPluginSourceParser(testPluginParser, testRefCheck)
	target, err := c.ResolveTarget(context.Background(), "Ubuntu", CLIPi)
	if err != nil {
		t.Fatalf("resolve target: %v", err)
	}
	return c, target
}

// registerInstalled 预置一份「已安装」登记（幂等/更新用例的既有态）。
func registerInstalled(t *testing.T, c *Coordinator, target TargetRef, entry PluginStateEntry) {
	t.Helper()
	if err := c.plugins.savePluginEntry(entry); err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// install
// ---------------------------------------------------------------------------

func TestPluginInstallAnchoredPinnedReplay(t *testing.T) {
	d := newFakePluginDistro(t)
	c, target := newPluginTestCoordinator(t, d)
	res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:foo", Kind: PluginOpInstall, AnchorRef: "1.3.0",
	})
	if res.Outcome != PluginOutcomeInstalled || !res.OK {
		t.Fatalf("anchored install must succeed: %+v", res)
	}
	if res.PinnedRef != "1.3.0" || res.RefKind != string(PluginRefNpmVersion) {
		t.Fatalf("pin must be recorded: %+v", res)
	}
	if !res.EntityVerified {
		t.Fatalf("entity must be verified")
	}
	if !d.hasScript("pi install 'npm:foo@1.3.0'") {
		t.Fatalf("replay must carry exact version, scripts: %v", d.recorded())
	}
	// 登记：重放成功才写已安装。
	st := c.state.load()
	entry, ok := c.plugins.pluginEntryByIdentity(&st, "npm:foo", target.Generation)
	if !ok || !entry.Installed || !entry.EntityVerified || entry.PinnedRef != "1.3.0" {
		t.Fatalf("registration must be installed+verified: %+v ok=%v", entry, ok)
	}
	// distro 侧实体与登记一致（验收 1 的隔离验证位）。
	sources := d.readSettingsSources(t)
	if len(sources) != 1 || sources[0] != "npm:foo@1.3.0" {
		t.Fatalf("distro settings must carry pinned source: %v", sources)
	}
}

func TestPluginInstallResolvesInDistroWhenUnpinned(t *testing.T) {
	d := newFakePluginDistro(t)
	d.npmViewVer = "2.0.0"
	c, _ := newPluginTestCoordinator(t, d)
	res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:foo", Kind: PluginOpInstall,
	})
	if res.Outcome != PluginOutcomeInstalled {
		t.Fatalf("unpinned install must resolve and succeed: %+v", res)
	}
	if res.PinnedRef != "2.0.0" {
		t.Fatalf("must pin to resolved version: %+v", res)
	}
	if !d.hasScript("npm view 'foo' version") {
		t.Fatalf("resolution must happen in distro, scripts: %v", d.recorded())
	}
	if !d.hasScript("pi install 'npm:foo@2.0.0'") {
		t.Fatalf("replay must carry resolved exact version, scripts: %v", d.recorded())
	}
}

func TestPluginInstallGitCommitPinned(t *testing.T) {
	d := newFakePluginDistro(t)
	sha := strings.Repeat("a1", 20)
	d.lsRemoteSHA = sha
	c, _ := newPluginTestCoordinator(t, d)
	res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "git:github.com/u/repo", Kind: PluginOpInstall,
	})
	if res.Outcome != PluginOutcomeInstalled {
		t.Fatalf("git install must succeed: %+v", res)
	}
	if res.PinnedRef != sha || res.RefKind != string(PluginRefGitCommit) {
		t.Fatalf("git replay must pin commit: %+v", res)
	}
	if !d.hasScript("git ls-remote 'https://github.com/u/repo' 'HEAD'") {
		t.Fatalf("commit resolution must happen in distro, scripts: %v", d.recorded())
	}
	if !d.hasScript("pi install 'git:github.com/u/repo@" + sha + "'") {
		t.Fatalf("replay must carry pinned commit, scripts: %v", d.recorded())
	}
}

func TestPluginInstallFailureNeverRegistersInstalled(t *testing.T) {
	d := newFakePluginDistro(t)
	d.piErr = errors.New("npm install failed")
	c, target := newPluginTestCoordinator(t, d)
	res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:foo", Kind: PluginOpInstall, AnchorRef: "1.3.0",
	})
	if res.Outcome != PluginOutcomeFailed || res.ErrCode != PluginErrReplayFailed {
		t.Fatalf("replay failure must be structured failed: %+v", res)
	}
	st := c.state.load()
	entry, ok := c.plugins.pluginEntryByIdentity(&st, "npm:foo", target.Generation)
	if ok && entry.Installed {
		t.Fatalf("failure must never write installed state: %+v", entry)
	}
	// R3 幽灵行钉子：从未登记的失败绝不追加任何登记行（旧实现会以空
	// Identity 落盘追加，无法清除且污染 Probe/Summary 计数）。
	if len(st.Plugins) != 0 {
		t.Fatalf("failed install must not leave ghost registration: %+v", st.Plugins)
	}
	if len(d.readSettingsSources(t)) != 0 {
		t.Fatalf("distro settings must be untouched: %v", d.readSettingsSources(t))
	}
}

// TestPluginDefaultRunnerHonorsCanceledContext（R1）：默认执行面必须承载
// ctx——exec.CommandContext 下已取消的 ctx 使命令立即以 context.Canceled
// 失败（不启动 wsl.exe）；旧 exec.Command 实现会真的执行命令，本钉子变红。
func TestPluginDefaultRunnerHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := defaultPluginCommandRunner(ctx, "Ubuntu", "echo hi")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled ctx must abort the default runner immediately: %v", err)
	}
}

// TestPluginContextDeadlineYieldsStructuredTimeout（R1）：ctx 到期经
// RunPluginOperation → runScript → replay 接线归类为结构化 timeout（单次
// 尝试不重试）；模拟 wsl.exe 被 CommandContext 终止后返回 ctx 错误。
func TestPluginContextDeadlineYieldsStructuredTimeout(t *testing.T) {
	d := newFakePluginDistro(t)
	c, _ := newPluginTestCoordinator(t, d)
	installAttempts := 0
	c.SetPluginCommandRunner(func(ctx context.Context, distro, script string) (string, error) {
		if strings.HasPrefix(strings.TrimSpace(script), "pi install ") {
			installAttempts++
			<-ctx.Done()
			return "partial output", ctx.Err()
		}
		return d.run(ctx, distro, script)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	res := c.RunPluginOperation(ctx, "Ubuntu", PluginOperation{
		Source: "npm:foo", Kind: PluginOpInstall, AnchorRef: "1.3.0",
	})
	if res.Outcome != PluginOutcomeFailed || res.ErrCode != PluginErrTimeout {
		t.Fatalf("ctx deadline must surface structured timeout: %+v", res)
	}
	if installAttempts != 1 {
		t.Fatalf("timeout must be single-attempt (no retry storm), got %d", installAttempts)
	}
	st := c.state.load()
	if len(st.Plugins) != 0 {
		t.Fatalf("timeout must not leave ghost registration: %+v", st.Plugins)
	}
}

// TestPluginInstallVersionSwitchDirtyRepoFrozen（R5）：已托管 git 插件的
// install 版本切换路径（distro 登记源串 ≠ 重放源串 → remove+install）与
// runUpdate 同受脏仓库冻结——旁路不豁免 gitDirtyCheck。
func TestPluginInstallVersionSwitchDirtyRepoFrozen(t *testing.T) {
	sha := strings.Repeat("a1", 20)
	d := newFakePluginDistro(t)
	// distro 侧登记旧 commit 源串，实体在但工作区脏。
	d.writeSettings(t, []string{"git:github.com/u/repo@" + sha})
	d.writeGitEntity(t, "github.com", "u/repo", sha)
	d.gitStatusOut = " M src/local-edit.ts\n"
	c, target := newPluginTestCoordinator(t, d)
	registerInstalled(t, c, target, PluginStateEntry{
		Source: "git:github.com/u/repo@" + sha, Identity: "git:github.com/u/repo", SourceType: "git",
		Generation: target.Generation, PinnedRef: sha, RefKind: PluginRefGitCommit,
		Installed: true, EntityVerified: true,
	})
	res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "git:github.com/u/repo", Kind: PluginOpInstall, AnchorRef: strings.Repeat("c3", 20),
	})
	if res.Outcome != PluginOutcomeBlocked || res.ErrCode != PluginErrDirtyRepo {
		t.Fatalf("version switch on dirty repo must freeze: %+v", res)
	}
	for _, s := range d.recorded() {
		if strings.HasPrefix(strings.TrimSpace(s), "pi ") {
			t.Fatalf("frozen switch must not replay remove/install: %v", d.recorded())
		}
	}
	st := c.state.load()
	entry, _ := c.plugins.pluginEntryByIdentity(&st, "git:github.com/u/repo", target.Generation)
	if !entry.Frozen || entry.FrozenReason != PluginErrDirtyRepo {
		t.Fatalf("freeze must be recorded: %+v", entry)
	}
}

// TestPluginRemoveCorruptSettingsBlockedNotFakeOK（R4）：settings.json 损坏
// （不可解析）时 remove 不得把「无法读取」冒充「登记已消失」——blocked，
// 登记保持，不冒充成功。
func TestPluginRemoveCorruptSettingsBlockedNotFakeOK(t *testing.T) {
	d := newFakePluginDistro(t)
	c, target := newPluginTestCoordinator(t, d)
	registerInstalled(t, c, target, PluginStateEntry{
		Source: "npm:foo@1.3.0", Identity: "npm:foo", SourceType: "npm",
		Generation: target.Generation, PinnedRef: "1.3.0", Installed: true, EntityVerified: true,
	})
	// 写入损坏的 settings.json（非法 JSON）。
	if err := os.MkdirAll(d.agentRoot(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(d.settingsPath(), []byte("{not-json"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:foo", Kind: PluginOpRemove,
	})
	if res.Outcome != PluginOutcomeBlocked || res.ErrCode != PluginErrRegistrationMismatch {
		t.Fatalf("corrupt settings must block remove (not fake already-ok): %+v", res)
	}
	st := c.state.load()
	if _, ok := c.plugins.pluginEntryByIdentity(&st, "npm:foo", target.Generation); !ok {
		t.Fatalf("registration must survive a blocked remove")
	}
	for _, s := range d.recorded() {
		if strings.HasPrefix(strings.TrimSpace(s), "pi ") {
			t.Fatalf("no replay against unreadable settings: %v", d.recorded())
		}
	}
}

func TestPluginInstallIdempotentAlreadyOK(t *testing.T) {
	d := newFakePluginDistro(t)
	d.writeSettings(t, []string{"npm:foo@1.3.0"})
	d.writeNpmEntity(t, "foo", "1.3.0")
	c, target := newPluginTestCoordinator(t, d)
	registerInstalled(t, c, target, PluginStateEntry{
		Source: "npm:foo@1.3.0", Identity: "npm:foo", SourceType: "npm",
		Generation: target.Generation, PinnedRef: "1.3.0", RefKind: PluginRefNpmVersion,
		Installed: true, EntityVerified: true, LastOp: PluginOpInstall,
	})
	res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:foo@1.3.0", Kind: PluginOpInstall, AnchorRef: "1.3.0",
	})
	if res.Outcome != PluginOutcomeAlreadyOK || !res.OK {
		t.Fatalf("same-version install must be already-ok: %+v", res)
	}
	for _, s := range d.recorded() {
		if strings.Contains(s, "pi install") || strings.Contains(s, "pi remove") {
			t.Fatalf("already-ok must execute zero replay commands, scripts: %v", d.recorded())
		}
	}
}

func TestPluginInstallUnmanagedExistingBlocked(t *testing.T) {
	d := newFakePluginDistro(t)
	d.writeSettings(t, []string{"npm:foo"})
	c, _ := newPluginTestCoordinator(t, d)
	res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:foo", Kind: PluginOpInstall, AnchorRef: "1.3.0",
	})
	if res.Outcome != PluginOutcomeBlocked || res.ErrCode != PluginErrUnmanagedExisting {
		t.Fatalf("local manual registration must block takeover: %+v", res)
	}
	for _, s := range d.recorded() {
		if strings.HasPrefix(strings.TrimSpace(s), "pi ") {
			t.Fatalf("no pi command may run against a local item: %v", d.recorded())
		}
	}
}

func TestPluginInstallLocalSourceBlocked(t *testing.T) {
	d := newFakePluginDistro(t)
	c, _ := newPluginTestCoordinator(t, d)
	res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "C:\\work\\plugin", Kind: PluginOpInstall,
	})
	if res.Outcome != PluginOutcomeBlocked || res.ErrCode != PluginErrLocalSource {
		t.Fatalf("local source must be rejected: %+v", res)
	}
	if len(d.recorded()) != 0 {
		t.Fatalf("no distro interaction allowed: %v", d.recorded())
	}
}

func TestPluginInstallCLIMissing(t *testing.T) {
	d := newFakePluginDistro(t)
	d.piMissing = true
	c, _ := newPluginTestCoordinator(t, d)
	res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:foo", Kind: PluginOpInstall, AnchorRef: "1.3.0",
	})
	if res.Outcome != PluginOutcomeBlocked || res.ErrCode != PluginErrCLIMissing {
		t.Fatalf("missing pi CLI must block: %+v", res)
	}
}

func TestPluginInstallSourceUnresolvableFrozenDiagnostic(t *testing.T) {
	d := newFakePluginDistro(t)
	d.npmViewErr = errors.New("ENOTFOUND") // 离线/包消失
	c, _ := newPluginTestCoordinator(t, d)
	res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:foo", Kind: PluginOpInstall,
	})
	if res.Outcome != PluginOutcomeBlocked || res.ErrCode != PluginErrSourceUnresolvable {
		t.Fatalf("unresolvable source must freeze with diagnostic: %+v", res)
	}
	for _, s := range d.recorded() {
		if strings.Contains(s, "pi install") {
			t.Fatalf("install must not be attempted after resolution failure: %v", d.recorded())
		}
	}
}

func TestPluginInstallGenerationMismatchTreatsAsUnmanaged(t *testing.T) {
	d := newFakePluginDistro(t)
	d.writeSettings(t, []string{"npm:foo"})
	c, target := newPluginTestCoordinator(t, d)
	// 旧代际登记（distro 重装语义）：不自动继承管理权。
	registerInstalled(t, c, target, PluginStateEntry{
		Source: "npm:foo", Identity: "npm:foo", SourceType: "npm",
		Generation: "old-generation", Installed: true, EntityVerified: true,
	})
	res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:foo", Kind: PluginOpInstall, AnchorRef: "1.3.0",
	})
	if res.Outcome != PluginOutcomeBlocked || res.ErrCode != PluginErrUnmanagedExisting {
		t.Fatalf("stale-generation registration must not manage: %+v", res)
	}
}

// ---------------------------------------------------------------------------
// update
// ---------------------------------------------------------------------------

func TestPluginUpdateNotManagedBlocked(t *testing.T) {
	d := newFakePluginDistro(t)
	d.writeSettings(t, []string{"npm:foo"})
	c, _ := newPluginTestCoordinator(t, d)
	res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:foo", Kind: PluginOpUpdate,
	})
	if res.Outcome != PluginOutcomeBlocked || res.ErrCode != PluginErrNotManaged {
		t.Fatalf("update only applies to managed plugins: %+v", res)
	}
	if len(d.recorded()) != 0 {
		t.Fatalf("local item must stay untouched: %v", d.recorded())
	}
}

func TestPluginUpdateDirtyRepoFrozenNeverResets(t *testing.T) {
	d := newFakePluginDistro(t)
	d.writeSettings(t, []string{"git:github.com/u/repo@" + strings.Repeat("a1", 20)})
	d.writeGitEntity(t, "github.com", "u/repo", strings.Repeat("a1", 20))
	d.gitStatusOut = " M src/local-edit.ts\n"
	c, target := newPluginTestCoordinator(t, d)
	registerInstalled(t, c, target, PluginStateEntry{
		Source:   "git:github.com/u/repo@" + strings.Repeat("a1", 20),
		Identity: "git:github.com/u/repo", SourceType: "git",
		Generation: target.Generation, PinnedRef: strings.Repeat("a1", 20),
		RefKind: PluginRefGitCommit, Installed: true, EntityVerified: true,
	})
	res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "git:github.com/u/repo", Kind: PluginOpUpdate, AnchorRef: strings.Repeat("c3", 20),
	})
	if res.Outcome != PluginOutcomeBlocked || res.ErrCode != PluginErrDirtyRepo {
		t.Fatalf("dirty repo must freeze update: %+v", res)
	}
	for _, s := range d.recorded() {
		if strings.Contains(s, "pi ") || strings.Contains(s, "reset") || strings.Contains(s, "clean") {
			t.Fatalf("frozen update must not touch the entity (no reset/clean): %v", d.recorded())
		}
	}
	// 实体仍在（未被 reset/clean），且登记记入冻结原因。
	entity := filepath.Join(d.agentRoot(), "git", "github.com", "u", "repo", "package.json")
	if _, err := os.Stat(entity); err != nil {
		t.Fatalf("entity must remain after freeze: %v", err)
	}
	st := c.state.load()
	entry, _ := c.plugins.pluginEntryByIdentity(&st, "git:github.com/u/repo", target.Generation)
	if !entry.Frozen || entry.FrozenReason != PluginErrDirtyRepo {
		t.Fatalf("freeze must be recorded: %+v", entry)
	}
}

func TestPluginUpdateReplayCarriesAnchor(t *testing.T) {
	d := newFakePluginDistro(t)
	d.writeSettings(t, []string{"npm:foo@1.3.0"})
	d.writeNpmEntity(t, "foo", "1.3.0")
	c, target := newPluginTestCoordinator(t, d)
	registerInstalled(t, c, target, PluginStateEntry{
		Source: "npm:foo@1.3.0", Identity: "npm:foo", SourceType: "npm",
		Generation: target.Generation, PinnedRef: "1.3.0", RefKind: PluginRefNpmVersion,
		Installed: true, EntityVerified: true,
	})
	// 宿主锚点 1.5.0（宿主一次 update 的解析结果）：WSL 重放固定到锚点，
	// 不是再次追 latest（npm view 未被调用）。
	res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:foo", Kind: PluginOpUpdate, AnchorRef: "1.5.0",
	})
	if res.Outcome != PluginOutcomeUpdated || res.PinnedRef != "1.5.0" {
		t.Fatalf("update must replay to anchor: %+v", res)
	}
	if d.hasScript("npm view") {
		t.Fatalf("anchored update must not chase latest: %v", d.recorded())
	}
	if !d.hasScript("pi remove 'npm:foo@1.3.0'") || !d.hasScript("pi install 'npm:foo@1.5.0'") {
		t.Fatalf("update replay must remove+install pinned: %v", d.recorded())
	}
	sources := d.readSettingsSources(t)
	if len(sources) != 1 || sources[0] != "npm:foo@1.5.0" {
		t.Fatalf("distro settings must carry new pinned source: %v", sources)
	}
}

func TestPluginUpdateAlreadyOKWhenAnchorMatches(t *testing.T) {
	d := newFakePluginDistro(t)
	d.writeSettings(t, []string{"npm:foo@1.3.0"})
	d.writeNpmEntity(t, "foo", "1.3.0")
	c, target := newPluginTestCoordinator(t, d)
	registerInstalled(t, c, target, PluginStateEntry{
		Source: "npm:foo@1.3.0", Identity: "npm:foo", SourceType: "npm",
		Generation: target.Generation, PinnedRef: "1.3.0", RefKind: PluginRefNpmVersion,
		Installed: true, EntityVerified: true,
	})
	res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:foo", Kind: PluginOpUpdate, AnchorRef: "1.3.0",
	})
	if res.Outcome != PluginOutcomeAlreadyOK {
		t.Fatalf("anchor-matching update must be already-ok: %+v", res)
	}
	for _, s := range d.recorded() {
		if strings.Contains(s, "pi ") {
			t.Fatalf("no replay commands allowed: %v", d.recorded())
		}
	}
}

func TestPluginUpdateRegistrationMismatchBlocked(t *testing.T) {
	d := newFakePluginDistro(t)
	d.writeSettings(t, []string{}) // 用户已在 WSL 内移除
	c, target := newPluginTestCoordinator(t, d)
	registerInstalled(t, c, target, PluginStateEntry{
		Source: "npm:foo@1.3.0", Identity: "npm:foo", SourceType: "npm",
		Generation: target.Generation, PinnedRef: "1.3.0", Installed: true, EntityVerified: true,
	})
	res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:foo", Kind: PluginOpUpdate, AnchorRef: "1.5.0",
	})
	if res.Outcome != PluginOutcomeBlocked || res.ErrCode != PluginErrRegistrationMismatch {
		t.Fatalf("lost distro registration must block (drift): %+v", res)
	}
}

// ---------------------------------------------------------------------------
// remove / 生命周期 / 探测
// ---------------------------------------------------------------------------

func TestPluginRemoveManagedDropsRegistration(t *testing.T) {
	d := newFakePluginDistro(t)
	d.writeSettings(t, []string{"npm:foo@1.3.0"})
	d.writeNpmEntity(t, "foo", "1.3.0")
	c, target := newPluginTestCoordinator(t, d)
	registerInstalled(t, c, target, PluginStateEntry{
		Source: "npm:foo@1.3.0", Identity: "npm:foo", SourceType: "npm",
		Generation: target.Generation, PinnedRef: "1.3.0", Installed: true, EntityVerified: true,
	})
	res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:foo", Kind: PluginOpRemove,
	})
	if res.Outcome != PluginOutcomeRemoved {
		t.Fatalf("managed remove must succeed: %+v", res)
	}
	if !d.hasScript("pi remove 'npm:foo@1.3.0'") {
		t.Fatalf("remove must replay pi remove: %v", d.recorded())
	}
	if got := d.readSettingsSources(t); len(got) != 0 {
		t.Fatalf("settings must lose the entry: %v", got)
	}
	// 实体保留（pi 语义：重装可用）。
	entity := filepath.Join(d.agentRoot(), "npm", "node_modules", "foo", "package.json")
	if _, err := os.Stat(entity); err != nil {
		t.Fatalf("entity must be retained after remove: %v", err)
	}
	st := c.state.load()
	if _, ok := c.plugins.pluginEntryByIdentity(&st, "npm:foo", target.Generation); ok {
		t.Fatalf("registration must be dropped after remove")
	}
}

func TestPluginRemoveLocalItemBlocked(t *testing.T) {
	d := newFakePluginDistro(t)
	d.writeSettings(t, []string{"npm:foo"}) // WSL 本地手动项
	c, _ := newPluginTestCoordinator(t, d)
	res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:foo", Kind: PluginOpRemove,
	})
	if res.Outcome != PluginOutcomeBlocked || res.ErrCode != PluginErrNotManaged {
		t.Fatalf("remove must not touch local items: %+v", res)
	}
	if got := d.readSettingsSources(t); len(got) != 1 || got[0] != "npm:foo" {
		t.Fatalf("local item must stay: %v", got)
	}
}

func TestPluginRemoveAlreadyGoneCleansRegistration(t *testing.T) {
	d := newFakePluginDistro(t)
	c, target := newPluginTestCoordinator(t, d)
	registerInstalled(t, c, target, PluginStateEntry{
		Source: "npm:foo@1.3.0", Identity: "npm:foo", SourceType: "npm",
		Generation: target.Generation, Installed: true, EntityVerified: true,
	})
	res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:foo", Kind: PluginOpRemove,
	})
	if res.Outcome != PluginOutcomeAlreadyOK || !res.OK {
		t.Fatalf("remove of already-gone entry must be already-ok: %+v", res)
	}
	st := c.state.load()
	if _, ok := c.plugins.pluginEntryByIdentity(&st, "npm:foo", target.Generation); ok {
		t.Fatalf("registration must be cleaned")
	}
}

func TestPluginTimeoutSingleAttemptNoRetryStorm(t *testing.T) {
	d := newFakePluginDistro(t)
	d.piErr = errors.New("boom")
	d.piErrKind = "timeout"
	c, target := newPluginTestCoordinator(t, d)
	res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:foo", Kind: PluginOpInstall, AnchorRef: "1.3.0",
	})
	if res.Outcome != PluginOutcomeFailed || res.ErrCode != PluginErrTimeout {
		t.Fatalf("timeout must be structured: %+v", res)
	}
	// 不重试风暴：pi install 恰好执行一次。
	count := 0
	for _, s := range d.recorded() {
		if strings.Contains(s, "pi install") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("single attempt expected, got %d (scripts: %v)", count, d.recorded())
	}
	st := c.state.load()
	if entry, ok := c.plugins.pluginEntryByIdentity(&st, "npm:foo", target.Generation); ok && entry.Installed {
		t.Fatalf("timeout must never write installed")
	}
}

func TestPluginCanceledStructured(t *testing.T) {
	d := newFakePluginDistro(t)
	d.piErr = errors.New("stop")
	d.piErrKind = "canceled"
	c, _ := newPluginTestCoordinator(t, d)
	res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:foo", Kind: PluginOpInstall, AnchorRef: "1.3.0",
	})
	if res.Outcome != PluginOutcomeFailed || res.ErrCode != PluginErrCanceled {
		t.Fatalf("cancel must be structured: %+v", res)
	}
}

func TestPluginDegradedWithoutParser(t *testing.T) {
	d := newFakePluginDistro(t)
	c := NewCoordinator(t.TempDir(), nil, Probes{
		Identity: func(string) platform.WSLUserIdentity {
			return platform.WSLUserIdentity{UID: "1000", Home: "/home/u", MachineID: "m", OK: true}
		},
		ToUNC: func(_, p string) string {
			return filepath.Join(d.uncRoot, filepath.FromSlash(strings.TrimPrefix(p, "/")))
		},
		Chmod: func(_, _ string, _ ...string) error { return nil },
	})
	res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:foo", Kind: PluginOpInstall,
	})
	if res.Outcome != PluginOutcomeBlocked || res.ErrCode != PluginErrParserUnavailable {
		t.Fatalf("bare coordinator must degrade honestly: %+v", res)
	}
}

func TestPluginTargetUnresolved(t *testing.T) {
	d := newFakePluginDistro(t)
	c, _ := newPluginTestCoordinator(t, d)
	res := c.RunPluginOperation(context.Background(), "", PluginOperation{
		Source: "npm:foo", Kind: PluginOpInstall,
	})
	if res.Outcome != PluginOutcomeBlocked || res.ErrCode != PluginErrTargetUnresolved {
		t.Fatalf("unresolvable target must block: %+v", res)
	}
}

func TestPluginProbeStates(t *testing.T) {
	d := newFakePluginDistro(t)
	c, target := newPluginTestCoordinator(t, d)
	sha := strings.Repeat("a1", 20)
	// ① 本地项（distro 有登记，非本系统）；② 已安装（登记+实体）；③ 登记
	// 漂移（settings 已无但本系统登记在）。
	d.writeSettings(t, []string{"npm:local-pkg", "npm:foo@1.3.0"})
	d.writeNpmEntity(t, "foo", "1.3.0")
	d.writeNpmEntity(t, "local-pkg", "0.0.1")
	registerInstalled(t, c, target, PluginStateEntry{
		Source: "npm:foo@1.3.0", Identity: "npm:foo", SourceType: "npm",
		Generation: target.Generation, PinnedRef: "1.3.0", Installed: true, EntityVerified: true,
	})
	registerInstalled(t, c, target, PluginStateEntry{
		Source: "git:github.com/u/gone@" + sha, Identity: "git:github.com/u/gone", SourceType: "git",
		Generation: target.Generation, PinnedRef: sha, Installed: true, EntityVerified: true,
	})
	snap, err := c.ProbeWSLPlugins(context.Background(), "Ubuntu")
	if err != nil || snap.Distro != "Ubuntu" || !snap.Checked {
		t.Fatalf("probe must return checked snapshot: err=%v snap=%+v", err, snap)
	}
	byIdent := map[string]WSLPluginItem{}
	for _, it := range snap.Items {
		byIdent[it.Identity] = it
	}
	if it := byIdent["npm:local-pkg"]; it.State != PluginItemLocal || it.Managed {
		t.Fatalf("unmanaged entry must be local-item: %+v", it)
	}
	if it := byIdent["npm:foo"]; it.State != PluginItemInstalled || !it.Managed || !it.EntityPresent || !it.SettingsPresent {
		t.Fatalf("managed entry must be installed with both faces: %+v", it)
	}
	if it := byIdent["git:github.com/u/gone"]; it.State != PluginItemRegisteredMissed {
		t.Fatalf("lost registration must be registered-missing: %+v", it)
	}
	if snap.Local != 1 || snap.Managed != 2 || snap.Installed != 1 || snap.Mismatched != 1 {
		t.Fatalf("counts: %+v", snap)
	}
	// 缓存快照可再读（不唤醒）。
	if cached := c.WSLPluginsCachedSnapshot("Ubuntu"); !cached.Checked {
		t.Fatalf("cached snapshot must survive")
	}
	// 登记摘要（状态页数据源）：纯状态清单视角——distro 侧真实漂移
	//（gone 登记实为 registered-missing）只有 Probe 能发现，摘要不冒充。
	if s := c.PluginRegistrationSummary(); s.Managed != 2 {
		t.Fatalf("summary: %+v", s)
	}
}

func TestPluginEntityMismatchAfterUserRemovesEntity(t *testing.T) {
	d := newFakePluginDistro(t)
	c, target := newPluginTestCoordinator(t, d)
	// settings 有登记但实体被删（单向漂移 → mismatch）。
	d.writeSettings(t, []string{"npm:foo@1.3.0"})
	registerInstalled(t, c, target, PluginStateEntry{
		Source: "npm:foo@1.3.0", Identity: "npm:foo", SourceType: "npm",
		Generation: target.Generation, PinnedRef: "1.3.0", Installed: true, EntityVerified: true,
	})
	snap, err := c.ProbeWSLPlugins(context.Background(), "Ubuntu")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	found := false
	for _, it := range snap.Items {
		if it.Identity == "npm:foo" {
			found = true
			if it.State != PluginItemMismatch {
				t.Fatalf("missing entity must show mismatch: %+v", it)
			}
		}
	}
	if !found {
		t.Fatalf("item must be listed")
	}
}

// ---------------------------------------------------------------------------
// 结构性保证：插件操作不进 RunUnits 单元词汇表（启动不自动安装）
// ---------------------------------------------------------------------------

func TestRunUnitsRejectsPluginShapedUnits(t *testing.T) {
	d := newFakePluginDistro(t)
	c, _ := newPluginTestCoordinator(t, d)
	_, err := c.RunUnits(context.Background(), "Ubuntu", CLIPi, ReasonLaunch,
		[]UnitID{"pi/plugin/npm:foo"})
	if err == nil || !strings.Contains(err.Error(), "unknown wsl supply unit") {
		t.Fatalf("plugin-shaped unit must be rejected from RunUnits vocabulary: %v", err)
	}
	// 已知单元保持原样（裸协调器无 S1/S2 适配器：A2 恒在即代表单元面不受
	// S5 影响）。
	if !c.HasUnit(UnitPiA2Auth) {
		t.Fatalf("a2 unit must stay known")
	}
	if c.HasUnit("pi/plugin/npm:foo") {
		t.Fatalf("plugin-shaped unit must not be a known unit")
	}
}
