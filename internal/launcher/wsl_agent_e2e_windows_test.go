//go:build windows

package launcher

// 真实 WSL 环境端到端实证（本机 WSL2 Ubuntu）。默认跳过；设置
// AMAGI_WSL_E2E=1 后运行：
//
//	AMAGI_WSL_E2E=1 go test -run TestManualRealWSL ./internal/launcher/
//
// 覆盖（生产代码路径，零注入）：distro 探测 → WSLUserHome（wsl.exe -- sh -lc）
// → UNC 映射（\\wsl.localhost / \\wsl$）→ merge 读现有配置 → 原子写 →
// wsl.exe chmod 补偿 0700/0600 → 读回比对。写入目标为 WSL 侧真实的
// ~/.pi/agent：测试内 t.Cleanup 自动备份/恢复原文件（diting m4），不依赖
// 外部脚本。
import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/platform"
)

func manualWSLDistro(t *testing.T) string {
	t.Helper()
	distro := platform.DefaultWSLDistro(nil)
	if distro == "" {
		t.Skip("no usable WSL distro on this machine")
	}
	return distro
}

func TestManualRealWSLUserHome(t *testing.T) {
	if os.Getenv("AMAGI_WSL_E2E") == "" {
		t.Skip("set AMAGI_WSL_E2E=1 to run the real-WSL end-to-end probe")
	}
	distro := manualWSLDistro(t)
	home := platform.WSLUserHome(distro)
	if !strings.HasPrefix(home, "/") {
		t.Fatalf("WSLUserHome(%q) = %q, want an absolute Linux path", distro, home)
	}
	unc := platform.WSLToUNC(distro, home)
	if !strings.HasPrefix(unc, `\\`) {
		t.Fatalf("WSLToUNC = %q, want a UNC path", unc)
	}
	t.Logf("distro=%s home=%s unc=%s", distro, home, unc)
}

// readWSLFileForTest 读取 distro 内文件（不存在返回 ok=false；供 e2e 前后快照）。
func readWSLFileForTest(t *testing.T, uncPath string) (data []byte, ok bool) {
	t.Helper()
	data, err := os.ReadFile(uncPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false
		}
		t.Fatalf("read %s: %v", uncPath, err)
	}
	return data, true
}

func TestManualRealWSLWriteOpenCodeConfig(t *testing.T) {
	if os.Getenv("AMAGI_WSL_E2E") == "" {
		t.Skip("set AMAGI_WSL_E2E=1 to run the real-WSL end-to-end probe")
	}
	distro := manualWSLDistro(t)

	linuxDir, uncDir := wslOpenCodeDirsForTest(t, distro)
	before, hadBefore := readWSLFileForTest(t, filepath.Join(uncDir, openCodeWSLConfigFile))
	t.Cleanup(func() {
		restoreWSLOpenCodeFileForTest(t, distro, linuxDir, before, hadBefore)
	})

	cfg := map[string]any{
		"model": "wsltest/wsltest-model",
		"provider": map[string]any{
			"wsltest": map[string]any{
				"npm":     "@ai-sdk/openai-compatible",
				"options": map[string]any{"apiKey": "sk-manual-e2e", "baseURL": "https://wsltest.example/v4"},
			},
		},
	}
	linuxPath, err := WriteWSLOpenCodeAgentConfig(distro, cfg)
	if err != nil {
		t.Fatalf("WriteWSLOpenCodeAgentConfig: %v", err)
	}
	if want := linuxDir + "/" + openCodeWSLConfigFile; linuxPath != want {
		t.Fatalf("linuxPath = %q, want %q", linuxPath, want)
	}
	t.Logf("written to %s (distro %s)", linuxPath, distro)

	data, err := os.ReadFile(platform.WSLToUNC(distro, linuxPath))
	if err != nil {
		t.Fatalf("read back via UNC: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, data)
	}
	providers, _ := got["provider"].(map[string]any)
	if _, ok := providers["wsltest"]; !ok {
		t.Fatalf("wsltest provider missing after write: %v", providers)
	}
	t.Logf("amagi-codebox.json content OK (%d bytes)", len(data))
}

// wslOpenCodeDirsForTest 解析 ~/.config/opencode 的 Linux/UNC 双形态（快照辅助）。
func wslOpenCodeDirsForTest(t *testing.T, distro string) (linuxDir, uncDir string) {
	t.Helper()
	target, err := resolveWSLAgentTarget(distro, ".config/opencode")
	if err != nil {
		t.Fatalf("resolveWSLAgentTarget: %v", err)
	}
	return target.LinuxDir, target.UNCDir
}

// restoreWSLOpenCodeFileForTest 把快照内容写回 WSL 侧并 chmod 600；快照不
// 存在时删除测试写入的文件（目标态 = 测试前状态）。
func restoreWSLOpenCodeFileForTest(t *testing.T, distro, linuxDir string, before []byte, hadBefore bool) {
	t.Helper()
	uncPath := platform.WSLToUNC(distro, linuxDir+"/"+openCodeWSLConfigFile)
	if !hadBefore {
		if err := os.Remove(uncPath); err != nil && !os.IsNotExist(err) {
			t.Logf("cleanup remove %s: %v", uncPath, err)
		}
		return
	}
	if err := os.WriteFile(uncPath, before, 0o600); err != nil {
		t.Logf("cleanup restore %s: %v", uncPath, err)
		return
	}
	if err := wslChmodFn(context.Background(), distro, "600", linuxDir+"/"+openCodeWSLConfigFile); err != nil {
		t.Logf("cleanup chmod 600: %v", err)
	}
}

func TestManualRealWSLSearchTools(t *testing.T) {
	if os.Getenv("AMAGI_WSL_E2E") == "" {
		t.Skip("set AMAGI_WSL_E2E=1 to run the real-WSL end-to-end probe")
	}
	distro := manualWSLDistro(t)
	tools := platform.WSLSearchToolStatus(distro)
	t.Logf("distro=%s fd=%v ripgrep=%v", distro, tools.FD, tools.Ripgrep)
}
