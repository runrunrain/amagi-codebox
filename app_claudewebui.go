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
	// WSL 目标（claude-webui design §5.1 WSL 扩展，2026-11）：claude 在 distro
	// 内运行，转录写在发行版文件系统的 Linux 路径下——Windows 侧的 env 推导
	// （USERPROFILE/HOME）指向错根。改经 PTY 捕获的实际 distro + 身份探测翻译
	// 为 UNC（\wsl.localhost\<distro>\...）供进程内数据面 server 经 9P 读取；
	// 解析失败仅 WARN 放弃平面（不阻断启动，与端口/token 分配失败同口径）。
	if spec.BootstrapMode == platform.BootstrapWSL {
		root, rootErr := a.resolveWSLClaudeTranscriptRoot(sessionID, spec)
		if rootErr != nil {
			a.Log.Warn("claudewebui", "WSL 会话转录根解析失败，本会话无 Web 平面（会话照常）", rootErr.Error())
			return
		}
		transcriptRoot = root
	}

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
// 开关开（默认 true）+ Manager 已接线。WSL 目标同样放行（2026-11 扩展：转录根
// 在 startClaudeWebPlane 内经 UNC 翻译解析，失败才降级为无平面）。Settings 未
// 接线（测试构造）视为关（fail-closed，不误起平面）。
func (a *App) claudeWebPlaneEnabled(spec platform.ResolvedLaunchSpec) bool {
	return a.ClaudeWebUI != nil &&
		a.Settings != nil && a.Settings.GetClaudeWebUIEnabled()
}

// wslClaudeIdentityFn 是 WSL distro 身份探测接缝（生产
// platform.WSLTargetIdentity——一次 wsl.exe 取 uid/$HOME/machine-id）；测试注入
// 以免真调 wsl.exe。
var wslClaudeIdentityFn = platform.WSLTargetIdentity

// claudePlaneWSLTargetFn 是 PTY 实际目标查询接缝（生产 a.Pty.SessionWSLTarget；
// 测试注入。模式对齐 app_wsldirpicker.go 的 sessionWSLTargetFn）。
var claudePlaneWSLTargetFn = func(a *App, sessionID string) (bool, string, error) {
	if a.Pty == nil {
		return false, "", fmt.Errorf("PTY 服务未接线，无法取得 WSL 会话的实际发行版")
	}
	return a.Pty.SessionWSLTarget(sessionID)
}

// isWindowsDrivePath 报告 s 是否 Windows 盘符绝对路径（C:\... 形态；大小写
// 不敏感，兼容正斜杠）。
func isWindowsDrivePath(s string) bool {
	if len(s) < 3 {
		return false
	}
	c := s[0]
	if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')) {
		return false
	}
	return s[1] == ':' && (s[2] == '\\' || s[2] == '/')
}

// resolveWSLClaudeTranscriptRoot 为 WSL claude 会话解析宿主侧可读的转录根
// （claude-webui design §5.1 WSL 扩展）。优先级：
//   - CLAUDE_CONFIG_DIR 显式重定向（WSL 防线剥离后存活的值）：Windows 盘符
//     路径（per-key /p 放行场景——CLI 内经翻译用 /mnt 形态，转录实际落在
//     Windows 侧）→ 原样使用（宿主原生可读）；Linux 绝对路径 → UNC 翻译；
//     相对路径/怪值 → 拒绝（fail closed，防错根静默空转录）；
//   - 缺省：distro 默认用户 home（wslClaudeIdentityFn 探测）+ /.claude → UNC 翻译。
//
// distro 取 PTY 进程启动瞬间捕获的实际目标（SessionWSLTarget，不可变），与
// 供给/启动链同一权威源；探测失败返回错误（调用方仅 WARN 降级）。
func (a *App) resolveWSLClaudeTranscriptRoot(sessionID string, spec platform.ResolvedLaunchSpec) (string, error) {
	wsl, distro, err := claudePlaneWSLTargetFn(a, sessionID)
	if err != nil {
		return "", fmt.Errorf("查询 WSL 会话目标失败: %w", err)
	}
	if !wsl {
		return "", fmt.Errorf("会话 %s 不是 WSL 目标", sessionID)
	}
	return wslClaudeTranscriptRootForDistro(distro, spec.Env.Variables)
}

// wslClaudeTranscriptRootForDistro 是 resolveWSLClaudeTranscriptRoot 的纯逻辑核
// （distro 已知形态，供 remote 启动链 executor 复用——executor 无 PTY 会话
// 可查，distro 由 DefaultWSLDistro(spec.Env) 与 PTY 同源预判）。env 为最终生效
// spec 的进程环境（含 WSL 防线剥离结果）。
func wslClaudeTranscriptRootForDistro(distro string, env []string) (string, error) {
	if dir := claude.LookupConfigDir(env); dir != "" {
		if isWindowsDrivePath(dir) {
			return dir, nil // 宿主原生路径，无需跨界
		}
		return platform.WSLToUNCPath(distro, dir)
	}
	ident := wslClaudeIdentityFn(distro)
	if !ident.OK || ident.Home == "" {
		return "", fmt.Errorf("WSL %s 身份探测失败（uid/$HOME），无法定位转录根", distro)
	}
	return platform.WSLToUNCPath(distro, ident.Home+"/.claude")
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
