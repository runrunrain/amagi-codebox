package wslsupply

// codex_config_test.go — Codex config.toml 导入钉子（P3-S4）：TOML 节投影
//（mcp_servers + 选中的 profiles）、TOML 保真（非目标节/注释字节保留）、
// 负面断言（trust/sandbox/approval/项目绝对路径/未知节绝不跨界——验收 2/3/5）。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/launcher"
)

type stubCodexConfigSource struct {
	toml []byte
	err  bool
}

func (s *stubCodexConfigSource) ConfigTOML() ([]byte, error) {
	if s.err {
		return nil, os.ErrPermission
	}
	return s.toml, nil
}

const codexSourceSample = `# 用户注释：model 与 provider 由 CodeBox env 管理
model = "gpt-5"

[projects."/home/win/proj"]
trust_level = "trusted"

[profiles.fast]
model = "gpt-5-mini"
model_reasoning_effort = "low"

[mcp_servers.web]
url = "https://mcp.example/sse"

[mcp_servers.fetch]
command = "npx.cmd"
args = ["-y", "fetch-mcp"]

[mcp_servers.fetch.env]
FETCH_TOKEN = "secret"

[mcp_servers.winonly]
command = "setup.bat"
`

func newS4CodexCoordinator(t *testing.T) (*Coordinator, *fakeProbes, *stubCodexConfigSource, *stubFactsSource) {
	t.Helper()
	fp := newFakeProbes(t)
	c := newCoordinatorWith(t, fp, &stubA2{outcome: launcher.SyncWSLPiLoginAuthFresh})
	src := &stubCodexConfigSource{}
	facts := &stubFactsSource{}
	facts.set(TransformFacts{
		ResolvedCommands: map[string]string{"npx": "/usr/bin/npx"},
		HostHomeWindows:  `C:\Users\u`,
	})
	c.RegisterProjection(NewCodexConfigAdapter(src, facts))
	if err := c.GrantUnitConsent(context.Background(), "Ubuntu", CLICodex, UnitCodexConfig); err != nil {
		t.Fatalf("grant consent: %v", err)
	}
	return c, fp, src, facts
}

func codexFile(fp *fakeProbes) string {
	return filepath.Join(fp.uncRoot, filepath.FromSlash("home/u/.codex/config.toml"))
}

func runCodexUnit(t *testing.T, c *Coordinator) UnitResult {
	t.Helper()
	res, err := c.RunUnits(context.Background(), "Ubuntu", CLICodex, ReasonSave, []UnitID{UnitCodexConfig})
	if err != nil {
		t.Fatalf("RunUnits: %v", err)
	}
	return res.Results[0]
}

