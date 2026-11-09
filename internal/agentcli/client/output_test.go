package client

// output_test.go — 人类模式输出：表格 CJK 对齐、单元格截断、嵌套摘要、
// 键值缩进、数字格式化。

import (
	"bytes"
	"strings"
	"testing"

	"amagi-codebox/internal/agentcli/spec"
)

func TestDisplayWidth(t *testing.T) {
	cases := map[string]int{
		"":      0,
		"abc":   3,
		"中文":    4,
		"a中b":   4,
		"プロバイダ": 10,
		"ok✓":   3, // ✓ 记 1
	}
	for s, want := range cases {
		if got := displayWidth(s); got != want {
			t.Fatalf("displayWidth(%q)=%d want %d", s, got, want)
		}
	}
}

func TestTruncateCell(t *testing.T) {
	long := strings.Repeat("字", 25) // 宽 50 > 40
	got := truncateCell(long)
	if w := displayWidth(got); w > 40 {
		t.Fatalf("截断后宽度 %d 超限: %q", w, got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("截断应以 … 结尾: %q", got)
	}
	if truncateCell("short") != "short" {
		t.Fatal("短文本不应截断")
	}
}

func TestRenderTableAligned(t *testing.T) {
	var buf bytes.Buffer
	renderTable(&buf, []map[string]any{
		{"名称": "提供者甲", "状态": "running"},
		{"名称": "乙", "状态": "stopped"},
	})
	out := buf.String()
	want := "名称      状态\n提供者甲  running\n乙        stopped\n（共 2 项；截断/省略字段可用 --json 查看全量）\n"
	if out != want {
		t.Fatalf("表格渲染:\n got:\n%s\nwant:\n%s", out, want)
	}
}

func TestRenderTableNestedAndTruncate(t *testing.T) {
	var buf bytes.Buffer
	renderTable(&buf, []map[string]any{
		{"名称": "x", "嵌套": map[string]any{"k": "v"}, "列表": []any{1, 2}, "长文本": strings.Repeat("a", 60)},
	})
	out := buf.String()
	for _, want := range []string{"{…}", "[2项]", "…", "--json"} {
		if !strings.Contains(out, want) {
			t.Fatalf("表格缺 %q:\n%s", want, out)
		}
	}
}

func TestRenderTableEmpty(t *testing.T) {
	var buf bytes.Buffer
	renderTable(&buf, nil)
	if !strings.Contains(buf.String(), "0 项") {
		t.Fatalf("空表: %q", buf.String())
	}
}

func TestValueSummary(t *testing.T) {
	cases := []struct {
		v    any
		want string
	}{
		{map[string]any{"a": 1}, "{…}"},
		{[]any{float64(1), float64(2)}, "1, 2"},
		{[]any{map[string]any{}}, "[1项]"},
		{[]any{}, "[]"},
		{float64(3), "3"},
		{true, "true"},
		{nil, "null"},
		{"s", "s"},
	}
	for _, c := range cases {
		if got := valueSummary(c.v); got != c.want {
			t.Fatalf("valueSummary(%v)=%q want %q", c.v, got, c.want)
		}
	}
}

func TestFormatNumber(t *testing.T) {
	if formatNumber(3.0) != "3" {
		t.Fatal("整数不带 .0")
	}
	if formatNumber(1.5) != "1.5" {
		t.Fatal("小数保持")
	}
}

func TestRenderMapKeyValue(t *testing.T) {
	var buf bytes.Buffer
	renderMap(&buf, map[string]any{
		"b":  "x",
		"a":  float64(2),
		"嵌套": map[string]any{"k": "v"},
	}, "")
	out := buf.String()
	// 键排序稳定 + 嵌套缩进。
	want := "a: 2\nb: x\n嵌套:\n  k: v\n"
	if out != want {
		t.Fatalf("键值渲染:\n got:\n%s\nwant:\n%s", out, want)
	}
}

func TestRenderMapSoleArrayField(t *testing.T) {
	var buf bytes.Buffer
	renderMap(&buf, map[string]any{
		"total": float64(2),
		"items": []any{map[string]any{"k": "v1"}, map[string]any{"k": "v2"}},
	}, "")
	out := buf.String()
	for _, want := range []string{"total: 2", "k", "（共 2 项"} {
		if !strings.Contains(out, want) {
			t.Fatalf("唯一数组字段形态缺 %q:\n%s", want, out)
		}
	}
}

func TestHumanSuccessOutput(t *testing.T) {
	fs := newFakeServer(t, fakeOptions{
		handler: func(name string, req spec.Request) (any, *spec.Error) {
			return "plain-text-result", nil
		},
	})
	fs.useEndpoint(t)
	code, out, _ := runTest(t, []string{"status"}, "")
	if code != 0 || !strings.Contains(out, "plain-text-result") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestStdinSourceCached(t *testing.T) {
	// 多个 `-` 参数共享同一份 stdin（读一次）。
	s := &stdinSource{r: strings.NewReader("once\n")}
	b1, err := s.all()
	if err != nil {
		t.Fatal(err)
	}
	b2, _ := s.all()
	if string(b1) != "once\n" || string(b2) != "once\n" {
		t.Fatalf("stdin 缓存失效: %q %q", b1, b2)
	}
}
