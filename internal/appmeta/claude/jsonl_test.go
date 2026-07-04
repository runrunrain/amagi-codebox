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
