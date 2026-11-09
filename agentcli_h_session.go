package main

// agentcli_h_session.go — agent-CLI 会话域 Handler（命令表见 internal/agentcli/cmds/session.go）。
// 停止/移除/清理经 App 既有 Gate 门面（StopSession/RemoveSession/ClearStoppedSessions*），
// 输入经 PtyWrite/PtyWriteLarge 门面，输出经 GetOutputHistorySnapshot。

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"amagi-codebox/internal/agentcli/spec"
	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/session"
)

func (h *agentCLIHost) sessionHandlers() map[string]spec.Handler {
	return map[string]spec.Handler{
		"session.list":          h.sessionList,
		"session.get":           h.sessionGet,
		"session.output":        h.sessionOutput,
		"session.launch":        h.sessionLaunch,
		"session.send":          h.sessionSend,
		"session.stop":          h.sessionStop,
		"session.remove":        h.sessionRemove,
		"session.clear-stopped": h.sessionClearStopped,
	}
}

// ---- list / get ----

func (h *agentCLIHost) sessionList(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	sessions := h.app.GetSessions()
	if status := args.String("status"); status != "" {
		filtered := make([]session.SessionInfo, 0, len(sessions))
		for _, s := range sessions {
			if string(s.Status) == status {
				filtered = append(filtered, s)
			}
		}
		sessions = filtered
	}
	if sessions == nil {
		sessions = []session.SessionInfo{}
	}
	return sessions, nil
}

func (h *agentCLIHost) sessionGet(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	id := strings.TrimSpace(args.String("sessionId"))
	if id == "" {
		return nil, spec.Errorf(spec.ErrBadArgs, "sessionId 不能为空")
	}
	info, err := h.app.GetSession(id)
	if err != nil {
		return nil, mapAgentSessionError(err)
	}
	return info, nil
}

// ---- output ----

// agentOutputSnapshot 对应 GetOutputHistorySnapshot 的 JSON 形状
// （control 就绪时含 runToken/runVersion，此处不透出内部字段）。
type agentOutputSnapshot struct {
	Data string `json:"data"`
	Seq  uint64 `json:"seq"`
}

// sessionOutputResult 是 session.output 的返回结构。
type sessionOutputResult struct {
	SessionID string `json:"sessionId"`
	Status    string `json:"status"`
	Seq       uint64 `json:"seq"`
	Truncated bool   `json:"truncated"`
	Text      string `json:"text"`
}

func (h *agentCLIHost) sessionOutput(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	id := strings.TrimSpace(args.String("sessionId"))
	if id == "" {
		return nil, spec.Errorf(spec.ErrBadArgs, "sessionId 不能为空")
	}
	tailBytes := args.Int("tailBytes", 0)
	if tailBytes < 0 {
		return nil, spec.Errorf(spec.ErrBadArgs, "tailBytes 不能为负数")
	}
	sinceSeq := args.Int("sinceSeq", -1)
	if sinceSeq < -1 {
		return nil, spec.Errorf(spec.ErrBadArgs, "sinceSeq 不能为负数")
	}
	info, err := h.app.GetSession(id)
	if err != nil {
		return nil, mapAgentSessionError(err)
	}
	snapshot, err := h.app.GetOutputHistorySnapshot(id)
	if err != nil {
		// 会话记录存在但 PTY 无历史：terminal 模式会话或已回收的内嵌会话。
		specErr := mapAgentSessionError(err)
		if specErr.Code == spec.ErrNotFound {
			specErr.Hint = "会话存在但无内嵌终端输出（terminal 模式会话输出进独立终端窗口，或内嵌输出已回收）"
		}
		return nil, specErr
	}
	return renderSessionOutput(id, string(info.Status), snapshot, tailBytes, int64(sinceSeq), args.Bool("raw"))
}