func TestCodexConfigSelectionAndTOMLFidelity(t *testing.T) {
	c, fp, src, _ := newS4CodexCoordinator(t)
	src.toml = []byte(codexSourceSample)
	// distro 侧已有用户自有 config.toml（注释/信任决定/未知节——非受管内容
	// 字节不变是验收 3 的对象）。
	const distroSeed = "# distro 用户注释：保留\nmodel = \"gpt-5\"\n\n[projects.\"/home/linux/proj\"]\ntrust_level = \"trusted\"\n\n[sandbox]\nmode = \"read-only\"\n\n[mcp_servers.distro-own]\ncommand = \"user-tool\"\n"
	os.MkdirAll(filepath.Dir(codexFile(fp)), 0o700)
	os.WriteFile(codexFile(fp), []byte(distroSeed), 0o600)

	// 无选择：不写目标。
	res := runCodexUnit(t, c)
	if res.Outcome != UnitFresh {
		t.Fatalf("no selection must be a no-op, got %+v", res)
	}
	if got, _ := os.ReadFile(codexFile(fp)); string(got) != distroSeed {
		t.Fatalf("nothing must be written without selection")
	}

	// 选择 web（远程）+ fetch（shim 经验证映射）+ fast（profile）。
	if err := c.SetUnitSelection("Ubuntu", CLICodex, UnitCodexConfig, []string{"web", "fetch", "fast", "winonly"}); err != nil {
		t.Fatal(err)
	}
	res = runCodexUnit(t, c)
	if res.Outcome != UnitApplied {
		t.Fatalf("selected import must apply, got %+v", res)
	}
	data, err := os.ReadFile(codexFile(fp))
	if err != nil {
		t.Fatal(err)
	}
	// TOML 保真（验收 3，F4 整文确定性期望）：非受管内容逐字节保留 + 受管段
	// 按判定序（键字典序）以序列化形态追加；winonly 形态被拒不落盘；profile
	// 按 SA-04 白名单只携带可移植字段。
	const afterImport = "# distro 用户注释：保留\nmodel = \"gpt-5\"\n\n[projects.\"/home/linux/proj\"]\ntrust_level = \"trusted\"\n\n[sandbox]\nmode = \"read-only\"\n\n[mcp_servers.distro-own]\ncommand = \"user-tool\"\n\n[mcp_servers.fetch]\ncommand = \"npx\"\nargs = [\"-y\", \"fetch-mcp\"]\n\n[mcp_servers.fetch.env]\nFETCH_TOKEN = \"secret\"\n\n[mcp_servers.web]\nurl = \"https://mcp.example/sse\"\n\n[profiles.fast]\nmodel = \"gpt-5-mini\"\nmodel_reasoning_effort = \"low\"\n"
	if got := string(data); got != afterImport {
		t.Fatalf("import result must equal the deterministic expectation; got:\n%s\nwant:\n%s", got, afterImport)
	}
	// 再次运行（无变化，但选择集中含不可转换项）→ 稳态受阻（conversion-blocked，
	// 与 S2 pi/mcp 同语义）；取消选择不可转换项后收敛 fresh。
	res = runCodexUnit(t, c)
	if res.Outcome != UnitBlocked || !strings.Contains(res.Detail, "conversion-blocked") {
		t.Fatalf("steady state with unconvertible selection must stay blocked, got %+v", res)
	}
	if err := c.SetUnitSelection("Ubuntu", CLICodex, UnitCodexConfig, []string{"web", "fetch", "fast"}); err != nil {
		t.Fatal(err)
	}
	res = runCodexUnit(t, c)
	if res.Outcome != UnitFresh {
		t.Fatalf("unchanged without blocked selection must be fresh, got %+v", res)
	}

	// 取消选择 fetch：来源删除语义——仍等于上次写入的段被移除，其余字节不动。
	if err := c.SetUnitSelection("Ubuntu", CLICodex, UnitCodexConfig, []string{"web", "fast"}); err != nil {
		t.Fatal(err)
	}
	res = runCodexUnit(t, c)
	if res.Outcome != UnitApplied {
		t.Fatalf("deselection must remove the managed section, got %+v", res)
	}
	data2, _ := os.ReadFile(codexFile(fp))
	// F4：取消选择后的整文确定性期望（fetch 段及其尾随空行移除，其余逐字节不变）。
	const afterDeselect = "# distro 用户注释：保留\nmodel = \"gpt-5\"\n\n[projects.\"/home/linux/proj\"]\ntrust_level = \"trusted\"\n\n[sandbox]\nmode = \"read-only\"\n\n[mcp_servers.distro-own]\ncommand = \"user-tool\"\n\n[mcp_servers.web]\nurl = \"https://mcp.example/sse\"\n\n[profiles.fast]\nmodel = \"gpt-5-mini\"\nmodel_reasoning_effort = \"low\"\n"
	if got2 := string(data2); got2 != afterDeselect {
		t.Fatalf("deselect result must equal the deterministic expectation; got:\n%s\nwant:\n%s", got2, afterDeselect)
	}
}

func TestCodexConfigExistingUserTOMLMerge(t *testing.T) {
	c, fp, src, _ := newS4CodexCoordinator(t)
	src.toml = []byte("[mcp_servers.web]\nurl = \"https://new.example/sse\"\n\n[mcp_servers.own]\ncommand = \"user-tool\"\n\n# 用户自己的全局注释\n[sandbox]\nmode = \"read-only\"\n")
	// distro 侧已有用户 config.toml（含同名 web 段——按未托管处理）。
	os.MkdirAll(filepath.Dir(codexFile(fp)), 0o700)
	os.WriteFile(codexFile(fp), []byte("# distro 注释\n[mcp_servers.web]\nurl = \"https://user-own.example/sse\"\n\n[mcp_servers.own]\ncommand = \"user-tool\"\n"), 0o600)
	if err := c.SetUnitSelection("Ubuntu", CLICodex, UnitCodexConfig, []string{"web"}); err != nil {
		t.Fatal(err)
	}
	res := runCodexUnit(t, c)
	if res.Outcome != UnitBlocked || res.Detail != "unmanaged-existing" {
		t.Fatalf("same-name section on target must freeze unmanaged, got %+v", res)
	}
	data, _ := os.ReadFile(codexFile(fp))
	if !strings.Contains(string(data), "user-own.example") || !strings.Contains(string(data), "# distro 注释") {
		t.Fatalf("user toml must be untouched:\n%s", data)
	}
}

