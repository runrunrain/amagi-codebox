package main

// app_claudewebui.go — Claude Code 会话 Web 平面的 App 接线层（claude-webui
// design §5.3，2026-11）。
//
// 职责：
//   - startClaudeWebPlane/stopClaudeWebPlane：embedded claude 会话的平面起停
//     助手（LaunchSession 成功路径与退出/删除/清理 commit 点共用）；
//   - appClaudeInputPort：把 claudewebui.InputPort 适配到控制门的双车道写口
//     （桌面车道 DesktopInput/DesktopPasteChunk take-first；设备平面车道
//     DevicePlaneInput/DevicePlanePasteChunk 无夺权，design §5.6）。
//
// 全部助手 nil 安全：ClaudeWebUI / WebUI / control 未接线（测试构造或早退路径）
// 时零操作、不 panic。

import (
	"context"
	"errors"
	"fmt"
	"os"

	"amagi-codebox/internal/appmeta/claude"
	"amagi-codebox/internal/claudewebui"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/remote/contract"
)

// startClaudeWebPlane 为一个 embedded claude 会话启动 Web 平面（LaunchSession
// 在 SetPID 之后调用，design §5.3-2）。port/token 是启动前预分配的材料（见
// LaunchSession 的 claudeWebPlaneEnabled 门控 + AllocateFreePort/GenerateToken）；
// port<=0 或 token 空表示门控未放行或分配失败（仅 WARN 放弃平面，不阻断启动，
// 对齐 pi 口径 app.go:3935-3951）——本函数对这些输入零操作。
//
// 成功路径：ClaudeWebUI.Start 起进程内回环数据面 server → WebUI.RegisterSession
// 注册探测 tracker（状态 probing → available）。Start 失败仅 WARN 不注册——
// tracker 无记录即 unknown，前端无切换控件（design §5.3-2 的有意降级）。
func (a *App) startClaudeWebPlane(sessionID, claudeSID string, spec platform.ResolvedLaunchSpec, pid, port int, token string) {
	if a.ClaudeWebUI == nil || port <= 0 || token == "" {
		return
	}
	// 转录根从最终生效 spec 的进程 env 解析（design §5.1/R12）：CLAUDE_CONFIG_DIR
	// 优先 → HOME/USERPROFILE → 缺省 <homeDir>/.claude。受管 claude-accounts 会话
	// 在最终边界（managedClaudeIdentityBoundarySpec）已剥离用户自定义
	// CLAUDE_CONFIG_DIR，此处自然回落 home 根。homeDir 解析失败仅 WARN（空根 →
	// 空转录流，非错误；启动不受影响）。
	homeDir := ""
	if home, err := os.UserHomeDir(); err != nil {
		a.Log.Warn("claudewebui", "解析用户主目录失败，Web 平面转录根缺省为空", err.Error())
	} else {
		homeDir = home
	}
	transcriptRoot := claude.ClaudeConfigRoot(homeDir, spec.Env.Variables)

	boundPort, err := a.ClaudeWebUI.Start(claudewebui.PlaneConfig{
		SessionID:       sessionID,
		ClaudeSessionID: claudeSID,
		WorkDir:         spec.WorkDir,
		TranscriptRoot:  transcriptRoot,
		PID:             pid,
		Port:            port,
		Token:           token,
	}, appClaudeInputPort{a: a})
	if err != nil {
		a.Log.Warn("claudewebui", "claude Web 平面启动失败，本会话无 Web 平面", fmt.Sprintf("id=%s err=%v", sessionID, err))
		return
	}
	// webui.Service 为 pi 与 claudecode 两 AppType 共用，按 sessionID 键控
	//（design §4-D3：复用单实例状态机，代码路径无 appType 判定）。
	if a.WebUI != nil {
		a.WebUI.RegisterSession(sessionID, pid, boundPort, token)
	}
}

// stopClaudeWebPlane 收掉一个会话的 Web 平面（design §5.3-3/4）：ClaudeWebUI.Stop
// （幂等，listener 立即释放，R9）+ WebUI.Invalidate（tracker 落 ended，对齐 pi
// 退出 goroutine app.go:4025）。用于 PTY 退出 goroutine、removeWebUITracker、
// SetRunTerminalCleanup 与 SetPostRemoveCleanup 四处收尾点。
func (a *App) stopClaudeWebPlane(sessionID string) {
	if a.ClaudeWebUI != nil {
		a.ClaudeWebUI.Stop(sessionID)
	}
	if a.WebUI != nil {
		a.WebUI.Invalidate(sessionID)
	}
}

// claudeWebPlaneEnabled 判定 embedded claude 会话是否预分配 Web 平面材料
// （design §5.3-1 门控，LaunchSession 在 resolveEmbeddedLaunchSpec 成功后调用）：
// 开关开（默认 true）+ 非 WSL 目标（WSL 降级为无平面，design §2/§9-A4）+
// Manager 已接线。Settings 未接线（测试构造）视为关（fail-closed，不误起平面）。
func (a *App) claudeWebPlaneEnabled(spec platform.ResolvedLaunchSpec) bool {
	return a.ClaudeWebUI != nil &&
		a.Settings != nil && a.Settings.GetClaudeWebUIEnabled() &&
		spec.BootstrapMode != platform.BootstrapWSL
}

