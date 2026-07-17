// Package claude 提供读取 Claude Code 应用元数据（会话 jsonl）的工具。
//
// Claude Code（claude cli）会把每个会话的对话记录以 JSON Lines 格式写入：
//
//	<homeDir>/.claude/projects/<encoded-cwd>/<session-uuid>.jsonl
//
// 其中 encoded-cwd 把路径中的冒号、反斜杠、正斜杠全部替换为连字符，
// 例如 workDir="X:/WorkSpace" → encoded="X--WorkSpace"。
//
// 每行一个 JSON 对象，常见记录形态：
//
//	{"type":"system","subtype":"init","cwd":"...","permissions":{...}}
//	{"type":"user","message":{"role":"user","content":"用户输入"},"origin":{"kind":"human"}}
//	{"type":"assistant","message":{"role":"assistant","content":[...]},...}
//	{"type":"user","message":{"role":"user","content":[{"type":"tool_result",...}]},"origin":{"kind":"tool"}}
//
// 本包的 ExtractFirstUserMessage 只关心首条「真实人类用户输入」的文本，
// 用于在 amagi-codebox 会话侧栏展示会话标题（对标 Claude Code resume 列表）。
package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// pathSepReplacer 把路径分隔符（: / \）统一替换为连字符，匹配 Claude Code 的 encoded-cwd 编码。
//
// 实测：Claude Code 对 workDir 做字符级替换（不是 path.Clean），所以三种字符都要处理。
// 例："X:/WorkSpace" → "X--WorkSpace"；"C:\\Users\\a" → "C--Users-a"。
var pathSepReplacer = strings.NewReplacer(
	":", "-",
	"\\", "-",
	"/", "-",
)

// jsonlRecord 描述 jsonl 单行记录中我们关心的字段。
//
// message 与 origin 用 json.RawMessage 延迟解码，便于：
//   - 跳过 schema 不一致或字段缺失的行；
//   - 对 message.content 二次判型（string vs 数组）。
type jsonlRecord struct {
	Type    string          `json:"type"`
	Message json.RawMessage `json:"message"`
	Origin  json.RawMessage `json:"origin"`
}

// messageStruct 是 jsonlRecord.Message 的二次解析结构。
//
// Content 用 json.RawMessage，因为它可能是：
//   - string：正常用户/助手文本；
//   - array：tool_result 等结构化内容（type=user 但 origin.kind=tool 时常见）。
type messageStruct struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// originStruct 是 jsonlRecord.Origin 的二次解析结构。
//
// origin.kind 区分消息来源："human"（真实用户输入）vs "tool"（工具调用回写）。
// 仅 human 类型的 user 消息才能作为会话标题。
type originStruct struct {
	Kind string `json:"kind"`
}

// SessionJSONLPath 拼接 Claude Code 会话 jsonl 文件路径。
//
// 规则：<homeDir>/.claude/projects/<encoded-cwd>/<sessionID>.jsonl
//
// 参数：
//   - homeDir：用户主目录（通常是 os.UserHomeDir() 的返回值）
//   - workDir：会话工作目录（Claude Code 启动时的 -C/--add-dir 目标）
//   - sessionID：Claude 会话 uuid（也是 jsonl 文件主名；方案 P 下由 tracker 动态跟踪 mtime 最新 jsonl 得到，不通过启动命令注入）
//
// encoded-cwd：路径中冒号、反斜杠、正斜杠全部替换为连字符。
// 例：homeDir="C:/Users/毛润", workDir="X:/WorkSpace", sessionID="abc-123"
//
//	→ "C:/Users/毛润/.claude/projects/X--WorkSpace/abc-123.jsonl"
//
// 注：homeDir / workDir 由调用方传入（不在此处硬编码或读环境变量），
// 便于单元测试与服务端复用。
func SessionJSONLPath(homeDir, workDir, sessionID string) string {
	encoded := pathSepReplacer.Replace(workDir)
	return filepath.Join(homeDir, ".claude", "projects", encoded, sessionID+".jsonl")
}

