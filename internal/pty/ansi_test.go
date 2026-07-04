//go:build windows

package pty

import (
	"strings"
	"testing"
)

func TestStripAnsi(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"no_escape", "hello world", "hello world"},
		{"csi_color", "\x1B[32mgreen\x1B[0m text", "green text"},
		{"csi_cursor", "abc\x1B[2J\x1B[Hdef", "abcdef"},
		{"osc_title", "\x1B]0;My Title\x07prompt$", "prompt$"},
		{"osc_terminator", "\x1B]0;X\x1B\\tail", "tail"},
		{"esc_single", "a\x1B=b", "ab"},
		{"cr_to_lf", "a\r\nb\rc", "a\nb\nc"},
		{"mixed", "\x1B[1;31m[ERR]\x1B[0m \r\n\x1B]0;T\x07hello", "[ERR] \nhello"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := StripAnsi(c.in)
			if got != c.want {
				t.Errorf("StripAnsi mismatch:\n in=%q\n got=%q\nwant=%q", c.in, got, c.want)
			}
		})
	}
}

func TestExtractFirstMeaningfulLine(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "pure_ansi_blank",
			in:   "\x1B[2J\x1B[H \x1B[0m",
			want: "",
		},
		{
			name: "ps_prompt_only",
			in:   "PS C:\\Users\\test>\r\n",
			want: "",
		},
		{
			name: "bare_prompt_only",
			in:   "> \n$ \n# \n",
			want: "",
		},
		{
			name: "blank_then_real_line",
			in:   "\r\n\r\n  \r\nWelcome to Claude Code v1.0\r\n",
			want: "Welcome to Claude Code v1.0",
		},
		{
			name: "multiline_picks_first_valid",
			in:   "\x1B[32mboot\x1B[0m\r\n\r\nchoose model:\r\n> ",
			want: "boot",
		},
		{
			name: "chinese_utf8",
			in:   "\x1B[?25l欢迎使用 Claude Code\r\n> ",
			want: "欢迎使用 Claude Code",
		},
		{
			name: "short_line_fallback",
			in:   "ab\r\ncd\r\n",
			want: "cd", // 兜底：取最后一段非空 trim
		},
		{
			name: "truncate_overlong",
			in:   strings.Repeat("abcdefghij", 100), // 1000 rune
			want: strings.Repeat("abcdefghij", 5) + "abcdefghi…", // 59 + … = 60
		},
		{
			name: "ansi_then_prompt_then_real",
			in:   "\x1B[?1h\x1B=\x1B[?25lPS C:\\code>\x1B[?25h\r\nLoading project...\r\n",
			want: "Loading project...",
		},
		{
			name: "with_osc_title_strip_first",
			in:   "\x1B]0;title\x07This is the first line\r\n",
			want: "This is the first line",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ExtractFirstMeaningfulLine([]byte(c.in))
			if got != c.want {
				t.Errorf("ExtractFirstMeaningfulLine mismatch:\n in=%q\n got=%q\nwant=%q", c.in, got, c.want)
			}
		})
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := truncateRunes("hello", 10); got != "hello" {
		t.Errorf("short input should pass through, got=%q", got)
	}
	if got := truncateRunes("helloworld", 5); got != "hell…" {
		t.Errorf("long input should truncate, got=%q", got)
	}
	// 中文按 rune 计数
	zh := strings.Repeat("字", 100)
	if got := truncateRunes(zh, 5); len([]rune(got)) != 5 || !strings.HasSuffix(got, "…") {
		t.Errorf("chinese truncate wrong, got=%q", got)
	}
}
