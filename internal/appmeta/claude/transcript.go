// transcript.go 提供 Claude Code 会话转录（jsonl）的增量解析器与转录根解析助手。
//
// 契约声明：~/.claude/projects/ 下的会话转录 jsonl 是 Claude Code 的「落盘事实」，
// 不是官方冻结契约（设计 §1/§13-R1）。本文件全部块级字段名（content[].type /
// thinking / text / id / name / input / tool_use_id / is_error）均以本机真实样本
// 回放为依据——见 testdata/transcript.sample.jsonl（Claude Code 2.1.319 实测采集
// 脱敏固化：-p 模式两轮 + winpty 交互式一轮；thinking/tool_use/tool_result/
// usage 四分项/origin.kind 全部来自实测，非手写想象）。
//
// 行级容错理由：转录 schema 随 Claude Code 版本演进（实测 2.1.319 已出现
// -p/sdk 模式 user 行缺失 origin 字段、queue-operation/attachment/api-request 等
// 十余种非消息行类型），尾随读取必须做到「单行解析失败跳过、未知行类型忽略、
// offset 照常推进」——坏一行不能中断整个 Web 平面的数据流（设计 §5.1）。
package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TranscriptRecord 是单条转录行的中立投影（wire 形状见设计 §6.3）。
//
// 只投影三种 Kind：user / assistant / system（实测其余行类型——attachment、
// queue-operation、api-request*、last-prompt、tools-used、cost-state、mode、
// permission-mode、atis-latch、file-history-snapshot——均为 Claude Code 内部
// 记账行，不产出记录、offset 照常推进）。
type TranscriptRecord struct {
	// Kind 取根级 type："user" | "assistant" | "system"。
	Kind string
	// UUID 取根级 uuid（行级唯一；注意同一 API message 的 thinking/tool_use 块
	// 会拆成多行、共享 message.id 但各有不同 uuid——实测形态）。
	UUID string
	// IsHuman 仅对 user 行有意义：origin.kind=="human"（交互式输入）时为 true。
	// 实测注：2.1.319 的 -p/sdk 模式 user 行没有 origin 字段（取而代之
	// promptSource:"sdk"），此时 IsHuman=false；origin.kind=="tool" 的工具回写行亦为 false。
	IsHuman bool
	// Text 文本内容：message.content 为 string 时直接取值；为块数组时拼接全部
	// type=="text" 块（多块以 "\n" 连接）。
	Text string
	// Thinking 拼接 assistant 行 content 数组中全部 type=="thinking" 块的
	// thinking 字段（多块以 "\n" 连接）。
	Thinking string
	// ToolUses 取 assistant 行 content 数组中 type=="tool_use" 块（输入原样保留）。
	ToolUses []ToolUse
	// ToolResults 取 user 行 content 数组中 type=="tool_result" 块（前端按 ID
	// 与 ToolUses 配对成工具卡，服务端不做配对——设计 §5.1/§6.3）。
	ToolResults []ToolResult
	// Usage 取 assistant 行 message.usage 四分项；无 usage 的行为 nil。
	Usage *Usage
	// Model 取 assistant 行 message.model（合成错误行实测为 "<synthetic>"）。
	Model string
	// Timestamp 取根级 timestamp 原样 ISO 串（如 "2026-11-13T21:12:17.110Z"），
	// 前端负责格式化。
	Timestamp string
}

// ToolUse 是 assistant 行 content 数组中 type=="tool_use" 块的投影。
type ToolUse struct {
	ID    string
	Name  string
	Input json.RawMessage // 工具入参原样 JSON（如 {"file_path":"..."}），不解释
}

// ToolResult 是 user 行 content 数组中 type=="tool_result" 块的投影。
type ToolResult struct {
	ID      string // 对应 tool_use 块的 id（json 字段名 tool_use_id）
	IsError bool   // json 字段名 is_error；实测成功结果无该字段（零值 false）
	Content string
}

// Usage 对应 Anthropic message.usage 的四分项 token 计数。
// 字段名（json tag）对齐 jsonl.go 的 usageMessageUse（input_tokens 等）。
type Usage struct {
	In         int `json:"input_tokens"`
	Out        int `json:"output_tokens"`
	CacheRead  int `json:"cache_read_input_tokens"`
	CacheWrite int `json:"cache_creation_input_tokens"`
}

