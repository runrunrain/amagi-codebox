package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// mvGNU 在 BSD 宿主上把 `mv -nT A B` 等价模拟为 GNU 语义（字面目的
// 地 + 不覆写）：目的已存在（任何形态，含目录/目录符号链接，-e 跟随链接
// 判存在）→ 原样拒绝且返回 0；目的不存在 → command mv A B（即 rename）。
// 生产环境（WSL/Ubuntu，GNU coreutils）使用真实 mv -nT。注入竞争的测试
// 定义 mv 包装：先制造槽位竞争者，再回落到本语义函数。
const mvGNU = `mv() { __mv_gnu "$@"; }
__mv_gnu() {
  case "$1" in
    -nT)
      if [ -e "$3" ]; then return 0; fi
      command mv "$2" "$3"
      ;;
    *)
      command mv "$@"
      ;;
  esac
}`

// runSharedSkillsScriptInShell 在本地 POSIX sh 中执行生成的建链脚本（HOME
// 指向临时目录）。R2/R3 审核要求以真实行为（而非字符串形状）钉住并发语义；
// preamble 链在 mvNTAdapter 之后，用于注入确定性竞争（在真实迁移执行前于
// 槽位制造竞争者）。
func runSharedSkillsScriptInShell(t *testing.T, homeSetup func(home string), preambles ...string) (string, string, string, error) {
	t.Helper()
	home := t.TempDir()
	if homeSetup != nil {
		homeSetup(home)
	}
	target := t.TempDir() // Windows 技能根的 distro 侧形态（存在即可）
	script := wslSharedSkillsScript(target)
	parts := append([]string{mvGNU}, preambles...)
	cmd := exec.Command("sh", "-c", strings.Join(parts, "\n")+";\n"+script)
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.CombinedOutput()
	return string(out), home, target, err
}

