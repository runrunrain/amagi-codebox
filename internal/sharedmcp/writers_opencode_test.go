package sharedmcp

// writers_opencode_test.go — OpenCode 写入器测试（spec §2.4）：无 mcp 键 →
// V1 新建；已有 V1 手写 server → 保真合并；已有 mcp.servers（V2）→ 写入
// V2 且 V1 无残留、mcp.timeout 保留；映射断言（command 数组拼合 /
// environment 键名 / description 丢弃 / 不写 enabled|disabled）；Remove 幂等。

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tidwall/gjson"
)

func newOpencodeTestWriter(t *testing.T) (*opencodeWriter, string) {
	t.Helper()
	home := t.TempDir()
	w := &opencodeWriter{userHome: home}
	if err := os.MkdirAll(filepath.Dir(w.path()), 0o755); err != nil {
		t.Fatal(err)
	}
	return w, home
}

func TestOpencodeWriter_NoMcpKeyDefaultsV1(t *testing.T) {
	w, home := newOpencodeTestWriter(t)
	if err := os.WriteFile(w.path(), []byte("{\n  \"theme\": \"dark\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Apply(stdioEntry("demo")); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, _ := os.ReadFile(w.path())
	got := gjson.GetBytes(data, "mcp.demo")
	if !got.Exists() {
		t.Fatalf("V1 条目缺失: %s", data)
	}
	if got.Get("type").String() != "local" {
		t.Fatalf("type 应为 local: %s", got.Raw)
	}
	// command 数组拼合：[command, ...args]，无独立 args 字段。
	if !gjson.GetBytes(data, "mcp.demo.command").IsArray() {
		t.Fatalf("command 应为数组: %s", got.Raw)
	}
	var cmd []string
	for _, item := range gjson.GetBytes(data, "mcp.demo.command").Array() {
		cmd = append(cmd, item.String())
	}
	if len(cmd) != 3 || cmd[0] != "npx" || cmd[2] != "demo-server" {
		t.Fatalf("command 拼合错误: %v", cmd)
	}
	if got.Get("args").Exists() {
		t.Fatalf("不应有 args 字段: %s", got.Raw)
	}
	// environment 键名（不是 env）。
	if got.Get("environment.K").String() != "V" {
		t.Fatalf("environment 键名错误: %s", got.Raw)
	}
	if got.Get("env").Exists() {
		t.Fatalf("不应有 env 字段: %s", got.Raw)
	}
	// description 丢弃；不写 enabled/disabled。
	if got.Get("description").Exists() || got.Get("enabled").Exists() || got.Get("disabled").Exists() {
		t.Fatalf("不应写 description/enabled/disabled: %s", got.Raw)
	}
	// theme 值级保留。
	if gjson.GetBytes(data, "theme").String() != "dark" {
		t.Fatalf("theme 丢失: %s", data)
	}
	_ = home
}

func TestOpencodeWriter_V1HandwrittenPreserved(t *testing.T) {
	w, _ := newOpencodeTestWriter(t)
	fixture := "{\n  \"theme\": \"light\",\n  \"mcp\": {\n    \"hand\": {\n      \"type\": \"local\",\n      \"command\": [\"keep-me\"]\n    }\n  }\n}\n"
	if err := os.WriteFile(w.path(), []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Apply(httpEntry("remote")); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, _ := os.ReadFile(w.path())
	if gjson.GetBytes(data, "mcp.hand.command.0").String() != "keep-me" {
		t.Fatalf("手写 V1 server 被改动: %s", data)
	}
	if gjson.GetBytes(data, "mcp.remote.type").String() != "remote" || gjson.GetBytes(data, "mcp.remote.url").String() != "https://demo.example.com/mcp" {
		t.Fatalf("remote 翻译错误: %s", data)
	}
	if gjson.GetBytes(data, "mcp.remote.headers.Authorization").String() != "Bearer x" {
		t.Fatalf("headers 未翻译: %s", data)
	}
}

func TestOpencodeWriter_V2DetectionWritesServersNested(t *testing.T) {
	w, _ := newOpencodeTestWriter(t)
	fixture := "{\n  \"theme\": \"dark\",\n  \"mcp\": {\n    \"timeout\": 5000,\n    \"servers\": {\n      \"existing\": {\n        \"type\": \"remote\",\n        \"url\": \"https://existing.example.com\"\n      }\n    }\n  }\n}\n"
	if err := os.WriteFile(w.path(), []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Apply(stdioEntry("demo")); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, _ := os.ReadFile(w.path())
	// V2：写入 mcp.servers.<name>；V1 直挂位置无残留。
	if !gjson.GetBytes(data, "mcp.servers.demo").Exists() {
		t.Fatalf("V2 条目缺失: %s", data)
	}
	if gjson.GetBytes(data, "mcp.demo").Exists() {
		t.Fatalf("V1 位置出现残留: %s", data)
	}
	// 既有 V2 server 与 mcp.timeout 保留。
	if !gjson.GetBytes(data, "mcp.servers.existing").Exists() {
		t.Fatalf("既有 V2 server 丢失: %s", data)
	}
	if gjson.GetBytes(data, "mcp.timeout").Int() != 5000 {
		t.Fatalf("mcp.timeout 丢失: %s", data)
	}
	// Remove 幂等。
	if err := w.Remove("demo"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := w.Remove("demo"); err != nil {
		t.Fatalf("重复 Remove 应幂等: %v", err)
	}
	data, _ = os.ReadFile(w.path())
	if gjson.GetBytes(data, "mcp.servers.demo").Exists() {
		t.Fatalf("删除失败: %s", data)
	}
	if !gjson.GetBytes(data, "mcp.servers.existing").Exists() {
		t.Fatalf("Remove 波及他人: %s", data)
	}
}

func TestOpencodeWriter_MissingFileCreatedAndReadMapped(t *testing.T) {
	w, _ := newOpencodeTestWriter(t)
	if _, err := w.Apply(stdioEntry("e")); err != nil {
		t.Fatalf("Apply on missing: %v", err)
	}
	raw, err := w.Read("e")
	if err != nil || raw == nil {
		t.Fatalf("Read: %v %v", raw, err)
	}
	mapped, _ := w.Mapped(stdioEntry("e"))
	if !equalJSONValue(raw, mapped) {
		t.Fatalf("Read 与 Mapped 不一致:\n%s\n%s", raw, mapped)
	}
	has, _ := w.Has("e")
	if !has {
		t.Fatal("Has 应命中")
	}
}

func TestOpencodeWriter_CorruptTargetRefused(t *testing.T) {
	w, _ := newOpencodeTestWriter(t)
	if err := os.WriteFile(w.path(), []byte("{BROKEN"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Apply(stdioEntry("x")); KindOf(err) != KindTargetCorrupt {
		t.Fatalf("坏 JSON 应拒绝: %v", err)
	}
	data, _ := os.ReadFile(w.path())
	if string(data) != "{BROKEN" {
		t.Fatal("坏 JSON 被改动")
	}
}
