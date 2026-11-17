package main

// app_sharedmcp_test.go — App 门面层测试（共用 MCP spec §2.4/§3）：临时
// home + configDir 夹具端到端（Create → Distribute 四引擎 → Overview 状态
// distributed×4 → Undistribute → Delete 后各目标文件无残留、顶层键集复
// 原）；DTO 无 token 字段名；omp excluded 行存在。绝不触碰真实用户配置
// （SharedMcp 服务经 env 注入指向临时目录）。

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/sharedmcp"
)

// newSharedMcpFacadeTestApp 构造指向临时 home 的最小 App（服务经 env 注入
// 隔离所有引擎目标路径）。
func newSharedMcpFacadeTestApp(t *testing.T) (*App, string, string) {
	t.Helper()
	home := t.TempDir()
	cfgDir := t.TempDir()
	env := []string{
		"CODEX_HOME=" + filepath.Join(home, ".codex"),
		"PI_CODING_AGENT_DIR=" + filepath.Join(home, ".pi", "agent"),
		"HOME=" + home,
		"USERPROFILE=" + home,
	}
	logSvc := logging.NewService(cfgDir)
	app := &App{
		configDir: cfgDir,
		Log:       logSvc,
		SharedMcp: sharedmcp.New(home, cfgDir, env, logSvc),
	}
	t.Cleanup(logSvc.Close)
	return app, home, cfgDir
}

func overviewCell(t *testing.T, ov *SharedMcpOverview, name, engine string) sharedmcp.EngineCell {
	t.Helper()
	for _, e := range ov.Entries {
		if e.Entry.Name == name {
			return e.Engines[engine]
		}
	}
	t.Fatalf("Overview 缺条目 %s", name)
	return sharedmcp.EngineCell{}
}

func TestAppSharedMcp_EndToEndLifecycle(t *testing.T) {
	app, home, cfgDir := newSharedMcpFacadeTestApp(t)
	// 预置 .claude.json（多顶层键——键集复原断言基准）。
	claudePath := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(claudePath, claude54KeyFixtureMain(), 0o644); err != nil {
		t.Fatal(err)
	}
	claudeBefore, _ := os.ReadFile(claudePath)

	entry := sharedmcp.SharedMcpEntry{
		Name:    "demo-server",
		Type:    "stdio",
		Command: "npx",
		Args:    []string{"-y", "demo"},
		Env:     map[string]string{"K": "V"},
	}
	// Create → 登记文件新建。
	if _, err := app.CreateSharedMcpEntry(entry); err != nil {
		t.Fatalf("Create: %v", err)
	}
	regPath := filepath.Join(home, ".agents", ".mcp.json")
	if _, err := os.Stat(regPath); err != nil {
		t.Fatalf("登记文件未创建: %v", err)
	}

	// 四引擎分发 → distributed×4。
	engines := []string{"claude", "codex", "opencode", "pi"}
	for _, eng := range engines {
		if _, err := app.SetSharedMcpEngineSelection("demo-server", eng, true, false); err != nil {
			t.Fatalf("分发 %s: %v", eng, err)
		}
	}
	ov, err := app.ListSharedMcp()
	if err != nil {
		t.Fatal(err)
	}
	for _, eng := range engines {
		if c := overviewCell(t, ov, "demo-server", eng); c.Status != sharedmcp.EngineDistributed || !c.Selected {
			t.Fatalf("%s 应 distributed+selected: %+v", eng, c)
		}
	}
	// 目标文件均写入。
	assertFileContains(t, claudePath, `"command":"npx"`)
	assertFileContains(t, filepath.Join(home, ".codex", "config.toml"), "[mcp_servers.demo-server]")
	assertFileContains(t, filepath.Join(home, ".config", "opencode", "opencode.json"), `"mcp"`)
	piTarget, _ := os.ReadFile(filepath.Join(home, ".pi", "agent", "mcp.json"))
	if !strings.Contains(string(piTarget), "demo-server") {
		t.Fatalf("Pi 目标未写入: %s", piTarget)
	}

	// 逐引擎关闭 → 状态 not-distributed。
	if _, err := app.SetSharedMcpEngineSelection("demo-server", "codex", false, false); err != nil {
		t.Fatalf("关闭 codex: %v", err)
	}
	ov, _ = app.ListSharedMcp()
	if c := overviewCell(t, ov, "demo-server", "codex"); c.Status != sharedmcp.EngineNotDistributed {
		t.Fatalf("codex 应 not-distributed: %+v", c)
	}

	// Delete（含已分发引擎的还原联动）→ 各目标文件无残留、.claude.json 顶层
	// 键集复原。
	if _, err := app.DeleteSharedMcpEntry("demo-server", false); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	assertFileNotContains(t, claudePath, "demo-server")
	assertFileNotContains(t, filepath.Join(home, ".codex", "config.toml"), "demo-server")
	assertFileNotContains(t, filepath.Join(home, ".config", "opencode", "opencode.json"), "demo-server")
	piTarget, _ = os.ReadFile(filepath.Join(home, ".pi", "agent", "mcp.json"))
	if strings.Contains(string(piTarget), "demo-server") {
		t.Fatalf("Pi 目标残留: %s", piTarget)
	}
	// .claude.json 键集复原（契约语义：空 mcpServers 保留，其余键集恒等）。
	after, _ := os.ReadFile(claudePath)
	bk, ak := topKeyNamesMain(claudeBefore), topKeyNamesMain(after)
	if len(ak) != len(bk)+1 {
		t.Fatalf(".claude.json 顶层键集未复原:\nbefore=%v\nafter=%v", bk, ak)
	}
	orig := map[string]bool{}
	for _, k := range bk {
		orig[k] = true
	}
	delta := []string{}
	for _, k := range ak {
		if !orig[k] {
			delta = append(delta, k)
		}
	}
	if !reflect.DeepEqual(delta, []string{"mcpServers"}) {
		t.Fatalf("键集差集应恰为 mcpServers: %v", delta)
	}
	// 登记条目删除后 Overview 无该条目；状态库无残留。
	ov, _ = app.ListSharedMcp()
	for _, e := range ov.Entries {
		if e.Entry.Name == "demo-server" {
			t.Fatal("删除后条目仍在 Overview")
		}
	}
	cfgState, _ := os.ReadFile(filepath.Join(cfgDir, "shared-mcp-state.json"))
	if strings.Contains(string(cfgState), "demo-server") {
		t.Fatalf("状态库残留: %s", cfgState)
	}
	_ = regPath
}

