// Package codex 提供解析 Codex CLI 会话 jsonl 的工具。
//
// Codex（OpenAI 官方 CLI）的 rollout jsonl 格式（实测主上机器 0.144.5）：
//
//	~/.codex/sessions/YYYY/MM/DD/rollout-<timestamp>-<uuid>.jsonl
//
// 每行一个 JSON 对象，常见记录形态（与设计文档原假设不同，已在实测中确认）：
//
//	{"timestamp":"2026-07-16T10:27:44.327Z","type":"session_meta","payload":{
//	    "session_id":"...","id":"...","cwd":"...","cli_version":"0.144.5",
//	    "model_provider":"openai","model":null, ...}}
//	{"timestamp":"...","type":"turn_context","payload":{
//	    "turn_id":"...","cwd":"...","model":"gpt-5.6-sol", ...}}
//	{"timestamp":"...","type":"event_msg","payload":{
//	    "type":"token_count","info":{
//	      "total_token_usage":{"input_tokens":N,"cached_input_tokens":N,
//	        "output_tokens":N,"reasoning_output_tokens":N,"total_tokens":N},
//	      "last_token_usage":{...同结构，每 turn 增量...}}}}
//
// 关键事实（设计 17.1 / 17.2 实测结论）：
//   - usage 字段位置：payload.info.last_token_usage（不是根级 usage）
//   - 字段名：cached_input_tokens（不是 cache_read_input_tokens）
//   - 时间戳：根级 timestamp（ISO8601）
//   - model：turn_context.payload.model（session_meta.payload.model 通常为 null）
//   - model_provider：session_meta.payload.model_provider
//
// cache 语义：input_tokens **包含** cached_input_tokens，调用方按 codex 做扣减。
// reasoning_output_tokens 第一期归入 output_tokens（推理输出）。
package codex

