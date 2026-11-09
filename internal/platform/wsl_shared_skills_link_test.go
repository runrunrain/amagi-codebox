package platform

import (
	"strings"
	"testing"
)

// TestWSLSharedSkillsScriptShape 钉住受管符号链接脚本的骨架：幂等短路、真实
// 目录先备份、既有链接替换、失败各有专属标记（越界改脚本会破坏 distro 内
// 行为契约）。
func TestWSLSharedSkillsScriptShape(t *testing.T) {
	script := wslSharedSkillsScript("/mnt/c/Users/demo/.agents/skills")
	for _, want := range []string{
		`L="$HOME/.agents/skills"`,
		`if [ -L "$L" ] && [ "$(readlink "$L")" = "$T" ]; then printf 'OK-LINKED\n'; exit 0; fi`,
		// R1-F02→R3：排他槽位（时间戳+PID）+ mv -nT 字面目的地迁移——迁移身份
		// 由原语结果自证（L 消失⇔B 存在），不用子条目名启发式；目的被任何形态
		// 占用（含目录/目录符号链接）时原语拒绝，不存在穿透写入。
		`B="$HOME/.agents/skills.pre-shared.$(date +%Y%m%d%H%M%S).$$"`,
		`mv -nT "$L" "$B" || { printf 'FAIL-MOVE\n'; exit 1; }`,
		`if [ -e "$L" ] || [ ! -e "$B" ]; then printf 'FAIL-MOVE-LOST\n'; exit 1; fi`,
		`printf 'BACKED-UP:%s\n' "$B"`,
		`rm -f "$L" || { printf 'FAIL-RM\n'; exit 1; }`,
		// R1-F01：临时名建链 + mv -T 原子发布（不穿透既有目录符号链接写入
		// 目标目录），发布后 readlink 终验。
		`ln -s "$T" "$N" || { rm -f "$N"; printf 'FAIL-LN\n'; exit 1; }`,
		`if mv -T "$N" "$L" 2>/dev/null; then`,
		`printf 'FAIL-PUBLISH\n'; exit 1`,
		`if [ "$(readlink "$L")" = "$T" ]; then`,
		`printf 'FAIL-VERIFY\n'; exit 1`,
		`printf 'LINKED\n'`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script must contain %q, got:\n%s", want, script)
		}
	}
	// 禁止旧的直达式发布（L 已是目录符号链接时会穿透写入目标目录内）。
	if strings.Contains(script, `ln -s "$T" "$L"`) {
		t.Fatalf("script must NOT publish directly onto $L (symlink traversal), got:\n%s", script)
	}
	// 目标路径单引号包裹（含空格/中文用户名安全）。
	if !strings.Contains(script, `T='/mnt/c/Users/demo/.agents/skills'`) {
		t.Fatalf("script must quote target path, got:\n%s", script)
	}
}

// TestWSLSharedSkillsQuote 单引号转义：值内单引号必须转出 POSIX 安全形态。
func TestWSLSharedSkillsQuote(t *testing.T) {
	if got := wslShQuote(`/mnt/c/Users/毛润/.agents/skills`); got != `'/mnt/c/Users/毛润/.agents/skills'` {
		t.Fatalf("plain path quote mismatch: %s", got)
	}
	if got := wslShQuote("it's"); got != `'it'\''s'` {
		t.Fatalf("embedded quote must be escaped POSIX-style, got: %s", got)
	}
}

// TestParseWSLSharedSkillsLinkResult 正负成对：四种确认终态 + 目标缺失附注 +
// 未确认输出（含空输出）必须 failed，不冒充成功。
func TestParseWSLSharedSkillsLinkResult(t *testing.T) {
	const target = "/mnt/c/Users/u/.agents/skills"
	cases := []struct {
		name, in   string
		wantState  WSLSharedSkillsLinkState
		wantBackup string
	}{
		{"already", "OK-LINKED\n", WSLSkillsAlreadyLinked, ""},
		{"linked", "LINKED\n", WSLSkillsLinked, ""},
		{"linked-with-target-missing-note", "TARGET-MISSING\nLINKED\n", WSLSkillsLinked, ""},
		{"backed-up", "BACKED-UP:/home/u/.agents/skills.pre-shared.20261109\nLINKED\n", WSLSkillsBackedUp, "/home/u/.agents/skills.pre-shared.20261109"},
		{"replaced", "REPLACED-LINK\nLINKED\n", WSLSkillsReplacedLink, ""},
		{"fail-ln", "FAIL-LN\n", WSLSkillsFailed, ""},
		{"fail-backup-slot", "FAIL-BACKUP-SLOT\n", WSLSkillsFailed, ""},
		{"fail-move-lost", "FAIL-MOVE-LOST\n", WSLSkillsFailed, ""},
		{"fail-publish", "FAIL-PUBLISH\n", WSLSkillsFailed, ""},
		{"fail-verify", "FAIL-VERIFY\n", WSLSkillsFailed, ""},
		{"empty", "", WSLSkillsFailed, ""},
		{"garbage", "line1\nline2\n", WSLSkillsFailed, ""},
	}
	for _, tc := range cases {
		res := parseWSLSharedSkillsLinkResult(tc.in, target)
		if res.State != tc.wantState {
			t.Fatalf("%s: state=%s want=%s detail=%s", tc.name, res.State, tc.wantState, res.Detail)
		}
		if res.Backup != tc.wantBackup {
			t.Fatalf("%s: backup=%q want=%q", tc.name, res.Backup, tc.wantBackup)
		}
		if tc.wantState == WSLSkillsFailed && res.Detail == "" {
			t.Fatalf("%s: failure must carry detail", tc.name)
		}
	}
	// 附注行进入 detail（诚实披露目标目录暂不存在）。
	res := parseWSLSharedSkillsLinkResult("TARGET-MISSING\nLINKED\n", target)
	if !strings.Contains(res.Detail, "目标目录当前不存在") {
		t.Fatalf("target-missing note must surface in detail: %s", res.Detail)
	}
}

// TestWSLEnsureSharedAgentsSkillsLinkInputGates 参数门（跨平台恒成立）：
// 空 distro / 非 Windows 形态路径 → failed，不触达 runner。
func TestWSLEnsureSharedAgentsSkillsLinkInputGates(t *testing.T) {
	calls := 0
	orig := wslScriptRunner
	wslScriptRunner = func(string, string) ([]byte, error) { calls++; return nil, nil }
	defer func() { wslScriptRunner = orig }()

	if res := WSLEnsureSharedAgentsSkillsLink("", `C:\Users\u\.agents\skills`); res.State != WSLSkillsFailed {
		t.Fatalf("empty distro must fail, got %s", res.State)
	}
	if res := WSLEnsureSharedAgentsSkillsLink("Ubuntu", ""); res.State != WSLSkillsFailed {
		t.Fatalf("empty host dir must fail, got %s", res.State)
	}
	if res := WSLEnsureSharedAgentsSkillsLink("Ubuntu", "relative/path"); res.State != WSLSkillsFailed {
		t.Fatalf("relative path must fail, got %s", res.State)
	}
	if calls != 0 {
		t.Fatalf("input gates must not reach the runner, got %d calls", calls)
	}
}