func TestCodexConfigLocalModificationFreezesAndTakeOver(t *testing.T) {
	c, fp, src, _ := newS4CodexCoordinator(t)
	src.toml = []byte("[mcp_servers.web]\nurl = \"https://mcp.example/sse\"\n")
	if err := c.SetUnitSelection("Ubuntu", CLICodex, UnitCodexConfig, []string{"web"}); err != nil {
		t.Fatal(err)
	}
	if res := runCodexUnit(t, c); res.Outcome != UnitApplied {
		t.Fatalf("first import failed: %+v", res)
	}
	// WSL 手改该段 → 冻结；来源变化也保留 WSL（both-changed）。
	os.WriteFile(codexFile(fp), []byte("[mcp_servers.web]\nurl = \"https://hand-edited.example/sse\"\n"), 0o600)
	src.toml = []byte("[mcp_servers.web]\nurl = \"https://changed.example/sse\"\n")
	res := runCodexUnit(t, c)
	if res.Outcome != UnitBlocked || res.Detail != "both-changed-frozen" {
		t.Fatalf("hand edit must freeze, got %+v", res)
	}
	// 预览后接管：以预览 hash CAS，托管域覆写，域外保留。
	previews, err := c.PreviewUnits(context.Background(), "Ubuntu", CLICodex, []UnitID{UnitCodexConfig})
	if err != nil || len(previews) != 1 {
		t.Fatalf("preview: %v %v", previews, err)
	}
	takeRes, err := c.TakeOverUnit(context.Background(), "Ubuntu", CLICodex, UnitCodexConfig, previews[0].TargetHash)
	if err != nil {
		t.Fatalf("takeover: %v", err)
	}
	if takeRes.Outcome != UnitApplied {
		t.Fatalf("takeover must apply, got %+v", takeRes)
	}
	data, _ := os.ReadFile(codexFile(fp))
	if !strings.Contains(string(data), "changed.example") {
		t.Fatalf("takeover must overwrite managed domain with source:\n%s", data)
	}
}

func TestCodexFrozenKeyDoesNotVetoHealthyUpdate(t *testing.T) {
	// S4 diting F2①：多键中一冻结键（目标手改、登记基准陈旧）与健康键更新
	// 共存——自校验只覆盖本轮写入面：健康键必须成功落盘，冻结键保留本地。
	c, fp, src, _ := newS4CodexCoordinator(t)
	src.toml = []byte("[mcp_servers.web]\nurl = \"https://old.example/sse\"\n\n[mcp_servers.fetch]\ncommand = \"npx.cmd\"\nargs = [\"-y\", \"fetch-mcp\"]\n")
	if err := c.SetUnitSelection("Ubuntu", CLICodex, UnitCodexConfig, []string{"web", "fetch"}); err != nil {
		t.Fatal(err)
	}
	if res := runCodexUnit(t, c); res.Outcome != UnitApplied {
		t.Fatalf("initial import failed: %+v", res)
	}
	// 目标手改 web 段（基准变陈旧）；来源更新 fetch 参数。
	data, _ := os.ReadFile(codexFile(fp))
	hand := strings.Replace(string(data), "https://old.example/sse", "https://hand-edited.example/sse", 1)
	if err := os.WriteFile(codexFile(fp), []byte(hand), 0o600); err != nil {
		t.Fatal(err)
	}
	src.toml = []byte("[mcp_servers.web]\nurl = \"https://old.example/sse\"\n\n[mcp_servers.fetch]\ncommand = \"npx.cmd\"\nargs = [\"-y\", \"other-pkg\"]\n")
	res := runCodexUnit(t, c)
	if res.Outcome != UnitApplied {
		t.Fatalf("healthy key update must not be vetoed by frozen-key stale baseline, got %+v", res)
	}
	if !strings.Contains(res.Detail, "partial") {
		t.Fatalf("partial expected (web frozen, fetch updated): %s", res.Detail)
	}
	got, _ := os.ReadFile(codexFile(fp))
	if !strings.Contains(string(got), "hand-edited.example") {
		t.Fatalf("frozen key must keep local value:\n%s", got)
	}
	if !strings.Contains(string(got), "other-pkg") {
		t.Fatalf("healthy key update must land:\n%s", got)
	}
	if strings.Contains(string(got), "fetch-mcp") {
		t.Fatalf("stale fetch args must be replaced:\n%s", got)
	}
}