// renderSessionOutput 是 session.output 的纯函数主体：
// 解析快照 JSON → base64 解码 → since-seq 去重判定 →（默认）剥离控制序列 → tail 截断。
func renderSessionOutput(sessionID, status, snapshotJSON string, tailBytes int, sinceSeq int64, raw bool) (any, error) {
	var snap agentOutputSnapshot
	if err := json.Unmarshal([]byte(snapshotJSON), &snap); err != nil {
		return nil, spec.Errorf(spec.ErrInternal, "解析输出快照失败: %v", err)
	}
	data, err := base64.StdEncoding.DecodeString(snap.Data)
	if err != nil {
		return nil, spec.Errorf(spec.ErrInternal, "解码输出快照 base64 失败: %v", err)
	}
	// since-seq：seq 是 PTY 读循环的分块计数（非字节偏移），快照是有限字节环，
	// 无法精确切出"seq 之后新增的字节"。语义定为轮询去重：seq 未前进返回空文本；
	// 前进了返回完整快照（受 tailBytes 约束）供调用方整体重算。
	if sinceSeq >= 0 && snap.Seq <= uint64(sinceSeq) {
		return sessionOutputResult{SessionID: sessionID, Status: status, Seq: snap.Seq}, nil
	}
	text := string(data)
	if !raw {
		text = stripTerminalControls(text)
	}
	truncated := false
	if tailBytes > 0 {
		text, truncated = tailUTF8Safe(text, tailBytes)
	}
	return sessionOutputResult{SessionID: sessionID, Status: status, Seq: snap.Seq, Truncated: truncated, Text: text}, nil
}

// stripTerminalControls 剥离 ANSI/控制序列，把带覆盖语义的原始终端字节流
// 折叠为可读文本：
//   - CSI（ESC[...终符 0x40-0x7E）与 OSC（ESC]...BEL 或 ESC\）整段丢弃；
//     其余两字符转义（如 ESC M）整段丢弃；
//   - \n 提交当前行；\r 回到行首（后续字符覆盖写）；\b 光标左移一格（后续字符覆盖写，
//     与真实终端回显一致；不补空格、不删除尾部残余）；
//   - 其余 C0 控制字符与 DEL 丢弃；CSI 光标移动/清屏不模拟光标（按丢弃处理）。
//
// 无效 UTF-8 字节在 []rune 转换中变为 U+FFFD，不会 panic。
func stripTerminalControls(s string) string {
	runes := []rune(s)
	n := len(runes)
	var lines []string
	var line []rune
	cursor := 0
	flush := func() {
		lines = append(lines, string(line))
		line = line[:0]
		cursor = 0
	}
	write := func(r rune) {
		if cursor < len(line) {
			line[cursor] = r
		} else {
			line = append(line, r)
		}
		cursor++
	}
	for i := 0; i < n; {
		r := runes[i]
		switch {
		case r == 0x1b: // ESC
			if i+1 >= n { // 悬空 ESC
				i++
				continue
			}
			switch runes[i+1] {
			case '[': // CSI
				j := i + 2
				for j < n && !(runes[j] >= 0x40 && runes[j] <= 0x7e) {
					j++
				}
				if j < n {
					j++ // 吃掉终结符
				}
				i = j
			case ']': // OSC：BEL 或 ESC\ 结束
				j := i + 2
				terminated := false
				for j < n {
					if runes[j] == 0x07 {
						j++
						terminated = true
						break
					}
					if runes[j] == 0x1b && j+1 < n && runes[j+1] == '\\' {
						j += 2
						terminated = true
						break
					}
					j++
				}
				_ = terminated // 未终结的 OSC 同样整段丢弃
				i = j
			default: // 其余两字符转义整段丢弃
				i += 2
			}
		case r == '\n':
			flush()
			i++
		case r == '\r':
			cursor = 0
			i++
		case r == '\b':
			if cursor > 0 {
				cursor--
			}
			i++
		case r < 0x20 || r == 0x7f:
			i++ // 其余 C0/DEL 丢弃
		default:
			write(r)
			i++
		}
	}
	if len(line) > 0 {
		flush()
	}
	return strings.Join(lines, "\n")
}

