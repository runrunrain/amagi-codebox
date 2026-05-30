package structured

import (
	"fmt"
	"strings"
	"testing"
)

func TestClassifyCoversStructuredTypes(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want PartType
	}{
		{name: "text", in: "plain assistant response", want: PartTypeText},
		{name: "markdown", in: "# Plan\n\n- inspect\n- implement", want: PartTypeMarkdown},
		{name: "tool", in: "Read src/main.go\nLoaded 20 lines", want: PartTypeTool},
		{name: "diff", in: "--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-old\n+new", want: PartTypeDiff},
		{name: "raw-terminal", in: "\x1b[32mgreen\x1b[0m", want: PartTypeRawTerminal},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			part := Classify([]byte(tt.in), uint64(i+1))
			if part.Type != tt.want {
				t.Fatalf("Classify() type = %s, want %s", part.Type, tt.want)
			}
			if part.Source.Kind != "pty" || part.Source.SeqStart != uint64(i+1) || part.Source.SeqEnd != uint64(i+1) {
				t.Fatalf("unexpected source: %+v", part.Source)
			}
			if part.ID == "" || part.CreatedAt == "" {
				t.Fatalf("part should include id and createdAt: %+v", part)
			}
		})
	}
}

func TestClassifyRecoversReadableAnsiContent(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want PartType
	}{
		{name: "ansi markdown", in: "\x1b[32m# Plan\x1b[0m\n\n- inspect\n- implement", want: PartTypeMarkdown},
		{name: "ansi readable text", in: "\x1b[32mBuild completed successfully with 3 files changed.\x1b[0m", want: PartTypeText},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			part := Classify([]byte(tt.in), uint64(40+i))
			if part.Type != tt.want {
				t.Fatalf("Classify() type = %s, want %s; part = %+v", part.Type, tt.want, part)
			}
		})
	}
}

func TestClassifyKeepsTUIOnlyMenusRaw(t *testing.T) {
	tests := []string{
		"╭─ Menu\n│ ❯ Continue\n╰──────",
		"╭─ Menu\n│ - Continue\n╰──────",
		"╭─ Menu\n│ * Continue\n╰──────",
		"╭─ Menu\n│ > Continue\n╰──────",
	}

	for i, input := range tests {
		t.Run(fmt.Sprintf("menu-%d", i), func(t *testing.T) {
			part := Classify([]byte(input), uint64(44+i))
			if part.Type != PartTypeRawTerminal {
				t.Fatalf("type = %s, want raw-terminal", part.Type)
			}
			if part.Raw == nil || part.Raw.Reason != RawReasonTUI {
				t.Fatalf("raw reason = %+v, want tui", part.Raw)
			}
		})
	}
}

func TestClassifyOverflowFallsBackToRawTerminal(t *testing.T) {
	part := Classify([]byte(strings.Repeat("a", MaxClassifiableBytes+10)), 9)
	if part.Type != PartTypeRawTerminal {
		t.Fatalf("type = %s, want raw-terminal", part.Type)
	}
	if part.Raw == nil || part.Raw.Reason != RawReasonClassifierOverflow {
		t.Fatalf("raw reason = %+v, want classifier-overflow", part.Raw)
	}
	if len(part.Raw.Text) > MaxClassifiableBytes {
		t.Fatalf("overflow raw text len = %d, want <= %d", len(part.Raw.Text), MaxClassifiableBytes)
	}
}

func TestClassifyInvalidUTF8DoesNotPanic(t *testing.T) {
	part := Classify([]byte{0xff, 0xfe, 0xfd}, 3)
	if part.Type != PartTypeRawTerminal {
		t.Fatalf("type = %s, want raw-terminal", part.Type)
	}
	if part.Raw == nil || part.Raw.Reason != RawReasonUnsupportedPattern {
		t.Fatalf("raw reason = %+v, want unsupported-pattern", part.Raw)
	}
}

func TestRawPartStripsAnsiTUIAndControlCharacters(t *testing.T) {
	part := Classify([]byte("\x1b[31mred\x1b[0m\n\x1b]0;title\x07\x1b(B╭─ panel\x00"), 4)
	if part.Type != PartTypeRawTerminal || part.Raw == nil {
		t.Fatalf("type = %s raw = %+v, want raw-terminal", part.Type, part.Raw)
	}
	if strings.ContainsAny(part.Raw.Text, "\x1b\x00") || strings.Contains(part.Raw.Text, "╭") {
		t.Fatalf("raw text was not cleaned: %q", part.Raw.Text)
	}
	if !strings.Contains(part.Raw.Text, "red") || !strings.Contains(part.Raw.Text, "panel") {
		t.Fatalf("clean raw text lost readable content: %q", part.Raw.Text)
	}
}