func TestCodexAdoptKeepsHandModifiedStaleSection(t *testing.T) {
	// S4 diting F2②：adopt + 来源已删 + 目标手改 → 保留本地 note + 接管成功，
	// 目标保留本地值（陈旧基准不参与自校验，也不被移除）。
	c, fp, src, _ := newS4CodexCoordinator(t)
	src.toml = []byte("[mcp_servers.web]\nurl = \"https://old.example/sse\"\n\n[profiles.fast]\nmodel = \"gpt-5-mini\"\n")
	if err := c.SetUnitSelection("Ubuntu", CLICodex, UnitCodexConfig, []string{"web", "fast"}); err != nil {
		t.Fatal(err)
	}
	if res := runCodexUnit(t, c); res.Outcome != UnitApplied {
		t.Fatalf("initial import failed: %+v", res)
	}
	// 来源删除 web；目标手改 web 段；fast 变化（接管覆写对象）。
	data, _ := os.ReadFile(codexFile(fp))
	hand := strings.Replace(string(data), "https://old.example/sse", "https://hand-edited.example/sse", 1)
	if err := os.WriteFile(codexFile(fp), []byte(hand), 0o600); err != nil {
		t.Fatal(err)
	}
	src.toml = []byte("[profiles.fast]\nmodel = \"gpt-5-max\"\n")
	previews, err := c.PreviewUnits(context.Background(), "Ubuntu", CLICodex, []UnitID{UnitCodexConfig})
	if err != nil || len(previews) != 1 {
		t.Fatalf("preview: %v %v", previews, err)
	}
	takeRes, err := c.TakeOverUnit(context.Background(), "Ubuntu", CLICodex, UnitCodexConfig, previews[0].TargetHash)
	if err != nil {
		t.Fatalf("takeover: %v", err)
	}
	if takeRes.Outcome != UnitApplied {
		t.Fatalf("adopt must succeed with stale hand-modified section kept local, got %+v", takeRes)
	}
	if !strings.Contains(takeRes.Detail, "保留本地") {
		t.Fatalf("keep-local note expected: %s", takeRes.Detail)
	}
	got, _ := os.ReadFile(codexFile(fp))
	if !strings.Contains(string(got), "hand-edited.example") {
		t.Fatalf("hand-modified stale section must be preserved:\n%s", got)
	}
	if !strings.Contains(string(got), "gpt-5-max") {
		t.Fatalf("managed section must be taken over from source:\n%s", got)
	}
}

func TestCodexProfilesPortability(t *testing.T) {
	c, fp, src, _ := newS4CodexCoordinator(t)
	// SA-04：profile 按白名单导入——未知字段（cwd 携带项目绝对路径）不导入，
	// 白名单字段照常落地（验收 2：项目绝对路径不跨界，由白名单边界保证）。
	src.toml = []byte("[profiles.good]\nmodel = \"gpt-5-mini\"\nmodel_reasoning_effort = \"low\"\n\n[profiles.badpath]\nmodel = \"gpt-5\"\ncwd = \"C:\\\\work\\\\proj\"\n")
	if err := c.SetUnitSelection("Ubuntu", CLICodex, UnitCodexConfig, []string{"good", "badpath"}); err != nil {
		t.Fatal(err)
	}
	res := runCodexUnit(t, c)
	if res.Outcome != UnitApplied || !strings.Contains(res.Detail, "partial") {
		t.Fatalf("both profiles apply (badpath partially: cwd dropped), got %+v", res)
	}
	data, _ := os.ReadFile(codexFile(fp))
	got := string(data)
	if !strings.Contains(got, "[profiles.good]") {
		t.Fatalf("portable profile must land:\n%s", got)
	}
	if !strings.Contains(got, "[profiles.badpath]") || !strings.Contains(got, "gpt-5") {
		t.Fatalf("badpath profile whitelist fields must land:\n%s", got)
	}
	if strings.Contains(got, "cwd") || strings.Contains(got, `C:\work`) {
		t.Fatalf("unknown cwd field (project absolute path) must not cross (验收 2):\n%s", got)
	}
	if !strings.Contains(res.Detail, "cwd") {
		t.Fatalf("dropped fields must be reported in detail: %s", res.Detail)
	}
}