// appClaudeInputPort 把 claudewebui.InputPort 适配到 App 的控制门双车道写口
// （design §5.3/§5.6）。桌面车道与 App.PtyWrite/PtyWriteLarge 同口径（take-first：
// 本地 WebUI 输入 = 本地用户意图，与在 xterm 打字同语义）；设备平面车道走
// t6 的无夺权原语（写时校验该设备持控，失败 DenyNotController）。
type appClaudeInputPort struct{ a *App }

// gateCtx 返回控制门调用上下文：a.ctx（app 生命周期，生产中由 Startup 注入，
// 与 App.PtyWrite 同源）。请求作用域取消刻意不透传——已过门/Checkpoint 的
// PTY 写不随客户端断连半途取消；a.ctx 未注入（测试）时回落 Background。
func (p appClaudeInputPort) gateCtx() context.Context {
	if p.a != nil && p.a.ctx != nil {
		return p.a.ctx
	}
	return context.Background()
}

// controlReady 是四方法共用的 fail-closed 门（对齐 App.PtyWrite app.go:6052-6060：
// control nil/未就绪一律拒绝，绝不回落 raw PTY）。
func (p appClaudeInputPort) controlReady() bool {
	return p.a != nil && p.a.control != nil && p.a.control.IsReady()
}

// WriteInput 桌面车道单发（≤1KB，claudewebui server 侧已按阈值分流）。
func (p appClaudeInputPort) WriteInput(_ context.Context, sessionID string, data []byte) error {
	if !p.controlReady() {
		return mapClaudeLaneError(remote.ErrControlNotReady)
	}
	return mapClaudeLaneError(p.a.control.DesktopInput(p.gateCtx(), contract.SessionID(sessionID), data))
}

// PasteChunk 桌面车道长文本分块（1KB 逐块 + Checkpoint 栅栏）。
func (p appClaudeInputPort) PasteChunk(_ context.Context, sessionID string, data []byte) error {
	if !p.controlReady() {
		return mapClaudeLaneError(remote.ErrControlNotReady)
	}
	return mapClaudeLaneError(p.a.control.DesktopPasteChunk(p.gateCtx(), contract.SessionID(sessionID), data))
}

// DeviceWrite 设备平面车道单发（无夺权；写时校验 deviceID 持控）。
func (p appClaudeInputPort) DeviceWrite(_ context.Context, sessionID, deviceID string, data []byte) error {
	if !p.controlReady() {
		return mapClaudeLaneError(remote.ErrControlNotReady)
	}
	return mapClaudeLaneError(p.a.control.DevicePlaneInput(p.gateCtx(), contract.SessionID(sessionID), contract.DeviceID(deviceID), data))
}

// DevicePasteChunk 设备平面车道长文本分块（每块重校持控）。
func (p appClaudeInputPort) DevicePasteChunk(_ context.Context, sessionID, deviceID string, data []byte) error {
	if !p.controlReady() {
		return mapClaudeLaneError(remote.ErrControlNotReady)
	}
	return mapClaudeLaneError(p.a.control.DevicePlanePasteChunk(p.gateCtx(), contract.SessionID(sessionID), contract.DeviceID(deviceID), data))
}

// mapClaudeLaneError 把控制门错误桥接到 claudewebui 的车道哨兵（t2 input.go
// 文档化的适配器契约："App 适配器把 remote 控制门错误映射到本哨兵；server
// 统一映射 403 control.forbidden / 503 service.down"，design §6.1）。
//
// 映射纪律——只追加哨兵，不改写原语义（不吞不换）：
//   - errors.Join 保持原错误完整挂在 Unwrap 链上：errors.Is(err,
//     remote.ErrControlNotReady) 与 errors.As(err, *remote.ControlGateError)
//     在映射后依然成立（桌面车道"未就绪返回 remote.ErrControlNotReady"的
//     任务口径与 server 侧 503 信封同时满足）；
//   - 仅两类错误映射：ErrControlNotReady → ErrControlDown（503）；持控校验
//     失败 *ControlGateError{DenyNotController} → ErrControlForbidden（403，
//     design §5.6 的文档化情形）。其余错误（含 DenyDeviceRevoked 等其他门
//     拒绝与 raw PTY 写错误）原样透传，server 侧落 500 input.failed。
func mapClaudeLaneError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, remote.ErrControlNotReady) {
		return errors.Join(claudewebui.ErrControlDown, err)
	}
	var gateErr *remote.ControlGateError
	if errors.As(err, &gateErr) && gateErr.Kind == remote.DenyNotController {
		return errors.Join(claudewebui.ErrControlForbidden, err)
	}
	return err
}