// FindLatestActiveJSONL 在 workDir 对应的 Claude projects 目录下，返回 mtime 最新的 .jsonl 文件路径与 sessionId。
//
// 方案 P（动态跟踪）：Claude Code TUI 的 /resume 切换不向外部暴露当前 session，
// 只能通过「最近被写入的 jsonl」推断当前活跃会话：
//   - 新会话首条输入 → 新 jsonl 创建 → mtime 最新；
//   - /resume 切到历史会话并继续输入 → 目标 jsonl 被追加 → mtime 变最新。
//
// 参数：
//   - homeDir：用户主目录（os.UserHomeDir() 的返回值）
//   - workDir：会话工作目录（用于推导 encoded-cwd）
//
// 返回值：
//   - path：mtime 最新的 .jsonl 完整路径（filepath.Join 平台分隔符）
//   - sessionID：文件主名去掉 .jsonl 后缀（即 Claude 会话 uuid）
//   - err：目录不存在 / 目录存在但无 .jsonl 文件时返回非 nil
//
// 注意：仅按 mtime 排序，不读取文件内容（避免对大 jsonl 全文 IO）；
// 同 workDir 并发多个 amagi 会话会指向同一最新 jsonl（串扰，主上已接受为边缘场景）。
func FindLatestActiveJSONL(homeDir, workDir string) (path, sessionID string, err error) {
	encoded := pathSepReplacer.Replace(workDir)
	dir := filepath.Join(homeDir, ".claude", "projects", encoded)

	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		return "", "", fmt.Errorf("read dir %q: %w", dir, readErr)
	}

	var (
		latestPath string
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

// ExtractFirstUserMessage 从 Claude Code 的 <session-uuid>.jsonl 中提取首条真实用户消息文本。
//
// 解析规则：逐行 JSON 解码，命中
//
//	type == "user" && message.content 可解码为 string && origin.kind == "human"
//
// 的第一条记录，返回其 message.content（未截断，调用方负责截断）。
//
// 返回值：
//   - content：首条 user message 文本（可能含 \n，调用方按需取首行）
//   - found：是否找到合格记录（文件存在但无合格 user 消息时 found=false, err=nil）
//   - err：文件 IO 错误（如 os.IsNotExist）或读首行时的意外错误；
//     JSON 解码单行失败时跳过该行继续，不直接返回 err（兼容 schema 演进）
//
// 行为细节：
//   - 使用 bufio.Scanner 逐行读取，缓冲区扩到 1MiB 防 "token too long"（单行 JSON
//     可能含 base64 图片或长 tool_result）。
//   - 每行用 json.Unmarshal 到 jsonlRecord；解析失败则 continue。
//   - message 与 origin 二次解码，任一缺失或非对象则跳过。
//   - content 尝试解码为 string；失败（说明是数组，如 tool_result）则跳过。
//   - 文件不存在：返回 ("", false, err)，err 可由调用方用 os.IsNotExist 判定。
func ExtractFirstUserMessage(jsonlPath string) (content string, found bool, err error) {
	f, openErr := os.Open(jsonlPath)
	if openErr != nil {
		return "", false, fmt.Errorf("open jsonl %q: %w", jsonlPath, openErr)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// 默认缓冲区 64KiB，单行 jsonl 可能远超（图片/长 tool_result），扩到 1MiB 防 token too long。
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var rec jsonlRecord
		if jsonErr := json.Unmarshal(line, &rec); jsonErr != nil {
			// 单行解析失败不中断：兼容 schema 演进与外部污染。
			continue
		}

		if rec.Type != "user" {
			continue
		}

		// origin 必须存在且 kind=="human"（区分真实用户输入与工具回写）。
		if len(rec.Origin) == 0 {
			continue
		}
		var origin originStruct
		if jsonErr := json.Unmarshal(rec.Origin, &origin); jsonErr != nil {
			continue
		}
		if origin.Kind != "human" {
			continue
		}

		// message 必须存在且能二次解码。
		if len(rec.Message) == 0 {
			continue
		}
		var msg messageStruct
		if jsonErr := json.Unmarshal(rec.Message, &msg); jsonErr != nil {
			continue
		}

		// content 必须能解码为 string；tool_result 等数组形态会被跳过。
		if len(msg.Content) == 0 {
			continue
		}
		var text string
		if jsonErr := json.Unmarshal(msg.Content, &text); jsonErr != nil {
			// 不是字符串（多半是数组），跳过本行继续找。
			continue
		}

		return text, true, nil
	}

	if scanErr := scanner.Err(); scanErr != nil {
		return "", false, fmt.Errorf("scan jsonl %q: %w", jsonlPath, scanErr)
	}

	// 文件读完无合格记录（含空文件）：found=false, err=nil。
	return "", false, nil
}

// UsageEventStub 是 appmeta 层产出的中立事件结构（避免 appmeta 反向依赖 usage 包）。
//
// usage.Service 内部把 Stub 转换为自身的 UsageEvent。
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

	// OpenCode 专用：CostProvided=true 时跳过价格表计算，直接用 NativeCost。
	CostProvided bool
	NativeCost   int64
	CurrencyCode string
	TimeUpdated  int64 // OpenCode 增量游标（毫秒）
}

