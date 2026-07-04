package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeTitleLogger 是 titleLogger 的测试桩，记录最近一次 Info 调用。
type fakeTitleLogger struct {
	lastMsg string
}

func (f *fakeTitleLogger) Info(source, message string, detail ...string) {
	f.lastMsg = message
}

// writeJSONLFixture 在 baseDir 下创建一个 .claude/projects/<encoded-workDir>/ 子目录，
// 并写入指定 sessionId 的 jsonl（首条 user message = content），返回其完整路径。
func writeJSONLFixture(t *testing.T, baseDir, workDir, sessionID, firstUserContent string) string {
	t.Helper()
	encoded := encodeWorkDirForTest(workDir)
	dir := filepath.Join(baseDir, ".claude", "projects", encoded)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	line, err := json.Marshal(map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": firstUserContent,
		},
		"origin": map[string]any{"kind": "human"},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	fp := filepath.Join(dir, sessionID+".jsonl")
	if err := os.WriteFile(fp, append(line, '\n'), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return fp
}

// encodeWorkDirForTest 复刻 claude.pathSepReplacer 的编码（测试包不依赖 claude 内部）。
func encodeWorkDirForTest(workDir string) string {
	r := strings.NewReplacer(":", "-", "\\", "-", "/", "-")
	return r.Replace(workDir)
}

func TestTruncateFirstLine(t *testing.T) {
	cases := []struct {
		name    string
		content string
		max     int
		workDir string
		want    string
	}{
		{"plain_first_line", "hello world", 60, "", "hello world"},
		{"multiline_first", "line1\nline2", 60, "", "line1"},
		{"truncate_long", strings.Repeat("a", 100), 10, "", "aaaaaaaaa…"},
		{"truncate_multiline_first", "0123456789012\nignored", 5, "", "0123…"},
		{"crlf_only", "abc\r\nrest", 60, "", "abc"},
		{"empty", "", 60, "", ""},
		{"max_zero", "abc", 0, "", ""},
		// 核心修复：首行是 workDir 路径 → 跳过取次行（主上看到的 Bug 现场）
		{"workdir_first_line_skipped", "X:\\WorkSpace\\amagi-codebox\n该项目有部分未提交修改", 60, "X:\\WorkSpace\\amagi-codebox", "该项目有部分未提交修改"},
		// 归一化比较：workDir 用反斜杠、content 用正斜杠也应跳过
		{"workdir_backslash_vs_forward", "X:/WorkSpace/foo\n内容描述", 60, "X:\\WorkSpace\\foo", "内容描述"},
		// 首行是别的纯路径（非 workDir）也跳过
		{"pure_path_skipped", "C:\\Users\\test\\project\n描述文本", 60, "X:\\other", "描述文本"},
		// 首行是 UNIX 绝对路径也跳过
		{"unix_path_skipped", "/home/user/project\n描述文本", 60, "X:\\other", "描述文本"},
		// 整行 XML 标签跳过（slash command 内部表示）
		{"xml_tag_skipped", "<command-message>amagi:pull-all-repos</command-message>\n真实指令描述", 60, "", "真实指令描述"},
		// 系统注入标签整行跳过
		{"xml_system_tag_skipped", "<system-reminder>notice</system-reminder>\n真实描述", 60, "", "真实描述"},
		// markdown 标题行不跳过（视为有意义，保留）
		{"markdown_header_kept", "## Task Contract\n详情内容", 60, "", "## Task Contract"},
		// 正常自然语言首行不跳过（不以 < 开头，不是路径）
		{"normal_text_kept", "帮我重构 bridge.go\n下一行", 60, "", "帮我重构 bridge.go"},
		// 全部行都是纯路径：兜底取首个非空行（避免空标题）
		{"all_skipped_fallback", "X:\\foo\nY:\\bar", 60, "", "X:\\foo"},
		// 全部行都是 workDir：兜底取首个非空行
		{"all_workdir_fallback", "X:\\foo\nX:\\foo", 60, "X:\\foo", "X:\\foo"},
		// 多行混合：空行 + workDir + 纯路径 + XML + 真实内容 → 取首个有意义行
		{"mixed_noise_skipped", "\nX:\\WorkSpace\n<command-message>x</command-message>\nC:\\other\n真实首条消息", 60, "X:\\WorkSpace", "真实首条消息"},
		// 超长首条有意义行截断
		{"truncate_meaningful_after_skip", "X:\\foo\n" + strings.Repeat("好", 100), 10, "X:\\foo", "好好好好好好好好好…"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := truncateFirstLine(c.content, c.max, c.workDir)
			if got != c.want {
				t.Errorf("truncateFirstLine(%q, %d, %q):\n got=%q\nwant=%q", c.content, c.max, c.workDir, got, c.want)
			}
		})
	}
}

