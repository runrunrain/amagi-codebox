//go:build windows

package launcher

// 真实 WSL 环境端到端实证（A2 凭据双向同步，M-1 后语义）。默认跳过；设置
// AMAGI_WSL_E2E=1：
//
//	AMAGI_WSL_E2E=1 go test -run TestManualRealWSLPiLoginSync ./internal/launcher/
//
// 覆盖（生产代码路径，零注入）：Windows 侧标准 ~/.pi/agent/auth.json 读取 →
// distro home/UNC 探测 → WSLFileMTime（wsl.exe 内 stat -c %Y）→ SHA-256 +
// mtime 双向新陈判定 → 任一方向的 UNC 原子写 → 两侧内容收敛一致 → 二次
// 同步幂等（Fresh）。两侧快照恢复（t.Cleanup）：auth.json 属真实登录凭据，
// 测试后 Windows 侧恢复原字节、distro 侧恢复原字节（不存在则删除）。
// 本机无 Windows 侧登录时跳过。
import (
	"os"
	"path/filepath"
	"testing"

	"amagi-codebox/internal/piconfig"
)

func TestManualRealWSLPiLoginSync(t *testing.T) {
	if os.Getenv("AMAGI_WSL_E2E") == "" {
		t.Skip("set AMAGI_WSL_E2E=1 to run the real-WSL end-to-end probe")
	}
	distro := manualWSLDistro(t)

	srcDir, err := piconfig.StandardAgentDir()
	if err != nil {
		t.Fatal(err)
	}
	srcPath := filepath.Join(srcDir, "auth.json")
	src, err := os.ReadFile(srcPath)
	if os.IsNotExist(err) {
		t.Skip("no windows-side pi auth.json on this machine; log in with pi first")
	}
	if err != nil {
		t.Fatal(err)
	}
	winBefore := append([]byte(nil), src...)

	linuxAuth := ""
	uncAuth := ""
	before, hadBefore := []byte(nil), false
	snapshot := func() {
		linuxAgentDir, uncAgentDir := wslAgentDirsForTest(t, distro)
		linuxAuth = linuxAgentDir + "/auth.json"
		uncAuth = uncAgentDir + "\\auth.json"
		before, hadBefore = readWSLFileForTest(t, uncAuth)
	}
	snapshot()
	t.Cleanup(func() {
		// 双向同步可能改写任一侧：两侧都要恢复 pre-test 字节（Windows 侧
		// 反向同步被改写时回写原内容；distro 侧同理，原不存在则删除）。
		if cur, rerr := os.ReadFile(srcPath); rerr == nil && string(cur) != string(winBefore) {
			if err := os.WriteFile(srcPath, winBefore, 0o600); err != nil {
				t.Logf("cleanup restore windows side: %v", err)
			}
		}
		if !hadBefore {
			if err := os.Remove(uncAuth); err != nil && !os.IsNotExist(err) {
				t.Logf("cleanup remove %s: %v", uncAuth, err)
			}
			return
		}
		if err := os.WriteFile(uncAuth, before, 0o600); err != nil {
			t.Logf("cleanup restore %s: %v", uncAuth, err)
			return
		}
		if err := wslChmodFn(distro, "600", linuxAuth); err != nil {
			t.Logf("cleanup chmod 600: %v", err)
		}
	})

	outcome, err := SyncWSLPiLoginAuth(distro)
	if err != nil {
		t.Fatalf("SyncWSLPiLoginAuth: %v (outcome=%d)", err, outcome)
	}
	t.Logf("first sync outcome=%d (0=Copied 1=Fresh 2=DistroNewer 3=TieKept)", outcome)

	// 双向收敛契约：无论哪个方向胜出，两侧内容必须一致。
	winAfter, rerr := os.ReadFile(srcPath)
	if rerr != nil {
		t.Fatalf("read windows side after sync: %v", rerr)
	}
	distAfter, rerr := os.ReadFile(uncAuth)
	if rerr != nil {
		t.Fatalf("read distro side after sync: %v", rerr)
	}
	if string(winAfter) != string(distAfter) {
		t.Fatalf("bidirectional sync must converge both sides: win=%d bytes distro=%d bytes", len(winAfter), len(distAfter))
	}

	// 幂等：第二次运行必须报 Fresh（内容已一致，不再改写）。
	outcome2, err2 := SyncWSLPiLoginAuth(distro)
	if err2 != nil || outcome2 != SyncWSLPiLoginAuthFresh {
		t.Fatalf("second sync not fresh: outcome=%d err=%v", outcome2, err2)
	}
}
