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
effort = "low"

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
	// 按判定序（键字典序）以序列化形态追加；winonly 形态被拒不落盘。
	const afterImport = "# distro 用户注释：保留\nmodel = \"gpt-5\"\n\n[projects.\"/home/linux/proj\"]\ntrust_level = \"trusted\"\n\n[sandbox]\nmode = \"read-only\"\n\n[mcp_servers.distro-own]\ncommand = \"user-tool\"\n\n[mcp_servers.fetch]\ncommand = \"npx\"\nargs = [\"-y\", \"fetch-mcp\"]\n\n[mcp_servers.fetch.env]\nFETCH_TOKEN = \"secret\"\n\n[mcp_servers.web]\nurl = \"https://mcp.example/sse\"\n\n[profiles.fast]\neffort = \"low\"\nmodel = \"gpt-5-mini\"\n"
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
	const afterDeselect = "# distro 用户注释：保留\nmodel = \"gpt-5\"\n\n[projects.\"/home/linux/proj\"]\ntrust_level = \"trusted\"\n\n[sandbox]\nmode = \"read-only\"\n\n[mcp_servers.distro-own]\ncommand = \"user-tool\"\n\n[mcp_servers.web]\nurl = \"https://mcp.example/sse\"\n\n[profiles.fast]\neffort = \"low\"\nmodel = \"gpt-5-mini\"\n"
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
	src.toml = []byte("[profiles.good]\nmodel = \"gpt-5-mini\"\neffort = \"low\"\n\n[profiles.badpath]\nmodel = \"gpt-5\"\ncwd = \"C:\\\\work\\\\proj\"\n")
	if err := c.SetUnitSelection("Ubuntu", CLICodex, UnitCodexConfig, []string{"good", "badpath"}); err != nil {
		t.Fatal(err)
	}
	res := runCodexUnit(t, c)
	if res.Outcome != UnitApplied || !strings.Contains(res.Detail, "partial") {
		t.Fatalf("good profile applies, bad path profile stays, got %+v", res)
	}
	data, _ := os.ReadFile(codexFile(fp))
	got := string(data)
	if !strings.Contains(got, "[profiles.good]") {
		t.Fatalf("portable profile must land:\n%s", got)
	}
	if strings.Contains(got, "[profiles.badpath]") {
		t.Fatalf("windows-path profile must not land (验收 2：项目绝对路径不导入):\n%s", got)
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