// tailUTF8Safe 返回 s 的最后 limit 字节，切点右移到 UTF-8 字符边界，
// 保证结果不超 limit 字节且不舍开多字节字符；当 limit 小于末尾一个完整
// 字符的字节数时可能返回空文本。第二返回值表示是否发生了截断。
func tailUTF8Safe(s string, limit int) (string, bool) {
	if limit <= 0 || len(s) <= limit {
		return s, false
	}
	b := []byte(s)
	start := len(b) - limit
	for start < len(b) && !utf8.RuneStart(b[start]) {
		start++
	}
	return string(b[start:]), true
}

// ---- launch ----

// agentLaunchParams 是 session.launch 的归一化参数。
type agentLaunchParams struct {
	AppType  string
	Provider string
	Model    string
	Preset   string
	Mode     string
	WorkDir  string
	Headroom bool
	Shell    string
}

// agentSessionLauncher 抽象 App 的五个启动入口，供参数映射层独立测试。
type agentSessionLauncher interface {
	LaunchSession(providerName, presetName, mode, workDir string, useHeadroom bool, shellPath string) (string, error)
	LaunchOpenCode(providerName, presetName, mode, workDir, shellPath string) (string, error)
	LaunchCodexSession(modelName, providerID, mode, workDir, shellPath string) (string, error)
	LaunchPiSession(modelName, providerID, mode, workDir, shellPath string) (string, error)
	LaunchOmpSession(modelName, providerID, mode, workDir, shellPath string) (string, error)
}

var _ agentSessionLauncher = (*App)(nil)

func (h *agentCLIHost) sessionLaunch(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	p := agentLaunchParams{
		AppType:  strings.TrimSpace(args.String("appType")),
		Provider: strings.TrimSpace(args.String("provider")),
		Model:    strings.TrimSpace(args.String("model")),
		Preset:   strings.TrimSpace(args.String("preset")),
		Mode:     strings.TrimSpace(args.String("mode")),
		WorkDir:  args.String("workDir"),
		Headroom: args.Bool("headroom"),
		Shell:    strings.TrimSpace(args.String("shell")),
	}
	norm, normErr := normalizeAgentLaunchParams(p)
	if normErr != nil {
		return nil, normErr
	}
	// 副作用前检查：启动入口无 ctx 参数，取消后不得再拉起新进程（D-06）。
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	sessionID, err := dispatchAgentLaunch(h.app, norm)
	if err != nil {
		return nil, spec.Errorf(spec.ErrInternal, "启动失败: %v", err)
	}
	resp := map[string]any{"sessionId": sessionID}
	if info, gErr := h.app.GetSession(sessionID); gErr == nil {
		resp["session"] = info
	}
	return resp, nil
}

// normalizeAgentLaunchParams 校验并归一化 launch 参数（纯函数，返回业务错误）。
func normalizeAgentLaunchParams(p agentLaunchParams) (agentLaunchParams, *spec.Error) {
	switch p.AppType {
	case "claudecode", "opencode", "codex", "pi", "omp":
	default:
		return p, spec.Errorf(spec.ErrBadArgs, "appType 必须是 claudecode/opencode/codex/pi/omp 之一，得到 %q", p.AppType)
	}
	if p.Mode == "" {
		p.Mode = "embedded" // 显式默认，避免依赖各 Launch* 入口的空 mode 语义
	} else if p.Mode != "embedded" && p.Mode != "terminal" {
		return p, spec.Errorf(spec.ErrBadArgs, "mode 必须是 embedded 或 terminal，得到 %q", p.Mode)
	}
	workDir, err := normalizeAgentLaunchWorkDir(p.WorkDir)
	if err != nil {
		return p, err
	}
	p.WorkDir = workDir
	switch p.AppType {
	case "claudecode":
		if p.Provider == "" {
			return p, spec.Errorf(spec.ErrBadArgs, "claudecode 启动需要 --provider（OAuth 模式也需指定提供商名）")
		}
	case "opencode":
		if p.Preset == "" && p.Provider == "" {
			return p, spec.Errorf(spec.ErrBadArgs, "opencode 启动需要 --preset（opencode 预设键）或 --provider 至少其一")
		}
		if p.Headroom {
			return p, spec.Errorf(spec.ErrBadArgs, "headroom 仅 claudecode 支持")
		}
	case "codex", "pi", "omp":
		if p.Model == "" && p.Preset == "" {
			return p, spec.Errorf(spec.ErrBadArgs, "%s 启动需要 --model 或 --preset（terminal preset 键）至少其一", p.AppType)
		}
		if p.Headroom {
			return p, spec.Errorf(spec.ErrBadArgs, "headroom 仅 claudecode 支持")
		}
	}
	return p, nil
}