func TestTrackTitle_CapturesTitleAndSessionID(t *testing.T) {
	homeDir := t.TempDir()
	const workDir = "X:/WorkSpace/demo"

	mgr := NewManager()
	sess := mgr.Create(AppTypeClaudeCode, "p", "default", "model", ModeEmbedded, workDir, false)

	// 预先写入 jsonl（模拟 Claude Code 启动后已落第一条用户消息）。
	const wantSessionID = "abc-123-uuid"
	const wantContent = "帮我重构 bridge.go"
	writeJSONLFixture(t, homeDir, workDir, wantSessionID, wantContent)

	log := &fakeTitleLogger{}
	ctx := t.Context()

	done := make(chan struct{})
	go func() {
		TrackTitle(ctx, mgr, sess.ID, homeDir, workDir, log)
		close(done)
	}()

	// 等待标题被写入（pollOnce 立即跑一轮，应该 < 1s 完成）。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := mgr.GetStatus(sess.ID); got == "" || got != StatusRunning {
			t.Fatalf("session should be running, status=%q", got)
		}
		info, _ := mgr.Get(sess.ID)
		if info.Title == wantContent {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	info, err := mgr.Get(sess.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if info.Title != wantContent {
		t.Errorf("Title mismatch:\n got=%q\nwant=%q", info.Title, wantContent)
	}
	if info.ClaudeSessionID != wantSessionID {
		t.Errorf("ClaudeSessionID mismatch:\n got=%q\nwant=%q", info.ClaudeSessionID, wantSessionID)
	}

	// 标记停止 → tracker 应在 ≤ 一个 tick 退出。
	mgr.MarkExited(sess.ID)
	// 缩短等待：用 select 监听 done。
	select {
	case <-done:
		// PASS
	case <-time.After(2*titlePollInterval + 1*time.Second):
		t.Errorf("tracker goroutine did not exit after session stopped (leak)")
	}
}

func TestTrackTitle_FollowsResumeSwitch(t *testing.T) {
	homeDir := t.TempDir()
	const workDir = "X:/WorkSpace/demo"

	mgr := NewManager()
	sess := mgr.Create(AppTypeClaudeCode, "p", "default", "model", ModeEmbedded, workDir, false)

	// 初始：第一个会话
	const sid1 = "session-one"
	writeJSONLFixture(t, homeDir, workDir, sid1, "原始会话首条")

	log := &fakeTitleLogger{}
	ctx := t.Context()

	go TrackTitle(ctx, mgr, sess.ID, homeDir, workDir, log)

	// 等首轮捕获
	waitFor(t, mgr, sess.ID, "原始会话首条", 2*time.Second)

	// 模拟 /resume：写入第二个 jsonl，并 backdate 第一个，确保第二个 mtime 最新
	const sid2 = "session-two-resumed"
	writeJSONLFixture(t, homeDir, workDir, sid2, "切换后的会话首条")
	// 显式让 sid2 的 mtime 比 sid1 新
	encoded := encodeWorkDirForTest(workDir)
	newer := time.Now().Add(time.Hour) // 远在未来，保证最新
	if err := os.Chtimes(filepath.Join(homeDir, ".claude", "projects", encoded, sid2+".jsonl"), newer, newer); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	// 等下一个 tick（≤ pollInterval）后标题应切换
	waitFor(t, mgr, sess.ID, "切换后的会话首条", titlePollInterval+2*time.Second)

	info, _ := mgr.Get(sess.ID)
	if info.ClaudeSessionID != sid2 {
		t.Errorf("ClaudeSessionID should follow resume switch:\n got=%q\nwant=%q", info.ClaudeSessionID, sid2)
	}
}

// waitFor 轮询直到 Title 匹配 want 或超时失败。
func waitFor(t *testing.T, mgr *Manager, id, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		info, _ := mgr.Get(id)
		if info.Title == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	info, _ := mgr.Get(id)
	t.Fatalf("waitFor Title=%q timeout; got %q", want, info.Title)
}

func TestList_FillsExitedTitleFromJSONL(t *testing.T) {
	homeDir := t.TempDir()
	const workDir = "X:/WorkSpace/demo"
	const claudeSID = "frozen-uuid-001"
	const wantTitle = "冻结会话的首条消息"

	writeJSONLFixture(t, homeDir, workDir, claudeSID, wantTitle)

	mgr := NewManager()
	mgr.SetHomeDir(homeDir)

	sess := mgr.Create(AppTypeClaudeCode, "p", "default", "model", ModeEmbedded, workDir, false)
	// 模拟会话已退出且 tracker 已冻结 ClaudeSessionID（标题未填，留空触发 List 直读）
	mgr.SetClaudeSessionID(sess.ID, claudeSID)
	mgr.MarkExited(sess.ID)

	infos := mgr.List()
	var found *SessionInfo
	for i := range infos {
		if infos[i].ID == sess.ID {
			found = &infos[i]
		}
	}
	if found == nil {
		t.Fatalf("session not in List result")
	}
	if found.Title != wantTitle {
		t.Errorf("List should fill Title from jsonl for exited session:\n got=%q\nwant=%q", found.Title, wantTitle)
	}

	// 第二次 List 应读缓存（Title 已写回 Session.Title），无额外 IO 风险（此处仅断言结果一致）
	infos2 := mgr.List()
	for i := range infos2 {
		if infos2[i].ID == sess.ID && infos2[i].Title != wantTitle {
			t.Errorf("second List Title drifted: got=%q want=%q", infos2[i].Title, wantTitle)
		}
	}
}

func TestList_SkipsRunningAndMissingJSONL(t *testing.T) {
	homeDir := t.TempDir()
	const workDir = "X:/WorkSpace/demo"

	mgr := NewManager()
	mgr.SetHomeDir(homeDir)

	// 1) Running 会话不直读（即使 ClaudeSessionID 已设）
	runSess := mgr.Create(AppTypeClaudeCode, "p", "default", "m", ModeEmbedded, workDir, false)
	mgr.SetClaudeSessionID(runSess.ID, "never-written")
	// 不写 jsonl 文件
	// 故意不 MarkExited：保持 Running

	// 2) Exited 但 jsonl 不存在 → 静默空标题
	exitSess := mgr.Create(AppTypeClaudeCode, "p", "default", "m", ModeEmbedded, workDir, false)
	mgr.SetClaudeSessionID(exitSess.ID, "missing-uuid")
	mgr.MarkExited(exitSess.ID)

	// 3) Exited 但 ClaudeSessionID 为空（未跟踪到）→ 静默空标题
	exitEmptySID := mgr.Create(AppTypeClaudeCode, "p", "default", "m", ModeEmbedded, workDir, false)
	mgr.MarkExited(exitEmptySID.ID)

	infos := mgr.List()
	for _, info := range infos {
		if info.Title != "" {
			t.Errorf("expected empty Title for session %q, got %q", info.ID, info.Title)
		}
	}
}
