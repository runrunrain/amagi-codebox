// Package pi provides a read-only parser for Pi coding-agent session JSONL files.
//
// Pi stores each session as JSONL under
//
//	<PI_CODING_AGENT_DIR>/sessions/--<cwd-slashes-as-dashes>--/<timestamp>_<uuid>.jsonl
//
// (default config dir ~/.pi/agent; see pi docs/session-format.md). amagi-codebox
// now uses that same default directory. Historical CodeBox releases used an
// isolated <configDir>/pi-runtime root; usage synchronization may still read
// those old session files, whose on-disk layout is identical.
//
// Each line is a JSON object carrying a "type" field. The first line is a
// SessionHeader; billable usage lives on four entry shapes (see
// docs/session-format.md — all are "included in session token and cost totals"):
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
// Pi's createBranchedSession/forkFrom (session-manager.js) write a NEW file whose
// header carries "parentSession": "<abs path of source>" and COPY every non-header
// entry verbatim — same 8-hex entry ids. A naive (filePath, entryID) dedup key
// would bill the copied history twice; a key derived from the parentSession
// lineage chain breaks as soon as an ancestor file is deleted/moved (the chain
// is then unresolvable and the fallback changes the key — re-billing copies).
// We therefore key every record on a content fingerprint:
// (entryID, occurredAt, model, provider, token counts). Copied entries are
// byte-identical regardless of which file they live in or whether ancestors
// still exist, so they dedup together; distinct entries — including two
// branches in one file, or unrelated sessions with a coincidentally-equal
// 8-hex id — differ in timestamp/usage and are all counted.
// Residual edge: an entry with neither message- nor entry-level timestamp
// falls back to file mtime, which differs across copies; such entries are
// rare (pi always writes timestamps) and the worst case is one extra count.
//
// # Currency
//
// Pi computes usage.cost.total in USD (see @earendil-works/pi-ai README). When a
// record carries a non-zero native cost we tag it CostProvided with
// CurrencyCode="USD" so the usage service never re-infers a domestic currency.
// Records without native cost stay CostProvided=false and let the caller estimate
// from its own (provider-aware) price table.
package pi

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

// dedupPrefixPi is the dedup_key prefix for pi session-log records.
// Defined locally to avoid a reverse dependency on the usage package; its value
// must match usage.dedupPrefixPi.
const dedupPrefixPi = "pi:"

// UsageEventStub is the appmeta-level neutral event (mirrors the claude/codex/
// opencode stubs). The usage.Service converts it into its own UsageEvent.
type UsageEventStub struct {
	DedupKey                 string
	Model                    string
	Provider                 string // raw provider as recorded by pi (e.g. "amagi-glm", "anthropic")
	ProjectDir               string
	SessionID                string
	RawMessageID             string // pi entry id (8-hex)
	InputTokens              int
	OutputTokens             int
	CacheReadInputTokens     int
	CacheCreationInputTokens int
	OccurredAt               time.Time

	// CostProvided is true when pi reported a non-zero aggregate cost; the usage
	// service then uses NativeCost as the authoritative total.
	CostProvided bool
	NativeCost   int64  // pi usage.cost.total × 1e6 (micro-native-currency)
	CurrencyCode string // "USD" when CostProvided (pi native cost is USD); empty otherwise
}

