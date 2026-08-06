package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeJSONL 是测试辅助函数：把任意多行字符串（或 []byte）按行写入 path。
// 调用方负责构造每行的 JSON 文本（用 rawJSONLine 包装任意对象）。
func writeJSONL(t *testing.T, path string, lines []string) {
	t.Helper()
	// 确保父目录存在（部分 case 直接在临时目录根写，存在也无妨）。
	if mkErr := os.MkdirAll(filepath.Dir(path), 0o755); mkErr != nil {
		t.Fatalf("mkdir %q: %v", filepath.Dir(path), mkErr)
	}
	if writeErr := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); writeErr != nil {
		t.Fatalf("write %q: %v", path, writeErr)
	}
}

// rawJSONLine 把任意对象序列化为单行 JSON 字符串（用于构造 fixture）。
func rawJSONLine(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return string(b)
}

func TestSessionJSONLPath(t *testing.T) {
	cases := []struct {
		name       string
		homeDir    string
		workDir    string
		session    string
		wantSuffix string // 仅断言后缀，避免跨平台 filepath.Join 的分隔符差异
	}{
		{
			name:       "drive_forward_slash",
			homeDir:    "C:/Users/毛润",
			workDir:    "X:/WorkSpace",
			session:    "abc-123",
			wantSuffix: filepath.Join(".claude", "projects", "X--WorkSpace", "abc-123.jsonl"),
		},
		{
			name:       "drive_backslash",
			homeDir:    "C:/Users/a",
			workDir:    `C:\Users\a`,
			session:    "deadbeef",
			wantSuffix: filepath.Join(".claude", "projects", "C--Users-a", "deadbeef.jsonl"),
		},
		{
			name:       "unix_path",
			homeDir:    "/home/u",
			workDir:    "/home/u/proj",
			session:    "sid",
			wantSuffix: filepath.Join(".claude", "projects", "-home-u-proj", "sid.jsonl"),
		},
		{
			name:       "nested_workdir",
			homeDir:    "C:/Users/x",
			workDir:    "X:/a/b/c",
			session:    "z",
			wantSuffix: filepath.Join(".claude", "projects", "X--a-b-c", "z.jsonl"),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SessionJSONLPath(c.homeDir, c.workDir, c.session)

			// 1) 后缀断言（编码 + 文件名 + 子目录结构正确）。
			if !strings.HasSuffix(filepath.ToSlash(got), filepath.ToSlash(c.wantSuffix)) {
				t.Errorf("SessionJSONLPath suffix mismatch:\n got=%q\nwant suffix=%q", got, c.wantSuffix)
			}

			// 2) homeDir 仍是前缀（filepath.Join 会把 / 归一化为平台分隔符，
			//    所以比较时把两边都 ToSlash）。
			if !strings.HasPrefix(filepath.ToSlash(got), filepath.ToSlash(c.homeDir)+"/") {
				t.Errorf("SessionJSONLPath home prefix lost:\n got=%q\nexpect prefix=%q/", got, c.homeDir)
			}
		})
	}
}

