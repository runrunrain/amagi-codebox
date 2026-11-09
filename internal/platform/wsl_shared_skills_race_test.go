package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// runSharedSkillsScriptInShell 在本地 POSIX sh 中执行生成的建链脚本（HOME 指
// 向临时目录）。R2 审核要求以真实行为（而非字符串形状）钉住并发语义；preamble
// 用于注入确定性竞争（函数覆盖 mv，在真实 mv 执行前制造 B 位竞争者）。
func runSharedSkillsScriptInShell(t *testing.T, preamble string) (string, string, error) {
	t.Helper()
	home := t.TempDir()
	agents := filepath.Join(home, ".agents")
	if err := os.MkdirAll(filepath.Join(agents, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agents, "skills", "SKILL.md"), []byte("# USER ORIGINAL"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir() // Windows 技能根的 distro 侧形态（存在即可）
	script := wslSharedSkillsScript(target)
	full := script
	if preamble != "" {
		full = preamble + ";\n" + script
	}
	cmd := exec.Command("sh", "-c", full)
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.CombinedOutput()
	return string(out), home, err
}

// TestSharedSkillsBackupRaceFileFailsClosed（R2-F02 场景 A）：竞争者在选槽
// 之后、真实 mv 之前创建同名文件 → mv -n 让位、L 未迁离 → 必须 FAIL-MOVE-LOST
// 并停止发布（不拿竞争者空文件冒充本轮备份、不覆盖未备份的用户目录）。
func TestSharedSkillsBackupRaceFileFailsClosed(t *testing.T) {
	preamble := `mv() {
  case "$3" in
    "$HOME"/.agents/skills.pre-shared.*) : >"$3" ;;
  esac
  command mv "$@"
}`
	out, home, err := runSharedSkillsScriptInShell(t, preamble)
	if err == nil {
		t.Fatalf("no-clobber yield must fail closed, got exit 0, output:\n%s", out)
	}
	if !strings.Contains(out, "FAIL-MOVE-LOST") {
		t.Fatalf("must report FAIL-MOVE-LOST, got:\n%s", out)
	}
	if strings.Contains(out, "BACKED-UP") || strings.Contains(out, "LINKED") {
		t.Fatalf("yielded backup must not report success, got:\n%s", out)
	}
	// 用户目录原样保留（未被链接替换——发布未发生）。
	data, rerr := os.ReadFile(filepath.Join(home, ".agents", "skills", "SKILL.md"))
	if rerr != nil || string(data) != "# USER ORIGINAL" {
		t.Fatalf("user tree must stay in place (not replaced by link), err=%v", rerr)
	}
}

// TestSharedSkillsBackupRaceDirFailsClosed（R2-F02 场景 B）：竞争者在 B 位创建
// 目录 → mv 把 L 挪进目录内部（L 消失但 B 是目录）→ 必须识别为不可确认的
// 迁移并 FAIL-MOVE-LOST，不继续发布；用户内容仍可在树内找到。
func TestSharedSkillsBackupRaceDirFailsClosed(t *testing.T) {
	preamble := `mv() {
  case "$3" in
    "$HOME"/.agents/skills.pre-shared.*) mkdir -p "$3" ;;
  esac
  command mv "$@"
}`
	out, home, err := runSharedSkillsScriptInShell(t, preamble)
	if err == nil {
		t.Fatalf("dir-at-slot must fail closed, got exit 0, output:\n%s", out)
	}
	if !strings.Contains(out, "FAIL-MOVE-LOST") {
		t.Fatalf("must report FAIL-MOVE-LOST, got:\n%s", out)
	}
	if strings.Contains(out, "LINKED") {
		t.Fatalf("must not publish after unconfirmed migration, got:\n%s", out)
	}
	// 内容保全：SKILL.md 仍在 .agents 树内某处（原位或备份目录内部）。
	found := false
	_ = filepath.Walk(filepath.Join(home, ".agents"), func(p string, info os.FileInfo, werr error) error {
		if werr == nil && !info.IsDir() && info.Name() == "SKILL.md" {
			found = true
		}
		return nil
	})
	if !found {
		t.Fatal("user content must survive somewhere under ~/.agents")
	}
}

// TestSharedSkillsBackupMigratesRealDir（正向）：无竞争时真实目录被完整迁移
// 到唯一备份槽（BACKED-UP 披露路径、内容原样）。BSD mv 无 -T 时发布阶段会
// FAIL-PUBLISH（macOS 取证边界，如实接受）；GNU 环境则 LINKED——两者都证明
// 备份语义正确。
func TestSharedSkillsBackupMigratesRealDir(t *testing.T) {
	out, home, err := runSharedSkillsScriptInShell(t, "")
	backupLine := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "BACKED-UP:") {
			backupLine = strings.TrimPrefix(l, "BACKED-UP:")
		}
	}
	if backupLine == "" {
		t.Fatalf("real dir migration must report BACKED-UP, got (err=%v):\n%s", err, out)
	}
	if !strings.HasPrefix(backupLine, home+"/.agents/skills.pre-shared.") {
		t.Fatalf("backup must live under ~/.agents with pre-shared prefix: %s", backupLine)
	}
	data, rerr := os.ReadFile(filepath.Join(backupLine, "SKILL.md"))
	if rerr != nil || string(data) != "# USER ORIGINAL" {
		t.Fatalf("backup must carry the user tree intact, err=%v", rerr)
	}
	if !(strings.Contains(out, "LINKED") || strings.Contains(out, "FAIL-PUBLISH")) {
		t.Fatalf("terminal must be LINKED (GNU mv -T) or FAIL-PUBLISH (BSD), got:\n%s", out)
	}
}

// TestWSLTranslatePathsCoreErrorSurface：错误内核把 runner 失败向上冒出
// （R1-F07 语义的行为级钉住——公共 WSLTranslatePaths 仍吞错返回空 map）。
func TestWSLTranslatePathsCoreErrorSurface(t *testing.T) {
	orig := wslScriptRunner
	defer func() { wslScriptRunner = orig }()
	wslScriptRunner = func(string, string) ([]byte, error) {
		return nil, os.ErrDeadlineExceeded
	}
	m, err := wslTranslatePathsCore("Ubuntu", `C:\Users\u\.agents\skills`)
	if runtime.GOOS == "windows" {
		if err == nil || !strings.Contains(err.Error(), "translate probe failed") {
			t.Fatalf("runner failure must surface as error, got %v", err)
		}
		if len(m) != 0 {
			t.Fatalf("failed probe must not fabricate mappings: %v", m)
		}
	} else {
		// 非 Windows：平台门拦截（空映射 + nil error），不得伪造错误。
		if err != nil || len(m) != 0 {
			t.Fatalf("non-windows must gate to empty mapping without error, got %v %v", m, err)
		}
	}
	// 公共包装保持旧契约（空 map、无 error）。
	if got := WSLTranslatePaths("Ubuntu", `C:\Users\u\.agents\skills`); len(got) != 0 {
		t.Fatalf("public wrapper must keep empty-map contract, got %v", got)
	}
}