func writeDefaultUserSkill(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# USER ORIGINAL"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// competitorAtSlot 在 -nT 迁移执行前于槽位创建竞争者（经 mv 函数链注入）。
// competitorAtSlot 构造 mv 包装：在 -nT 迁移执行前于槽位（$3）创建竞争者
// （makeShell 为创建原语，可引用脚本内的 $T——函数与脚本同 shell，变量
// 可见），随后回落 __mv_gnu 语义（目的已存在 → 拒绝）。
func competitorAtSlot(makeShell string) string {
	return `mv() {
  case "$1 $3" in
    "-nT "$HOME"/.agents/skills.pre-shared."*) ` + makeShell + ` ;;
  esac
  __mv_gnu "$@"
}`
}

// TestSharedSkillsBackupRaceFileFailsClosed（R2-F02 场景 A）：竞争者在迁移
// 执行前于槽位创建同名文件 → mv -nT 字面拒绝（L 未迁离）→ FAIL-MOVE-LOST
// 并停止发布；用户目录原字节保留在原位。
func TestSharedSkillsBackupRaceFileFailsClosed(t *testing.T) {
	out, home, _, err := runSharedSkillsScriptInShell(t,
		func(home string) { writeDefaultUserSkill(t, home) },
		competitorAtSlot(`: > "$3"`),
	)
	if err == nil {
		t.Fatalf("occupied slot must fail closed, got exit 0, output:\n%s", out)
	}
	if !strings.Contains(out, "FAIL-MOVE-LOST") {
		t.Fatalf("must report FAIL-MOVE-LOST, got:\n%s", out)
	}
	if strings.Contains(out, "BACKED-UP") || strings.Contains(out, "LINKED") {
		t.Fatalf("occupied slot must not report success, got:\n%s", out)
	}
	data, rerr := os.ReadFile(filepath.Join(home, ".agents", "skills", "SKILL.md"))
	if rerr != nil || string(data) != "# USER ORIGINAL" {
		t.Fatalf("user tree must stay in place byte-identical, err=%v", rerr)
	}
}

// TestSharedSkillsBackupRaceDirLinkFailsClosed（R3-F02）：竞争者在槽位创建
// 指向目标目录的符号链接（模拟指向宿主技能根）→ 迁移原语对已存在目的一律
// 拒绝、绝不把目的解释为目录内移动——无穿透写入（T/skills 不得出现），报
// FAIL-MOVE-LOST，用户目录原位保留。
func TestSharedSkillsBackupRaceDirLinkFailsClosed(t *testing.T) {
	var targetDir string
	out, home, target, err := runSharedSkillsScriptInShell(t,
		func(home string) { writeDefaultUserSkill(t, home) },
		competitorAtSlot(`ln -s "$T" "$3"`),
	)
	targetDir = target
	if err == nil {
		t.Fatalf("dirlink-occupied slot must fail closed, got exit 0, output:\n%s", out)
	}
	if !strings.Contains(out, "FAIL-MOVE-LOST") {
		t.Fatalf("must report FAIL-MOVE-LOST, got:\n%s", out)
	}
	if strings.Contains(out, "BACKED-UP") || strings.Contains(out, "LINKED") {
		t.Fatalf("occupied slot must not report success, got:\n%s", out)
	}
	// 穿透断言：宿主目标目录内不得出现任何内容（尤其 skills/）。
	entries, rerr := os.ReadDir(targetDir)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if len(entries) != 0 {
		t.Fatalf("slot dirlink must NOT be traversed into target: %v", entries)
	}
	data, rerr := os.ReadFile(filepath.Join(home, ".agents", "skills", "SKILL.md"))
	if rerr != nil || string(data) != "# USER ORIGINAL" {
		t.Fatalf("user tree must stay in place byte-identical, err=%v", rerr)
	}
}

// TestSharedSkillsBackupDirOccupantFailsClosed：竞争者在槽位创建真实目录
// → 同样拒绝、L 原位保留、FAIL-MOVE-LOST。
func TestSharedSkillsBackupDirOccupantFailsClosed(t *testing.T) {
	out, home, _, err := runSharedSkillsScriptInShell(t,
		func(home string) { writeDefaultUserSkill(t, home) },
		competitorAtSlot(`mkdir -p "$3"`),
	)
	if err == nil {
		t.Fatalf("dir-occupied slot must fail closed, got exit 0, output:\n%s", out)
	}
	if !strings.Contains(out, "FAIL-MOVE-LOST") || strings.Contains(out, "BACKED-UP") {
		t.Fatalf("dir-occupied slot must fail honestly, got:\n%s", out)
	}
	data, rerr := os.ReadFile(filepath.Join(home, ".agents", "skills", "SKILL.md"))
	if rerr != nil || string(data) != "# USER ORIGINAL" {
		t.Fatalf("user tree must stay in place byte-identical, err=%v", rerr)
	}
}

// TestSharedSkillsBackupMigratesRealDir（正向）：无竞争时真实目录被完整迁移
// 到排他槽位（BACKED-UP 披露路径、内容逐字节原样）。BSD mv 无 -T 时发布阶段
// 经适配接缝由真实 ln/rm 组合完成（macOS 取证边界）；GNU 环境则 LINKED——
// 两者都证明备份语义正确。
func TestSharedSkillsBackupMigratesRealDir(t *testing.T) {
	out, home, _, err := runSharedSkillsScriptInShell(t,
		func(home string) { writeDefaultUserSkill(t, home) },
	)
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
		t.Fatalf("backup must carry the user tree byte-identical, err=%v", rerr)
	}
	if !(strings.Contains(out, "LINKED") || strings.Contains(out, "FAIL-PUBLISH")) {
		t.Fatalf("terminal must be LINKED (GNU mv -T) or FAIL-PUBLISH (BSD), got:\n%s", out)
	}
}

// TestSharedSkillsBackupKeepsLegalSkillsSubdir（R3-F10 正例）：`skills` 是
// 合法技能名——`~/.agents/skills/skills/SKILL.md` 是普通用户内容，正常迁移
// 不得被任何子条目名启发式误判：必须 BACKED-UP 且备份内该子技能逐字节保留
// （迁移身份由 mv -nT 原语结果自证）。
func TestSharedSkillsBackupKeepsLegalSkillsSubdir(t *testing.T) {
	out, _, _, err := runSharedSkillsScriptInShell(t,
		func(home string) {
			writeDefaultUserSkill(t, home)
			nested := filepath.Join(home, ".agents", "skills", "skills")
			if err := os.MkdirAll(nested, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(nested, "SKILL.md"), []byte("# VALID SKILL NAMED skills"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	)
	backupLine := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "BACKED-UP:") {
			backupLine = strings.TrimPrefix(l, "BACKED-UP:")
		}
	}
	if backupLine == "" {
		t.Fatalf("legal skills-named subdir must migrate normally, got (err=%v):\n%s", err, out)
	}
	if strings.Contains(out, "FAIL-MOVE-LOST") {
		t.Fatalf("name heuristic must not reject legal content, got:\n%s", out)
	}
	nested, rerr := os.ReadFile(filepath.Join(backupLine, "skills", "SKILL.md"))
	if rerr != nil || string(nested) != "# VALID SKILL NAMED skills" {
		t.Fatalf("nested legal skill must survive byte-identical, err=%v", rerr)
	}
	data, rerr := os.ReadFile(filepath.Join(backupLine, "SKILL.md"))
	if rerr != nil || string(data) != "# USER ORIGINAL" {
		t.Fatalf("top-level skill must survive byte-identical, err=%v", rerr)
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
