//go:build windows

package pty

import (
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
