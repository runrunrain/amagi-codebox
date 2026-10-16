package launcher

// wsl_opencode_mcp_test.go — AdaptWSLOpenCodeMCP 确定性形态检查的钉子
//（P3-S1 任务书 §3：npx.cmd→npx 仅验证过才映射；Windows exe/ps1 不生成假可用
// 条目；HTTP/远程保留 URL；distro 全局同名默认保留本地）。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func installFakeMCPResolver(t *testing.T, resolved map[string]string) {
	t.Helper()
	// 同时装 fake WSL target：readWSLOpenCodeGlobalMCPServers 不触发真实
	// wsl.exe（无全局文件的用例得到空冲突集）；需要全局配置的用例再显式
	// 覆盖安装并写文件。
	installFakeWSLTarget(t)
	orig := wslResolveCommandsFn
	wslResolveCommandsFn = func(_ string, cmds ...string) map[string]string {
		out := map[string]string{}
		for _, c := range cmds {
			if p, ok := resolved[c]; ok {
				out[c] = p
			}
		}
		return out
	}
	t.Cleanup(func() { wslResolveCommandsFn = orig })
}

func mcpConfig(servers map[string]any) map[string]any {
	return map[string]any{"provider": map[string]any{"x": 1}, "mcp": servers}
}

func TestAdaptWSLOpenCodeMCPBareShimMappedOnlyWhenVerified(t *testing.T) {
	t.Run("native npx verified maps npx.cmd to npx", func(t *testing.T) {
		installFakeMCPResolver(t, map[string]string{"npx": "/usr/bin/npx"})
		cfg := mcpConfig(map[string]any{
			"fetch": map[string]any{"command": "npx.cmd", "args": []any{"-y", "@modelcontextprotocol/server-fetch"}},
		})
		out, report := AdaptWSLOpenCodeMCP("Ubuntu", cfg)
		mcp := out["mcp"].(map[string]any)
		if mcp["fetch"].(map[string]any)["command"] != "npx" {
			t.Fatalf("verified bare shim must map to native npx: %v", mcp["fetch"])
		}
		if len(report) != 0 {
			t.Fatalf("no report expected: %v", report)
		}
	})

	t.Run("native npx unverified drops server with report", func(t *testing.T) {
		installFakeMCPResolver(t, map[string]string{}) // 探测失败/未解析到
		cfg := mcpConfig(map[string]any{
			"fetch": map[string]any{"command": "npx.cmd", "args": []any{"-y", "pkg"}},
		})
		out, report := AdaptWSLOpenCodeMCP("Ubuntu", cfg)
		if _, ok := out["mcp"]; ok {
			t.Fatalf("unverified shim must not produce a fake usable entry: %v", out["mcp"])
		}
		if len(report) != 1 || !strings.Contains(report[0], "fetch") || !strings.Contains(report[0], "手动适配") {
			t.Fatalf("drop must be reported: %v", report)
		}
		if _, ok := out["provider"]; !ok {
			t.Fatal("non-mcp sections must be untouched")
		}
	})
}

func TestAdaptWSLOpenCodeMCPWindowsOnlyForms(t *testing.T) {
	installFakeMCPResolver(t, map[string]string{"npx": "/usr/bin/npx"})
	for name, command := range map[string]string{
		"drive path": `C:\tools\server.exe`,
		"ps1":        `C:\Users\u\hook.ps1`,
		"bare exe":   `some-server.exe`,
		"cmd":        "setup.bat",
		"cmd.exe":    "cmd.exe",
		"powershell": "powershell",
		"pwsh":       "pwsh.exe",
	} {
		t.Run(name, func(t *testing.T) {
			cfg := mcpConfig(map[string]any{
				"win": map[string]any{"command": command},
			})
			out, report := AdaptWSLOpenCodeMCP("Ubuntu", cfg)
			if _, ok := out["mcp"]; ok {
				t.Fatalf("windows-only form must be dropped: %v", out["mcp"])
			}
			if len(report) != 1 || !strings.Contains(report[0], "手动适配") {
				t.Fatalf("drop must be reported as manual adaptation: %v", report)
			}
		})
	}
	// 参数携带盘符路径：同样不生成假可用条目。
	cfg := mcpConfig(map[string]any{
		"node-server": map[string]any{"command": "node", "args": []any{`C:\tools\server.js`}},
	})
	out, report := AdaptWSLOpenCodeMCP("Ubuntu", cfg)
	if _, ok := out["mcp"]; ok {
		t.Fatalf("drive-letter arg must drop the server: %v", out["mcp"])
	}
	if len(report) != 1 || !strings.Contains(report[0], "盘符") {
		t.Fatalf("drive-letter arg report: %v", report)
	}
}

func TestAdaptWSLOpenCodeMCPRemoteKept(t *testing.T) {
	installFakeMCPResolver(t, map[string]string{})
	cfg := mcpConfig(map[string]any{
		"remote": map[string]any{"url": "https://mcp.example/sse", "headers": map[string]any{"Authorization": "Bearer x"}},
	})
	out, report := AdaptWSLOpenCodeMCP("Ubuntu", cfg)
	if len(report) != 0 {
		t.Fatalf("remote server must not be reported: %v", report)
	}
	mcp := out["mcp"].(map[string]any)
	if mcp["remote"].(map[string]any)["url"] != "https://mcp.example/sse" {
		t.Fatal("remote URL must be preserved")
	}
}

func TestAdaptWSLOpenCodeMCPGlobalNameConflictKeepsLocal(t *testing.T) {
	installFakeMCPResolver(t, map[string]string{"npx": "/usr/bin/npx"})
	ft := installFakeWSLTarget(t)
	// distro 全局配置已有同名 server（JSONC 注释形态也要能读）。
	global := `{
		// my global servers
		"mcp": {"fetch": {"command": "npx", "args": ["-y", "other"]}}
	}`
	if err := os.MkdirAll(ft.uncPath("/home/wslu/.config/opencode"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ft.uncPath("/home/wslu/.config/opencode"), "opencode.json"), []byte(global), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := mcpConfig(map[string]any{
		"fetch":  map[string]any{"command": "npx.cmd", "args": []any{"-y", "pkg"}},
		"other2": map[string]any{"command": "npx", "args": []any{"pkg2"}},
	})
	out, report := AdaptWSLOpenCodeMCP("Ubuntu", cfg)
	mcp := out["mcp"].(map[string]any)
	if _, ok := mcp["fetch"]; ok {
		t.Fatal("global-name conflict server must not be written into the附加层 (keep local)")
	}
	if _, ok := mcp["other2"]; !ok {
		t.Fatal("non-conflicting server must be kept")
	}
	found := false
	for _, line := range report {
		if strings.Contains(line, "fetch") && strings.Contains(line, "全局") {
			found = true
		}
	}
	if !found {
		t.Fatalf("conflict must be reported: %v", report)
	}
}

func TestAdaptWSLOpenCodeMCPNoMCPSectionNoop(t *testing.T) {
	installFakeMCPResolver(t, map[string]string{})
	cfg := map[string]any{"provider": map[string]any{"x": 1}}
	out, report := AdaptWSLOpenCodeMCP("Ubuntu", cfg)
	if len(report) != 0 {
		t.Fatalf("no mcp section → no report: %v", report)
	}
	b, _ := json.Marshal(out["provider"])
	if string(b) != `{"x":1}` {
		t.Fatal("config must be returned untouched")
	}
}
