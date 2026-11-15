package sharedmcp

// registry_test.go — 登记文件 CRUD 的保真与边界测试（spec §2.4）。全部
// 使用临时目录 fixture，绝不触碰真实 ~/.agents/.mcp.json。

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

// registryFixture 是仿真登记文件：CRLF 行尾 + 多行 2 空格缩进 + 未知顶层键
// （$schema）+ 手写条目（web-search-prime / zhipu-vision，后者携带未知键
// x-custom）。spec §2.4 CRUD 保真夹具。
const registryFixture = "{\r\n  \"$schema\": \"https://agents.example/schema\",\r\n  \"mcpServers\": {\r\n    \"web-search-prime\": {\r\n      \"type\": \"http\",\r\n      \"url\": \"https://mcp.example.com/search\",\r\n      \"headers\": {\r\n        \"Authorization\": \"Bearer SECRET-HEADER\"\r\n      },\r\n      \"description\": \"web search\"\r\n    },\r\n    \"zhipu-vision\": {\r\n      \"type\": \"stdio\",\r\n      \"command\": \"npx\",\r\n      \"args\": [\r\n        \"-y\",\r\n        \"zhipu-vision\"\r\n      ],\r\n      \"env\": {\r\n        \"API_KEY\": \"SECRET-ENV\"\r\n      },\r\n      \"x-custom\": {\"a\": [1, 2]}\r\n    }\r\n  }\r\n}"

// newRegistryTestService 构造指向临时 home 的服务（env 指向临时引擎路径，
// 与真实用户目录隔离）。
func newRegistryTestService(t *testing.T) (*Service, string, string) {
	t.Helper()
	home := t.TempDir()
	cfgDir := t.TempDir()
	env := testEnv(home)
	return New(home, cfgDir, env, nil), home, cfgDir
}

func testEnv(home string) []string {
	return []string{
		"CODEX_HOME=" + filepath.Join(home, ".codex"),
		"PI_CODING_AGENT_DIR=" + filepath.Join(home, ".pi", "agent"),
		"HOME=" + home,
		"USERPROFILE=" + home,
	}
}