func claude54KeyFixtureMain() []byte {
	var b strings.Builder
	b.WriteString("{\r\n")
	for i := 0; i < 6; i++ {
		comma := ","
		if i == 5 {
			comma = ""
		}
		b.WriteString("  \"k" + string(rune('a'+i)) + "\": " + strconvItoa(i) + comma + "\r\n")
	}
	b.WriteString("}")
	return []byte(b.String())
}

func strconvItoa(i int) string { return strconv.Itoa(i) }

func topKeyNamesMain(data []byte) []string {
	root := gjson.ParseBytes(data)
	if !root.IsObject() {
		return nil
	}
	names := make([]string, 0, len(root.Map()))
	for name := range root.Map() {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func assertFileContains(t *testing.T, path, substr string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s: %v", path, err)
	}
	if !strings.Contains(string(b), substr) {
		t.Fatalf("%s 缺 %q:\n%s", path, substr, b)
	}
}

func assertFileNotContains(t *testing.T, path, substr string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s: %v", path, err)
	}
	if strings.Contains(string(b), substr) {
		t.Fatalf("%s 不应含 %q:\n%s", path, substr, b)
	}
}

func TestAppSharedMcp_DTOHasNoTokenFieldNames(t *testing.T) {
	// 无令牌断言（spec §2.4）：DTO 字段名不含 token 字样（值域脱敏由
	// MaskedValue 前端呈现纪律承担；后端不引入任何 token 概念字段）。
	typ := reflect.TypeOf(sharedmcp.SharedMcpEntry{})
	for i := 0; i < typ.NumField(); i++ {
		name := strings.ToLower(typ.Field(i).Name)
		if strings.Contains(name, "token") {
			t.Fatalf("DTO 字段 %s 含 token 字样", typ.Field(i).Name)
		}
	}
}

func TestAppSharedMcp_OMPExcludedRowPresent(t *testing.T) {
	app, _, _ := newSharedMcpFacadeTestApp(t)
	ov, err := app.ListSharedMcp()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range ov.EnginesMeta {
		if m.ID == "omp" {
			found = true
			if m.Support != "excluded" || !strings.Contains(m.Detail, "裁定") {
				t.Fatalf("omp excluded 行不符: %+v", m)
			}
		}
	}
	if !found {
		t.Fatal("EnginesMeta 缺 omp excluded 行")
	}
}

func TestAppSharedMcp_FacadeReturnsLatestOverview(t *testing.T) {
	app, _, _ := newSharedMcpFacadeTestApp(t)
	ov, err := app.CreateSharedMcpEntry(sharedmcp.SharedMcpEntry{
		Name: "r", Type: "http", URL: "https://r.example.com/mcp",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ov.RegistryExists || len(ov.Entries) != 1 || ov.Entries[0].Entry.Name != "r" {
		t.Fatalf("Create 返回的 Overview 不是最新: %+v", ov)
	}
	// Refresh 语义别名。
	ov2, err := app.RefreshSharedMcp()
	if err != nil || len(ov2.Entries) != 1 {
		t.Fatalf("Refresh: %v", err)
	}
	// 名字校验错误从门面透出（中文可读）。
	if _, err := app.CreateSharedMcpEntry(sharedmcp.SharedMcpEntry{Name: "bad.name", Type: "http", URL: "https://x.example.com/m"}); err == nil || !strings.Contains(err.Error(), "不兼容") {
		t.Fatalf("名字校验未透出: %v", err)
	}
}
