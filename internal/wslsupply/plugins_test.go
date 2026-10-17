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

// readSettingsEntries 返回 packages[] 的原始元素（字符串或对象）；对象元素
// 以 json.RawMessage 形式返回，供过滤形态断言（SA12）。
func (d *fakePluginDistro) readSettingsEntries(t *testing.T) []json.RawMessage {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	data, err := os.ReadFile(d.settingsPath())
	if err != nil {
		return nil
	}
	var root struct {
		Packages []json.RawMessage `json:"packages"`
	}
	_ = json.Unmarshal(data, &root)
	return root.Packages
}

func (d *fakePluginDistro) readSettingsSources(t *testing.T) []string {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.readSettingsSourcesLocked()
}

// readSettingsSourcesLocked 返回字符串源形态的登记（对象元素也取其 source
// 字段——SA12 后 distro 侧可能携带过滤对象）。
func (d *fakePluginDistro) readSettingsSourcesLocked() []string {
	data, err := os.ReadFile(d.settingsPath())
	if err != nil {
		return nil
	}
	var root struct {
		Packages []json.RawMessage `json:"packages"`
	}
	if json.Unmarshal(data, &root) != nil {
		return nil
	}
	out := []string{}
	for _, raw := range root.Packages {
		if s := rawEntrySource(raw); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// rawEntrySource 返回单个 packages[] 元素的源串（字符串或对象形态；无效返回空）。
func rawEntrySource(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return ""
	}
	if trimmed[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		return ""
	}
	var obj struct {
		Source string `json:"source"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return strings.TrimSpace(obj.Source)
	}
	return ""
}

// writeSettingsRaw 写入原始（字符串/对象混合）packages 元素，供过滤
// 对象用例预置 distro 侧状态（SA12）。
func (d *fakePluginDistro) writeSettingsRaw(t *testing.T, entries []any) {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	d.writeSettingsRawLocked(entries)
}

// rawPackagesLocked 返回 settings.json 的原始 packages 元素。
func (d *fakePluginDistro) rawPackagesLocked() []json.RawMessage {
	data, err := os.ReadFile(d.settingsPath())
	if err != nil {
		return nil
	}
	var root struct {
		Packages []json.RawMessage `json:"packages"`
	}
	_ = json.Unmarshal(data, &root)
	return root.Packages
}

// writeSettingsRawLocked 写入原始（字符串/对象混合）packages 元素：pi
// install/remove 模拟里未触碰的元素保留原形态（对象不被扁平化——SA12）。
func (d *fakePluginDistro) writeSettingsRawLocked(entries []any) {
	data, _ := json.Marshal(map[string]any{"packages": entries})
	_ = os.MkdirAll(d.agentRoot(), 0o755)
	_ = os.WriteFile(d.settingsPath(), data, 0o644)
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
		// 未命中元素保留原形态（对象不被扁平化）；命中元素替换为新字符串源
		//（pi install 写纯字符串，对象形态由 applyDistroFilters 后续重建）。
		next := []any{}
		replaced := false
		for _, raw := range d.rawPackagesLocked() {
			s := rawEntrySource(raw)
			if s != "" && pluginIdentity(testPluginParser(s)) == identity {
				next = append(next, src)
				replaced = true
				continue
			}
			next = append(next, raw)
		}
		if !replaced {
			next = append(next, src)
		}
		d.writeSettingsRawLocked(next)
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
		next := []any{}
		for _, raw := range d.rawPackagesLocked() {
			s := rawEntrySource(raw)
			if s != "" && pluginIdentity(testPluginParser(s)) == identity {
				continue
			}
			next = append(next, raw)
		}
		d.writeSettingsRawLocked(next) // 实体保留（pi 语义）
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
		Identity: func(context.Context, string) platform.WSLUserIdentity {
			return platform.WSLUserIdentity{UID: "1000", Home: "/home/u", MachineID: "m-s5", OK: true}
		},
		ToUNC: func(_, linuxPath string) string {
			return filepath.Join(distro.uncRoot, filepath.FromSlash(strings.TrimPrefix(linuxPath, "/")))
		},
		Chmod: func(context.Context, string, string, ...string) error { return nil },
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
		Identity: func(context.Context, string) platform.WSLUserIdentity {
			return platform.WSLUserIdentity{UID: "1000", Home: "/home/u", MachineID: "m", OK: true}
		},
		ToUNC: func(_, p string) string {
			return filepath.Join(d.uncRoot, filepath.FromSlash(strings.TrimPrefix(p, "/")))
		},
		Chmod: func(context.Context, string, string, ...string) error { return nil },
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
	// 登记摘要（状态页数据源）：纯状态清单视角——distro 侧真实漂移
	//（gone 登记实为 registered-missing）只有 Probe 能发现，摘要不冒充。
	// SA-02：摘要只统计可归属到当前代际的登记——先 Inspect 把目标代际写入
	// 状态清单（生产路径中保存/检查/供给均会写入）。
	if _, ierr := c.Inspect(context.Background(), target); ierr != nil {
		t.Fatalf("inspect: %v", ierr)
	}
	if s := c.PluginRegistrationSummary(); s.Managed != 2 {
		t.Fatalf("summary: %+v", s)
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

// ---------------------------------------------------------------------------
// SA12：子资源过滤清单按 Pi 原生 packages 语义应用与复检
// ---------------------------------------------------------------------------

// filterEntryObject 解析 packages[] 中某源的对象形态（非对象返回 nil）。
func filterEntryObject(t *testing.T, entries []json.RawMessage, source string) map[string]any {
	t.Helper()
	for _, raw := range entries {
		var probe any
		if json.Unmarshal(raw, &probe) != nil {
			continue
		}
		if obj, ok := probe.(map[string]any); ok {
			if s, _ := obj["source"].(string); s == source {
				return obj
			}
		}
	}
	return nil
}

func TestPluginInstallAppliesFiltersAsNativeObject(t *testing.T) {
	d := newFakePluginDistro(t)
	c, target := newPluginTestCoordinator(t, d)
	filters := PluginFilters{
		Extensions: []string{"extensions/*.ts", "!extensions/legacy.ts"},
		Prompts:    []string{"prompts/review.md"},
	}
	res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:foo", Kind: PluginOpInstall, AnchorRef: "1.3.0", Filters: &filters,
	})
	if res.Outcome != PluginOutcomeInstalled || !res.OK {
		t.Fatalf("filtered install must succeed: %+v", res)
	}
	// distro settings 必须携带 Pi 原生过滤对象（不是只记录在 CodeBox 清单的
	// 纯字符串全量形态）。
	obj := filterEntryObject(t, d.readSettingsEntries(t), "npm:foo@1.3.0")
	if obj == nil {
		t.Fatalf("distro settings must carry filter object for npm:foo@1.3.0: %s", mustReadSettings(t, d))
	}
	if ext, _ := obj["extensions"].([]any); len(ext) != 2 || ext[0] != "extensions/*.ts" || ext[1] != "!extensions/legacy.ts" {
		t.Fatalf("extensions filter must be preserved verbatim (allow/deny): %+v", obj)
	}
	if pr, _ := obj["prompts"].([]any); len(pr) != 1 || pr[0] != "prompts/review.md" {
		t.Fatalf("prompts filter must be preserved: %+v", obj)
	}
	if _, has := obj["skills"]; has {
		t.Fatalf("unfiltered type must be omitted (Pi: 加载全部), got %+v", obj)
	}
	// 登记快照同步携带清单。
	st := c.state.load()
	entry, ok := c.plugins.pluginEntryByIdentity(&st, "npm:foo", target.Generation)
	if !ok || !pluginFiltersEqual(entry.Filters, filters) {
		t.Fatalf("registration must record filters: %+v ok=%v", entry, ok)
	}
}

func mustReadSettings(t *testing.T, d *fakePluginDistro) string {
	t.Helper()
	entries := d.readSettingsEntries(t)
	parts := make([]string, 0, len(entries))
	for _, e := range entries {
		parts = append(parts, string(e))
	}
	return strings.Join(parts, ", ")
}

func TestPluginInstallNoFiltersWritesPlainStringForm(t *testing.T) {
	d := newFakePluginDistro(t)
	c, _ := newPluginTestCoordinator(t, d)
	res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:foo", Kind: PluginOpInstall, AnchorRef: "1.3.0",
	})
	if res.Outcome != PluginOutcomeInstalled {
		t.Fatalf("plain install must succeed: %+v", res)
	}
	if obj := filterEntryObject(t, d.readSettingsEntries(t), "npm:foo@1.3.0"); obj != nil {
		t.Fatalf("filterless install must stay plain string form: %+v", obj)
	}
}

// TestPluginInstallFiltersConvergeFromDrift：同版本但过滤清单变化（旧登记
// 对象/字符串形态与期望不一致）不得 already-ok——重放重建过滤对象后复检。
func TestPluginInstallFiltersConvergeFromDrift(t *testing.T) {
	d := newFakePluginDistro(t)
	// distro 侧已有旧字符串登记 + 实体；本系统登记称已安装同版本。
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
		Filters: &PluginFilters{Skills: []string{"skills/core"}},
	})
	if res.Outcome != PluginOutcomeInstalled {
		t.Fatalf("filter drift must converge via replay (not already-ok): %+v", res)
	}
	obj := filterEntryObject(t, d.readSettingsEntries(t), "npm:foo@1.3.0")
	if obj == nil {
		t.Fatalf("settings must now carry the filter object: %s", mustReadSettings(t, d))
	}
	if sk, _ := obj["skills"].([]any); len(sk) != 1 || sk[0] != "skills/core" {
		t.Fatalf("skills filter must be applied: %+v", obj)
	}
}

// TestPluginInstallFiltersRemovedConvergesToPlainForm：Windows 侧移除过滤
// （面板空清单）后重放，distro 侧过滤对象归一回纯字符串全量形态。
func TestPluginInstallFiltersRemovedConvergesToPlainForm(t *testing.T) {
	d := newFakePluginDistro(t)
	d.writeSettingsRaw(t, []any{map[string]any{
		"source": "npm:foo@1.3.0", "extensions": []string{"extensions/a.ts"},
	}})
	d.writeNpmEntity(t, "foo", "1.3.0")
	c, target := newPluginTestCoordinator(t, d)
	registerInstalled(t, c, target, PluginStateEntry{
		Source: "npm:foo@1.3.0", Identity: "npm:foo", SourceType: "npm",
		Generation: target.Generation, PinnedRef: "1.3.0", RefKind: PluginRefNpmVersion,
		Installed: true, EntityVerified: true, LastOp: PluginOpInstall,
		Filters: PluginFilters{Extensions: []string{"extensions/a.ts"}},
	})
	res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:foo@1.3.0", Kind: PluginOpInstall, AnchorRef: "1.3.0",
	})
	if res.Outcome != PluginOutcomeInstalled {
		t.Fatalf("filter removal must converge: %+v", res)
	}
	if obj := filterEntryObject(t, d.readSettingsEntries(t), "npm:foo@1.3.0"); obj != nil {
		t.Fatalf("settings must return to plain string form: %+v", obj)
	}
}

// TestPluginUpdateReappliesRegistrationFilters：update 未携带新清单时按既有
// 登记清单重建过滤对象（remove 旧登记串 + install 新源后形态不丢）。
func TestPluginUpdateReappliesRegistrationFilters(t *testing.T) {
	d := newFakePluginDistro(t)
	d.writeSettingsRaw(t, []any{map[string]any{
		"source": "npm:foo@1.3.0", "themes": []string{"themes/dark.json"},
	}})
	d.writeNpmEntity(t, "foo", "1.3.0")
	c, target := newPluginTestCoordinator(t, d)
	registerInstalled(t, c, target, PluginStateEntry{
		Source: "npm:foo@1.3.0", Identity: "npm:foo", SourceType: "npm",
		Generation: target.Generation, PinnedRef: "1.3.0", RefKind: PluginRefNpmVersion,
		Installed: true, EntityVerified: true,
		Filters: PluginFilters{Themes: []string{"themes/dark.json"}},
	})
	res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:foo", Kind: PluginOpUpdate, AnchorRef: "1.5.0",
	})
	if res.Outcome != PluginOutcomeUpdated {
		t.Fatalf("update must succeed: %+v", res)
	}
	obj := filterEntryObject(t, d.readSettingsEntries(t), "npm:foo@1.5.0")
	if obj == nil {
		t.Fatalf("update must rebuild filter object on the new source: %s", mustReadSettings(t, d))
	}
	if th, _ := obj["themes"].([]any); len(th) != 1 || th[0] != "themes/dark.json" {
		t.Fatalf("registration filters must be reapplied: %+v", obj)
	}
}

// TestPluginUpdateFilterDriftNotAlreadyOK：锚点一致但 distro 过滤形态漂移
// （用户在 WSL 内手改成全量字符串）→ 不 already-ok，重建登记清单。
func TestPluginUpdateFilterDriftNotAlreadyOK(t *testing.T) {
	d := newFakePluginDistro(t)
	d.writeSettings(t, []string{"npm:foo@1.3.0"}) // 全量字符串（漂移）
	d.writeNpmEntity(t, "foo", "1.3.0")
	c, target := newPluginTestCoordinator(t, d)
	registerInstalled(t, c, target, PluginStateEntry{
		Source: "npm:foo@1.3.0", Identity: "npm:foo", SourceType: "npm",
		Generation: target.Generation, PinnedRef: "1.3.0", RefKind: PluginRefNpmVersion,
		Installed: true, EntityVerified: true,
		Filters: PluginFilters{Extensions: []string{"extensions/safe.ts"}},
	})
	res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:foo", Kind: PluginOpUpdate, AnchorRef: "1.3.0",
	})
	if res.Outcome != PluginOutcomeUpdated {
		t.Fatalf("filter drift must not be already-ok: %+v", res)
	}
	obj := filterEntryObject(t, d.readSettingsEntries(t), "npm:foo@1.3.0")
	if obj == nil {
		t.Fatalf("filter object must be rebuilt: %s", mustReadSettings(t, d))
	}
}

// TestPluginFiltersKeepSiblingEntriesUntouched：applyDistroFilters 的 sjson
// 定点替换不得破坏 packages[] 中其它登记与 settings 其它顶层键（pi install
// 本身会重写文件，故直接钉写入原语——安装链的其余行为由上游用例覆盖）。
func TestPluginFiltersKeepSiblingEntriesUntouched(t *testing.T) {
	d := newFakePluginDistro(t)
	c, target := newPluginTestCoordinator(t, d)
	if err := os.MkdirAll(d.agentRoot(), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `{"packages": ["npm:local@1.0.0", "npm:foo@1.3.0"], "theme": {"name": "user-theme"}}`
	if err := os.WriteFile(d.settingsPath(), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.plugins.applyDistroFilters(context.Background(), target, "npm:foo", "npm:foo@1.3.0",
		PluginFilters{Extensions: []string{"extensions/a.ts"}}); err != nil {
		t.Fatalf("applyDistroFilters: %v", err)
	}
	data, err := os.ReadFile(d.settingsPath())
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if json.Unmarshal(data, &root) != nil {
		t.Fatalf("settings must stay valid JSON: %s", data)
	}
	if theme, ok := root["theme"].(map[string]any); !ok || theme["name"] != "user-theme" {
		t.Fatalf("sibling top-level keys must be preserved: %s", data)
	}
	pkgs, _ := root["packages"].([]any)
	if len(pkgs) != 2 {
		t.Fatalf("both entries must exist: %s", data)
	}
	if pkgs[0] != "npm:local@1.0.0" {
		t.Fatalf("sibling entry must stay first & untouched: %s", data)
	}
	obj, ok := pkgs[1].(map[string]any)
	if !ok || obj["source"] != "npm:foo@1.3.0" {
		t.Fatalf("target entry must become filter object: %s", data)
	}
}

// ---------------------------------------------------------------------------
// SA-02 插件面：身份代未知不落/不读持久登记（保守过渡，等待 C2 状态分区）
// ---------------------------------------------------------------------------

func TestPluginUnknownGenerationNeverPersistsOrMatches(t *testing.T) {
	d := newFakePluginDistro(t)
	uncRoot := d.uncRoot
	c := NewCoordinator(t.TempDir(), nil, Probes{
		Identity: func(context.Context, string) platform.WSLUserIdentity {
			// 无 machine-id：generation = GenerationUnknown（最小 distro 形态）。
			return platform.WSLUserIdentity{UID: "1000", Home: "/home/u", OK: true}
		},
		ToUNC: func(_, linuxPath string) string {
			return filepath.Join(uncRoot, filepath.FromSlash(strings.TrimPrefix(linuxPath, "/")))
		},
		Chmod: func(context.Context, string, string, ...string) error { return nil },
	})
	c.SetPluginCommandRunner(d.run)
	c.SetPluginSourceParser(testPluginParser, testRefCheck)
	res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:foo", Kind: PluginOpInstall, AnchorRef: "1.3.0",
	})
	if res.Outcome != PluginOutcomeInstalled || !res.OK {
		t.Fatalf("install itself must still succeed and verify: %+v", res)
	}
	// 不落持久登记（读侧永不匹配 → 无死数据）。
	st := c.state.load()
	if len(st.Plugins) != 0 {
		t.Fatalf("unknown-generation target must not persist registration: %+v", st.Plugins)
	}
	// 二次操作按未登记处理（诚实 unmanaged-existing，不跨目标继承管理权）。
	res2 := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:foo", Kind: PluginOpInstall, AnchorRef: "1.3.0",
	})
	if res2.Outcome != PluginOutcomeBlocked || res2.ErrCode != PluginErrUnmanagedExisting {
		t.Fatalf("second op on unknown-generation target must be honestly blocked: %+v", res2)
	}
}

// TestPluginRegistrationsDontCrossDistros：两个已知不同代际的 distro 交替
// 使用时，登记不跨代际继承（读侧已保证）；写侧单槽覆盖的完整多目标分区
// 需要 C2 状态结构（state.go），此处钉住读侧边界。
func TestPluginRegistrationsDontCrossDistros(t *testing.T) {
	d := newFakePluginDistro(t)
	c, target := newPluginTestCoordinator(t, d)
	registerInstalled(t, c, target, PluginStateEntry{
		Source: "npm:foo@1.3.0", Identity: "npm:foo", SourceType: "npm",
		Generation: "gen-distro-a", Installed: true, EntityVerified: true,
	})
	// 目标 B（不同代际）：不得继承 A 的管理权。
	st := c.state.load()
	if _, ok := c.plugins.pluginEntryByIdentity(&st, "npm:foo", "gen-distro-b"); ok {
		t.Fatalf("registration must not cross generations")
	}
	// 目标身份代未知：同样不继承。
	if _, ok := c.plugins.pluginEntryByIdentity(&st, "npm:foo", GenerationUnknown); ok {
		t.Fatalf("unknown generation must not inherit registrations")
	}
	if _, ok := c.plugins.pluginEntryByIdentity(&st, "npm:foo", ""); ok {
		t.Fatalf("empty generation must not inherit registrations")
	}
}

// ---------------------------------------------------------------------------
// SA16：取消/超时优先按 ctx 归因（ExitError 形态不推翻取消事实）
// ---------------------------------------------------------------------------

// TestPluginTimeoutAttributedViaCtxOverExitError：真实 wsl.exe 被
// CommandContext 终止时错误常为 ExitError（不携带 ctx 错误链）——归因必须以
// ctx 为准（旧实现会把超时误报为 replay-failed）。假 runner 直接回 ctx.Err
// 的既有用例（TestPluginContextDeadlineYieldsStructuredTimeout）不足以证明
// 此路径。
func TestPluginTimeoutAttributedViaCtxOverExitError(t *testing.T) {
	d := newFakePluginDistro(t)
	c, _ := newPluginTestCoordinator(t, d)
	installRan := false
	c.SetPluginCommandRunner(func(ctx context.Context, _, script string) (string, error) {
		if strings.HasPrefix(strings.TrimSpace(script), "pi install ") {
			installRan = true
			<-ctx.Done()
			// 模拟被杀进程的非零退出：错误不包装 ctx.Err。
			return "killed output", errors.New("exit status 1")
		}
		return d.run(ctx, "", script)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	res := c.RunPluginOperation(ctx, "Ubuntu", PluginOperation{
		Source: "npm:foo", Kind: PluginOpInstall, AnchorRef: "1.3.0",
	})
	if res.Outcome != PluginOutcomeFailed || res.ErrCode != PluginErrTimeout {
		t.Fatalf("ctx deadline must win attribution over exit-error form: %+v", res)
	}
	if !installRan {
		t.Fatalf("install must have been attempted once")
	}
	if !strings.Contains(res.Detail, "Windows 侧命令已终止") {
		t.Fatalf("detail must state the honest process boundary: %+v", res)
	}
}

// TestPluginCanceledContextNeverQueuesOrRuns：已取消的请求不排队等 opMu、
// 不启动任何 distro 命令（含另一操作持锁期间取消的场景）。
func TestPluginCanceledContextNeverQueuesOrRuns(t *testing.T) {
	d := newFakePluginDistro(t)
	c, _ := newPluginTestCoordinator(t, d)
	release := make(chan struct{})
	held := make(chan struct{})
	c.SetPluginCommandRunner(func(ctx context.Context, _, script string) (string, error) {
		if strings.Contains(script, "npm view") {
			close(held)
			<-release
		}
		return d.run(ctx, "", script)
	})
	// 占用 opMu 的在途操作（未带锚点 install 走 distro 内解析）。
	bgDone := make(chan struct{})
	go func() {
		defer close(bgDone)
		_ = c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
			Source: "npm:foo", Kind: PluginOpInstall,
		})
	}()
	// 前置条件：在途操作须真正到达 npm view 并占住 opMu；若它在此之前就结束
	// （如 pending 摘要保存失败），立即失败而非无界等待。
	select {
	case <-held:
	case <-bgDone:
		t.Fatal("in-flight operation finished before reaching npm view; precondition (held opMu) not established")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := c.RunPluginOperation(ctx, "Ubuntu", PluginOperation{
		Source: "npm:bar", Kind: PluginOpInstall, AnchorRef: "2.0.0",
	})
	if res.Outcome != PluginOutcomeFailed || res.ErrCode != PluginErrCanceled {
		t.Fatalf("canceled request must fail fast as canceled: %+v", res)
	}
	close(release)
	<-bgDone // 等在途操作结束后再清理临时目录，避免后台写入与 cleanup 竞争。
	for _, s := range d.recorded() {
		if strings.Contains(s, "npm:bar") {
			t.Fatalf("canceled request must not run any command: %v", d.recorded())
		}
	}
}

// TestPluginFacadeCanceledBeforeResolve：协调器门面在目标解析前拦截已取消
// 请求（SA16：不触发任何身份探测/命令面——不产生 wsl.exe 进程）。
func TestPluginFacadeCanceledBeforeResolve(t *testing.T) {
	d := newFakePluginDistro(t)
	probeRan := false
	c := NewCoordinator(t.TempDir(), nil, Probes{
		Identity: func(context.Context, string) platform.WSLUserIdentity {
			probeRan = true
			return platform.WSLUserIdentity{UID: "1000", Home: "/home/u", MachineID: "m", OK: true}
		},
		ToUNC: func(_, p string) string {
			return filepath.Join(d.uncRoot, filepath.FromSlash(strings.TrimPrefix(p, "/")))
		},
		Chmod: func(context.Context, string, string, ...string) error { return nil },
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := c.RunPluginOperation(ctx, "Ubuntu", PluginOperation{Source: "npm:foo", Kind: PluginOpInstall})
	if res.Outcome != PluginOutcomeFailed || res.ErrCode != PluginErrCanceled {
		t.Fatalf("canceled facade call must be structured canceled: %+v", res)
	}
	if probeRan {
		t.Fatalf("canceled request must not trigger identity probe")
	}
	if len(d.recorded()) != 0 {
		t.Fatalf("canceled request must not run any distro command: %v", d.recorded())
	}
	if _, err := c.ProbeWSLPlugins(ctx, "Ubuntu"); err != nil {
		t.Fatalf("canceled probe returns snapshot, not error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// SA12 补全：显式空数组 []（该类型一项都不加载）与键省略（全部加载）的
// 三态全链保留
// ---------------------------------------------------------------------------

// TestPluginFiltersNilVersusEmptyStrictlyDistinct：过滤清单的三态语义在
// 比较、规范写入与 JSON 持久化三处都严格区分 nil 与空切片——折叠会把
// 「全关」错当「全开」，扩大 WSL 侧启用面。
func TestPluginFiltersNilVersusEmptyStrictlyDistinct(t *testing.T) {
	empty := PluginFilters{Extensions: []string{}}
	omitted := PluginFilters{}
	if pluginFiltersEqual(omitted, empty) {
		t.Fatal("nil (omitted key = load all) must differ from explicit [] (load none)")
	}
	if pluginFiltersEqual(empty, PluginFilters{Extensions: []string{}}) {
		// 同为空切片应相等（此处不可能为 false，仅钉住正向等价）。
	} else {
		t.Fatal("two explicit empty slices must be equal")
	}
	// 规范写入：空数组键必须写 []，不得省略；未声明键省略。
	if got := canonicalPackageEntryJSON("npm:foo@1.0.0", empty); !strings.Contains(got, `"extensions":[]`) {
		t.Fatalf("explicit [] must be written as an empty array, got %s", got)
	}
	if got := canonicalPackageEntryJSON("npm:foo@1.0.0", omitted); strings.Contains(got, "extensions") {
		t.Fatalf("omitted key must stay omitted (plain/all-load form), got %s", got)
	}
	// JSON 持久化 round-trip：空切片落 []、读回仍非 nil（state.go 不用
	// omitempty——省略会与 null/缺失混淆丢三态）。
	b, err := json.Marshal(map[string]PluginFilters{"f": empty})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"extensions":[]`) {
		t.Fatalf("state JSON must preserve empty array: %s", b)
	}
	var back struct {
		F PluginFilters `json:"f"`
	}
	if json.Unmarshal(b, &back) != nil || back.F.Extensions == nil || len(back.F.Extensions) != 0 {
		t.Fatalf("empty array must round-trip as non-nil empty slice: %#v", back.F.Extensions)
	}
}

// TestPluginInstallExplicitEmptyArrayFilter：Windows 侧显式空数组（该类型
// 一项都不加载）经重放写入 distro settings 时保持 [] 形态并复检通过；幂等
// 重放 already-ok；登记快照经磁盘 round-trip 后仍保留空切片三态。
func TestPluginInstallExplicitEmptyArrayFilter(t *testing.T) {
	d := newFakePluginDistro(t)
	stateDir := t.TempDir()
	uncRoot := d.uncRoot
	newCoord := func() *Coordinator {
		c := NewCoordinator(stateDir, nil, Probes{
			Identity: func(context.Context, string) platform.WSLUserIdentity {
				return platform.WSLUserIdentity{UID: "1000", Home: "/home/u", MachineID: "m-s5", OK: true}
			},
			ToUNC: func(_, linuxPath string) string {
				return filepath.Join(uncRoot, filepath.FromSlash(strings.TrimPrefix(linuxPath, "/")))
			},
			Chmod: func(context.Context, string, string, ...string) error { return nil },
		})
		c.SetPluginCommandRunner(d.run)
		c.SetPluginSourceParser(testPluginParser, testRefCheck)
		return c
	}
	c := newCoord()
	filters := PluginFilters{Extensions: []string{}, Skills: []string{"skills/core"}}
	res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:foo", Kind: PluginOpInstall, AnchorRef: "1.3.0", Filters: &filters,
	})
	if res.Outcome != PluginOutcomeInstalled || !res.OK {
		t.Fatalf("explicit-empty-array install must succeed: %+v", res)
	}
	obj := filterEntryObject(t, d.readSettingsEntries(t), "npm:foo@1.3.0")
	if obj == nil {
		t.Fatalf("settings must carry the filter object: %s", mustReadSettings(t, d))
	}
	ext, _ := obj["extensions"].([]any)
	if ext == nil || len(ext) != 0 {
		t.Fatalf("extensions must be written as an explicit empty array (load none), got %#v", obj["extensions"])
	}
	if sk, _ := obj["skills"].([]any); len(sk) != 1 || sk[0] != "skills/core" {
		t.Fatalf("skills filter must be preserved: %+v", obj)
	}
	if _, has := obj["prompts"]; has {
		t.Fatalf("omitted type must stay omitted (load all), got %+v", obj)
	}
	// 登记快照磁盘 round-trip：新协调器实例（独立 stateStore 缓存）从磁盘
	// 读回，空切片仍非 nil。
	c2 := newCoord()
	st := c2.state.load()
	var entry *PluginStateEntry
	for i := range st.Plugins {
		if st.Plugins[i].Identity == "npm:foo" {
			entry = &st.Plugins[i]
		}
	}
	if entry == nil || entry.Filters.Extensions == nil || len(entry.Filters.Extensions) != 0 {
		t.Fatalf("registration must round-trip the explicit empty array: %+v", entry)
	}
	// 幂等：过滤形态一致 → already-ok。
	res2 := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:foo@1.3.0", Kind: PluginOpInstall, AnchorRef: "1.3.0", Filters: &filters,
	})
	if res2.Outcome != PluginOutcomeAlreadyOK {
		t.Fatalf("matching empty-array form must be idempotent already-ok: %+v", res2)
	}
}