func TestExtractFirstUserMessage(t *testing.T) {
	type want struct {
		content string
		found   bool
		wantErr bool
	}

	cases := []struct {
		name  string
		lines []string
		want  want
	}{
		{
			name: "pure_metadata",
			// 仅 system/init 等元数据行，无 user 消息。
			lines: []string{
				rawJSONLine(t, map[string]any{
					"type":    "system",
					"subtype": "init",
					"cwd":     "X:/WorkSpace",
				}),
				rawJSONLine(t, map[string]any{
					"type":           "system",
					"permissionMode": "default",
				}),
			},
			want: want{content: "", found: false, wantErr: false},
		},
		{
			name: "first_user_string",
			// 首条 user content 为字符串、origin.kind=human，应直接返回。
			lines: []string{
				rawJSONLine(t, map[string]any{
					"type":    "system",
					"subtype": "init",
				}),
				rawJSONLine(t, map[string]any{
					"type": "user",
					"message": map[string]any{
						"role":    "user",
						"content": "帮我重构 bridge.go 的 session_id",
					},
					"origin": map[string]any{"kind": "human"},
				}),
			},
			want: want{content: "帮我重构 bridge.go 的 session_id", found: true, wantErr: false},
		},
		{
			name: "tool_result_skipped",
			// type=user 但 content 是数组（tool_result），origin.kind=tool，应跳过；
			// 后续真正 human 输入应被返回。
			lines: []string{
				rawJSONLine(t, map[string]any{
					"type": "user",
					"message": map[string]any{
						"role": "user",
						"content": []map[string]any{
							{"type": "tool_result", "tool_use_id": "tu_1", "content": "ok"},
						},
					},
					"origin": map[string]any{"kind": "tool"},
				}),
				rawJSONLine(t, map[string]any{
					"type": "user",
					"message": map[string]any{
						"role":    "user",
						"content": "这是真正的用户输入",
					},
					"origin": map[string]any{"kind": "human"},
				}),
			},
			want: want{content: "这是真正的用户输入", found: true, wantErr: false},
		},
		{
			name: "chinese_utf8",
			lines: []string{
				rawJSONLine(t, map[string]any{
					"type": "user",
					"message": map[string]any{
						"role":    "user",
						"content": "你好，世界 —— 中文测试用例",
					},
					"origin": map[string]any{"kind": "human"},
				}),
			},
			want: want{content: "你好，世界 —— 中文测试用例", found: true, wantErr: false},
		},
		{
			name: "multiline_content",
			// content 含 \n：应原样返回（截断是调用方职责）。
			lines: []string{
				rawJSONLine(t, map[string]any{
					"type": "user",
					"message": map[string]any{
						"role":    "user",
						"content": "第一行\n第二行\n第三行",
					},
					"origin": map[string]any{"kind": "human"},
				}),
			},
			want: want{content: "第一行\n第二行\n第三行", found: true, wantErr: false},
		},
		{
			name: "no_user_message",
			// 有 assistant/system 行但无 user。
			lines: []string{
				rawJSONLine(t, map[string]any{"type": "system", "subtype": "init"}),
				rawJSONLine(t, map[string]any{
					"type": "assistant",
					"message": map[string]any{
						"role":    "assistant",
						"content": "Welcome to Claude Code",
					},
				}),
			},
			want: want{content: "", found: false, wantErr: false},
		},
		{
			name: "user_without_origin_skipped",
			// type=user 但缺 origin 字段（schema 不全），应跳过继续。
			lines: []string{
				rawJSONLine(t, map[string]any{
					"type": "user",
					"message": map[string]any{
						"role":    "user",
						"content": "应该被跳过因为没有 origin",
					},
				}),
				rawJSONLine(t, map[string]any{
					"type": "user",
					"message": map[string]any{
						"role":    "user",
						"content": "我才是首条",
					},
					"origin": map[string]any{"kind": "human"},
				}),
			},
			want: want{content: "我才是首条", found: true, wantErr: false},
		},
		{
			name: "user_origin_tool_skipped",
			// type=user 且 origin 存在，但 kind=tool（不是 human），应跳过。
			lines: []string{
				rawJSONLine(t, map[string]any{
					"type": "user",
					"message": map[string]any{
						"role":    "user",
						"content": "tool ack",
					},
					"origin": map[string]any{"kind": "tool"},
				}),
				rawJSONLine(t, map[string]any{
					"type": "user",
					"message": map[string]any{
						"role":    "user",
						"content": "人类输入",
					},
					"origin": map[string]any{"kind": "human"},
				}),
			},
			want: want{content: "人类输入", found: true, wantErr: false},
		},
		{
			name: "malformed_line_skipped",
			// 中间某行 JSON 非法：应跳过不中断，仍能找到后续合法 user。
			lines: []string{
				`{"type":"system","subtype":"init"}`,
				`{this is not valid json`,
				`{"type":"assistant","message":{"role":"assistant","content":"hi"}`,
				rawJSONLine(t, map[string]any{
					"type": "user",
					"message": map[string]any{
						"role":    "user",
						"content": "在乱码之后找到的合法 user",
					},
					"origin": map[string]any{"kind": "human"},
				}),
			},
			want: want{content: "在乱码之后找到的合法 user", found: true, wantErr: false},
		},
		{
			name: "empty_file",
			// 空文件（无任何行）：found=false, err=nil。
			lines: []string{},
			want:  want{content: "", found: false, wantErr: false},
		},
		{
			name: "blank_lines_only",
			// 仅空行：应全部跳过。
			lines: []string{"", "", ""},
			want:  want{content: "", found: false, wantErr: false},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "session.jsonl")
			if len(c.lines) > 0 {
				writeJSONL(t, path, c.lines)
			} else {
				// 显式创建空文件，确保与「文件存在但空」语义对齐。
				if writeErr := os.WriteFile(path, []byte{}, 0o644); writeErr != nil {
					t.Fatalf("write empty fixture: %v", writeErr)
				}
			}

			content, found, err := ExtractFirstUserMessage(path)
			if c.want.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (content=%q found=%v)", content, found)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if found != c.want.found {
				t.Errorf("found mismatch:\n got=%v\nwant=%v (content=%q)", found, c.want.found, content)
			}
			if content != c.want.content {
				t.Errorf("content mismatch:\n got=%q\nwant=%q", content, c.want.content)
			}
		})
	}
}