func TestCodexCorruptManagedScopeRefuses(t *testing.T) {
	c, fp, src, _ := newS4CodexCoordinator(t)
	src.toml = []byte("[mcp_servers.a]\ncommand = \"npx\"\n")
	// 目标受管段损坏（不可解析值）：拒绝不覆盖。
	os.MkdirAll(filepath.Dir(codexFile(fp)), 0o700)
	os.WriteFile(codexFile(fp), []byte("[mcp_servers.a]\ncommand = @broken\n"), 0o600)
	if err := c.SetUnitSelection("Ubuntu", CLICodex, UnitCodexConfig, []string{"a"}); err != nil {
		t.Fatal(err)
	}
	res := runCodexUnit(t, c)
	if res.Outcome != UnitBlocked || res.Detail != "target-corrupt" {
		t.Fatalf("corrupt target must refuse (target-corrupt), got %+v", res)
	}
	data, _ := os.ReadFile(codexFile(fp))
	if !strings.Contains(string(data), "@broken") {
		t.Fatalf("corrupt target must be preserved as-is:\n%s", data)
	}
	// 来源受管段损坏同样受阻（source-corrupt）。
	src.toml = []byte("[mcp_servers.a]\ncommand = @bad\n")
	os.Remove(codexFile(fp))
	res = runCodexUnit(t, c)
	if res.Outcome != UnitBlocked || res.Detail != "source-corrupt" {
		t.Fatalf("corrupt source must block, got %+v", res)
	}
}

// TestCodexProfileWhitelistBlocksPermissionsAndUnknown（SA-04）：profile 内
// 权限键（approval_policy/sandbox_mode）、未知字段与嵌套表不导入；白名单字段
// 照常落地并诚实报告丢弃项；目标侧既有权限决定（其它节/顶层键）逐字节保留。
func TestCodexProfileWhitelistBlocksPermissionsAndUnknown(t *testing.T) {
	c, fp, src, _ := newS4CodexCoordinator(t)
	src.toml = []byte(`[profiles.demo]
model = "gpt-5-mini"
approval_policy = "never"
sandbox_mode = "danger-full-access"
unknown_host_field = "copied"

[profiles.demo.experimental]
flag = true
`)
	// distro 侧用户自有权限决定（顶层 approval_policy + 独立节）必须保留。
	const distroSeed = "# user comment\napproval_policy = \"never\"\n\n[projects.\"/home/linux/proj\"]\ntrust_level = \"trusted\"\n"
	os.MkdirAll(filepath.Dir(codexFile(fp)), 0o700)
	os.WriteFile(codexFile(fp), []byte(distroSeed), 0o600)
	if err := c.SetUnitSelection("Ubuntu", CLICodex, UnitCodexConfig, []string{"profiles/demo"}); err != nil {
		t.Fatal(err)
	}
	res := runCodexUnit(t, c)
	if res.Outcome != UnitApplied {
		t.Fatalf("profile import must apply, got %+v", res)
	}
	if !strings.Contains(res.Detail, "partial") || !strings.Contains(res.Detail, "approval_policy") {
		t.Fatalf("dropped permission fields must be reported: %s", res.Detail)
	}
	data, _ := os.ReadFile(codexFile(fp))
	got := string(data)
	if !strings.Contains(got, "[profiles.demo]") || !strings.Contains(got, `model = "gpt-5-mini"`) {
		t.Fatalf("whitelist fields must land:\n%s", got)
	}
	for _, banned := range []string{"danger-full-access", "unknown_host_field", "experimental", "copied"} {
		if strings.Contains(got, banned) {
			t.Fatalf("permission/unknown/nested field %q must not cross:\n%s", banned, got)
		}
	}
	// 目标侧既有权限决定逐字节保留（顶层 approval_policy 与 projects 节）。
	if !strings.Contains(got, "# user comment") || !strings.Contains(got, `[projects."/home/linux/proj"]`) {
		t.Fatalf("target local decisions must be preserved byte-for-byte:\n%s", got)
	}
	if strings.Count(got, `approval_policy = "never"`) != 1 {
		t.Fatalf("exactly the pre-existing approval_policy must remain:\n%s", got)
	}
}