// normalizeAgentLaunchWorkDir 校验 workDir：空串放行（走应用默认目录链）；
// 非空时必须是已存在目录，返回 Abs+Clean 归一化后的路径。
func normalizeAgentLaunchWorkDir(workDir string) (string, *spec.Error) {
	trimmed := strings.TrimSpace(workDir)
	if trimmed == "" {
		return "", nil
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return "", spec.Errorf(spec.ErrBadArgs, "工作目录路径无效: %v", err)
	}
	abs = filepath.Clean(abs)
	info, statErr := os.Stat(abs)
	if statErr != nil {
		return "", spec.Errorf(spec.ErrBadArgs, "工作目录不存在: %s", abs)
	}
	if !info.IsDir() {
		return "", spec.Errorf(spec.ErrBadArgs, "工作目录不是目录: %s", abs)
	}
	return abs, nil
}

// dispatchAgentLaunch 按类型把统一参数映射到 App 的五个启动入口（纯映射层）。
// codex/pi/omp 的首参数（模型位）支持 terminal preset 键：显式 model 优先，
// 否则用 preset 键传入（LaunchCodexSession 等内部会做 preset 桥接）。
func dispatchAgentLaunch(l agentSessionLauncher, p agentLaunchParams) (string, error) {
	switch p.AppType {
	case "claudecode":
		return l.LaunchSession(p.Provider, p.Preset, p.Mode, p.WorkDir, p.Headroom, p.Shell)
	case "opencode":
		return l.LaunchOpenCode(p.Provider, p.Preset, p.Mode, p.WorkDir, p.Shell)
	case "codex":
		return l.LaunchCodexSession(agentModelOrPreset(p), p.Provider, p.Mode, p.WorkDir, p.Shell)
	case "pi":
		return l.LaunchPiSession(agentModelOrPreset(p), p.Provider, p.Mode, p.WorkDir, p.Shell)
	case "omp":
		return l.LaunchOmpSession(agentModelOrPreset(p), p.Provider, p.Mode, p.WorkDir, p.Shell)
	}
	return "", fmt.Errorf("未知 appType %q", p.AppType)
}

func agentModelOrPreset(p agentLaunchParams) string {
	if p.Model != "" {
		return p.Model
	}
	return p.Preset
}

// ---- send ----

func (h *agentCLIHost) sessionSend(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	id := strings.TrimSpace(args.String("sessionId"))
	if id == "" {
		return nil, spec.Errorf(spec.ErrBadArgs, "sessionId 不能为空")
	}
	payload := args.String("text")
	if args.Bool("enter") {
		payload += "\r"
	}
	if payload == "" {
		return nil, spec.Errorf(spec.ErrBadArgs, "text 不能为空")
	}
	info, err := h.app.GetSession(id)
	if err != nil {
		return nil, mapAgentSessionError(err)
	}
	if info.Status != session.StatusRunning {
		return nil, spec.Errorf(spec.ErrConflict, "会话 %s 状态为 %s，只有 running 会话可接收输入", id, info.Status)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(payload))
	// 副作用前检查：写入通道不可撤销，取消后不得再写入（D-06）。
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	// ≤1KB 走键盘输入通道（单操作）；更长走分块粘贴通道（PtyWriteLarge 内部 1KB 分块）。
	if len(payload) <= 1024 {
		err = h.app.PtyWrite(id, encoded)
	} else {
		err = h.app.PtyWriteLarge(id, encoded)
	}
	if err != nil {
		return nil, mapAgentSessionError(err)
	}
	return map[string]any{"sessionId": id, "status": string(info.Status), "bytesSent": len(payload)}, nil
}

