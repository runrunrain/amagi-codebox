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

// TestAdaptWSLOpenCodeMCPEnvDriveValue 钉 P4-A3 对称补齐：env 值携带 Windows
// 盘符路径 → 拒配不落附加层（其余 CLI 落地面经 wslsupply.TransformMCPServer
// 全检，opencode 启动层原独漏 env）；非盘符 env 值（标量/Linux 路径/URL）
// 回归不受影响。
func TestAdaptWSLOpenCodeMCPEnvDriveValue(t *testing.T) {
	installFakeMCPResolver(t, map[string]string{"npx": "/usr/bin/npx"})

	t.Run("drive value rejected with key named", func(t *testing.T) {
		cfg := mcpConfig(map[string]any{
			"local": map[string]any{
				"command": "node",
				"args":    []any{"server.js"},
				"env":     map[string]any{"API_KEY": "secret", "TOOL_HOME": `D:\\tools\\mcp`},
			},
		})
		out, report := AdaptWSLOpenCodeMCP("Ubuntu", cfg)
		if _, ok := out["mcp"]; ok {
			t.Fatalf("env drive value must not produce a fake usable entry: %v", out["mcp"])
		}
		if len(report) != 1 || !strings.Contains(report[0], "TOOL_HOME") ||
			!strings.Contains(report[0], "盘符") || !strings.Contains(report[0], "手动适配") {
			t.Fatalf("drop must name the env key and reason: %v", report)
		}
		if strings.Contains(report[0], `D:\\tools\\mcp`) {
			t.Fatalf("diagnostic must not echo the env value: %v", report)
		}
	})

	t.Run("first offender deterministic by key order", func(t *testing.T) {
		cfg := mcpConfig(map[string]any{
			"win": map[string]any{"command": "node", "env": map[string]any{
				"Z_DIR": `Z:\\a`, "A_DIR": `A:\\a`,
			}},
		})
		_, report := AdaptWSLOpenCodeMCP("Ubuntu", cfg)
		if len(report) != 1 || !strings.Contains(report[0], "A_DIR") {
			t.Fatalf("deterministic first offender expected (sorted keys): %v", report)
		}
	})

	t.Run("non-drive env values kept untouched", func(t *testing.T) {
		cfg := mcpConfig(map[string]any{
			"ok": map[string]any{
				"command": "node",
				"args":    []any{"server.js"},
				"env":     map[string]any{"LOG_LEVEL": "debug", "DATA_DIR": "/opt/mcp-data", "ENDPOINT": "https://internal.example/sse"},
			},
		})
		out, report := AdaptWSLOpenCodeMCP("Ubuntu", cfg)
		if len(report) != 0 {
			t.Fatalf("non-drive env values must not be reported: %v", report)
		}
		env := out["mcp"].(map[string]any)["ok"].(map[string]any)["env"].(map[string]any)
		if env["LOG_LEVEL"] != "debug" || env["DATA_DIR"] != "/opt/mcp-data" {
			t.Fatalf("clean env must be preserved as-is: %v", env)
		}
	})

	t.Run("preview marks env-hit server needs adapt", func(t *testing.T) {
		cfg := mcpConfig(map[string]any{
			"local": map[string]any{"command": "node", "env": map[string]any{"CFG_HOME": `C:\\mcp\\cfg`}},
		})
		previews := PreviewWSLOpenCodeMCP("Ubuntu", cfg)
		if len(previews) != 1 || !previews[0].NeedsAdapt || previews[0].Remote {
			t.Fatalf("env drive value must surface as NeedsAdapt in preview: %+v", previews)
		}
		if !strings.Contains(previews[0].Reason, "CFG_HOME") || !strings.Contains(previews[0].Reason, "手动适配") {
			t.Fatalf("preview reason must name the key: %+v", previews[0])
		}
	})
}
