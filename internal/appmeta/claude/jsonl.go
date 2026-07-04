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