// transcriptRoot 是转录单行根级的粗解析结构（只取关心字段，其余忽略）。
// message/origin 用 json.RawMessage 延迟解码，便于二次判型与坏行跳过
// （沿用 jsonl.go jsonlRecord 的惯例）。
type transcriptRoot struct {
	Type      string          `json:"type"`
	UUID      string          `json:"uuid"`
	Timestamp string          `json:"timestamp"`
	Message   json.RawMessage `json:"message"`
	Origin    json.RawMessage `json:"origin"`
}

// transcriptMessage 是 transcriptRoot.Message 的二次解析结构。
// Content 用 RawMessage 延迟解码：string（普通文本）与块数组（结构化内容）
// 两种形态都要兼容（沿用 jsonl.go messageStruct 的判型惯例）。
type transcriptMessage struct {
	Role    string          `json:"role"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
	Usage   *Usage          `json:"usage"`
}

// 下列结构对应 content 数组中各块类型的实测形状（fixture 回放为单测依据）。

type transcriptBlockType struct {
	Type string `json:"type"`
}

type transcriptTextBlock struct {
	Text string `json:"text"`
}

type transcriptThinkingBlock struct {
	Thinking string `json:"thinking"`
}

type transcriptToolUseBlock struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type transcriptToolResultBlock struct {
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

// ReadTranscriptIncrement 从 offset 字节起增量解析转录 jsonl（断点续传）。
//
// 语义（对齐 ExtractUsageRecords 的口径，设计 §5.1）：
//   - 从 offset 字节起按行读取（offset 必落在行边界 \n 之后，即本函数上次返回的
//     newOffset）；单行 unmarshal 失败或行类型未知则跳过该行，offset 照常推进；
//   - newOffset 含行尾 \n 累加（末行无 \n 时也计入其字节长度），文件追加后从
//     newOffset 续读不丢不重；
//   - truncated：文件当前 size < offset（claude /resume 重写、换文件或游标越过
//     EOF）时为 true，此时从 0 重读全量并返回全量记录，调用方据此重建缓冲；
//   - err 仅文件级 IO 错误（如文件不存在，可用 os.IsNotExist 判定）。
//
// 读取用 bufio.Reader.ReadBytes 按需扩容（沿用 ExtractUsageRecords 惯例），
// 可吞下含 base64 图片或超长 tool_result 的单行，不受固定缓冲上限约束。
func ReadTranscriptIncrement(path string, offset int64) (recs []TranscriptRecord, newOffset int64, truncated bool, err error) {
	f, openErr := os.Open(path)
	if openErr != nil {
		return nil, 0, false, fmt.Errorf("open jsonl %q: %w", path, openErr)
	}
	defer f.Close()

	info, statErr := f.Stat()
	if statErr != nil {
		return nil, 0, false, fmt.Errorf("stat jsonl %q: %w", path, statErr)
	}

	startOffset := offset
	if startOffset < 0 {
		startOffset = 0
	}
	if startOffset > info.Size() {
		// 文件比游标短：转录被重写/替换（/resume 或换文件），从 0 重读全量。
		truncated = true
		startOffset = 0
	}
	if startOffset > 0 {
		if _, seekErr := f.Seek(startOffset, 0); seekErr != nil {
			return nil, startOffset, truncated, fmt.Errorf("seek jsonl %q to %d: %w", path, startOffset, seekErr)
		}
	}

	reader := bufio.NewReaderSize(f, 64*1024)

	currentOffset := startOffset
	for {
		rawLine, readErr := reader.ReadBytes('\n')
		if len(rawLine) > 0 {
			// ReadBytes 返回值含分隔符 \n，其长度即本行实际占用字节数；
			// 直接累加保证断点续传 offset 精确（含文件末尾无 \n 的尾行）。
			currentOffset += int64(len(rawLine))
			line := bytes.TrimRight(rawLine, "\r\n")
			if len(line) > 0 {
				if rec, ok := parseTranscriptLine(line); ok {
					recs = append(recs, rec)
				}
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return recs, currentOffset, truncated, fmt.Errorf("scan jsonl %q: %w", path, readErr)
			}
			break
		}
	}

	return recs, currentOffset, truncated, nil
}

// parseTranscriptLine 解析单行转录。返回 ok=false 表示该行不产出记录
// （JSON 损坏、或 user/assistant/system 之外的记账行类型）。
func parseTranscriptLine(line []byte) (rec TranscriptRecord, ok bool) {
	var root transcriptRoot
	if json.Unmarshal(line, &root) != nil {
		// 单行解析失败不中断：兼容 schema 演进与外部污染（jsonl.go 惯例）。
		return TranscriptRecord{}, false
	}
	switch root.Type {
	case "user", "assistant", "system":
	default:
		return TranscriptRecord{}, false
	}

	rec = TranscriptRecord{
		Kind:      root.Type,
		UUID:      root.UUID,
		Timestamp: root.Timestamp,
	}

	// IsHuman 仅看 user 行的 origin.kind=="human"（jsonl.go originStruct 复用）。
	if root.Type == "user" && len(root.Origin) > 0 {
		var origin originStruct
		if json.Unmarshal(root.Origin, &origin) == nil && origin.Kind == "human" {
			rec.IsHuman = true
		}
	}

	if len(root.Message) == 0 {
		return rec, true // system 行等无 message 的形态（实测 stop_hook_summary）
	}
	var msg transcriptMessage
	if json.Unmarshal(root.Message, &msg) != nil {
		// message 存在但形态异常：仍产出记录（Kind/UUID/Timestamp 已有），
		// 内容字段留空——行级容错。
		return rec, true
	}
	rec.Model = msg.Model
	rec.Usage = msg.Usage

	if len(msg.Content) == 0 {
		return rec, true
	}
	// content 二次判型：string（普通文本）vs 块数组（结构化内容）。
	var text string
	if json.Unmarshal(msg.Content, &text) == nil {
		rec.Text = text
		return rec, true
	}
	var blocks []json.RawMessage
	if json.Unmarshal(msg.Content, &blocks) != nil {
		// 既非 string 也非数组（schema 漂移）：留空跳过。
		return rec, true
	}

	var texts, thinkings []string
	for _, block := range blocks {
		var probe transcriptBlockType
		if json.Unmarshal(block, &probe) != nil {
			continue // 块级坏数据跳过，不影响同数组其余块
		}
		switch probe.Type {
		case "text":
			var b transcriptTextBlock
			if json.Unmarshal(block, &b) == nil && b.Text != "" {
				texts = append(texts, b.Text)
			}
		case "thinking":
			var b transcriptThinkingBlock
			if json.Unmarshal(block, &b) == nil && b.Thinking != "" {
				thinkings = append(thinkings, b.Thinking)
			}
		case "tool_use":
			var b transcriptToolUseBlock
			if json.Unmarshal(block, &b) == nil {
				rec.ToolUses = append(rec.ToolUses, ToolUse(b))
			}
		case "tool_result":
			var b transcriptToolResultBlock
			if json.Unmarshal(block, &b) == nil {
				rec.ToolResults = append(rec.ToolResults, ToolResult{
					ID:      b.ToolUseID,
					IsError: b.IsError,
					Content: toolResultContent(b.Content),
				})
			}
		default:
			// 未知块类型（schema 演进，如 redacted_thinking 等）：忽略。
		}
	}
	rec.Text = strings.Join(texts, "\n")
	rec.Thinking = strings.Join(thinkings, "\n")
	return rec, true
}

// toolResultContent 提取 tool_result 块的 content。实测（fixture）为 string；
// Anthropic 消息形状亦允许块数组（[{type:"text",text:...}]），此处做同款判型
// 降级：string 直接取值，数组拼接 text 块，其余形态返回空串。
func toolResultContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []json.RawMessage
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var texts []string
	for _, block := range blocks {
		var b transcriptTextBlock
		var probe transcriptBlockType
		if json.Unmarshal(block, &probe) == nil && probe.Type == "text" && json.Unmarshal(block, &b) == nil && b.Text != "" {
			texts = append(texts, b.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// ClaudeConfigRoot 解析 Claude Code 的转录根目录（设计 §5.1，v1.2 规则）。
//
// 优先级（覆盖用户自定义 HOME/USERPROFILE 与 CLAUDE_CONFIG_DIR 两类重定向，
// 设计 R12 提取链：CLAUDE_CONFIG_DIR 优先 → HOME/USERPROFILE → 缺省 homeDir）：
//  1. env 中 CLAUDE_CONFIG_DIR 非空 → 直接以其值为根（受管 claude-accounts 会话
//     在最终边界剥离该键，自然回落后续分支）；
//  2. env 中 HOME 非空 → <HOME>/.claude；
//  3. env 中 USERPROFILE 非空 → <USERPROFILE>/.claude（Windows）；
//  4. 以上皆缺 → <homeDir>/.claude（homeDir 通常为 os.UserHomeDir()，由调用方
//     传入——保持纯函数便于测试与服务端复用）。
//
// 键查找大小写不敏感（Windows 环境变量语义）；值为空串/纯空白视为未设置。
// env 形如 os.Environ() 的 "KEY=VALUE" 列表（即会话 spec 的进程环境）。
func ClaudeConfigRoot(homeDir string, env []string) string {
	if v := lookupEnvFold(env, "CLAUDE_CONFIG_DIR"); v != "" {
		return v
	}
	if v := lookupEnvFold(env, "HOME"); v != "" {
		return filepath.Join(v, ".claude")
	}
	if v := lookupEnvFold(env, "USERPROFILE"); v != "" {
		return filepath.Join(v, ".claude")
	}
	return filepath.Join(homeDir, ".claude")
}

// lookupEnvFold 在 "KEY=VALUE" 列表中大小写不敏感地查找 key，
// 返回第一个非空值；不存在或全为空时返回 ""。
func lookupEnvFold(env []string, key string) string {
	for _, kv := range env {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		if strings.EqualFold(kv[:eq], key) {
			if v := strings.TrimSpace(kv[eq+1:]); v != "" {
				return kv[eq+1:]
			}
		}
	}
	return ""
}

// SessionJSONLPathInRoot 拼接指定转录根下的会话 jsonl 路径。
//
// 规则：<root>/projects/<encoded-cwd>/<sessionID>.jsonl
// encoded-cwd 统一走 claudeDirName（见 jsonl.go：实测 Claude Code 仅保留 ASCII
// 字母数字，其余字符逐个替换为连字符）。与既有 SessionJSONLPath 的关系：后者
// 等价于 SessionJSONLPathInRoot(<homeDir>/.claude, ...)，签名保持不动（既有
// 调用方零改动）；本函数供转录根可自定义（CLAUDE_CONFIG_DIR / HOME 重定向）的
// 新链路使用（设计 §5.1/§5.2）。
func SessionJSONLPathInRoot(root, workDir, sessionID string) string {
	encoded := claudeDirName(workDir)
	return filepath.Join(root, "projects", encoded, sessionID+".jsonl")
}

// FindLatestActiveJSONLInRoot 在指定转录根下查找 workDir 对应目录里 mtime 最新的
// .jsonl（方案 P 语义，与既有 FindLatestActiveJSONL 同一判定；后者签名保持不动，
// 本函数供转录根可自定义的新链路使用）。
func FindLatestActiveJSONLInRoot(root, workDir string) (path, sessionID string, err error) {
	encoded := claudeDirName(workDir)
	dir := filepath.Join(root, "projects", encoded)

	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		return "", "", fmt.Errorf("read dir %q: %w", dir, readErr)
	}

	var (
		latestPath  string
		latestMTime time.Time
		latestID    string
	)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			// 单个 entry 取不到 mtime 不影响整体扫描（ReadDir 已成功）。
			continue
		}
		if latestPath == "" || info.ModTime().After(latestMTime) {
			latestPath = filepath.Join(dir, name)
			latestMTime = info.ModTime()
			latestID = strings.TrimSuffix(name, ".jsonl")
		}
	}

	if latestPath == "" {
		return "", "", fmt.Errorf("no .jsonl file found in %q", dir)
	}
	return latestPath, latestID, nil
}