// TestCodexProfileTakeoverPreservesTargetLocalPolicy（SA-04）：目标已有同名
// profile（用户自有，含本地权限决定）时按未托管冻结；预览后接管仅覆盖白名单
// 字段，本地权限决定保留。
func TestCodexProfileTakeoverPreservesTargetLocalPolicy(t *testing.T) {
	c, fp, src, _ := newS4CodexCoordinator(t)
	src.toml = []byte("[profiles.demo]\nmodel = \"gpt-5-mini\"\napproval_policy = \"never\"\n")
	os.MkdirAll(filepath.Dir(codexFile(fp)), 0o700)
	os.WriteFile(codexFile(fp), []byte("# user\n[profiles.demo]\nmodel = \"old-model\"\napproval_policy = \"on-request\"\nnotify = true\n"), 0o600)
	if err := c.SetUnitSelection("Ubuntu", CLICodex, UnitCodexConfig, []string{"demo"}); err != nil {
		t.Fatal(err)
	}
	res := runCodexUnit(t, c)
	if res.Outcome != UnitBlocked || res.Detail != "unmanaged-existing" {
		t.Fatalf("same-name target profile must freeze unmanaged, got %+v", res)
	}
	previews, err := c.PreviewUnits(context.Background(), "Ubuntu", CLICodex, []UnitID{UnitCodexConfig})
	if err != nil || len(previews) != 1 || previews[0].TargetHash == "" {
		t.Fatalf("preview: %v %+v", err, previews)
	}
	takeRes, err := c.TakeOverUnit(context.Background(), "Ubuntu", CLICodex, UnitCodexConfig, previews[0].TargetHash)
	if err != nil {
		t.Fatalf("takeover: %v", err)
	}
	if takeRes.Outcome != UnitApplied {
		t.Fatalf("takeover must apply, got %+v", takeRes)
	}
	data, _ := os.ReadFile(codexFile(fp))
	got := string(data)
	if !strings.Contains(got, `model = "gpt-5-mini"`) {
		t.Fatalf("takeover must overwrite managed whitelist fields:\n%s", got)
	}
	// 源侧 approval_policy（never）不跨界；目标本地 policy/未知字段保留。
	if strings.Contains(got, `"never"`) {
		t.Fatalf("source approval_policy must not cross:\n%s", got)
	}
	if !strings.Contains(got, `approval_policy = "on-request"`) || !strings.Contains(got, "notify = true") || !strings.Contains(got, "# user") {
		t.Fatalf("target local policy/unknown fields must be preserved on takeover:\n%s", got)
	}
}

// TestCodexProfileUpdateKeepsLocalFields（SA-04）：接管把目标本地字段并入写入
// 基准后，来源更新走 merge——本地权限决定/未知字段继续保留，白名单字段更新。
func TestCodexProfileUpdateKeepsLocalFields(t *testing.T) {
	c, fp, src, _ := newS4CodexCoordinator(t)
	src.toml = []byte("[profiles.demo]\nmodel = \"gpt-5-mini\"\n")
	os.MkdirAll(filepath.Dir(codexFile(fp)), 0o700)
	os.WriteFile(codexFile(fp), []byte("# user\n[profiles.demo]\nmodel = \"old-model\"\napproval_policy = \"on-request\"\nnotify = true\n"), 0o600)
	if err := c.SetUnitSelection("Ubuntu", CLICodex, UnitCodexConfig, []string{"demo"}); err != nil {
		t.Fatal(err)
	}
	if res := runCodexUnit(t, c); res.Outcome != UnitBlocked || res.Detail != "unmanaged-existing" {
		t.Fatalf("same-name target profile must freeze unmanaged, got %+v", res)
	}
	previews, err := c.PreviewUnits(context.Background(), "Ubuntu", CLICodex, []UnitID{UnitCodexConfig})
	if err != nil || len(previews) != 1 {
		t.Fatalf("preview: %v %+v", err, previews)
	}
	if _, err := c.TakeOverUnit(context.Background(), "Ubuntu", CLICodex, UnitCodexConfig, previews[0].TargetHash); err != nil {
		t.Fatalf("takeover: %v", err)
	}
	// 来源更新 model：merge 保留本地 approval_policy/notify。
	src.toml = []byte("[profiles.demo]\nmodel = \"gpt-5-max\"\n")
	if res := runCodexUnit(t, c); res.Outcome != UnitApplied {
		t.Fatalf("source update must apply, got %+v", res)
	}
	got, _ := os.ReadFile(codexFile(fp))
	if !strings.Contains(string(got), `model = "gpt-5-max"`) {
		t.Fatalf("source update must land:\n%s", got)
	}
	if !strings.Contains(string(got), `approval_policy = "on-request"`) || !strings.Contains(string(got), "notify = true") {
		t.Fatalf("target local fields must survive managed update (merge):\n%s", got)
	}
}