func TestExtractFirstUserMessage_FileNotExist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "no-such.jsonl")

	content, found, err := ExtractFirstUserMessage(path)
	if err == nil {
		t.Fatalf("expected error for missing file, got nil (content=%q found=%v)", content, found)
	}
	if found {
		t.Errorf("found should be false for missing file, got %v", found)
	}
	if content != "" {
		t.Errorf("content should be empty for missing file, got %q", content)
	}
	if !os.IsNotExist(unwrapPathErr(err)) {
		t.Errorf("err should wrap os.ErrNotExist, got: %v", err)
	}
}

// unwrapPathErr 解开 fmt.Errorf("...: %w", err) 包装，便于 os.IsNotExist 判定。
func unwrapPathErr(err error) error {
	if err == nil {
		return nil
	}
	// errors.As 在 Go 1.13+ 可用，但 os.ErrNotExist 是 sentinel，errors.Is 更合适。
	// 这里直接返回底层：fmt.Errorf %w 在 errors.Unwrap 后即得到 os.PathError/ErrNotExist。
	type unwrapper interface{ Unwrap() error }
	if u, ok := err.(unwrapper); ok {
		if inner := u.Unwrap(); inner != nil {
			return inner
		}
	}
	return err
}

// touchWithMTime 设置文件的 mtime（测试用，用于稳定构造不同文件的最新顺序）。
// Windows 下 WriteFile 会更新 mtime，但跨文件相对顺序仍可能受 FS 时间精度影响，
// 所以显式 backdate 确保排序稳定。
func touchWithMTime(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("chtimes %q: %v", path, err)
	}
}

func TestFindLatestActiveJSONL(t *testing.T) {
	// workDir 用作编码推导：模拟 "X:/WorkSpace/demo"
	const workDir = "X:/WorkSpace/demo"

	cases := []struct {
		name      string
		setup     func(t *testing.T, homeDir string) // 在 homeDir 下构造伪 projects 目录
		wantID    string                              // 期望返回的 sessionId；空表示期望 err
		wantErr   bool
		wantEmpty bool // path 应为空
	}{
		{
			name: "picks_latest_mtime",
			setup: func(t *testing.T, homeDir string) {
				encoded := pathSepReplacer.Replace(workDir)
				dir := filepath.Join(homeDir, ".claude", "projects", encoded)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				// 3 个 jsonl，base=time，每个递增 10s，最新的是 "newest.jsonl"
				base := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
				paths := []struct {
					name  string
					off   time.Duration
				}{
					{"old.jsonl", 0},
					{"mid.jsonl", 10 * time.Second},
					{"newest.jsonl", 20 * time.Second},
				}
				for _, p := range paths {
					fp := filepath.Join(dir, p.name)
					if err := os.WriteFile(fp, []byte("{}\n"), 0o644); err != nil {
						t.Fatalf("write %s: %v", p.name, err)
					}
					touchWithMTime(t, fp, base.Add(p.off))
				}
			},
			wantID: "newest",
		},
		{
			name: "single_file",
			setup: func(t *testing.T, homeDir string) {
				encoded := pathSepReplacer.Replace(workDir)
				dir := filepath.Join(homeDir, ".claude", "projects", encoded)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				fp := filepath.Join(dir, "only.jsonl")
				if err := os.WriteFile(fp, []byte("{}\n"), 0o644); err != nil {
					t.Fatalf("write: %v", err)
				}
			},
			wantID: "only",
		},
		{
			name: "ignores_non_jsonl",
			setup: func(t *testing.T, homeDir string) {
				encoded := pathSepReplacer.Replace(workDir)
				dir := filepath.Join(homeDir, ".claude", "projects", encoded)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				// 非 jsonl 文件：.txt/.log，且 mtime 比 jsonl 新，仍应被忽略
				newer := time.Date(2026, 7, 4, 12, 5, 0, 0, time.UTC)
				older := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
				jsonlPath := filepath.Join(dir, "real.jsonl")
				if err := os.WriteFile(jsonlPath, []byte("{}\n"), 0o644); err != nil {
					t.Fatalf("write jsonl: %v", err)
				}
				touchWithMTime(t, jsonlPath, older)
				for _, np := range []string{"noise.txt", "debug.log"} {
					fp := filepath.Join(dir, np)
					if err := os.WriteFile(fp, []byte("x\n"), 0o644); err != nil {
						t.Fatalf("write %s: %v", np, err)
					}
					touchWithMTime(t, fp, newer)
				}
			},
			wantID: "real",
		},
		{
			name: "empty_dir",
			setup: func(t *testing.T, homeDir string) {
				encoded := pathSepReplacer.Replace(workDir)
				dir := filepath.Join(homeDir, ".claude", "projects", encoded)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				// 目录存在但空
			},
			wantErr:   true,
			wantEmpty: true,
		},
		{
			name: "dir_not_exist",
			setup: func(t *testing.T, homeDir string) {
				// 不创建任何目录
			},
			wantErr:   true,
			wantEmpty: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			homeDir := t.TempDir()
			c.setup(t, homeDir)

			path, sid, err := FindLatestActiveJSONL(homeDir, workDir)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (path=%q sid=%q)", path, sid)
				}
				if c.wantEmpty && path != "" {
					t.Errorf("expected empty path on error, got %q", path)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if sid != c.wantID {
				t.Errorf("sessionID mismatch:\n got=%q\nwant=%q", sid, c.wantID)
			}
			if path == "" {
				t.Errorf("expected non-empty path")
			}
			// path 应以 <sid>.jsonl 结尾
			wantSuffix := c.wantID + ".jsonl"
			if !strings.HasSuffix(filepath.ToSlash(path), wantSuffix) {
				t.Errorf("path suffix mismatch:\n got=%q\nwant suffix=%q", path, wantSuffix)
			}
		})
	}
}

