package session

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"amagi-codebox/internal/appmeta/claude"
)

// titleMaxRunes 是会话标题的 rune 上限（侧栏单行展示）。
// 与 internal/pty/ansi.go 的 firstOutputMaxRunes 保持一致，确保两侧展示长度对齐。
const titleMaxRunes = 60

// titlePollInterval 是动态跟踪 jsonl 的轮询周期。
// 10s 是权衡：过短（<5s）增加 IO 压力；过长（>30s）用户感知延迟明显。
// 实测 Claude Code 在用户输入瞬间即追加 jsonl，10s 内必能捕获。
const titlePollInterval = 10 * time.Second

// titleLogger 是 Tracker 依赖的最小日志接口（避免直接依赖具体 logging 包，便于测试）。
// 签名与 internal/logging.Service.Info 对齐（source, message string, detail ...string）。
type titleLogger interface {
	Info(source, message string, detail ...string)
}

// TrackTitle 动态跟踪 Claude Code 会话的最新 jsonl，自动设置会话标题与 ClaudeSessionID。
//
// 方案 P 核心：不依赖外部注入 sessionID（/resume 是 TUI 内部行为不暴露当前 session），
// 改为周期扫描 <homeDir>/.claude/projects/<encoded-workDir>/ 下 mtime 最新的 .jsonl。
// 自动覆盖两种场景：
//  1. 新会话首条输入 → 新 jsonl 被写入 → mtime 最新 → 标题 = 首条 user message；
//  2. 用户在 TUI 内 /resume 切历史会话并继续输入 → 目标 jsonl 被追加 → mtime 变最新 → 标题跟随。
//
// 退出策略（双重保险，任一触发即 return，无 goroutine 泄漏）：
//   - ctx.Done()：调用方 cancel（如 app 退出）；
//   - mgr.GetStatus 返回非 Running：会话已被 MarkStopped/MarkExited/MarkFailed。
//
// 停止时 ClaudeSessionID 冻结于最后跟踪到的值，供 List 直读历史 jsonl。
//
// 参数：
//   - mgr：会话管理器（用于 SetTitle / SetClaudeSessionID / GetStatus）
//   - amagiSessionID：amagi-codebox 内部会话 ID（mgr.sessions 的 key）
//   - homeDir：用户主目录（os.UserHomeDir() 的返回值，参数化便于测试）
//   - workDir：会话工作目录
//   - log：可选日志器；nil 时静默
func TrackTitle(ctx context.Context, mgr *Manager, amagiSessionID, homeDir, workDir string, log titleLogger) {
	ticker := time.NewTicker(titlePollInterval)
	defer ticker.Stop()

	var lastPath string

	// 启动后立即跑一轮（不等待首个 tick），缩短首条标题出现的感知延迟。
	if !pollOnce(mgr, amagiSessionID, homeDir, workDir, &lastPath, log) {
		return // 会话已停止
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !pollOnce(mgr, amagiSessionID, homeDir, workDir, &lastPath, log) {
				return // 会话已停止
			}
		}
	}
}

// pollOnce 执行一轮 jsonl 跟踪。返回 false 表示会话已停止，调用方应退出 goroutine。
//
// 状态检查放在每次 tick 开头：保证 MarkStopped/MarkExited/MarkFailed 触发后，
// tracker 在最多一个 tick 周期内退出，不泄漏。
func pollOnce(mgr *Manager, amagiSessionID, homeDir, workDir string, lastPath *string, log titleLogger) bool {
	status := mgr.GetStatus(amagiSessionID)
	if status != "" && status != StatusRunning {
		return false
	}

	path, sid, err := claude.FindLatestActiveJSONL(homeDir, workDir)
	if err != nil {
		// 目录不存在 / 无 jsonl：会话刚启动用户尚未输入，等下一轮。
		return true
	}
	if path == *lastPath {
		// 同一文件，标题与 sessionID 已设过，跳过。
		return true
	}

	content, found, extractErr := claude.ExtractFirstUserMessage(path)
	if extractErr != nil {
		// jsonl 读取失败（如临时锁/权限）：等下一轮重试。
		if log != nil {
			log.Info("session", "标题跟踪：读取 jsonl 失败", "id="+amagiSessionID+" err="+extractErr.Error())
		}
		return true
	}
	if !found {
		// 文件存在但首条 user message 尚未写入（仅 system/init 行）：等下一轮。
		return true
	}

	title := truncateFirstLine(content, titleMaxRunes)
	mgr.SetTitle(amagiSessionID, title)
	mgr.SetClaudeSessionID(amagiSessionID, sid)
	*lastPath = path

	if log != nil {
		log.Info("session", "标题已捕获", "id="+amagiSessionID+" claudeSid="+sid+" title="+title)
	}
	return true
}

// truncateFirstLine 取 content 的首个 \n 前的内容，再按 rune 截断到 max。
//
// 实测 Claude Code jsonl 的 user content 可能含多行（如粘贴的代码块、路径前缀等），
// 会话标题只用首行；超过 max rune 时末尾追加省略号。
func truncateFirstLine(content string, max int) string {
	// 取首个 \n 前的内容（content 可能不含 \n，那 whole string 即首行）。
	if idx := strings.IndexByte(content, '\n'); idx >= 0 {
		content = content[:idx]
	}
	content = strings.TrimRight(content, "\r")
	return truncateRunes(content, max)
}

// truncateRunes 按 rune 截断字符串到 max；超出部分以省略号结尾。
//
// session 包独立维护的 rune 截断（首行 + max rune 截断到 …），不依赖 pty 包
// （避免 session 包反向依赖 pty 包破坏依赖方向）。
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	if max-1 <= 0 {
		return "…"
	}
	return string(runes[:max-1]) + "…"
}
