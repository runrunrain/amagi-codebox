package main

// app_sharedmcp_import_test.go — 导入门面测试（app_sharedmcp_import.go）：
// 临时 home 夹具端到端（粘贴解析预览 → 显式提交登记 → Overview 返回）、
// 文件字节解析（BOM 剥离/大小上限）。对话框壳（PickSharedMcpImportFile 的
// 系统文件选择）无法在自动化环境弹出，其逻辑薄层（路径不经前端、Stat 上
// 限、读文件、委托 parseSharedMcpImportFileData）由本文件的纯函数测试与
// sharedmcp 包测试共同覆盖，弹窗行为留待人工验证。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"amagi-codebox/internal/sharedmcp"
)

func TestAppSharedMcpImport_ParsePreviewThenCommit(t *testing.T) {
	app, home, _ := newSharedMcpFacadeTestApp(t)
	// 预置既有条目（同名冲突基准）。
	if _, err := app.CreateSharedMcpEntry(sharedmcp.SharedMcpEntry{
		Name: "existing", Type: "stdio", Command: "npx",
	}); err != nil {
		t.Fatal(err)
	}

	text := `{"mcpServers": {
		"existing": {"command": "uvx"},
		"fresh-stdio": {"command": "npx", "args": ["-y", "fresh"], "env": {"K": "V"}, "x-extra": {"keep": true}},
		"fresh-http": {"type": "http", "url": "https://h.example.com/mcp", "headers": {"Authorization": "Bearer sk-1"}}
	}}`

	// ① 纯解析预览：同名标 exists、新条目 ok；预览不回传任何配置值。
	pv, err := app.ParseSharedMcpImportText(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(pv.Entries) != 3 {
		t.Fatalf("预览条目数不符: %+v", pv.Entries)
	}
	b, _ := json.Marshal(pv)
	for _, secret := range []string{"sk-1", "https://h.example.com/mcp", "\"V\"", "x-extra"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("预览泄漏配置值 %s: %s", secret, b)
		}
	}
	statusOf := map[string]string{}
	for _, row := range pv.Entries {
		statusOf[row.Name] = row.Status
	}
	if statusOf["existing"] != sharedmcp.ImportStatusExists || statusOf["fresh-stdio"] != sharedmcp.ImportStatusOk || statusOf["fresh-http"] != sharedmcp.ImportStatusOk {
		t.Fatalf("预览状态不符: %+v", statusOf)
	}

	// ② 显式提交：同名跳过报告 + 新条目登记 + 返回最新 Overview。
	outcome, err := app.ImportSharedMcpEntries(text)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(outcome.Imported, []string{"fresh-http", "fresh-stdio"}) {
		t.Fatalf("Imported 不符: %+v", outcome.Imported)
	}
	if len(outcome.Skipped) != 1 || outcome.Skipped[0].Name != "existing" || !strings.Contains(outcome.Skipped[0].Reason, "同名") {
		t.Fatalf("同名跳过报告不符: %+v", outcome.Skipped)
	}
	if len(outcome.Overview.Entries) != 3 {
		t.Fatalf("应返回最新 Overview: %+v", outcome.Overview)
	}
	// 未知配置键零丢弃写入登记文件。
	reg := string(mustReadFileMain(t, filepath.Join(home, ".agents", ".mcp.json")))
	if !gjson.Get(reg, "mcpServers.fresh-stdio.x-extra.keep").Bool() {
		t.Fatalf("未知配置键被丢弃: %s", reg)
	}
	// 既有同名条目未被覆盖（仍是手工 Create 的形态）。
	if got := gjson.Get(reg, "mcpServers.existing.command").String(); got != "npx" {
		t.Fatalf("既有条目被覆盖: command=%s", got)
	}

	// ③ 导入默认无分发：矩阵全未勾选且无任何引擎被写入（distributed/drift
	// 之外的实测状态——unsupported/conflict/not-distributed——均属无分发）。
	for _, eo := range outcome.Overview.Entries {
		for id, cell := range eo.Engines {
			if id == "omp" {
				continue
			}
			if cell.Selected || cell.Status == sharedmcp.EngineDistributed || cell.Status == sharedmcp.EngineDrift {
				t.Fatalf("导入默认无分发: %s/%s=%+v", eo.Entry.Name, id, cell)
			}
		}
	}
}

func TestAppSharedMcpImport_FileDataParsing(t *testing.T) {
	app, _, _ := newSharedMcpFacadeTestApp(t)

	// BOM 剥离 + 文件名回填 + 不透明确认凭据。
	data := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"mcpServers": {"bom-entry": {"command": "npx"}}}`)...)
	out, err := app.parseSharedMcpImportFileData(data, "/tmp/some dir/mcp-import.json")
	if err != nil {
		t.Fatal(err)
	}
	if out.FileName != "mcp-import.json" {
		t.Fatalf("FileName 不符: %q", out.FileName)
	}
	if len(out.Entries) != 1 || out.Entries[0].Name != "bom-entry" || out.Entries[0].Status != sharedmcp.ImportStatusOk {
		t.Fatalf("预览不符: %+v", out.Entries)
	}
	// 只有实际预览得到的不透明凭据才能提交，且不能重放。
	if _, err := app.ImportPickedSharedMcpFile("other-ticket"); err == nil {
		t.Fatal("unexpected ticket accepted")
	}
	committed, err := app.ImportPickedSharedMcpFile(out.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(committed.Imported, []string{"bom-entry"}) {
		t.Fatalf("提交不符: %+v", committed)
	}

	if _, err := app.ImportPickedSharedMcpFile(out.Ticket); err == nil {
		t.Fatal("ticket must be single-use")
	}

	// 非 JSON 文件 → 中文错误。
	if _, err := app.parseSharedMcpImportFileData([]byte("plain text"), "x.json"); err == nil || !strings.Contains(err.Error(), "JSON") {
		t.Fatalf("非 JSON 应报错: %v", err)
	}

	// 超过统一大小上限的文件内容 → 拒绝。
	big := strings.Repeat("a", sharedmcp.MaxImportBytes+1)
	if _, err := app.parseSharedMcpImportFileData([]byte(big), "big.json"); err == nil || !strings.Contains(err.Error(), "大小上限") {
		t.Fatalf("超限应拒绝: %v", err)
	}
}

func mustReadFileMain(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