// --- ExtractUsageRecords 超长行 / offset 精确性测试（token too long 修复验证） ---
//
// 下列 helper 返回 []byte（含/不含换行），与上面的 rawJSONLine（返回 string）并存：
// 超长行场景需要直接控制字节与尾换行，避免 20MB 字符串的额外拷贝。

// clJSONLine 序列化为单行 JSON 并追加换行符。
func clJSONLine(t *testing.T, obj any) []byte {
	t.Helper()
	data, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return append(data, '\n')
}

// clJSONLineNoNL 序列化为单行 JSON 但不追加换行符，用于构造末尾无 \n 的尾行。
func clJSONLineNoNL(t *testing.T, obj any) []byte {
	t.Helper()
	data, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

// writeClaudeSession 写入 <dir>/<encoded-cwd>/<session>.jsonl，返回路径与文件大小。
func writeClaudeSession(t *testing.T, lines ...[]byte) (string, int64) {
	t.Helper()
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, "-encoded-cwd")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sessionDir, "session-uuid-1.jsonl")
	var buf []byte
	for _, l := range lines {
		buf = append(buf, l...)
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, info.Size()
}

func claudeAssistantLine(t *testing.T) []byte {
	return clJSONLine(t, map[string]any{
		"type":       "assistant",
		"timestamp":  "2026-07-17T02:42:42.807Z",
		"session_id": "s1",
		"cwd":        "/proj",
		"message": map[string]any{
			"id":    "msg_1",
			"model": "claude-x",
			"usage": map[string]any{
				"input_tokens":                10,
				"output_tokens":               20,
				"cache_read_input_tokens":     5,
				"cache_creation_input_tokens": 2,
			},
		},
	})
}

// TestExtractUsageRecordsHandlesOversizedLine 验证单行 >16MiB 时不再报
// bufio.Scanner: token too long，且超长 user 行被跳过、后续 assistant usage 被提取、
// offset 精确等于文件大小。
func TestExtractUsageRecordsHandlesOversizedLine(t *testing.T) {
	bigUserLine := clJSONLine(t, map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": strings.Repeat("b", 20*1024*1024), // 20MB
		},
	})
	path, size := writeClaudeSession(t, bigUserLine, claudeAssistantLine(t))

	records, lastOffset, err := ExtractUsageRecords(path, 0)
	if err != nil {
		t.Fatalf("超长行应被完整读入而不报错: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("期望 1 条 assistant 记录，得到 %d", len(records))
	}
	r := records[0]
	if r.InputTokens != 10 || r.OutputTokens != 20 || r.CacheReadInputTokens != 5 || r.CacheCreationInputTokens != 2 {
		t.Fatalf("token 维度不符: %#v", r)
	}
	if r.Model != "claude-x" || r.DedupKey != "cc:msg_msg_1" || r.SessionID != "s1" || r.ProjectDir != "/proj" {
		t.Fatalf("元信息不符: %#v", r)
	}
	if lastOffset != size {
		t.Fatalf("lastOffset=%d 应等于文件大小 %d", lastOffset, size)
	}
}

