package structured

import "time"

type PartType string

const (
	PartTypeText        PartType = "text"
	PartTypeMarkdown    PartType = "markdown"
	PartTypeTool        PartType = "tool"
	PartTypeDiff        PartType = "diff"
	PartTypeRawTerminal PartType = "raw-terminal"
)

type ToolState string

const (
	ToolStatePending   ToolState = "pending"
	ToolStateRunning   ToolState = "running"
	ToolStateCompleted ToolState = "completed"
	ToolStateError     ToolState = "error"
)

type RawReason string

const (
	RawReasonANSI               RawReason = "ansi"
	RawReasonTUI                RawReason = "tui"
	RawReasonUnsupportedPattern RawReason = "unsupported-pattern"
	RawReasonClassifierOverflow RawReason = "classifier-overflow"
)

type Part struct {
	ID        string       `json:"id"`
	Type      PartType     `json:"type"`
	Text      string       `json:"text,omitempty"`
	Markdown  string       `json:"markdown,omitempty"`
	Tool      *ToolPayload `json:"tool,omitempty"`
	Diff      *DiffPayload `json:"diff,omitempty"`
	Raw       *RawPayload  `json:"raw,omitempty"`
	Source    SourceRef    `json:"source"`
	CreatedAt string       `json:"createdAt"`
}

type ToolPayload struct {
	Name          string    `json:"name"`
	State         ToolState `json:"state"`
	Title         string    `json:"title,omitempty"`
	InputPreview  string    `json:"inputPreview,omitempty"`
	OutputPreview string    `json:"outputPreview,omitempty"`
}

type DiffPayload struct {
	Text     string `json:"text"`
	Language string `json:"language,omitempty"`
}

type RawPayload struct {
	Text   string    `json:"text"`
	Reason RawReason `json:"reason"`
}

type SourceRef struct {
	Kind     string `json:"kind"`
	SeqStart uint64 `json:"seqStart"`
	SeqEnd   uint64 `json:"seqEnd"`
	AppType  string `json:"appType,omitempty"`
}

func nowRFC3339Nano() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
