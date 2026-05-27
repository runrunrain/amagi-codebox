package structured

import (
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