// TestCodexProfileOnlyPermissionsStays（SA-04）：profile 只含权限/未知字段时
// 无可移植内容——转换受阻保持目标现状。
func TestCodexProfileOnlyPermissionsStays(t *testing.T) {
	c, fp, src, _ := newS4CodexCoordinator(t)
	src.toml = []byte("[profiles.locked]\napproval_policy = \"never\"\nsandbox_mode = \"danger-full-access\"\n")
	if err := c.SetUnitSelection("Ubuntu", CLICodex, UnitCodexConfig, []string{"locked"}); err != nil {
		t.Fatal(err)
	}
	res := runCodexUnit(t, c)
	if res.Outcome != UnitBlocked || !strings.Contains(res.Detail, "conversion-blocked") {
		t.Fatalf("permission-only profile must stay blocked, got %+v", res)
	}
	if _, err := os.Stat(codexFile(fp)); !os.IsNotExist(err) {
		t.Fatal("permission-only profile must not write the target")
	}
}

// TestCodexDetachedExplicitTakeOverRestores（SA-11）：停跟随后，显式「预览后
// 确认接管」必须能重建基准并解除 Detached（修复前 AdoptCandidate 无条件拒绝
// detached，唯一声明的恢复操作必定失败）。
func TestCodexDetachedExplicitTakeOverRestores(t *testing.T) {
	c, fp, src, _ := newS4CodexCoordinator(t)
	src.toml = []byte("[mcp_servers.web]\nurl = \"https://mcp.example/sse\"\n")
	if err := c.SetUnitSelection("Ubuntu", CLICodex, UnitCodexConfig, []string{"web"}); err != nil {
		t.Fatal(err)
	}
	if res := runCodexUnit(t, c); res.Outcome != UnitApplied {
		t.Fatalf("initial import failed: %+v", res)
	}
	// 保留本地（停跟随）后，普通供给零动作（fresh no-change，不写目标）。
	if err := c.DropUnitRegistration("Ubuntu", CLICodex, UnitCodexConfig); err != nil {
		t.Fatalf("drop registration: %v", err)
	}
	if res := runCodexUnit(t, c); res.Outcome != UnitFresh || res.Detail != "no-change" {
		t.Fatalf("detached unit must stay idle (fresh no-change), got %+v", res)
	}
	// 显式接管：预览携带目标 hash 令牌，TakeOver 重建并解除。
	previews, err := c.PreviewUnits(context.Background(), "Ubuntu", CLICodex, []UnitID{UnitCodexConfig})
	if err != nil || len(previews) != 1 {
		t.Fatalf("preview: %v %+v", err, previews)
	}
	if previews[0].BlockedReason != "detached" || previews[0].TargetHash == "" {
		t.Fatalf("detached preview must carry takeover token: %+v", previews[0])
	}
	takeRes, err := c.TakeOverUnit(context.Background(), "Ubuntu", CLICodex, UnitCodexConfig, previews[0].TargetHash)
	if err != nil {
		t.Fatalf("takeover: %v", err)
	}
	if takeRes.Outcome != UnitApplied {
		t.Fatalf("explicit takeover must restore detached unit, got %+v", takeRes)
	}
	// 解除后恢复跟随：来源变化能再次安全更新。
	src.toml = []byte("[mcp_servers.web]\nurl = \"https://changed.example/sse\"\n")
	if res := runCodexUnit(t, c); res.Outcome != UnitApplied {
		t.Fatalf("restored unit must follow source again, got %+v", res)
	}
	data, _ := os.ReadFile(codexFile(fp))
	if !strings.Contains(string(data), "changed.example") {
		t.Fatalf("restored unit must apply source change:\n%s", data)
	}
}
