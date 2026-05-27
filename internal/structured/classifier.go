package structured

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const MaxClassifiableBytes = 32 * 1024

var (
	ansiPattern        = regexp.MustCompile(`\x1b\][^\x07]*(?:\x07|\x1b\\)|[\x1b\x9b][[\]()#;?]*(?:(?:\d{1,4}(?:;\d{0,4})*)?[\dA-PR-TZcf-nq-uy=><~])|\x1b[()#][A-Za-z0-9]`)
	tuiPattern         = regexp.MustCompile(`[─│┌┐└┘├┤┬┴┼━┃┏┓┗┛┣┫┳┻╋╔╗╚╝╠╣╦╩╬╭╮╰╯▁▂▃▄▅▆▇█▀▐▌░▒▓]`)
	controlCharPattern = regexp.MustCompile(`[\x00-\x08\x0B\x0C\x0E-\x1F\x7F\x80-\x9F]`)
)

func Classify(data []byte, seq uint64) Part {
	createdAt := nowRFC3339Nano()
	text := string(data)
	source := SourceRef{Kind: "pty", SeqStart: seq, SeqEnd: seq}

	if len(data) > MaxClassifiableBytes {
		return rawPart(seq, text[:safeStringLimit(text, MaxClassifiableBytes)], RawReasonClassifierOverflow, source, createdAt)
	}

	if !utf8.Valid(data) {
		return rawPart(seq, strings.ToValidUTF8(text, "�"), RawReasonUnsupportedPattern, source, createdAt)
	}

	normalized := normalizeChunk(text)
	trimmed := strings.TrimSpace(normalized)
	if trimmed == "" {
		return rawPart(seq, normalized, RawReasonUnsupportedPattern, source, createdAt)
	}

	if containsANSIOrCursorControl(text) {
		return rawPart(seq, normalized, RawReasonANSI, source, createdAt)
	}
	if containsTUIRunes(trimmed) {
		return rawPart(seq, normalized, RawReasonTUI, source, createdAt)
	}
	if looksLikeDiff(trimmed) {
		return Part{
			ID:        partID(seq, PartTypeDiff),
			Type:      PartTypeDiff,
			Diff:      &DiffPayload{Text: trimmed, Language: "diff"},
			Source:    source,
			CreatedAt: createdAt,
		}
	}
	if tool, ok := classifyTool(trimmed); ok {
		return Part{
			ID:        partID(seq, PartTypeTool),
			Type:      PartTypeTool,
			Tool:      tool,
			Source:    source,
			CreatedAt: createdAt,
		}
	}
	if looksLikeMarkdown(trimmed) {
		return Part{
			ID:        partID(seq, PartTypeMarkdown),
			Type:      PartTypeMarkdown,
			Markdown:  trimmed,
			Source:    source,
			CreatedAt: createdAt,
		}
	}

	return Part{
		ID:        partID(seq, PartTypeText),
		Type:      PartTypeText,
		Text:      trimmed,
		Source:    source,
		CreatedAt: createdAt,
	}
}

func partID(seq uint64, partType PartType) string {
	return fmt.Sprintf("pty-%d-%s", seq, partType)
}

func rawPart(seq uint64, text string, reason RawReason, source SourceRef, createdAt string) Part {
	cleaned := stripAnsiAndTUI(text)
	return Part{
		ID:        partID(seq, PartTypeRawTerminal),
		Type:      PartTypeRawTerminal,
		Raw:       &RawPayload{Text: cleaned, Reason: reason},
		Source:    source,
		CreatedAt: createdAt,
	}
}

func stripAnsiAndTUI(text string) string {
	cleaned := ansiPattern.ReplaceAllString(text, "")
	cleaned = tuiPattern.ReplaceAllString(cleaned, " ")
	cleaned = controlCharPattern.ReplaceAllString(cleaned, "")
	return cleaned
}

func normalizeChunk(text string) string {
	return strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
}

func containsANSIOrCursorControl(text string) bool {
	return strings.Contains(text, "\x1b[") || strings.Contains(text, "\x1b]") || strings.Contains(text, "\x1b(") || strings.Contains(text, "\x9b")
}

func containsTUIRunes(text string) bool {
	return strings.ContainsAny(text, "╭╮╯╰│─┌┐└┘├┤┬┴┼█▌▐░▒▓")
}

func looksLikeDiff(text string) bool {
	if strings.Contains(text, "diff --git ") || strings.Contains(text, "\n@@ ") || strings.HasPrefix(text, "@@ ") {
		return true
	}
	lines := strings.Split(text, "\n")
	hasOld, hasNew, changes := false, false, 0
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "--- "):
			hasOld = true
		case strings.HasPrefix(line, "+++ "):
			hasNew = true
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			changes++
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			changes++
		}
	}
	return hasOld && hasNew && changes > 0
}

func classifyTool(text string) (*ToolPayload, bool) {
	lines := nonEmptyLines(text)
	if len(lines) == 0 {
		return nil, false
	}
	first := strings.TrimLeft(lines[0], " •*-")
	fields := strings.Fields(first)
	if len(fields) == 0 {
		return nil, false
	}
	name := canonicalToolName(fields[0])
	if name == "" {
		return nil, false
	}
	return &ToolPayload{
		Name:          name,
		State:         ToolStateCompleted,
		Title:         first,
		OutputPreview: strings.Join(limitLines(lines[1:], 5), "\n"),
	}, true
}

func canonicalToolName(raw string) string {
	trimmed := strings.Trim(raw, ":()[]{}")
	lower := strings.ToLower(trimmed)
	switch lower {
	case "read", "write", "edit", "bash", "shell", "grep", "glob", "todowrite", "todo", "run", "exec", "tool", "call":
		if lower == "todowrite" {
			return "TodoWrite"
		}
		return strings.ToUpper(trimmed[:1]) + strings.ToLower(trimmed[1:])
	default:
		return ""
	}
}

func looksLikeMarkdown(text string) bool {
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "# ") || strings.HasPrefix(trimmed, "## ") || strings.HasPrefix(trimmed, "### ") || strings.HasPrefix(trimmed, "> ") || strings.HasPrefix(trimmed, "```") {
			return true
		}
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			return true
		}
		if strings.Contains(trimmed, "|") && strings.Count(trimmed, "|") >= 2 {
			return true
		}
	}
	return false
}

func nonEmptyLines(text string) []string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func limitLines(lines []string, limit int) []string {
	if len(lines) <= limit {
		return lines
	}
	return lines[:limit]
}

func safeStringLimit(text string, maxBytes int) int {
	if len(text) <= maxBytes {
		return len(text)
	}
	end := maxBytes
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	if end == 0 {
		return maxBytes
	}
	return end
}
