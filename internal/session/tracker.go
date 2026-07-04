package session

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"amagi-codebox/internal/appmeta/claude"
)

var (
	// pathLineRe 匹配"纯路径行"：整行是一个不带空格描述的绝对路径。
	// - Windows 盘符路径：X:\WorkSpace\foo 或 X:/WorkSpace/foo（盘符冒号 + 分隔符起始）
	// - UNIX 绝对路径：/home/user/foo
	// 整行不含空格（路径段字符之外没有描述性文字），匹配即视为无意义噪音行跳过。
	pathLineRe = regexp.MustCompile(`^(?:[A-Za-z]:[\\/][\w\\/.+-]*|/[\w/.+-]+)$`)

	// xmlTagLineRe 匹配"整行 XML/HTML 标签"：<tag>...</tag> 或 <tag/>。
	// 用于跳过 Claude Code 内部的 slash command 表示（如 <command-message>amagi:pull</command-message>）
	// 和系统注入的整行标签（如 <system-reminder>...</system-reminder>）。
	// 不误伤正常文本（正常消息不以 <tag> 开头）；markdown 标题（## Task Contract）也不匹配。
	xmlTagLineRe = regexp.MustCompile(`^<[A-Za-z/][^<>]*>.*$`)
)

// normalizePath 归一化路径用于比较：Clean + ToSlash + ToLower。
// 容忍 X:\ 与 X:/、大小写差异、尾部分隔符差异，用于 workDir 行匹配。
func normalizePath(p string) string {
	if p == "" {
		return ""
	}
	return strings.ToLower(filepath.ToSlash(filepath.Clean(p)))
}

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

// TrackTitle 动态跟踪 Claude Code 会话的 jsonl，自动设置会话标题与 ClaudeSessionID。
//
// 方案 R（锁定 jsonl，不跟随 /resume）：
//   - app.go 启动 embedded claudecode 时注入 --session-id <uuid>，让 Claude Code
//     用 amagi-codebox 指定的 uuid 写 jsonl；
//   - tracker 只读"自己锁定的 jsonl"（GetClaudeSessionID → SessionJSONLPath），
//     同 workDir 多会话各自读各自 jsonl，消除串扰；
//   - 标题固定为本会话首条 user message，不因用户暂停输入或 /resume 切换而改变
//     （早期实现的停滞检测会误把"用户暂停输入"判定为"已 /resume 切走"，
//     跟随到同 workDir 下别的活跃会话 jsonl，导致标题串扰，故彻底放弃跟随）。
//
// 方案 P 降级（external 模式 / 注入失败）：ClaudeSessionID 空 → 用 FindLatestActiveJSONL
// 取同目录最新 mtime jsonl（同 workDir 多会话会指向同一 jsonl，已接受为边缘场景）。
//
// 退出策略（双重保险，任一触发即 return，无 goroutine 泄漏）：
//   - ctx.Done()：调用方 cancel（如 app 退出）；
//   - mgr.GetStatus 返回非 Running：会话已被 MarkStopped/MarkExited/MarkFailed。
//
// 停止时 ClaudeSessionID 冻结于最后跟踪到的值，供 List 直读历史 jsonl。
//
// 参数：
//   - mgr：会话管理器（用于 GetClaudeSessionID / SetTitle / SetClaudeSessionID / GetStatus）
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
// 方案 R 路径选择（消串扰核心）：
//  1. ClaudeSessionID 非空（embedded 注入）：只读 SessionJSONLPath(homeDir, workDir, sid) 锁定文件，
//     不检测同目录其他 jsonl，不跟随 /resume（避免暂停输入误判串扰）；
//  2. ClaudeSessionID 空（external / 注入失败）：降级方案 P，用 FindLatestActiveJSONL 取最新。
//
// 状态检查放在每次 tick 开头：保证 MarkStopped/MarkExited/MarkFailed 触发后，
// tracker 在最多一个 tick 周期内退出，不泄漏。
func pollOnce(mgr *Manager, amagiSessionID, homeDir, workDir string, lastPath *string, log titleLogger) bool {
	status := mgr.GetStatus(amagiSessionID)
	if status != "" && status != StatusRunning {
		return false
	}

	sid := mgr.GetClaudeSessionID(amagiSessionID)

	var choosePath, chooseSid string
	if sid != "" {
		// 方案 R：只读锁定 jsonl（消串扰，不跟随 /resume）
		lockedPath := claude.SessionJSONLPath(homeDir, workDir, sid)
		_, err := os.Stat(lockedPath)
		if err != nil {
			if os.IsNotExist(err) {
				// claude 刚启动，jsonl 尚未创建，等下一轮
				return true
			}
			// 其他 stat 错误（如临时权限）：等下一轮重试
			if log != nil {
				log.Info("session", "标题跟踪：stat 锁定 jsonl 失败", "id="+amagiSessionID+" err="+err.Error())
			}
			return true
		}
		choosePath = lockedPath
		chooseSid = sid
	} else {
		// 方案 P 降级（external 模式 / 注入失败）：无锁定，用最新 mtime
		latestPath, latestSid, err := claude.FindLatestActiveJSONL(homeDir, workDir)
		if err != nil {
			// 目录不存在 / 无 jsonl：会话刚启动用户尚未输入，等下一轮
			return true
		}
		choosePath = latestPath
		chooseSid = latestSid
	}

	if choosePath == "" || choosePath == *lastPath {
		// 同一文件已处理过 / 路径为空：跳过
		return true
	}

	content, found, extractErr := claude.ExtractFirstUserMessage(choosePath)
	if extractErr != nil {
		// jsonl 读取失败（如临时锁/权限）：等下一轮重试
		if log != nil {
			log.Info("session", "标题跟踪：读取 jsonl 失败", "id="+amagiSessionID+" err="+extractErr.Error())
		}
		return true
	}
	if !found {
		// 文件存在但首条 user message 尚未写入（仅 system/init 行）：等下一轮
		return true
	}

	title := truncateFirstLine(content, titleMaxRunes, workDir)
	mgr.SetTitle(amagiSessionID, title)
	if chooseSid != sid && chooseSid != "" {
		// 方案 P 降级路径捕获到 ClaudeSessionID（external 模式下首次未注入）：
		// 回写到 Session，下一轮直接走方案 R 锁定路径。
		mgr.SetClaudeSessionID(amagiSessionID, chooseSid)
	}
	*lastPath = choosePath

	if log != nil {
		log.Info("session", "标题已捕获", "id="+amagiSessionID+" sid="+chooseSid+" title="+title)
	}
	return true
}

