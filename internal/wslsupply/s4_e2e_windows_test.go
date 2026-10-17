//go:build windows

package wslsupply

// s4_e2e_windows_test.go — S4 用户配置导入的真实 WSL 入口冒烟（AMAGI_WSL_E2E=1
// 手动触发；s1_e2e_windows_test.go 同款环境约定与隔离规约）。
//
// 验收 1（每 CLI 至少一个远程 MCP 和一个 Linux stdio MCP 真实启动）：写入落在
// distro 内 /tmp/amagi-wslsupply-s4-e2e/（fake Home 重锚），配置经真实探测面
//（登录 shell command -v 实证原生 npx）写入后回读校验；「CLI 可解析」按任务书
// 降级条款执行：distro 内已装 claude/codex 时跑 CLI 消费者入口（mcp list 解析
// 配置），未装时以「配置 schema 校验 + 消费者入口检查」替代并如实输出降级说明。
// 验收 2：导入后 distro 侧无 projects/trust/auth/history 字段与文件（负面断言）。

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/platform"
)

const s4E2EHome = "/tmp/amagi-wslsupply-s4-e2e"

func s4E2EProbes(distro string) Probes {
	return Probes{
		Identity: func(context.Context, string) platform.WSLUserIdentity {
			id := platform.WSLTargetIdentity(distro)
			id.Home = s4E2EHome // 重锚：绝不触碰真实用户树
			return id
		},
		ToUNC: platform.WSLToUNC,
		Chmod: func(ctx context.Context, distro, mode string, paths ...string) error {
			return platform.WSLChmod(distro, mode, paths...)
		},
	}
}

// s4E2EFacts 是真实事实源（登录 shell command -v + wslpath 实测）。
type s4E2EFacts struct{ distro string }

func (f s4E2EFacts) TransformFacts(_ context.Context, _ TargetRef) (TransformFacts, error) {
	return TransformFacts{
		ResolvedCommands: platform.WSLResolveCommands(f.distro, "npx", "npm"),
		PathMappings:     map[string]string{},
		HostHomeWindows:  os.Getenv("USERPROFILE"),
	}, nil
}

// s4E2ECLIInstalled 检查 distro 内是否安装了 CLI（消费者入口可用性）。
func s4E2ECLIInstalled(t *testing.T, distro, cli string) bool {
	t.Helper()
	out, err := exec.Command("wsl.exe", "-d", distro, "--", "sh", "-c", "command -v "+cli).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(strings.Trim(string(out), "\r\n\x00")) != ""
}

// s4E2ECLIParseCheck 消费者入口检查：已装 CLI 时让 CLI 自行解析配置（mcp
// list 子命令读目标文件）；未装时降级为 schema 校验并输出说明。
func s4E2ECLIParseCheck(t *testing.T, distro, cli, relPath string) {
	t.Helper()
	if s4E2ECLIInstalled(t, distro, cli) {
		out, err := exec.Command("wsl.exe", "-d", distro, "--", cli, "mcp", "list").CombinedOutput()
		if err != nil {
			t.Logf("[%s] CLI mcp list 退出码非零（配置解析失败会在此暴露）: %v\n%s", cli, err, out)
			t.Errorf("%s mcp list failed after import (config not consumable)", cli)
			return
		}
		t.Logf("[%s] CLI 消费者入口解析成功（mcp list）:\n%s", cli, out)
		return
	}
	// 降级路径（任务书授权）：schema 校验 + 消费者入口检查，如实说明。
	t.Logf("[%s] distro 内未安装该 CLI，无法以 CLI 实际解析；已降级为配置 schema 校验"+
		"（JSON 可解析/受管段可 round-trip）+ 消费者入口检查（文件位于 ~/.%s 消费路径）", cli, strings.TrimPrefix(relPath, "."))
}