// usageAssistantMessage 是 type=="assistant" 行的 message 二次解析结构。
//
// 仅提取用量统计所需字段（id / model / usage），content 用 RawMessage 延迟解码（不需要其内容）。
type usageAssistantMessage struct {
	ID    string          `json:"id"`
	Model string          `json:"model"`
	Usage usageMessageUse `json:"usage"`
}

// usageMessageUse 对应 Anthropic 的 message.usage 对象。
type usageMessageUse struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

// usageRecord 是 type=="assistant" 行的根级结构（只取统计需要的字段）。
type usageRecord struct {
	Type      string                 `json:"type"`
	Message   usageAssistantMessage  `json:"message"`
	Timestamp string                 `json:"timestamp"`
	SessionID string                 `json:"session_id"` // 部分 schema 有根级 session_id
	Cwd       string                 `json:"cwd"`
}

// ExtractUsageRecords 解析单个 Claude jsonl 文件，提取用量记录（断点续传）。
//
// 路径：<homeDir>/.claude/projects/<encoded-cwd>/<session-uuid>.jsonl
//
// 解析规则（设计 5.1）：
//   - 从 startOffset 字节偏移开始读取（断点续传）；startOffset=0 全量
//   - 逐行 JSON 解码（bufio.Scanner，缓冲扩到 1MiB）
//   - 仅处理 type=="assistant" 的行
//   - 从 message.usage 取四维 token：input_tokens / output_tokens /
//     cache_read_input_tokens / cache_creation_input_tokens
//   - 从 message.model 取模型名
//   - 从 message.id 取去重键（DedupKey = "cc:msg_" + message.id，天然全局唯一）
//   - 从根级 timestamp 取 ISO8601 时间
//
// Anthropic 语义：input_tokens 是 fresh input，不含 cache_read。
// 调用方（usage.Service）按 AppType=claudecode 不做扣减。
//
// 返回值：
//   - records：解析出的 UsageEventStub（已设置 DedupKey/AppType/Source/OccurredAt 等）
//   - lastOffset：已读到的字节偏移（含换行符），供 sync_state 断点续传
//   - err：文件级 IO 错误；单行解析失败 continue 不中断
//
// 注：第一期不扫 subagent/workflow 嵌套 jsonl（仅扫根级 type=="assistant" 行）。
func ExtractUsageRecords(jsonlPath string, startOffset int64) (records []UsageEventStub, lastOffset int64, err error) {
	f, openErr := os.Open(jsonlPath)
	if openErr != nil {
		return nil, 0, fmt.Errorf("open jsonl %q: %w", jsonlPath, openErr)
	}
	defer f.Close()

	// 断点续传：Seek 到上次偏移（必落在行边界 \n 之后）
	if startOffset > 0 {
		if _, seekErr := f.Seek(startOffset, 0); seekErr != nil {
			return nil, startOffset, fmt.Errorf("seek jsonl %q to %d: %w", jsonlPath, startOffset, seekErr)
		}
	}

	// SessionID 与 ProjectDir 从文件路径推断（避免每行重复解析）
	sessionID, projectDirEncoded := inferSessionAndProjectFromPath(jsonlPath)

	scanner := bufio.NewScanner(f)
	// 实测：含 base64 图片或大 tool_result 的单行可达数 MB；扩到 16MiB 防 token too long。
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	currentOffset := startOffset
	for scanner.Scan() {
		lineBytes := scanner.Bytes()
		// 累计字节偏移：len(lineBytes) + 1（\n 换行符）
		// 注意：bufio.Scanner 在 EOF 无 \n 时返回最后一行但不增加 +1，
		// 但 Claude jsonl 每行必以 \n 结尾（追加写），此假设成立。
		currentOffset += int64(len(lineBytes)) + 1

		if len(lineBytes) == 0 {
			continue
		}

		var rec usageRecord
		if jsonErr := json.Unmarshal(lineBytes, &rec); jsonErr != nil {
			// 单行解析失败不中断：兼容 schema 演进
			continue
		}
		if rec.Type != "assistant" {
			continue
		}
		if rec.Message.ID == "" {
			// 无 message.id 无法去重，跳过（保守）
			continue
		}

		var occurredAt time.Time
		if rec.Timestamp != "" {
			// ISO8601（如 "2026-07-17T02:42:42.807Z"）
			if t, parseErr := time.Parse(time.RFC3339Nano, rec.Timestamp); parseErr == nil {
				occurredAt = t.UTC()
			}
		}
		if occurredAt.IsZero() {
			// timestamp 缺失或解析失败：用文件 mtime 兜底（避免零值污染时间分布）
			if info, statErr := os.Stat(jsonlPath); statErr == nil {
				occurredAt = info.ModTime().UTC()
			} else {
				occurredAt = time.Now().UTC()
			}
		}

		// 优先用根级 cwd（更准确），其次从路径推断
		projectDir := rec.Cwd
		if projectDir == "" {
			projectDir = projectDirEncoded
		}
		sessID := sessionID
		if rec.SessionID != "" {
			sessID = rec.SessionID
		}

		records = append(records, UsageEventStub{
			DedupKey:                 dedupPrefixClaudeSession + rec.Message.ID,
			Model:                    rec.Message.Model,
			ProjectDir:               projectDir,
			SessionID:                sessID,
			RawMessageID:             rec.Message.ID,
			InputTokens:              rec.Message.Usage.InputTokens,
			OutputTokens:             rec.Message.Usage.OutputTokens,
			CacheReadInputTokens:     rec.Message.Usage.CacheReadInputTokens,
			CacheCreationInputTokens: rec.Message.Usage.CacheCreationInputTokens,
			OccurredAt:               occurredAt,
		})
	}

	if scanErr := scanner.Err(); scanErr != nil {
		return records, currentOffset, fmt.Errorf("scan jsonl %q: %w", jsonlPath, scanErr)
	}

	return records, currentOffset, nil
}

// dedupPrefixClaudeSession 是 Claude 行的 dedup_key 前缀（与 usage 包常量保持一致）。
// 这里独立定义避免反向依赖 usage 包。
const dedupPrefixClaudeSession = "cc:msg_"

// inferSessionAndProjectFromPath 从 jsonl 路径推断 SessionID 与 ProjectDir（encoded）。
//
// 路径形如：.../<home>/.claude/projects/<encoded-cwd>/<session-uuid>.jsonl
// SessionID = 文件主名（去 .jsonl）；ProjectDir = 父目录名（encoded）。
func inferSessionAndProjectFromPath(jsonlPath string) (sessionID, projectDirEncoded string) {
	base := filepath.Base(jsonlPath)
	sessionID = strings.TrimSuffix(base, ".jsonl")
	projectDirEncoded = filepath.Base(filepath.Dir(jsonlPath))
	return sessionID, projectDirEncoded
}