// TestPluginEmptyArrayFilterDriftNotAlreadyOK：期望显式空数组而 distro 侧
// 仍是纯字符串（或键省略）时不得 already-ok——重放射敛为带 [] 的过滤对象。
func TestPluginEmptyArrayFilterDriftNotAlreadyOK(t *testing.T) {
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
		Filters: &PluginFilters{Extensions: []string{}},
	})
	if res.Outcome != PluginOutcomeInstalled {
		t.Fatalf("plain-string drift against expected [] must converge via replay, got %+v", res)
	}
	obj := filterEntryObject(t, d.readSettingsEntries(t), "npm:foo@1.3.0")
	if obj == nil {
		t.Fatalf("settings must converge to the filter object: %s", mustReadSettings(t, d))
	}
	if ext, _ := obj["extensions"].([]any); ext == nil || len(ext) != 0 {
		t.Fatalf("extensions must converge to explicit [], got %#v", obj["extensions"])
	}
}

// ---------------------------------------------------------------------------
// SA-02 补全：identity+known generation 联合键的多目标保存/删除隔离
// ---------------------------------------------------------------------------

// TestPluginMultiTargetSamePackageCoexistsByGeneration：两个不同代际目标
// 交替使用同一包——B 的保存不覆盖 A 的登记（A→B→A 同包）、B 移除不删 A、
// 回 A 时旧登记仍可归属（already-ok 幂等继续工作）。
func TestPluginMultiTargetSamePackageCoexistsByGeneration(t *testing.T) {
	stateDir := t.TempDir()
	dA, dB := newFakePluginDistro(t), newFakePluginDistro(t)
	cur := dA
	machineID := "m-distro-a"
	probes := Probes{
		Identity: func(context.Context, string) platform.WSLUserIdentity {
			return platform.WSLUserIdentity{UID: "1000", Home: "/home/u", MachineID: machineID, OK: true}
		},
		ToUNC: func(_, linuxPath string) string {
			return filepath.Join(cur.uncRoot, filepath.FromSlash(strings.TrimPrefix(linuxPath, "/")))
		},
		Chmod: func(context.Context, string, string, ...string) error { return nil },
	}
	newCoord := func() *Coordinator {
		c := NewCoordinator(stateDir, nil, probes)
		c.SetPluginCommandRunner(func(ctx context.Context, _, script string) (string, error) {
			return cur.run(ctx, "", script)
		})
		c.SetPluginSourceParser(testPluginParser, testRefCheck)
		return c
	}
	countRows := func(c *Coordinator, identity string) map[string]string {
		st := c.state.load()
		out := map[string]string{}
		for _, e := range st.Plugins {
			if e.Identity == identity {
				out[e.Generation] = e.PinnedRef
			}
		}
		return out
	}
	genOf := func(c *Coordinator) string {
		target, err := c.ResolveTarget(context.Background(), "Ubuntu", CLIPi)
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		return target.Generation
	}

	// A 安装 npm:foo @1.3.0 → A 代登记一行。
	c := newCoord()
	genA := genOf(c)
	if res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:foo", Kind: PluginOpInstall, AnchorRef: "1.3.0",
	}); res.Outcome != PluginOutcomeInstalled {
		t.Fatalf("install on A: %+v", res)
	}
	// B 安装同包 @1.4.0 → 追加 B 代登记；A 行不被覆盖（单槽覆盖回归钉子）。
	machineID, cur = "m-distro-b", dB
	if res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:foo", Kind: PluginOpInstall, AnchorRef: "1.4.0",
	}); res.Outcome != PluginOutcomeInstalled {
		t.Fatalf("install on B: %+v", res)
	}
	rows := countRows(c, "npm:foo")
	if len(rows) != 2 {
		t.Fatalf("A and B registrations must coexist, got %+v", rows)
	}
	if rows[genA] != "1.3.0" {
		t.Fatalf("A's row must keep its own pinned ref (no single-slot overwrite): %+v", rows)
	}
	// 回 A：同锚点重放 → already-ok（A 登记仍可归属；distro A 侧形态未变）。
	machineID, cur = "m-distro-a", dA
	if res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:foo@1.3.0", Kind: PluginOpInstall, AnchorRef: "1.3.0",
	}); res.Outcome != PluginOutcomeAlreadyOK {
		t.Fatalf("return to A must hit its own registration (already-ok), got %+v", res)
	}
	// B 移除 → 只删 B 行；A 行保留。
	machineID, cur = "m-distro-b", dB
	if res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:foo@1.4.0", Kind: PluginOpRemove,
	}); res.Outcome != PluginOutcomeRemoved {
		t.Fatalf("remove on B: %+v", res)
	}
	rows = countRows(c, "npm:foo")
	if len(rows) != 1 || rows[genA] != "1.3.0" {
		t.Fatalf("removing on B must not delete A's registration: %+v", rows)
	}
	// 回 A 后 A 行仍归属可用（幂等继续工作）。
	machineID, cur = "m-distro-a", dA
	if res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:foo@1.3.0", Kind: PluginOpInstall, AnchorRef: "1.3.0",
	}); res.Outcome != PluginOutcomeAlreadyOK {
		t.Fatalf("A must still be managed after B's removal, got %+v", res)
	}
	// 未知代际的删除是 no-op（不可归属的删除请求不动任何行）。
	c.plugins.dropPluginEntry("npm:foo", GenerationUnknown)
	if rows = countRows(c, "npm:foo"); len(rows) != 1 {
		t.Fatalf("unknown-generation drop must be a no-op: %+v", rows)
	}
}