// truncateFirstLine 取 content 的首个有意义行（跳过空行、workDir 路径行、纯路径行、整行 XML 标签行），
// 再按 rune 截断到 max。全部行被跳过时兜底取首个非空行（避免空标题）。
//
// 背景：实测 Claude Code jsonl 的首条 user message content 首行常是无意义噪音——
//   - workDir 路径（"X:\WorkSpace\amagi-codebox"）
//   - slash command 的 XML 标签（"<command-message>amagi:pull</command-message>"）
//   - 其他纯路径行
// 直接取首行会把这些噪音设为标题。本函数跳过它们，找到首条描述性内容作标题。
//
// 不跳过的内容：markdown 标题（## Task Contract）、正常自然语言消息等。
func truncateFirstLine(content string, max int, workDir string) string {
	normWD := normalizePath(workDir)
	var fallback string
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(strings.TrimRight(line, "\r"))
		if trimmed == "" {
			continue
		}
		if fallback == "" {
			// 记录首个非空行作兜底（全部有意义行被跳过时使用，避免空标题）。
			fallback = trimmed
		}
		// 跳过 workDir 行（归一化比较，容忍 X:\ 与 X:/、大小写差异）。
		if normWD != "" && normalizePath(trimmed) == normWD {
			continue
		}
		// 跳过纯路径行（Windows 盘符路径 / UNIX 绝对路径，整行无描述性文字）。
		if pathLineRe.MatchString(trimmed) {
			continue
		}
		// 跳过整行 XML/HTML 标签（slash command 内部表示、系统注入标签等）。
		if xmlTagLineRe.MatchString(trimmed) {
			continue
		}
		return truncateRunes(trimmed, max)
	}
	if fallback != "" {
		return truncateRunes(fallback, max)
	}
	return ""
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
