//go:build realfixture

package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/appmeta/claude"
)

// TestRealFixture_MasterJSONL 用主上机器真实 jsonl 验证修复效果。
//
// 跑法（默认跳过，需显式 tag）：
//
//	go test -tags realfixture ./internal/session/... -run TestRealFixture_MasterJSONL -v
//
// 覆盖 Acceptance Check 中的"主上真实样本验证"：取
// ~/.claude/projects/X--WorkSpace/d3d7a466-e92a-4038-b72c-09002783be28.jsonl
// 的首条 user content，调用 truncateFirstLine(content, 60, "X:\\WorkSpace")，
// 应输出"该项目有部分未提交修改..."（而非首行 workDir 路径）。
func TestRealFixture_MasterJSONL(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	jsonlPath := filepath.Join(
		home, ".claude", "projects", "X--WorkSpace",
		"d3d7a466-e92a-4038-b72c-09002783be28.jsonl",
	)
	if _, err := os.Stat(jsonlPath); err != nil {
		t.Skipf("fixture 不存在（非主上机器）: %v", err)
	}

	content, found, err := claude.ExtractFirstUserMessage(jsonlPath)
	if err != nil || !found {
		t.Fatalf("ExtractFirstUserMessage: err=%v found=%v", err, found)
	}

	t.Logf("原始首条 content 首行: %q", firstLine(content))
	t.Logf("原始首条 content 全文（前 200 rune）: %q", headRunes(content, 200))

	const workDir = "X:\\WorkSpace"
	got := truncateFirstLine(content, titleMaxRunes, workDir)
	t.Logf("truncateFirstLine(content, 60, %q) = %q", workDir, got)

	if got == "" {
		t.Fatalf("标题为空，未取到有意义行")
	}
	// 主上反馈的期望：标题应是"该项目有部分未提交修改..."（次行），而不是首行 workDir 路径。
	if strings.Contains(got, "WorkSpace") && isPathLike(got) {
		t.Fatalf("标题仍是路径类行，未跳过 workDir：got=%q", got)
	}
	if !strings.Contains(got, "未提交") {
		t.Fatalf("标题不包含预期的'未提交'关键字：got=%q", got)
	}
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx]
	}
	return s
}

func headRunes(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n])
}

// isPathLike 粗略判断字符串像路径（仅用于本测试的可读断言）。
func isPathLike(s string) bool {
	return len(s) > 0 && (strings.ContainsAny(s, `\/`)) && !strings.Contains(s, " ")
}