// TestExtractUsageRecordsOffsetMatchesFileSizeNoTrailingNewline 验证文件末尾无 \n 时
// lastOffset 仍精确等于文件字节大小。
func TestExtractUsageRecordsOffsetMatchesFileSizeNoTrailingNewline(t *testing.T) {
	path, size := writeClaudeSession(t,
		claudeAssistantLine(t),
		// 末行无换行符（仍是一条合法 assistant 行）
		clJSONLineNoNL(t, map[string]any{
			"type":      "assistant",
			"timestamp": "2026-07-17T02:43:00.000Z",
			"message": map[string]any{
				"id":    "msg_2",
				"model": "claude-x",
				"usage": map[string]any{"input_tokens": 1, "output_tokens": 1},
			},
		}),
	)
	_, lastOffset, err := ExtractUsageRecords(path, 0)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if lastOffset != size {
		t.Fatalf("无尾换行 lastOffset=%d 应等于文件大小 %d", lastOffset, size)
	}
}

// --- Major-1：兼容旧 Scanner 产生的 size+1 历史游标 ---
//
// 旧 Scanner 对无尾 \n 的末行执行 len(line)+1，sync_state.last_line_offset 可能
// 等于当时 fileSize+1。新实现必须把这种游标安全迁移为 fileSize，避免永久空扫
// 与追加首字节错位（与 codex parser 保持同一口径）。

// claudeNoTrailingNewlineSession 构造末行无 \n 的会话，返回路径与文件大小。
// 旧 Scanner 会把 lastOffset 记为 size+1。
func claudeNoTrailingNewlineSession(t *testing.T) (string, int64) {
	t.Helper()
	return writeClaudeSession(t,
		claudeAssistantLine(t),
		clJSONLineNoNL(t, map[string]any{
			"type":      "assistant",
			"timestamp": "2026-07-17T02:43:00.000Z",
			"message": map[string]any{
				"id":    "msg_2",
				"model": "claude-x",
				"usage": map[string]any{"input_tokens": 1, "output_tokens": 1},
			},
		}),
	)
}

// TestExtractUsageRecordsHandlesLegacySizePlusOneCursorNoGrowth 验证旧 size+1 游标
// 在文件未增长时：不报错、无 records、返回 offset==fileSize（使 sync 层快速跳过生效）。
func TestExtractUsageRecordsHandlesLegacySizePlusOneCursorNoGrowth(t *testing.T) {
	path, size := claudeNoTrailingNewlineSession(t)
	legacyCursor := size + 1

	records, lastOffset, err := ExtractUsageRecords(path, legacyCursor)
	if err != nil {
		t.Fatalf("legacy size+1 游标在文件未增长时不应报错: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("文件未增长时应返回 0 records，得到 %d", len(records))
	}
	if lastOffset != size {
		t.Fatalf("lastOffset=%d 应被收敛为 fileSize=%d（消除 size+1 残留）", lastOffset, size)
	}
}

// TestExtractUsageRecordsHandlesLegacySizePlusOneCursorAfterAppend 验证旧 size+1 游标
// 在文件后续追加一行合法记录时：正确解析追加行、offset 推进到新文件大小。
func TestExtractUsageRecordsHandlesLegacySizePlusOneCursorAfterAppend(t *testing.T) {
	path, size := claudeNoTrailingNewlineSession(t)

	appended := clJSONLine(t, map[string]any{
		"type":      "assistant",
		"timestamp": "2026-07-17T02:44:00.000Z",
		"message": map[string]any{
			"id":    "msg_3",
			"model": "claude-x",
			"usage": map[string]any{"input_tokens": 42, "output_tokens": 7},
		},
	})
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	if _, err := f.Write([]byte("\n")); err != nil { // 补齐原末行 \n
		t.Fatalf("write newline: %v", err)
	}
	if _, err := f.Write(appended); err != nil {
		t.Fatalf("write appended: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	newSize := size + 1 + int64(len(appended))

	legacyCursor := size + 1
	records, lastOffset, err := ExtractUsageRecords(path, legacyCursor)
	if err != nil {
		t.Fatalf("追加后续读不应报错: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("应解析出追加的 1 条记录，得到 %d", len(records))
	}
	if records[0].InputTokens != 42 || records[0].OutputTokens != 7 {
		t.Fatalf("追加记录 token 不符: in=%d out=%d", records[0].InputTokens, records[0].OutputTokens)
	}
	if records[0].DedupKey != "cc:msg_msg_3" {
		t.Fatalf("追加记录 dedup 不符: %s", records[0].DedupKey)
	}
	if lastOffset != newSize {
		t.Fatalf("lastOffset=%d 应等于新文件大小 %d", lastOffset, newSize)
	}
}