// piLine is the minimal projection of a single JSONL line we need to dispatch on.
//
// Message is decoded lazily. Usage carries the ENTRY-LEVEL usage present on
// compaction / branch_summary entries (assistant/toolResult usage lives inside
// Message instead).
type piLine struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`        // entry id (8-hex) on non-header entries
	Timestamp string          `json:"timestamp"` // entry-level ISO timestamp
	Message   json.RawMessage `json:"message"`
	Usage     *piUsage        `json:"usage,omitempty"` // entry-level (compaction/branch_summary)
}

// piHeader is the SessionHeader (first line, type=="session").
type piHeader struct {
	ID            string `json:"id"`            // session uuid
	Cwd           string `json:"cwd"`           // working directory (authoritative project dir)
	Name          string `json:"name"`          // optional session display name
	ParentSession string `json:"parentSession"` // fork/clone source path (absolute)
}

// piMessage is the nested message on type=="message" entries.
//
// Provider/Model are only meaningful for assistant turns; toolResult carries
// neither (its nested usage is LLM work done inside the tool).
type piMessage struct {
	Role      string   `json:"role"`
	Provider  string   `json:"provider"`
	Model     string   `json:"model"`
	Usage     *piUsage `json:"usage,omitempty"`
	Timestamp int64    `json:"timestamp"` // message-level timestamp, Unix ms
}

// piUsage mirrors pi-ai's Usage type (see pi docs/session-format.md).
type piUsage struct {
	Input       int    `json:"input"`
	Output      int    `json:"output"`
	CacheRead   int    `json:"cacheRead"`
	CacheWrite  int    `json:"cacheWrite"`
	TotalTokens int    `json:"totalTokens"`
	Cost        piCost `json:"cost"`
}

// piCost is the per-message cost breakdown pi attaches to every usage.
type piCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Total      float64 `json:"total"`
}

// lineageRootCache memoises the lineageRoot of a session file (keyed by its
// canonical path) across sync runs. A session header is immutable once written,
// so the cached root stays valid for the lifetime of the process; sync workers
// ExtractUsageRecords parses a pi session JSONL file and extracts one
// UsageEventStub per billable entry (resumable via byte offset).
//
// Parsing rules:
//   - the type=="session" header (always pre-read from offset 0) supplies cwd
//     (project dir), session id and parentSession; this context is available to
//     every record even on incremental resumes (P1-3)
//   - read from startOffset (0 = full scan); resume lands on a line boundary
//   - per line JSON-decode; lines that fail to decode are skipped (schema drift)
//   - the committed offset only advances past a confirmed newline terminator;
//     an incomplete trailing line is left for the next sync (P2-5)
//   - billable entries: assistant turns, toolResult with nested usage, compaction
//     with entry usage, branch_summary with entry usage (P1-5)
//   - OccurredAt prefers the message/entry timestamp; falls back to file mtime
//
// Returns:
//   - records: one stub per billable entry
//   - lastOffset: byte offset of the last fully-consumed newline (resume point)
//   - err: file-level IO error only; per-line decode errors are skipped
func ExtractUsageRecords(jsonlPath string, startOffset int64) (records []UsageEventStub, lastOffset int64, err error) {
	// Header context is read once up front so incremental resumes (startOffset>0)
	// still see the session id / cwd that live on the first line (P1-3).
	// The header is immutable, so this is always authoritative.
	headerCwd, headerSessionID := readPiHeader(jsonlPath)

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
		return nil, startOffset, fmt.Errorf("open pi jsonl %q: %w", jsonlPath, openErr)
	}
	defer f.Close()

	if startOffset > 0 {
		if _, seekErr := f.Seek(startOffset, 0); seekErr != nil {
			return nil, startOffset, fmt.Errorf("seek pi jsonl %q to %d: %w", jsonlPath, startOffset, seekErr)
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
			// later-completed line is never skipped (P2-5).
			if strings.HasSuffix(line, "\n") {
				committed += int64(len(line))
				if rec, ok := parsePiLine(line, projectDir, sessID, jsonlPath); ok {
					records = append(records, rec)
				}
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return records, committed, fmt.Errorf("scan pi jsonl %q: %w", jsonlPath, readErr)
			}
			break
		}
	}

	return records, committed, nil
}

// parsePiLine decodes one newline-terminated line and, if it carries billable
// usage, returns the corresponding UsageEventStub. Header and non-usage lines
// return ok=false.
func parsePiLine(line, projectDir, sessID, jsonlPath string) (UsageEventStub, bool) {
	trimmed := strings.TrimRight(line, "\r\n")
	if len(trimmed) == 0 {
		return UsageEventStub{}, false
	}

	var pl piLine
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
		var msg piMessage
		if jsonErr := json.Unmarshal(pl.Message, &msg); jsonErr != nil {
			return UsageEventStub{}, false
		}
		switch msg.Role {
		case "assistant":
			// A real model turn: count it even with zero usage (matches prior
			// behaviour — the entry happened, it just may be unmetered).
			usage := piUsage{}
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
func buildRecord(entryID, projectDir, sessID, model, provider string, usage piUsage, occurredAt time.Time) UsageEventStub {
	total := usage.Cost.Total
	nativeCost := int64(total * 1_000_000)
	if nativeCost < 0 {
		nativeCost = 0
	}
	costProvided := total > 0
	currency := ""
	if costProvided {
		// Pi's usage.cost.total is denominated in USD (see pi-ai README).
		currency = "USD"
	}
	return UsageEventStub{
		DedupKey: dedupPrefixPi + hash16(entryID, occurredAt.UnixMilli(), model, provider,
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
func usageHasData(u piUsage) bool {
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

// readPiHeader reads only the first (header) line of a session file and returns
// its cwd and id. Any IO/parse failure yields zero values; the caller falls
// back to path-based inference.
func readPiHeader(jsonlPath string) (cwd, id string) {
	f, err := os.Open(jsonlPath)
	if err != nil {
		return
	}
	defer f.Close()
	br := bufio.NewReader(f)
	line, _ := br.ReadString('\n')
	var hdr piHeader
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

// inferProjectDirFromPath returns the encoded project directory from a pi session
// path (.../sessions/--<cwd>--/file.jsonl). The value is kept verbatim because
// decoding slashes-from-dashes is ambiguous; it is only a last-resort fallback
// when the session header lacks a cwd.
func inferProjectDirFromPath(jsonlPath string) string {
	return filepath.Base(filepath.Dir(jsonlPath))
}

// hash16 returns the first 16 hex chars of the SHA1 of the length-prefixed
// concatenated inputs (64-bit collision-resistant key). Length-prefixing kills
// the boundary ambiguity of naive concatenation ("ab"+"c" == "a"+"bc"), which
// matters here because the dedup fingerprint mixes free-form strings
// (model/provider names) with numbers (R3 复审 Minor-1)。
func hash16(parts ...any) string {
	h := sha1.New()
	for _, p := range parts {
		s := fmt.Sprintf("%v", p)
		fmt.Fprintf(h, "%d:%s;", len(s), s)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}
