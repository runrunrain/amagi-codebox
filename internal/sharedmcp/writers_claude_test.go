package sharedmcp

// writers_claude_test.go — Claude 写入器测试（spec §2.4）：54 顶层键仿真
// 夹具 → Apply 仅新增 mcpServers 一个顶层键、其余 54 键集合恒等；重复
// Apply 覆盖；Remove 后 mcpServers 保留空对象；预置同名非登记条目时写入器
// 直写成功（拒绝路径在 service_test 断言）；符号链接拒绝。

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/tidwall/gjson"
)

// claude54KeyFixture 生成 54 顶层键的 .claude.json 仿真夹具（本机实测键数，
// spec §0.1）。
func claude54KeyFixture() []byte {
	body := "{\r\n"
	for i := 0; i < 54; i++ {
		comma := ","
		if i == 53 {
			comma = ""
		}
		body += fmt.Sprintf("  \"key_%02d\": {\"n\": %d, \"s\": \"v%d\"}%s\r\n", i, i, i, comma)
	}
	body += "}"
	return []byte(body)
}

func newClaudeTestWriter(t *testing.T) (*claudeWriter, string) {
	t.Helper()
	home := t.TempDir()
	return &claudeWriter{userHome: home}, home
}

func TestClaudeWriter_ApplyAddsOnlyMcpServersKey(t *testing.T) {
	w, home := newClaudeTestWriter(t)
	if err := os.WriteFile(w.path(), claude54KeyFixture(), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(w.path())

	if _, err := w.Apply(stdioEntry("demo")); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	after, _ := os.ReadFile(w.path())
	bk, ak := topKeyNames(before), topKeyNames(after)
	if len(bk) != 54 {
		t.Fatalf("夹具键数不是 54: %d", len(bk))
	}
	if len(ak) != 55 || !supersetWithMcpServers(bk, ak) {
		t.Fatalf("写后键集应 = 写前 54 键 + mcpServers: got %d keys", len(ak))
	}
	got := gjson.GetBytes(after, "mcpServers.demo")
	if got.Get("type").String() != "stdio" || got.Get("command").String() != "npx" {
		t.Fatalf("条目形态错误: %s", got.Raw)
	}
	// 既有键值不受扰。
	for i := 0; i < 54; i++ {
		k := fmt.Sprintf("key_%02d", i)
		if gjson.GetBytes(after, k).Raw != gjson.GetBytes(before, k).Raw {
			t.Fatalf("顶层键 %s 被改动", k)
		}
	}

	// 重复 Apply 覆盖（幂等 resync 基础）。
	e2 := httpEntry("demo")
	if _, err := w.Apply(e2); err != nil {
		t.Fatalf("re-Apply: %v", err)
	}
	after2, _ := os.ReadFile(w.path())
	if gjson.GetBytes(after2, "mcpServers.demo.type").String() != "http" {
		t.Fatal("重复 Apply 未覆盖")
	}
	if len(topKeyNames(after2)) != 55 {
		t.Fatal("重复 Apply 后键集变化")
	}

	// Remove：条目删除、mcpServers 保留空对象、顶层键集复原。
	if err := w.Remove("demo"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	after3, _ := os.ReadFile(w.path())
	if gjson.GetBytes(after3, "mcpServers.demo").Exists() {
		t.Fatal("条目未删除")
	}
	if !gjson.GetBytes(after3, "mcpServers").Exists() {
		t.Fatal("mcpServers 键被删（应保留空对象）")
	}
	if !supersetWithMcpServers(bk, topKeyNames(after3)) {
		t.Fatal("Remove 后顶层键集不符（mcpServers 应保留）")
	}
	_ = home
}

func supersetWithMcpServers(before, after []string) bool {
	if len(after) != len(before)+1 {
		return false
	}
	set := map[string]bool{}
	for _, k := range before {
		set[k] = true
	}
	extra := 0
	for _, k := range after {
		if !set[k] {
			if k == "mcpServers" {
				extra++
				continue
			}
			return false
		}
	}
	return extra == 1
}

func TestClaudeWriter_MissingFileAndHandwrittenUntouched(t *testing.T) {
	w, _ := newClaudeTestWriter(t)
	// 文件缺失 → 视为 {} 新建。
	if _, err := w.Apply(httpEntry("first")); err != nil {
		t.Fatalf("Apply on missing: %v", err)
	}
	data, _ := os.ReadFile(w.path())
	if !gjson.ValidBytes(data) || !gjson.GetBytes(data, "mcpServers.first").Exists() {
		t.Fatalf("新建文件形态错误: %s", data)
	}

	// 预置手写同名条目：写入器直写成功（编排层的 conflict 拒绝在
	// service_test 断言——spec §2.4）。
	if err := os.WriteFile(w.path(), []byte("{\r\n  \"mcpServers\": {\r\n    \"hand\": {\r\n      \"type\": \"stdio\",\r\n      \"command\": \"keep\"\r\n    }\r\n  }\r\n}\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(w.path())
	if _, err := w.Apply(stdioEntry("hand")); err != nil {
		t.Fatalf("Apply over handwritten: %v", err)
	}
	after, _ := os.ReadFile(w.path())
	if gjson.GetBytes(after, "mcpServers.hand.command").String() != "npx" {
		t.Fatal("直写未覆盖")
	}
	_ = before
}

func TestClaudeWriter_SymlinkRefused(t *testing.T) {
	w, home := newClaudeTestWriter(t)
	real := filepath.Join(home, "real.json")
	if err := os.WriteFile(real, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := w.path()
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("本环境无法创建符号链接（权限/开发者模式）: %v", err)
	}
	if _, err := w.Apply(stdioEntry("x")); err == nil {
		t.Fatal("符号链接应被拒绝")
	}
}

func TestClaudeWriter_CorruptTargetRefused(t *testing.T) {
	w, _ := newClaudeTestWriter(t)
	if err := os.WriteFile(w.path(), []byte("{BROKEN"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Apply(stdioEntry("x")); KindOf(err) != KindTargetCorrupt {
		t.Fatalf("坏 JSON 目标应拒绝: %v", err)
	}
	if err := w.Remove("x"); KindOf(err) != KindTargetCorrupt {
		t.Fatalf("坏 JSON 目标 Remove 应拒绝: %v", err)
	}
	data, _ := os.ReadFile(w.path())
	if string(data) != "{BROKEN" {
		t.Fatal("坏 JSON 目标被改动")
	}
}

func TestClaudeWriter_HasReadIdempotentRemove(t *testing.T) {
	w, _ := newClaudeTestWriter(t)
	has, err := w.Has("nope")
	if err != nil || has {
		t.Fatalf("Has on missing file: %v %v", has, err)
	}
	raw, err := w.Read("nope")
	if err != nil || raw != nil {
		t.Fatalf("Read on missing: %v %v", raw, err)
	}
	if err := w.Remove("nope"); err != nil {
		t.Fatalf("Remove on missing 应幂等: %v", err)
	}
	if _, err := w.Apply(stdioEntry("e")); err != nil {
		t.Fatal(err)
	}
	has, _ = w.Has("e")
	raw, _ = w.Read("e")
	if !has || raw == nil {
		t.Fatal("Has/Read 应命中")
	}
	// Read 返回的内容与 Mapped 值语义相等（drift=none 基础）。
	mapped, _ := w.Mapped(stdioEntry("e"))
	if !equalJSONValue(raw, mapped) {
		t.Fatalf("Read 与 Mapped 不一致:\n%s\n%s", raw, mapped)
	}
	if err := w.Remove("e"); err != nil {
		t.Fatal(err)
	}
	if err := w.Remove("e"); err != nil {
		t.Fatalf("重复 Remove 应幂等: %v", err)
	}
}