// ---- stop / remove / clear-stopped ----

func (h *agentCLIHost) sessionStop(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	id := strings.TrimSpace(args.String("sessionId"))
	if id == "" {
		return nil, spec.Errorf(spec.ErrBadArgs, "sessionId 不能为空")
	}
	if _, err := h.app.GetSession(id); err != nil {
		return nil, mapAgentSessionError(err)
	}
	if err := h.app.StopSession(id); err != nil {
		return nil, mapAgentSessionError(err)
	}
	return map[string]any{"sessionId": id, "stopped": true}, nil
}

func (h *agentCLIHost) sessionRemove(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	id := strings.TrimSpace(args.String("sessionId"))
	if id == "" {
		return nil, spec.Errorf(spec.ErrBadArgs, "sessionId 不能为空")
	}
	info, err := h.app.GetSession(id)
	if err != nil {
		return nil, mapAgentSessionError(err)
	}
	if info.Status == session.StatusRunning || info.Status == session.StatusStopping {
		return nil, spec.Errorf(spec.ErrConflict, "会话 %s 状态为 %s，先 stop 再 remove", id, info.Status)
	}
	if err := h.app.RemoveSession(id); err != nil {
		return nil, mapAgentSessionError(err)
	}
	return map[string]any{"sessionId": id, "removed": true}, nil
}

func (h *agentCLIHost) sessionClearStopped(ctx context.Context, _ spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	return h.app.ClearStoppedSessionsDetailed(), nil
}

// ---- 错误映射 ----

// mapAgentSessionError 把 App 会话面错误映射为稳定错误码：
// ErrControlNotReady→not_ready；Gate 的 SessionNotFound→not_found；
// 会话不可写/未停止/仍在运行→conflict；"session not found"→not_found；其余→internal。
func mapAgentSessionError(err error) *spec.Error {
	if err == nil {
		return nil
	}
	var specErr *spec.Error
	if errors.As(err, &specErr) {
		return specErr
	}
	if errors.Is(err, remote.ErrControlNotReady) {
		return spec.Errorf(spec.ErrNotReady, "控制运行时未就绪（应用启动早期或正在关闭），稍后重试")
	}
	var gatePtr *remote.ControlGateError
	if errors.As(err, &gatePtr) {
		return mapControlGateError(gatePtr)
	}
	var gateVal remote.ControlGateError
	if errors.As(err, &gateVal) {
		return mapControlGateError(&gateVal)
	}
	if errors.Is(err, session.ErrSessionRunning) || errors.Is(err, session.ErrSessionStopping) {
		return spec.Errorf(spec.ErrConflict, "会话仍在运行/停止中，不能移除：%v", err)
	}
	msg := err.Error()
	if strings.Contains(msg, "session not found") {
		return spec.Errorf(spec.ErrNotFound, "会话不存在：%s", msg)
	}
	return spec.Errorf(spec.ErrInternal, "%s", msg)
}

func mapControlGateError(gate *remote.ControlGateError) *spec.Error {
	switch gate.Kind {
	case remote.DenySessionNotFound:
		return spec.Errorf(spec.ErrNotFound, "会话不存在或已清理")
	case remote.DenySessionNotWritable, remote.DenySessionNotStopped, remote.DenyBusy, remote.DenyNotController:
		return spec.Errorf(spec.ErrConflict, "会话当前状态不接受该操作：%s", gate.Error())
	case remote.DenyControlUnavailable, remote.DenyShutdown:
		return spec.Errorf(spec.ErrNotReady, "控制面不可用：%s", gate.Error())
	}
	return spec.Errorf(spec.ErrInternal, "控制门拒绝：%s", gate.Error())
}