import (
	"bufio"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// UsageEventStub 是 codex 包产出的中立事件结构（与 claude/opencode 子包字段一致，
// 但定义在各自包内避免反向依赖 usage 包）。
type UsageEventStub struct {
	DedupKey                  string
	Model                     string
	Provider                  string
	ProjectDir                string
	SessionID                 string
	RawMessageID              string
	InputTokens               int
	OutputTokens              int
	CacheReadInputTokens      int
	CacheCreationInputTokens  int
	OccurredAt                time.Time
}

// codexRecord 是 jsonl 单行根级结构（用 json.RawMessage 延迟解码 payload）。
type codexRecord struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

// sessionMetaPayload 对应 type=="session_meta" 的 payload。
type sessionMetaPayload struct {
	SessionID     string `json:"session_id"`
	ID            string `json:"id"`
	Cwd           string `json:"cwd"`
	ModelProvider string `json:"model_provider"`
	Model         string `json:"model"` // 通常为 null/空
	CliVersion    string `json:"cli_version"`
}

// turnContextPayload 对应 type=="turn_context" 的 payload。
type turnContextPayload struct {
	TurnID string `json:"turn_id"`
	Cwd    string `json:"cwd"`
	Model  string `json:"model"`
}

// eventMsgPayload 对应 type=="event_msg" 的 payload（只关心 token_count 子类型）。
type eventMsgPayload struct {
	Type string          `json:"type"`
	Info tokenCountInfo  `json:"info"`
}

// tokenCountInfo 包含 total 与 last（增量）。
//
// 实测发现 total_token_usage 是累计，last_token_usage 是当前 turn 增量；
// 第一期使用 last_token_usage 避免重复计费。
type tokenCountInfo struct {
	LastTokenUsage tokenUsage `json:"last_token_usage"`
}

// tokenUsage 是 Codex 单 turn 的 token 统计。
//
// 字段名（实测，与设计假设不同）：
//   - input_tokens（含 cached_input_tokens）
//   - cached_input_tokens（缓存读）
//   - output_tokens
//   - reasoning_output_tokens（推理输出，第一期归 output）
type tokenUsage struct {
	InputTokens            int `json:"input_tokens"`
	CachedInputTokens      int `json:"cached_input_tokens"`
	OutputTokens           int `json:"output_tokens"`
	ReasoningOutputTokens  int `json:"reasoning_output_tokens"`
	TotalTokens            int `json:"total_tokens"`
}

// ExtractUsageRecords 解析单个 Codex rollout jsonl 文件（断点续传）。
//
// 入参：
//   - jsonlPath：文件路径（...rollout-<ts>-<uuid>.jsonl）
//   - startOffset：上次读取到的字节偏移（落在行边界），0 表示全量
//
// 返回值：
//   - records：每个 token_count event 一条 UsageEventStub（last_token_usage 增量）
//   - lastOffset：本次已读到的字节偏移（含换行符）
//   - provider：从 session_meta 提取的 model_provider（全文件共享，传递给调用方）
//   - err：文件级 IO 错误；单行解析失败 continue 不中断
func ExtractUsageRecords(jsonlPath string, startOffset int64) (records []UsageEventStub, lastOffset int64, provider string, err error) {
	f, openErr := os.Open(jsonlPath)
	if openErr != nil {
		return nil, 0, "", fmt.Errorf("open codex jsonl %q: %w", jsonlPath, openErr)
	}
	defer f.Close()

	if startOffset > 0 {
		if _, seekErr := f.Seek(startOffset, 0); seekErr != nil {
			return nil, startOffset, "", fmt.Errorf("seek codex jsonl %q to %d: %w", jsonlPath, startOffset, seekErr)
		}
	}

	// SessionID 与 ProjectDir 兜底（从文件路径与首行 session_meta 推断）
	fileSessionID := inferSessionIDFromPath(jsonlPath)
	var (
		sessionMetaCwd     string
		sessionMetaModel   string
		turnCtxModel       string
		sessionMetaID      string
		fileProjectDir     string
	)

	scanner := bufio.NewScanner(f)
	// 实测：含大 tool_result 的 codex rollout 单行可达数 MB；扩到 16MiB 防 token too long。
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	currentOffset := startOffset
	for scanner.Scan() {
		lineBytes := scanner.Bytes()
		currentOffset += int64(len(lineBytes)) + 1 // +1 for \n
		if len(lineBytes) == 0 {
			continue
		}

		var rec codexRecord
		if jsonErr := json.Unmarshal(lineBytes, &rec); jsonErr != nil {
			continue
		}

		// 首次遇到的 session_meta / turn_context 记录提取元信息（全文件共享）
		if rec.Type == "session_meta" && len(rec.Payload) > 0 {
			var sm sessionMetaPayload
			if jsonErr := json.Unmarshal(rec.Payload, &sm); jsonErr == nil {
				if sessionMetaCwd == "" {
					sessionMetaCwd = sm.Cwd
					fileProjectDir = sm.Cwd
				}
				if provider == "" {
					provider = sm.ModelProvider
				}
				sessionMetaModel = sm.Model
				if sm.ID != "" {
					sessionMetaID = sm.ID
				}
			}
			continue
		}
		if rec.Type == "turn_context" && len(rec.Payload) > 0 {
			var tc turnContextPayload
			if jsonErr := json.Unmarshal(rec.Payload, &tc); jsonErr == nil {
				if turnCtxModel == "" && tc.Model != "" {
					turnCtxModel = tc.Model
				}
				if fileProjectDir == "" && tc.Cwd != "" {
					fileProjectDir = tc.Cwd
				}
			}
			continue
		}

		if rec.Type != "event_msg" {
			continue
		}
		var em eventMsgPayload
		if jsonErr := json.Unmarshal(rec.Payload, &em); jsonErr != nil {
			continue
		}
		if em.Type != "token_count" {
			continue
		}

		// 模型选择：优先 turn_context.model，其次 session_meta.model（通常 null）
		model := turnCtxModel
		if model == "" {
			model = sessionMetaModel
		}

		// 解析时间戳（ISO8601）
		var occurredAt time.Time
		if rec.Timestamp != "" {
			if t, parseErr := time.Parse(time.RFC3339Nano, rec.Timestamp); parseErr == nil {
				occurredAt = t.UTC()
			}
		}
		if occurredAt.IsZero() {
			if info, statErr := os.Stat(jsonlPath); statErr == nil {
				occurredAt = info.ModTime().UTC()
			} else {
				occurredAt = time.Now().UTC()
			}
		}

		// 四维 token（reasoning 归 output）
		in := em.Info.LastTokenUsage.InputTokens
		cr := em.Info.LastTokenUsage.CachedInputTokens
		out := em.Info.LastTokenUsage.OutputTokens + em.Info.LastTokenUsage.ReasoningOutputTokens

		// dedup_key: "cx:" + sha1(model|in|out|cr|cc|timestamp)[:16]
		// 含 timestamp 让同文件多 turn 不冲突；含四维 + model 避免不同会话碰撞。
		dedup := "cx:" + hash16(model, in, out, cr, 0, rec.Timestamp)

		sessID := fileSessionID
		if sessionMetaID != "" {
			sessID = sessionMetaID
		}

		records = append(records, UsageEventStub{
			DedupKey:                 dedup,
			Model:                    model,
			Provider:                 provider,
			ProjectDir:               fileProjectDir,
			SessionID:                sessID,
			RawMessageID:             "",
			InputTokens:              in,
			OutputTokens:             out,
			CacheReadInputTokens:     cr,
			CacheCreationInputTokens: 0, // codex 不提供 cache_creation
			OccurredAt:               occurredAt,
		})
	}

	if scanErr := scanner.Err(); scanErr != nil {
		return records, currentOffset, provider, fmt.Errorf("scan codex jsonl %q: %w", jsonlPath, scanErr)
	}

	_ = sessionMetaCwd // 保留供调试
	return records, currentOffset, provider, nil
}

// inferSessionIDFromPath 从 rollout-<ts>-<uuid>.jsonl 提取 session 标识。
//
// 文件名例：rollout-2026-07-16T18-27-44-019f6a77-ce0a-76d0-8853-39bacabb5d00.jsonl
// 取整个 basename 去 .jsonl 后缀作为 sessionID（足够唯一标识）。
func inferSessionIDFromPath(jsonlPath string) string {
	base := filepath.Base(jsonlPath)
	return strings.TrimSuffix(base, ".jsonl")
}

// hash16 计算输入字段拼接后的 SHA1，返回前 16 个 hex 字符。
func hash16(parts ...any) string {
	h := sha1.New()
	fmt.Fprint(h, parts...)
	return hex.EncodeToString(h.Sum(nil))[:16]
}
