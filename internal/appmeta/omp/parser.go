// Package omp provides a read-only parser for Oh My Pi (omp) session JSONL files.
//
// omp（@oh-my-pi/pi-coding-agent，CLI 命令 `omp`）与 pi（@earendil-works/pi-coding-agent）
// 同源，会话 JSONL 格式与 pi 同构（docs/session.md + packages/stats/src/parser.ts
// 双重确认）。omp stores each session as JSONL under
//
//	<PI_CODING_AGENT_DIR>/sessions/<dir-encoded>/<timestamp>_<id>.jsonl
//
// (default config dir ~/.omp/agent; PI_CODING_AGENT_DIR relocates the root).
// Differences from pi's on-disk layout:
//
//   - omp encodes the project directory home-relative (paths inside the user's
//     home are stored relative to it) instead of pi's "--<cwd>--" dash-encoding;
//     the legacy "--<cwd>--" form is still accepted for old sessions. The header
//     `cwd` field is authoritative; path-based inference is only a fallback.
//   - omp writes nested sub-session transcripts under
//     sessions/<project>/<session>/<id>.jsonl (subagent/advisor sessions, also
//     billable). Recursive enumeration naturally covers them, and the content
//     fingerprint dedup collapses any overlap with parent transcripts.
//   - cost is denominated in USD (stats formatter `$`), same as pi.
//
// Each line is a JSON object carrying a "type" field. The first line is a
// SessionHeader; billable usage lives on four entry shapes (same as pi):
//
//   - type=="message", message.role=="assistant": the model turn itself
//   - type=="message", message.role=="toolResult", message.usage present:
//     nested LLM work performed inside a tool
//   - type=="compaction", entry.usage present: LLM work that produced the
//     compaction summary
//   - type=="branch_summary", entry.usage present: LLM work that produced the
//     branch summary
//
// # Fork/clone dedup
//
// Same semantics as pi: omp's createBranchedSession/forkFrom copy non-header
// entries verbatim into a new file whose header carries "parentSession". We key
// every record on a content fingerprint (entryID, occurredAt, model, provider,
// token counts), so copied entries dedup together regardless of which file they
// live in or whether ancestors still exist. Entries without any timestamp fall
// back to file mtime (rare; worst case one extra count).
//
// # Currency
//
// omp computes usage.cost.total in USD. When a record carries a non-zero native
// cost we tag it CostProvided with CurrencyCode="USD" so the usage service never
// re-infers a domestic currency. Records without native cost stay
// CostProvided=false and let the caller estimate from its own price table.
package omp