func writeRegistryFixture(t *testing.T, home string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".agents", ".mcp.json"), []byte(registryFixture), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readRegistry(t *testing.T, home string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, ".agents", ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// assertProjectionEqual 断言「移除目标条目后的投影字节相等」（spec §2.1
// 步骤 5，测试与实现共用的保真断言语义）。
func assertProjectionEqual(t *testing.T, before, after []byte, name string) {
	t.Helper()
	pb, pa := projectionWithoutEntry(before, name), projectionWithoutEntry(after, name)
	if !bytes.Equal(pb, pa) {
		t.Fatalf("保真断言失败：移除条目 %s 后的投影字节不等\nbefore-proj:\n%q\nafter-proj:\n%q", name, pb, pa)
	}
}

func stdioEntry(name string) SharedMcpEntry {
	return SharedMcpEntry{
		Name:    name,
		Type:    "stdio",
		Command: "npx",
		Args:    []string{"-y", "demo-server"},
		Env:     map[string]string{"K": "V"},
	}
}

func httpEntry(name string) SharedMcpEntry {
	return SharedMcpEntry{
		Name:        name,
		Type:        "http",
		URL:         "https://demo.example.com/mcp",
		Headers:     map[string]string{"Authorization": "Bearer x"},
		Description: "demo",
	}
}

func TestRegistry_ListParsesFixture(t *testing.T) {
	svc, home, _ := newRegistryTestService(t)
	writeRegistryFixture(t, home)

	entries, exists, err := svc.List()
	if err != nil || !exists {
		t.Fatalf("List: err=%v exists=%v", err, exists)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	byName := map[string]SharedMcpEntry{}
	for _, e := range entries {
		byName[e.Name] = e
	}
	wsp := byName["web-search-prime"]
	if wsp.Type != "http" || wsp.URL != "https://mcp.example.com/search" || wsp.Headers["Authorization"] != "Bearer SECRET-HEADER" || wsp.Description != "web search" {
		t.Fatalf("web-search-prime 解析不符: %+v", wsp)
	}
	if len(wsp.UnknownKeys) != 0 {
		t.Fatalf("web-search-prime 不应有未知键: %v", wsp.UnknownKeys)
	}
	zv := byName["zhipu-vision"]
	if zv.Type != "stdio" || zv.Command != "npx" || !reflect.DeepEqual(zv.Args, []string{"-y", "zhipu-vision"}) || zv.Env["API_KEY"] != "SECRET-ENV" {
		t.Fatalf("zhipu-vision 解析不符: %+v", zv)
	}
	if !reflect.DeepEqual(zv.UnknownKeys, []string{"x-custom"}) {
		t.Fatalf("zhipu-vision 未知键不符: %v", zv.UnknownKeys)
	}
}

func TestRegistry_ListMissingFileAndMissingKey(t *testing.T) {
	svc, _, _ := newRegistryTestService(t)
	entries, exists, err := svc.List()
	if err != nil || exists || len(entries) != 0 {
		t.Fatalf("缺失文件应返回空列表+exists=false: err=%v exists=%v n=%d", err, exists, len(entries))
	}
	// 文件在但无 mcpServers 键 → 空列表 + exists=true。
	home := svc.userHome
	if err := os.MkdirAll(filepath.Join(home, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(svc.registryPath(), []byte("{\r\n  \"other\": 1\r\n}\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, exists, err = svc.List()
	if err != nil || !exists || len(entries) != 0 {
		t.Fatalf("无 mcpServers 键应返回空列表+exists=true: err=%v exists=%v n=%d", err, exists, len(entries))
	}
}

func TestRegistry_CreateUpdateDeleteFidelity(t *testing.T) {
	svc, home, _ := newRegistryTestService(t)
	writeRegistryFixture(t, home)

	// Create：投影字节相等 + json 合法 + 顶层键集不变 + 0600（Unix）。
	before := readRegistry(t, home)
	if err := svc.Create(httpEntry("new-remote")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	after := readRegistry(t, home)
	assertProjectionEqual(t, before, after, "new-remote")
	if !gjson.ValidBytes(after) {
		t.Fatal("Create 后不是合法 JSON")
	}
	if !reflect.DeepEqual(topKeyNames(before), topKeyNames(after)) {
		t.Fatalf("顶层键集变化: %v → %v", topKeyNames(before), topKeyNames(after))
	}
	if !bytes.Contains(after, []byte("\r\n")) {
		t.Fatal("CRLF 行尾被归一化")
	}
	assertMode0600(t, svc.registryPath())
	got := gjson.GetBytes(after, "mcpServers.new-remote")
	if !got.IsObject() || got.Get("type").String() != "http" {
		t.Fatalf("新条目缺失或形态错误: %s", got.Raw)
	}
	// 手写条目 raw 值不受扰（独立于投影断言的值级复核）。
	if gjson.GetBytes(after, "mcpServers.web-search-prime").Raw != gjson.GetBytes(before, "mcpServers.web-search-prime").Raw {
		t.Fatal("手写条目被改动")
	}

	// Update：改 zhipu-vision 的 description；未知键 x-custom 连同 raw 值保留。
	before = after
	updated := stdioEntry("zhipu-vision")
	updated.Description = "updated desc"
	if err := svc.Update("zhipu-vision", updated); err != nil {
		t.Fatalf("Update: %v", err)
	}
	after = readRegistry(t, home)
	assertProjectionEqual(t, before, after, "zhipu-vision")
	customBefore := gjson.GetBytes(before, "mcpServers.zhipu-vision.x-custom").Raw
	customAfter := gjson.GetBytes(after, "mcpServers.zhipu-vision.x-custom").Raw
	if customAfter != customBefore {
		t.Fatalf("未知键 raw 值未原样保留: before=%s after=%s", customBefore, customAfter)
	}
	if gjson.GetBytes(after, "mcpServers.zhipu-vision.description").String() != "updated desc" {
		t.Fatal("description 未更新")
	}

	// Delete：投影相等 + 顶层键集不变（mcpServers 清空后保留 {}）。
	before = after
	if err := svc.Delete("new-remote"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	after = readRegistry(t, home)
	assertProjectionEqual(t, before, after, "new-remote")
	if reflect.DeepEqual(topKeyNames(before), topKeyNames(after)) != true {
		t.Fatalf("顶层键集变化: %v → %v", topKeyNames(before), topKeyNames(after))
	}
	if !gjson.GetBytes(after, "mcpServers").Exists() {
		t.Fatal("mcpServers 键被删除（应保留空对象）")
	}
	if gjson.GetBytes(after, "mcpServers.new-remote").Exists() {
		t.Fatal("条目未删除")
	}
}

func assertMode0600(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		// Windows 无 POSIX 权限位（os.Stat 只反映只读位）；0600 断言仅 Unix
		// 有意义——单条断言跳过，不中断同测试其余保真断言。
		return
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("权限不是 0600: %v", fi.Mode().Perm())
	}
}

func TestRegistry_CreateOnMissingFileAndMissingKey(t *testing.T) {
	svc, home, _ := newRegistryTestService(t)
	// 文件整体缺失 → 直接成功并新建（R5 空态承诺）。
	if err := svc.Create(stdioEntry("first")); err != nil {
		t.Fatalf("Create on missing file: %v", err)
	}
	data := readRegistry(t, home)
	if !gjson.ValidBytes(data) || !gjson.GetBytes(data, "mcpServers.first").Exists() {
		t.Fatalf("新建文件形态错误: %s", data)
	}
	// mcpServers 键缺失 → 新建该键。
	if err := os.WriteFile(svc.registryPath(), []byte("{\r\n  \"other\": true\r\n}\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := svc.Create(stdioEntry("second")); err != nil {
		t.Fatalf("Create on missing key: %v", err)
	}
	data = readRegistry(t, home)
	if !gjson.GetBytes(data, "mcpServers.second").Exists() || !gjson.GetBytes(data, "other").Bool() {
		t.Fatalf("mcpServers 新键缺失或其它键丢失: %s", data)
	}
}

func TestRegistry_CorruptFileRefusedAndUntouched(t *testing.T) {
	svc, home, _ := newRegistryTestService(t)
	if err := os.MkdirAll(filepath.Join(home, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	corrupt := []byte("{\r\n  \"mcpServers\": [BROKEN\r\n")
	if err := os.WriteFile(svc.registryPath(), corrupt, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.List(); KindOf(err) != KindRegistryCorrupt {
		t.Fatalf("List 应拒绝坏 JSON: %v", err)
	}
	if err := svc.Create(stdioEntry("x")); KindOf(err) != KindRegistryCorrupt {
		t.Fatalf("Create 应拒绝坏 JSON: %v", err)
	}
	if err := svc.Update("x", stdioEntry("x")); KindOf(err) != KindRegistryCorrupt {
		t.Fatalf("Update 应拒绝坏 JSON: %v", err)
	}
	if err := svc.Delete("x"); KindOf(err) != KindRegistryCorrupt {
		t.Fatalf("Delete 应拒绝坏 JSON: %v", err)
	}
	// 根非对象同样拒绝。
	if err := os.WriteFile(svc.registryPath(), []byte("[1,2]"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.List(); KindOf(err) != KindRegistryCorrupt {
		t.Fatalf("根非对象应拒绝: %v", err)
	}
	got, _ := os.ReadFile(svc.registryPath())
	if !bytes.Equal(got, []byte("[1,2]")) {
		t.Fatal("坏 JSON 文件被改动")
	}
}

func TestRegistry_NameAndExistenceValidation(t *testing.T) {
	svc, home, _ := newRegistryTestService(t)
	writeRegistryFixture(t, home)

	for _, name := range []string{"../x", "a/b", `a\b`, "a.b", "", ".hidden", "中文名", "a b"} {
		e := stdioEntry(name)
		if err := svc.Create(e); KindOf(err) != KindNameInvalid {
			t.Fatalf("名字 %q 应被拒绝，got %v", name, err)
		}
	}
	if err := svc.Create(stdioEntry("a-b_2")); err != nil {
		t.Fatalf("合法名 a-b_2 应通过: %v", err)
	}
	if err := svc.Create(stdioEntry("web-search-prime")); KindOf(err) != KindExists {
		t.Fatalf("同名 Create 应拒绝: %v", err)
	}
	if err := svc.Update("nope", stdioEntry("nope")); KindOf(err) != KindNotFound {
		t.Fatalf("缺名 Update 应拒绝: %v", err)
	}
	if err := svc.Delete("nope"); KindOf(err) != KindNotFound {
		t.Fatalf("缺名 Delete 应拒绝: %v", err)
	}
	// name 不可改。
	if err := svc.Update("web-search-prime", stdioEntry("other-name")); KindOf(err) != KindInvalidEntry {
		t.Fatalf("改名 Update 应拒绝: %v", err)
	}
	// 字段校验。
	if err := svc.Create(SharedMcpEntry{Name: "bad", Type: "stdio", Command: " "}); KindOf(err) != KindInvalidEntry {
		t.Fatalf("空 command 应拒绝: %v", err)
	}
	if err := svc.Create(SharedMcpEntry{Name: "bad", Type: "http", URL: "not a url"}); KindOf(err) != KindInvalidEntry {
		t.Fatalf("坏 URL 应拒绝: %v", err)
	}
	if err := svc.Create(SharedMcpEntry{Name: "bad", Type: "sse"}); KindOf(err) != KindInvalidEntry {
		t.Fatalf("未知 type 应拒绝: %v", err)
	}
	if err := svc.Create(SharedMcpEntry{Name: "bad", Type: "stdio", Command: "x", Env: map[string]string{"": "v"}}); KindOf(err) != KindInvalidEntry {
		t.Fatalf("空 env 键应拒绝: %v", err)
	}
}

func TestRegistry_NewEntryDoesNotLeakDTOFields(t *testing.T) {
	svc, home, _ := newRegistryTestService(t)
	writeRegistryFixture(t, home)
	if err := svc.Create(stdioEntry("clean")); err != nil {
		t.Fatal(err)
	}
	data := readRegistry(t, home)
	if strings.Contains(string(data), "unknownKeys") {
		t.Fatal("DTO 信息字段 unknownKeys 落盘")
	}
	// 未知键的名字不进新条目。
	clean := gjson.GetBytes(data, "mcpServers.clean")
	if strings.Contains(clean.Raw, "x-custom") {
		t.Fatal("未知键被错误带入新条目")
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(clean.Raw), &obj); err != nil {
		t.Fatal(err)
	}
	for k := range obj {
		if !knownEntryKeys[k] {
			t.Fatalf("新条目含未知键 %s", k)
		}
	}
}

func TestRegistry_DeleteInvalidLegacyName(t *testing.T) {
	// 既有不合规名字（含点）：List 如实展示、Delete 可用（spec §1.4）。
	svc, home, _ := newRegistryTestService(t)
	if err := os.MkdirAll(filepath.Join(home, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	fixture := "{\r\n  \"mcpServers\": {\r\n    \"legacy.name\": {\r\n      \"type\": \"http\",\r\n      \"url\": \"https://x.example.com/m\"\r\n    },\r\n    \"keep\": {\r\n      \"type\": \"http\",\r\n      \"url\": \"https://y.example.com/m\"\r\n    }\r\n  }\r\n}"
	if err := os.WriteFile(svc.registryPath(), []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, _, err := svc.List()
	if err != nil || len(entries) != 2 {
		t.Fatalf("List: err=%v n=%d", err, len(entries))
	}
	if err := svc.Delete("legacy.name"); err != nil {
		t.Fatalf("删除不合规名字失败: %v", err)
	}
	data := readRegistry(t, home)
	if gjson.GetBytes(data, "mcpServers.legacy\\.name").Exists() {
		t.Fatal("legacy.name 未删除")
	}
	if !gjson.GetBytes(data, "mcpServers.keep").Exists() {
		t.Fatal("keep 被误删")
	}
}