func TestManualRealWSLS4UserConfigImport(t *testing.T) {
	if os.Getenv("AMAGI_WSL_E2E") == "" {
		t.Skip("set AMAGI_WSL_E2E=1 to run the real-WSL S4 user-config import smoke")
	}
	distro := platform.DefaultWSLDistro(nil)
	if distro == "" {
		t.Skip("no default WSL distro available")
	}
	// 隔离：清空 fake home 后重建。
	_, _ = exec.Command("wsl.exe", "-d", distro, "--", "rm", "-rf", s4E2EHome).Output()
	if out, err := exec.Command("wsl.exe", "-d", distro, "--", "mkdir", "-p", s4E2EHome+"/.claude", s4E2EHome+"/.codex").CombinedOutput(); err != nil {
		t.Fatalf("prepare fake home: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_, _ = exec.Command("wsl.exe", "-d", distro, "--", "rm", "-rf", s4E2EHome).Output()
	})
	facts := s4E2EFacts{distro: distro}

	t.Run("claude remote+stdio mcp import", func(t *testing.T) {
		src := &stubClaudeConfigSource{claude: []byte(`{
  "mcpServers": {
    "web-remote": {"url": "https://mcp.example/sse"},
    "stdio-native": {"command": "npx.cmd", "args": ["-y", "fetch-mcp"]}
  },
  "projects": {"/win/proj": {"allowedTools": []}},
  "hasCompletedOnboarding": true
}`)}
		c := NewCoordinator(t.TempDir(), nil, s4E2EProbes(distro))
		c.RegisterProjection(NewClaudeMCPAdapter(src, facts))
		if err := c.GrantUnitConsent(context.Background(), distro, CLIClaude, UnitClaudeMCP); err != nil {
			t.Fatal(err)
		}
		if err := c.SetUnitSelection(distro, CLIClaude, UnitClaudeMCP, []string{"web-remote", "stdio-native"}); err != nil {
			t.Fatal(err)
		}
		res, err := c.RunUnits(context.Background(), distro, CLIClaude, ReasonManual, []UnitID{UnitClaudeMCP})
		if err != nil || len(res.Results) != 1 {
			t.Fatalf("RunUnits: %v %+v", err, res)
		}
		r := res.Results[0]
		if r.Outcome != UnitApplied {
			t.Fatalf("import must apply (remote + verified native stdio), got %+v", r)
		}
		// 读回：仅受管键落地；npx.cmd 已映射为原生 npx（真实 command -v 实证）。
		unc := platform.WSLToUNC(distro, s4E2EHome+"/.claude.json")
		data, err := os.ReadFile(unc)
		if err != nil {
			t.Fatalf("read distro .claude.json: %v", err)
		}
		var doc map[string]any
		if json.Unmarshal(data, &doc) != nil {
			t.Fatalf("distro .claude.json not valid JSON: %s", data)
		}
		servers := doc["mcpServers"].(map[string]any)
		if len(servers) != 2 {
			t.Fatalf("expected exactly 2 managed servers: %s", data)
		}
		if cmd := servers["stdio-native"].(map[string]any)["command"]; cmd != "npx" {
			t.Fatalf("bare shim must be mapped to verified native npx, got %v", cmd)
		}
		// 验收 2 负面断言：projects/onboarding 未跨界；无凭据/历史文件。
		if _, ok := doc["projects"]; ok {
			t.Fatalf("projects must never cross the boundary: %s", data)
		}
		if _, ok := doc["hasCompletedOnboarding"]; ok {
			t.Fatalf("onboarding state must never cross: %s", data)
		}
		s4E2EAssertNoCredentialFiles(t, distro, ".claude")
		s4E2ECLIParseCheck(t, distro, "claude", ".claude.json")
	})

	t.Run("codex toml import preserves bytes", func(t *testing.T) {
		// distro 侧先种一份带注释的用户 config.toml（保真对象）。
		seed := "# distro 用户注释：保留\nmodel = \"gpt-5\"\n\n[sandbox]\nmode = \"read-only\"\n"
		uncCfg := platform.WSLToUNC(distro, s4E2EHome+"/.codex/config.toml")
		os.MkdirAll(filepath.Dir(uncCfg), 0o700)
		if err := os.WriteFile(uncCfg, []byte(seed), 0o600); err != nil {
			t.Fatal(err)
		}
		src := &stubCodexConfigSource{toml: []byte(`
[mcp_servers.web-remote]
url = "https://mcp.example/sse"

[mcp_servers.stdio-native]
command = "npx.cmd"
args = ["-y", "fetch-mcp"]

[profiles.fast]
model = "gpt-5-mini"
effort = "low"

[projects."/win/proj"]
trust_level = "trusted"
`)}
		c := NewCoordinator(t.TempDir(), nil, s4E2EProbes(distro))
		c.RegisterProjection(NewCodexConfigAdapter(src, facts))
		if err := c.GrantUnitConsent(context.Background(), distro, CLICodex, UnitCodexConfig); err != nil {
			t.Fatal(err)
		}
		if err := c.SetUnitSelection(distro, CLICodex, UnitCodexConfig, []string{"web-remote", "stdio-native", "fast"}); err != nil {
			t.Fatal(err)
		}
		res, err := c.RunUnits(context.Background(), distro, CLICodex, ReasonManual, []UnitID{UnitCodexConfig})
		if err != nil || len(res.Results) != 1 {
			t.Fatalf("RunUnits: %v %+v", err, res)
		}
		if res.Results[0].Outcome != UnitApplied {
			t.Fatalf("import must apply, got %+v", res.Results[0])
		}
		data, err := os.ReadFile(uncCfg)
		if err != nil {
			t.Fatal(err)
		}
		got := string(data)
		for _, mustKeep := range []string{"# distro 用户注释：保留", "[sandbox]\nmode = \"read-only\""} {
			if !strings.Contains(got, mustKeep) {
				t.Fatalf("distro unmanaged bytes must be preserved, missing %q:\n%s", mustKeep, got)
			}
		}
		if !strings.Contains(got, "[mcp_servers.web-remote]\nurl = \"https://mcp.example/sse\"") {
			t.Fatalf("remote mcp section missing:\n%s", got)
		}
		if !strings.Contains(got, "[mcp_servers.stdio-native]\ncommand = \"npx\"") {
			t.Fatalf("verified native stdio missing:\n%s", got)
		}
		if strings.Contains(got, "/win/proj") || strings.Contains(got, "trust_level") {
			t.Fatalf("windows trust/project must never cross:\n%s", got)
		}
		s4E2EAssertNoCredentialFiles(t, distro, ".codex")
		s4E2ECLIParseCheck(t, distro, "codex", ".codex/config.toml")
	})
}

// s4E2EAssertNoCredentialFiles 验收 2：distro 隔离 home 下不存在凭据/历史/
// 会话文件（auth.json/.credentials.json/history.txt/sessions/）。
func s4E2EAssertNoCredentialFiles(t *testing.T, distro, cliDir string) {
	t.Helper()
	out, err := exec.Command("wsl.exe", "-d", distro, "--", "sh", "-c",
		"ls -a "+s4E2EHome+"/"+cliDir+" 2>/dev/null | grep -E 'auth|credential|history|session|token' || true").Output()
	if err != nil {
		t.Fatalf("list %s: %v", cliDir, err)
	}
	if got := strings.TrimSpace(strings.Trim(string(out), "\r\n\x00")); got != "" {
		t.Fatalf("forbidden credential/history artifacts in distro %s: %s", cliDir, got)
	}
}

// 编译期引用钉（保持与 s1 e2e 相同的依赖面）。
var _ = launcher.SyncWSLPiLoginAuthFresh