import (
	"bufio"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// dedupPrefixOmp is the dedup_key prefix for omp session-log records.
// Defined locally to avoid a reverse dependency on the usage package; its value
// must match usage.dedupPrefixOmp.
const dedupPrefixOmp = "omp:"

// UsageEventStub is the appmeta-level neutral event (mirrors the claude/codex/
// opencode/pi stubs). The usage.Service converts it into its own UsageEvent.
type UsageEventStub struct {
	DedupKey                 string
	Model                    string
	Provider                 string // raw provider as recorded by omp (e.g. "amagi-glm", "anthropic")
	ProjectDir               string
	SessionID                string
	RawMessageID             string // omp entry id (8-hex)
	InputTokens              int
	OutputTokens             int
	CacheReadInputTokens     int
	CacheCreationInputTokens int
	OccurredAt               time.Time

	// CostProvided is true when omp reported a non-zero aggregate cost; the usage
	// service then uses NativeCost as the authoritative total.
	CostProvided bool
	NativeCost   int64  // omp usage.cost.total × 1e6 (micro-native-currency)
	CurrencyCode string // "USD" when CostProvided (omp native cost is USD); empty otherwise
}

// ompLine is the minimal projection of a single JSONL line we need to dispatch on.
//
// Message is decoded lazily. Usage carries the ENTRY-LEVEL usage present on
// compaction / branch_summary entries (assistant/toolResult usage lives inside
// Message instead).
type ompLine struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`        // entry id (8-hex) on non-header entries
	Timestamp string          `json:"timestamp"` // entry-level ISO timestamp
	Message   json.RawMessage `json:"message"`
	Usage     *ompUsage       `json:"usage,omitempty"` // entry-level (compaction/branch_summary)
}

// ompHeader is the SessionHeader (first line, type=="session").
type ompHeader struct {
	ID            string `json:"id"`            // session uuid
	Cwd           string `json:"cwd"`           // working directory (authoritative project dir)
	Name          string `json:"name"`          // optional session display name
	ParentSession string `json:"parentSession"` // fork/clone source path (absolute)
}

// ompMessage is the nested message on type=="message" entries.
//
// Provider/Model are only meaningful for assistant turns; toolResult carries
// neither (its nested usage is LLM work done inside the tool).
type ompMessage struct {
	Role      string   `json:"role"`
	Provider  string   `json:"provider"`
	Model     string   `json:"model"`
	Usage     *ompUsage `json:"usage,omitempty"`
	Timestamp int64    `json:"timestamp"` // message-level timestamp, Unix ms
}

// ompUsage mirrors omp's Usage type (identical shape to pi-ai's Usage).
type ompUsage struct {
	Input       int    `json:"input"`
	Output      int    `json:"output"`
	CacheRead   int    `json:"cacheRead"`
	CacheWrite  int    `json:"cacheWrite"`
	TotalTokens int    `json:"totalTokens"`
	Cost        ompCost `json:"cost"`
}

// ompCost is the per-message cost breakdown omp attaches to every usage.
type ompCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Total      float64 `json:"total"`
}

// ExtractUsageRecords parses an omp session JSONL file and extracts one
// UsageEventStub per billable entry (resumable via byte offset).
//
// Parsing rules（与 pi 解析器完全同构）:
//   - the type=="session" header (always pre-read from offset 0) supplies cwd
//     (project dir), session id and parentSession; this context is available to
//     every record even on incremental resumes
//   - read from startOffset (0 = full scan); resume lands on a line boundary
//   - per line JSON-decode; lines that fail to decode are skipped (schema drift)
//   - the committed offset only advances past a confirmed newline terminator;
//     an incomplete trailing line is left for the next sync
//   - billable entries: assistant turns, toolResult with nested usage, compaction
//     with entry usage, branch_summary with entry usage
//   - OccurredAt prefers the message/entry timestamp; falls back to file mtime
//
// Returns:
//   - records: one stub per billable entry
//   - lastOffset: byte offset of the last fully-consumed newline (resume point)
//   - err: file-level IO error only; per-line decode errors are skipped
func ExtractUsageRecords(jsonlPath string, startOffset int64) (records []UsageEventStub, lastOffset int64, err error) {
	// Header context is read once up front so incremental resumes (startOffset>0)
	// still see the session id / cwd that live on the first line. The header is
	// immutable, so this is always authoritative.
	headerCwd, headerSessionID := readOmpHeader(jsonlPath)

	projectDir := headerCwd
	if projectDir == "" {
		projectDir = inferProjectDirFromPath(jsonlPath)
	}
	sessID := headerSessionID
	if sessID == "" {
		sessID = inferSessionIDFromPath(jsonlPath)
	}

	f, openErr := os.Open(jsonlPath)
	if openErr != nil {
		return nil, startOffset, fmt.Errorf("open omp jsonl %q: %w", jsonlPath, openErr)
	}
	defer f.Close()

	if startOffset > 0 {
		if _, seekErr := f.Seek(startOffset, 0); seekErr != nil {
			return nil, startOffset, fmt.Errorf("seek omp jsonl %q to %d: %w", jsonlPath, startOffset, seekErr)
		}
	}

	// bufio.Reader.ReadString grows to accommodate long lines (base64 tool
	// results), so no explicit cap is needed. The initial buffer only affects
	// allocation chunking.
	reader := bufio.NewReaderSize(f, 64*1024)
	committed := startOffset

	for {
		line, readErr := reader.ReadString('\n')
		if len(line) > 0 {
			// Only a newline-terminated line is "committed": a partial trailing
			// line (no '\n' yet, e.g. mid-write) is left for the next sync so a
			// later-completed line is never skipped.
			if strings.HasSuffix(line, "\n") {
				committed += int64(len(line))
				if rec, ok := parseOmpLine(line, projectDir, sessID, jsonlPath); ok {
					records = append(records, rec)
				}
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return records, committed, fmt.Errorf("scan omp jsonl %q: %w", jsonlPath, readErr)
			}
			break
		}
	}

	return records, committed, nil
}

// parseOmpLine decodes one newline-terminated line and, if it carries billable
// usage, returns the corresponding UsageEventStub. Header and non-usage lines
// return ok=false.
func parseOmpLine(line, projectDir, sessID, jsonlPath string) (UsageEventStub, bool) {
	trimmed := strings.TrimRight(line, "\r\n")
	if len(trimmed) == 0 {
		return UsageEventStub{}, false
	}

	var pl ompLine
	if jsonErr := json.Unmarshal([]byte(trimmed), &pl); jsonErr != nil {
		return UsageEventStub{}, false // tolerate schema drift / externally polluted lines
	}

	// The header (type=="session") is pre-read for context; skip it here.
	if pl.Type == "session" || pl.ID == "" {
		return UsageEventStub{}, false
	}

	switch pl.Type {
	case "message":
		if len(pl.Message) == 0 {
			return UsageEventStub{}, false
		}
		var msg ompMessage
		if jsonErr := json.Unmarshal(pl.Message, &msg); jsonErr != nil {
			return UsageEventStub{}, false
		}
		switch msg.Role {
		case "assistant":
			// A real model turn: count it even with zero usage (matches pi
			// behaviour — the entry happened, it just may be unmetered).
			usage := ompUsage{}
			if msg.Usage != nil {
				usage = *msg.Usage
			}
			return buildRecord(pl.ID, projectDir, sessID, msg.Model, msg.Provider,
				usage, messageOccurredAt(msg.Timestamp, pl.Timestamp, jsonlPath)), true
		case "toolResult":
			// Nested LLM work performed inside a tool. toolResult carries no
			// provider/model, so attribution is "unknown" while the authoritative
			// cost (USD) is still captured.
			if msg.Usage == nil || !usageHasData(*msg.Usage) {
				return UsageEventStub{}, false
			}
			return buildRecord(pl.ID, projectDir, sessID, "", "",
				*msg.Usage, entryOccurredAt(pl.Timestamp, jsonlPath)), true
		default:
			return UsageEventStub{}, false
		}
	case "compaction", "branch_summary":
		// Entry-level usage from generating the compaction/branch summary.
		if pl.Usage == nil || !usageHasData(*pl.Usage) {
			return UsageEventStub{}, false
		}
		return buildRecord(pl.ID, projectDir, sessID, "", "",
			*pl.Usage, entryOccurredAt(pl.Timestamp, jsonlPath)), true
	default:
		return UsageEventStub{}, false
	}
}

// buildRecord assembles a UsageEventStub from a parsed usage object.
//
// DedupKey is a content fingerprint (see package doc "Fork/clone dedup"):
// identical copies dedup regardless of ancestor-file availability; entries
// sharing an 8-hex id but differing in time/usage keep distinct keys.
func buildRecord(entryID, projectDir, sessID, model, provider string, usage ompUsage, occurredAt time.Time) UsageEventStub {
	total := usage.Cost.Total
	nativeCost := int64(total * 1_000_000)
	if nativeCost < 0 {
		nativeCost = 0
	}
	costProvided := total > 0
	currency := ""
	if costProvided {
		// omp's usage.cost.total is denominated in USD (stats formatter `$`).
		currency = "USD"
	}
	return UsageEventStub{
		DedupKey: dedupPrefixOmp + hash16(entryID, occurredAt.UnixMilli(), model, provider,
			usage.Input, usage.Output, usage.CacheRead, usage.CacheWrite),
		Model:                    model,
		Provider:                 provider,
		ProjectDir:               projectDir,
		SessionID:                sessID,
		RawMessageID:             entryID,
		InputTokens:              usage.Input,
		OutputTokens:             usage.Output,
		CacheReadInputTokens:     usage.CacheRead,
		CacheCreationInputTokens: usage.CacheWrite,
		OccurredAt:               occurredAt,
		CostProvided:             costProvided,
		NativeCost:               nativeCost,
		CurrencyCode:             currency,
	}
}

// usageHasData reports whether a usage object carries any billable signal.
// Used to skip zero-only nested usages (e.g. a toolResult without inner LLM work).
func usageHasData(u ompUsage) bool {
	return u.Input > 0 || u.Output > 0 || u.CacheRead > 0 || u.CacheWrite > 0 ||
		u.TotalTokens > 0 || u.Cost.Total > 0
}

// messageOccurredAt resolves a message-level timestamp (Unix ms) first, falling
// back to the entry ISO timestamp, then to the file mtime.
func messageOccurredAt(msgMS int64, entryISO, jsonlPath string) time.Time {
	if msgMS > 0 {
		return time.UnixMilli(msgMS).UTC()
	}
	return entryOccurredAt(entryISO, jsonlPath)
}

// entryOccurredAt resolves an entry-level ISO timestamp, falling back to file mtime.
func entryOccurredAt(entryISO, jsonlPath string) time.Time {
	if entryISO != "" {
		if t, parseErr := time.Parse(time.RFC3339Nano, entryISO); parseErr == nil {
			return t.UTC()
		}
	}
	if info, statErr := os.Stat(jsonlPath); statErr == nil {
		return info.ModTime().UTC()
	}
	return time.Now().UTC()
}

// readOmpHeader reads only the first (header) line of a session file and returns
// its cwd and id. Any IO/parse failure yields zero values; the caller falls
// back to path-based inference.
func readOmpHeader(jsonlPath string) (cwd, id string) {
	f, err := os.Open(jsonlPath)
	if err != nil {
		return
	}
	defer f.Close()
	br := bufio.NewReader(f)
	line, _ := br.ReadString('\n')
	var hdr ompHeader
	// Tolerate a missing trailing newline on a header-only file.
	if json.Unmarshal([]byte(strings.TrimRight(line, "\r\n")), &hdr) == nil {
		cwd, id = hdr.Cwd, hdr.ID
	}
	return
}

// inferSessionIDFromPath derives a session identifier from the file name.
//
// File names look like "<timestamp>_<uuid>.jsonl"; the full basename (minus the
// suffix) is unique enough to identify a session when the header is missing.
func inferSessionIDFromPath(jsonlPath string) string {
	return strings.TrimSuffix(filepath.Base(jsonlPath), ".jsonl")
}

// inferProjectDirFromPath returns the encoded project directory from an omp
// session path (.../sessions/<dir-encoded>/file.jsonl). omp encodes the
// directory home-relative (paths inside the user's home are stored relative to
// it), so the value is kept verbatim — decoding is ambiguous; it is only a
// last-resort fallback when the session header lacks a cwd.
func inferProjectDirFromPath(jsonlPath string) string {
	return filepath.Base(filepath.Dir(jsonlPath))
}

// hash16 returns the first 16 hex chars of the SHA1 of the length-prefixed
// concatenated inputs (64-bit collision-resistant key). Length-prefixing kills
// the boundary ambiguity of naive concatenation ("ab"+"c" == "a"+"bc"), which
// matters here because the dedup fingerprint mixes free-form strings
// (model/provider names) with numbers.
func hash16(parts ...any) string {
	h := sha1.New()
	for _, p := range parts {
		s := fmt.Sprintf("%v", p)
		_, _ = fmt.Fprintf(h, "%d:%s;", len(s), s)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}
