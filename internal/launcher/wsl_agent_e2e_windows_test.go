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
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/config"
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

func TestManualRealWSLWritePiConfig(t *testing.T) {
	if os.Getenv("AMAGI_WSL_E2E") == "" {
		t.Skip("set AMAGI_WSL_E2E=1 to run the real-WSL end-to-end probe")
	}
	distro := manualWSLDistro(t)

	// diting m4：写入前快照真实文件，t.Cleanup 自动恢复原内容+权限，
	// 避免 amagi-wsltest（fake key）经 merge 驻留用户真实 WSL 侧配置。
	linuxAgentDir, uncAgentDir := wslAgentDirsForTest(t, distro)
	before, hadBefore := readWSLFileForTest(t, filepath.Join(uncAgentDir, "models.json"))
	t.Cleanup(func() {
		restoreWSLFileForTest(t, distro, linuxAgentDir, before, hadBefore)
	})

	provider := config.Provider{Anthropic: &config.AnthropicFormat{Enabled: true, BaseURL: "https://open.bigmodel.cn/api/coding/paas/v4"}}
	cfg, err := BuildPiModelsConfig("wsltest", provider, "wsltest-model", "sk-manual-e2e", config.Parameters{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	linuxDir, err := WriteWSLPiAgentConfig(distro, cfg)
	if err != nil {
		t.Fatalf("WriteWSLPiAgentConfig: %v", err)
	}
	t.Logf("written to %s (distro %s)", linuxDir, distro)

	// 读回：内容与 Windows 侧产物同构（同一序列化代码路径）。
	data, err := os.ReadFile(platform.WSLToUNC(distro, linuxDir+"/models.json"))
	if err != nil {
		t.Fatalf("read back via UNC: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, data)
	}
	providers, _ := got["providers"].(map[string]any)
	if _, ok := providers["amagi-wsltest"]; !ok {
		t.Fatalf("amagi-wsltest missing after write: %v", providers)
	}
	t.Logf("models.json content OK (%d bytes, providers: %v)", len(data), providerKeys(providers))
}

func providerKeys(providers map[string]any) []string {
	keys := make([]string, 0, len(providers))
	for k := range providers {
		keys = append(keys, k)
	}
	return keys
}

// wslAgentDirsForTest 解析 pi agent 目录的 Linux/UNC 双形态（快照辅助）。
func wslAgentDirsForTest(t *testing.T, distro string) (linuxDir, uncDir string) {
	t.Helper()
	target, err := resolveWSLAgentTarget(distro, ".pi/agent")
	if err != nil {
		t.Fatalf("resolveWSLAgentTarget: %v", err)
	}
	return target.LinuxDir, target.UNCDir
}

// readWSLFileForTest 经 UNC 读取 WSL 侧文件；不存在时 ok=false。
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

// restoreWSLFileForTest 把快照内容写回 WSL 侧并 chmod 600；快照不存在时
// 删除测试写入的文件（目标态 = 测试前状态）。
func restoreWSLFileForTest(t *testing.T, distro, linuxAgentDir string, before []byte, hadBefore bool) {
	t.Helper()
	uncPath := platform.WSLToUNC(distro, linuxAgentDir+"/models.json")
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
	if err := wslChmodFn(distro, "600", linuxAgentDir+"/models.json"); err != nil {
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
