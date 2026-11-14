package main

// app_session_pty.go — Desktop session launch/stop/list/remove/clear facades,
// embedded PTY I/O/snapshot facades, clipboard image saving, and session lifecycle helpers.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"amagi-codebox/internal/aiproxy"
	"amagi-codebox/internal/cleanupstore"
	"amagi-codebox/internal/config"
	"amagi-codebox/internal/headroom"
	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/secrets"
	"amagi-codebox/internal/session"
	"amagi-codebox/internal/webui"

	"github.com/google/uuid"
)

const maxClipboardImageBytes = 20 * 1024 * 1024

var pngSignature = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}

func (a *App) GetSession(sessionID string) (session.SessionInfo, error) {
	for _, s := range a.GetSessions() {
		if s.ID == sessionID {
			return s, nil
		}
	}
	return session.SessionInfo{}, fmt.Errorf("session not found: %s", sessionID)
}

func (a *App) validateLaunchMode(mode string) error {
	return platform.ValidateLaunchRequest(a.platformCapabilities(), mode)
}

func embeddedDefaultLaunchMode(mode string) session.LaunchMode {
	launchMode := session.LaunchMode(mode)
	if launchMode == "" {
		return session.ModeEmbedded
	}
	return launchMode
}

func (a *App) resolveEmbeddedLaunchSpec(appType session.AppType, mode string, shellPath string, workDir string, requestedWorkDir string, env []string, args []string) (platform.ResolvedLaunchSpec, error) {
	if err := a.validateLaunchMode(mode); err != nil {
		return platform.ResolvedLaunchSpec{}, err
	}
	if a.aiProxy != nil {
		var err error
		env, err = a.aiProxy.ApplyEnv(env)
		if err != nil {
			return platform.ResolvedLaunchSpec{}, err
		}
	}
	req := platform.ResolveRequest{
		AppType:            string(appType),
		LaunchMode:         mode,
		RequestedShellPath: shellPath,
		WorkDir:            workDir,
		Env:                env,
		CLIArgs:            args,
		PTYCols:            120,
		PTYRows:            40,
	}
	// 自定义环境变量（envvars.json）在 Windows 侧经 MergeWithSystem 直接注入；
	// WSL 模式下 CLI 跑在发行版内，这些键必须显式进入 WSLENV 转发清单才能跨界
	//（前缀白名单只覆盖 ANTHROPIC_/OPENAI_/CLAUDE_/CODEX_/PI_/代理）。
	if a.EnvVars != nil {
		req.EnvForwardKeys = a.EnvVars.Keys()
		// P4-A2 per-key 放行门：显式标记 allowPathValue 的键，其盘符路径值
		// 不再剥离，并在 WSLENV 清单中获得 /p 路径翻译（保留键除外）。
		req.EnvPathFlagKeys = a.EnvVars.PathAllowKeys()
	}
	// WSL 环境变量防线（B3/T6 泛化，1.3.59 PI_* 剥离的统一收口）：会话确实落在
	// distro 内时，剥离「将经 WSLENV 转发、值为 Windows 盘符路径」的变量（五前缀
	// + 精确键 + envvars.json 自定义键），必须在 Resolve 内 appendWSLENVForwarding
	// 拼清单之前完成；剥离结果记 WARN 保持可观测。此处是全部内嵌启动（五 CLI）
	// 的单一漏斗，pi/omp 原地各一份的旧剥离已并入。P4-A2：显式标记 allowPathValue
	// 的键豁免剥离，改由转发侧加 /p 路径翻译标志；豁免键记 INFO 保持可观测。
	// 门条件经 launchTargetsWSLFn（launch_workdir.go）：与 planner.resolveLaunch
	// 共用同一谓词/接缝，两入口判定永不分叉。
	if launchTargetsWSLFn(mode, shellPath) {
		// pi 共享 Windows agent 根（WSL 只提供运行环境）：钉定 PI_CODING_AGENT_DIR
		// 为 Windows ~/.pi/agent 并登记为路径放行键（躲过剥离 + WSLENV /p 翻译）。
		// 同源裁定延伸：~/.agents/skills 经受管符号链接直连 Windows 技能根
		//（每进程每 distro 首次核验，见 app_wsl_shared_skills.go）。
		if appType == session.AppTypePi {
			env, req.EnvPathFlagKeys = launcher.ApplyWSLSharedPiAgentRoot(env, defaultPiAgentDir(), req.EnvPathFlagKeys)
			req.Env = env
			a.Log.Info("session", "WSL 内 pi 共享 Windows agent 根", defaultPiAgentDir())
			a.wslEnsureSharedAgentsSkillsFunnel(wslDefaultDistroFn(nil))
		}
		kept, stripped, allowedPath := launcher.StripWSLHostPathEnvWithAllow(env, req.EnvForwardKeys, req.EnvPathFlagKeys)
		if len(allowedPath) > 0 {
			a.Log.Info("session", "已放行跨界路径值环境变量（per-key 放行 + /p 路径翻译）", strings.Join(allowedPath, ","))
		}
		if len(stripped) > 0 {
			env = kept
			req.Env = env
			a.Log.Warn("session", "已剥离携带 Windows 路径值的跨界环境变量", strings.Join(stripped, ","))
		}
	}
	spec, err := a.cliResolver().Resolve(req)
	if err != nil {
		return spec, err
	}
	// 工作目录定谳（纯目标判据：以解析产出的 spec.BootstrapMode 为准，
	// 本层零探测）。WSL 目标：Linux 形态请求/默认目录原样保留（wsl.exe
	// --cd 在目标侧消费），显式非空宿主形态校验失败直接拒绝启动（不静默
	// 换工程）；非 WSL 目标维持入口宿主链产物（原生回退语义不变）。
	finalWorkDir, wdErr := resolveLaunchWorkDirForSpec(
		spec.BootstrapMode == platform.BootstrapWSL, requestedWorkDir, workDir,
		a.Paths.GetDefaultPath(),
		func(preserved string) {
			a.Log.Info("session", "WSL 会话保留请求的 Linux 工作目录（由 wsl.exe --cd 在目标侧消费）", preserved)
		},
	)
	if wdErr != nil {
		return platform.ResolvedLaunchSpec{}, wdErr
	}
	// 最终目录随 spec 返回；调用方同步 recipe，统一激活入口再以该目录
	// 发布会话记录，避免桌面面板/remote 快照仍指向预留时的宿主回退目录。
	spec.WorkDir = finalWorkDir
	return aiproxy.PinProxyEnvironment(spec)
}

// --- 多终端会话管理 ---

// LaunchSession 启动一个新的终端会话
func (a *App) LaunchSession(providerName, presetName string, mode string, workDir string, useHeadroom bool, shellPath string) (string, error) {
	if err := a.Config.RejectPiLoginLaunchRefs(providerName, presetName); err != nil {
		return "", err
	}
	a.Log.Info("session", "启动会话请求", fmt.Sprintf("provider=%s preset=%s mode=%s workDir=%s headroom=%v shell=%s", providerName, presetName, mode, workDir, useHeadroom, shellPath))

	// ---- terminal_presets 桥接 ----
	// 先尝试用 presetName 作为 terminal_preset 的 stable key 查找新体系
	tpProvider, tp, tpErr := a.Config.ResolveTerminalPreset(string(config.TerminalPresetAnthropic), presetName)
	tpFound := tpErr == nil && tp != nil
	if tpFound {
		// 新体系中 provider 以 tp.Provider 为准，参数中传入的 providerName 作为 fallback
		if tpProvider != "" {
			providerName = tpProvider
		}
		a.Log.Info("session", "命中 terminal_preset", fmt.Sprintf("key=%s provider=%s model=%s", presetName, tpProvider, tp.Model))
	}

	// SnapshotProvider：单次读锁内同时拷贝 provider 字段与 Presets map。桥接
	// 要求 provider 与 presets 同配置代际——GetProvider + GetPresets 两次独立
	// 加锁之间 SaveProvider/SavePreset 可并发改写，会拼出跨代混合快照（谛听
	// 终审 Minor-2）；且返回的 Presets 是非 nil 副本，可直接注入桥接条目。
	provider, err := a.Config.SnapshotProvider(providerName)
	if err != nil {
		a.Log.Error("session", "获取提供商失败", err.Error())
		return "", fmt.Errorf("get provider: %w", err)
	}
	// 若命中 terminal preset，将其桥接为旧 config.Preset 注入 provider 快照
	// 这样后续 BuildOverrides / Launch 走完整旧链路，model + parameters 全部生效
	if tpFound {
		converted := config.Preset{
			Name:        tp.Name,
			Model:       tp.Model,
			ModelHaiku:  tp.ModelHaiku,
			ModelSonnet: tp.ModelSonnet,
			ModelOpus:   tp.ModelOpus,
			Parameters:  tp.Parameters,
		}
		provider.Presets[presetName] = converted
		a.Log.Info("session", "已桥接 terminal_preset 到 provider.Presets", fmt.Sprintf("key=%s model=%s", presetName, tp.Model))
	}
	if !provider.IsAnthropicCompatible() {
		a.Log.Error("session", "ClaudeCode 需要 Anthropic 格式提供商", "provider="+providerName)
		return "", fmt.Errorf("provider %q is not Anthropic-compatible and cannot be used to launch ClaudeCode", providerName)
	}

	// OAuth 模式（Anthropic）：白板启动，不设置任何代理环境变量
	// 非 OAuth 模式：正常代理启动，设置 ANTHROPIC_API_KEY 和 ANTHROPIC_BASE_URL
	var apiKey, keySource string
	if provider.IsOAuthMode() {
		// OAuth 模式不需要 API 密钥，使用 Claude Code 原生 OAuth 认证
		apiKey = ""
		a.Log.Info("session", "使用 OAuth 认证（白板启动）", "provider="+providerName)
	} else {
		apiKey, keySource = a.getProviderAPIKey(providerName, *provider)
		if apiKey == "" {
			a.Log.Error("session", "未找到API密钥", "provider="+providerName)
			return "", fmt.Errorf("no API key found for provider %q", providerName)
		}
		a.Log.Info("session", "API密钥已获取",
			fmt.Sprintf("provider=%s source=%s key=%s len=%d",
				providerName, keySource, secrets.MaskKey(apiKey), len(apiKey)))
	}

	agentTeams := a.Config.GetAgentTeams()

	// 模型名称：由 BuildOverrides 从 provider.Presets[presetName] 中读取
	// （旧链路或已桥接的 terminal preset 均已注入 provider.Presets）
	preset, hasPreset := provider.Presets[presetName]
	model := provider.DefaultModel
	if hasPreset && preset.Model != "" {
		model = preset.Model
	}

	// 确定启动模式
	launchMode := session.LaunchMode(mode)
	if launchMode == "" {
		launchMode = session.ModeTerminal
	}
	if err := a.validateLaunchMode(string(launchMode)); err != nil {
		return "", err
	}

	// 工作目录单一校验/回退 choke point（launch_workdir.go）：
	// Clean+Abs+Stat 验证；无效时回退默认目录 → 用户 Home（原生目标/会话
	// 记录/外部终端模式的权威值）。WSL 目标的最终目录不在此定谳：原始请求
	// 随 spec 交给 resolveEmbeddedLaunchSpec，由 spec.BootstrapMode 后置定谳
	//（纯目标判据，零探测）。
	requestedWorkDir := workDir
	resolvedWorkDir, wdErr := a.resolveLaunchWorkDir(workDir)
	if wdErr != nil {
		return "", wdErr
	}
	workDir = resolvedWorkDir

	// Claude 订阅账号轮换（design §6.3）：在认证读取/配置写入与进程副作用
	// 之前取得共享凭据 admission。无池时也取过渡 reservation，但不改身份
	// 环境；无后端/平台不支持返回 nil。blocked（needs-reconcile/未结事务/池损坏/启动恢复未完成）拒绝
	// 受管启动；HOME 重定向到独立根的会话不持 lease（可证明不读 canonical
	// 身份文件）；受管 OAuth 登录启动要求 canonical 身份路径可证明。
	claudeCredentials, credErr := a.beginClaudeCredentialsClaim("launch-claude")
	if credErr != nil {
		return "", credErr
	}
	defer func() { claudeCredentials.release(a) }()
	if a.claudeCredentialsLaunchCheckpoint != nil {
		if err := a.claudeCredentialsLaunchCheckpoint(); err != nil {
			return "", err
		}
	}
	if claudeCredentials != nil {
		if override := managedHomeOverride(a.EnvVars.MergeWithSystem()); override != "" {
			if provider.IsOAuthMode() && !claudeCredentials.absent {
				// design §6.3：自定义 HOME 使 canonical 身份路径不可证明 → 拒绝
				// 受管启动并给出具体变量名，不修改用户全局 shell 文件。
				return "", fmt.Errorf("REVISION_CONFLICT: 受管 OAuth 启动拒绝：环境变量 %s 重定向了身份根目录，无法证明 canonical 凭据路径", override)
			}
			// API 模式 + HOME 重定向：独立根，不持凭据 lease（只阻塞换号的
			// 保守计入不适用于可证明独立根的进程）。
			a.Log.Info("claude-accounts", "HOME 重定向会话不持有凭据 lease（独立根）", override)
			claudeCredentials.release(a)
			claudeCredentials = nil
		}
	}

	// R5-002/R6-001: every Headroom-dependent launch crosses the uninstall
	// admission barrier before Headroom.Start, session creation, resolution, or
	// PTY/external startup. Embedded launches promote before PTY start; external
	// launches keep the admission through Launcher startup and atomically promote
	// it to an opaque external-run lifetime lease before reporting success.
	var headroomAdmission *remote.SharedLaunchAdmission
	if useHeadroom {
		// D1（Headroom×WSL×NAT 启动闸门，桌面链）：launcher 会给 Headroom 会话
		// 注入 ANTHROPIC_BASE_URL=http://localhost:8787，NAT 模式下 distro 内不可达，
		// 会话必然静默失败——必须在 Headroom.Start、admission 获取、会话预留与
		// PTY 启动等任何副作用之前拒绝。mirrored 遵循现有准入策略放行（与
		// aiproxy.PinProxyEnvironment 同源判定，此处放行不构成连通声明）。
		// remote/restart 链的对等闸门在 buildClaudePlan（planner 侧）；legacy
		// remote（/api/sessions 直调本函数）由本闸门覆盖。
		if err := headroomWSLNetworkAdmission(
			platform.EmbeddedLaunchTargetsWSL(string(launchMode), shellPath, nil) && platform.DefaultWSLDistro(nil) != "",
			os.Environ(),
		); err != nil {
			a.Log.Warn("headroom", "已拒绝 Headroom WSL 会话启动（NAT 网络模式）", err.Error())
			return "", err
		}
		if a.isExternalCleanupRecoveryBlocked() {
			return "", remote.ErrSharedServiceInUse
		}
		if a.sharedCoord == nil {
			return "", remote.ErrSharedServiceInUse
		}
		var admissionErr error
		headroomAdmission, admissionErr = a.sharedCoord.AcquireLaunchAdmission(remote.SharedServiceClaudeHeadroom)
		if admissionErr != nil {
			return "", fmt.Errorf("headroom launch admission: %w", admissionErr)
		}
		defer func() {
			a.sharedCoord.ReleaseLaunchAdmission(headroomAdmission)
		}()
	}

	// realBackend 是真实 upstream 的 base URL（如 api.anthropic.com）。
	realBackend := provider.EffectiveBaseURL("anthropic")
	if useHeadroom {
		// CLI → headroom 压缩(:8787) → 真实 API
		if !a.Headroom.IsRunning() {
			if err := a.Headroom.Start(realBackend); err != nil {
				a.Log.Error("headroom", "上下文压缩启动失败", err.Error())
				return "", fmt.Errorf("start headroom: %w", err)
			}
			a.Log.Info("headroom", "上下文压缩已启用并生效",
				fmt.Sprintf("CLI → headroom:127.0.0.1:8787 → %s", realBackend))
		}
		a.Launcher.SetHeadroomPort(headroom.DefaultPort)
	} else {
		// CLI → 真实 API.
		//
		// Single-tenant headroom semantics (documented decision, audit §4.2):
		// the desktop app runs one shared headroom compression proxy (:8787).
		// Launching a non-headroom ClaudeCode session intentionally tears the
		// shared proxy down so the new session talks to the real upstream
		// directly. This assumes a single active headroom consumer at a time; a
		// concurrent headroom-dependent session would have its :8787 upstream
		// cut. The Session/SessionInfo records do not carry a useHeadroom flag,
		// so the session manager cannot identify headroom-dependent sessions and
		// an active-session guard is not feasible without expanding the
		// frontend-visible session contract. We therefore document the
		// single-tenant assumption here and surface Stop failures instead of
		// swallowing them (the previous `_ = a.Headroom.Stop()` hid errors).
		if a.Headroom.IsRunning() {
			if err := a.Headroom.Stop(); err != nil {
				a.Log.Warn("headroom", "关闭共享上下文压缩代理失败", err.Error())
			}
		}
		a.Launcher.SetHeadroomPort(0)
	}

	// Reserve embedded identity hidden; external keeps the legacy local timing but
	// remains remoteEligible=false in the same Authority owner.
	sess, authorityReservation, err := a.reserveLaunchSession(session.AppTypeClaudeCode, providerName, presetName, model, launchMode, workDir)
	if err != nil {
		return "", err
	}
	authorityCommitted := authorityReservation == nil
	defer func() {
		if !authorityCommitted && authorityReservation != nil {
			a.Sessions.AbortCreate(authorityReservation)
		}
	}()
	a.Log.Info("session", "会话身份已预留", fmt.Sprintf("id=%s model=%s mode=%s", sess.ID, model, launchMode))

	// 根据模式选择启动方式
	if launchMode == session.ModeEmbedded {
		// 内嵌终端模式：使用 ConPTY，终端渲染在前端 xterm.js 中
		overrides := a.Launcher.BuildOverrides(*provider, presetName, apiKey, agentTeams)

		// 诊断：记录认证相关的环境变量覆盖
		for _, authVar := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"} {
			if v, ok := overrides[authVar]; ok {
				if v == "" {
					a.Log.Info("session", "环境变量覆盖", fmt.Sprintf("%s → [删除]", authVar))
				} else {
					a.Log.Info("session", "环境变量覆盖", fmt.Sprintf("%s → %s", authVar, secrets.MaskKey(v)))
				}
			}
		}

		// 注入自定义环境变量（自定义 > 系统，再被 overrides 覆盖）
		baseEnv := a.EnvVars.MergeWithSystem()
		env := launcher.BuildEnv(baseEnv, overrides)

		// 方案 R：为 embedded claudecode 会话注入 --session-id <uuid>，
		// 让 Claude Code 用 amagi-codebox 指定的 uuid 写 jsonl（与同 workDir 其他会话区分），
		// tracker 优先读"自己锁定的 jsonl"消除串扰，仅当锁定 jsonl 停滞才检测同目录最新跟随。
		claudeSID := uuid.NewString()
		a.Sessions.SetClaudeSessionID(sess.ID, claudeSID)
		a.Log.Info("session", "注入 Claude session-id", fmt.Sprintf("id=%s sid=%s", sess.ID, claudeSID))

		spec, err := a.resolveEmbeddedLaunchSpec(session.AppTypeClaudeCode, string(launchMode), shellPath, workDir, requestedWorkDir, env, []string{"--session-id", claudeSID})
		if err != nil {
			a.Sessions.MarkFailed(sess.ID, err.Error())
			return "", err
		}
		workDir = spec.WorkDir

		// Claude 订阅账号轮换（design §6.3）：WSL 目标（distro 独立根，宿主
		// Keychain 不可达）不持有凭据 lease；受管 OAuth 登录在最终 shell 后
		// 边界清除身份/认证覆盖变量（不删模型参数）。
		if spec.BootstrapMode == platform.BootstrapWSL && claudeCredentials != nil {
			a.Log.Info("claude-accounts", "WSL Claude 会话不持有凭据 lease（distro 独立根）", "id="+sess.ID)
			claudeCredentials.release(a)
			claudeCredentials = nil
		}
		if claudeCredentials != nil && !claudeCredentials.absent && provider.IsOAuthMode() {
			spec = managedClaudeIdentityBoundarySpec(spec)
		}

		// Claude Web 平面材料预分配（design §5.3-1）：开关开（claudeWebUIEnabled，
		// 默认 true）+ 非 WSL 目标 + Manager 已接线才分配端口/token。任何失败
		// 仅 WARN 放弃平面（本会话 unknown，前端无切换控件），不阻断启动——
		// 对齐 pi 的 env 注入口径（app.go:3935-3951）。转录根在 SetPID 后的
		// startClaudeWebPlane 内从最终 spec env 解析（含受管边界剥离结果）。
		claudePlanePort, claudePlaneToken := 0, ""
		if a.claudeWebPlaneEnabled(spec) {
			if p, pErr := webui.AllocateFreePort(); pErr == nil {
				claudePlanePort = p
			} else {
				a.Log.Warn("claudewebui", "claude Web 平面端口分配失败，本会话无 Web 平面", pErr.Error())
			}
			if tok, tErr := webui.GenerateToken(); tErr == nil {
				claudePlaneToken = tok
			} else {
				a.Log.Warn("claudewebui", "claude Web 平面 token 生成失败，本会话无 Web 平面", tErr.Error())
			}
		}

		// M-006: acquire the shared Headroom lease so the
		// coordinator guard is non-empty while this run is active.
		var sharedKinds []remote.SharedServiceKind
		if useHeadroom {
			sharedKinds = append(sharedKinds, remote.SharedServiceClaudeHeadroom)
		}
		var launchClaims []*remote.SharedLaunchAdmission
		if headroomAdmission != nil {
			launchClaims = append(launchClaims, headroomAdmission)
		}
		if claudeCredentials != nil {
			sharedKinds = append(sharedKinds, remote.SharedServiceClaudeCredentials)
			launchClaims = append(launchClaims, claudeCredentials.admission)
		}
		recipe := stableDesktopRecipe(session.AppTypeClaudeCode, workDir, providerName, presetName, model, shellPath, useHeadroom)
		pid, err := a.launchEmbeddedPTYWithClaims(authorityReservation, recipe, spec, launchClaims, sharedKinds...)
		if err != nil {
			a.Sessions.MarkFailed(sess.ID, err.Error())
			a.Log.Error("session", "PTY启动失败", fmt.Sprintf("id=%s err=%v", sess.ID, err))
			return "", fmt.Errorf("start pty: %w", err)
		}
		authorityCommitted = true
		a.Sessions.SetPID(sess.ID, pid)
		a.Log.Info("session", "PTY进程已启动", fmt.Sprintf("id=%s pid=%d", sess.ID, pid))

		// 方案 R：tracker 优先读"自己锁定的 jsonl"（上面注入的 --session-id），
		// 锁定 jsonl 停滞（用户 /resume 切走）超 60s 才检测同目录最新跟随。
		// 仅 claudecode 启动（opencode/codex 不写 ~/.claude/projects jsonl）。
		a.startTitleTracker(sess.ID, workDir)

		// Claude Web 平面（design §5.3-2）：PTY 成功 + 材料齐备 → 起进程内数据面
		// server 并注册 webui tracker（app_claudewebui.go；失败仅 WARN 不注册，
		// 状态 unknown → 前端无切换控件）。
		a.startClaudeWebPlane(sess.ID, claudeSID, spec, pid, claudePlanePort, claudePlaneToken)

		// 监控 PTY 进程退出
		go func(id string) {
			for a.Pty.IsRunning(id) {
				select {
				case <-a.ctx.Done():
					return
				case <-time.After(time.Second):
				}
			}
			a.Sessions.MarkExited(id)
			a.stopClaudeWebPlane(id)  // claude Web 平面随 PTY 退出收尾（Stop + Invalidate，design §5.3-3）
			a.releaseSharedLeases(id) // M-006: release shared leases on natural PTY exit
			a.Log.Info("session", "PTY进程已退出", "id="+id)
		}(sess.ID)

		return sess.ID, nil
	}

	// 外部终端/VSCode/Zed 模式：使用 Launcher。R6-001 keeps the
	// startup admission live until a successful process start can atomically
	// promote to an opaque external-run dependency lease.
	external := a.externalSessionLauncher()
	if external == nil {
		err := errors.New("external launcher is not initialized")
		a.Sessions.MarkFailed(sess.ID, err.Error())
		return "", err
	}
	var externalRun *remote.ExternalRunIdentity
	if headroomAdmission != nil || claudeCredentials != nil {
		if headroomAdmission != nil {
			if storeErr := a.requireExternalCleanupStore(); storeErr != nil {
				a.Sessions.MarkFailed(sess.ID, storeErr.Error())
				return "", fmt.Errorf("prepare durable external Claude cleanup: %w", storeErr)
			}
		}
		externalRun, err = a.sharedCoord.MintExternalRunIdentity(contract.SessionID(sess.ID))
		if err != nil {
			a.Sessions.MarkFailed(sess.ID, err.Error())
			return "", fmt.Errorf("prepare external Claude run: %w", err)
		}
	}
	// Claude 订阅账号轮换（design §6.3）：真正进程启动前复核 admission 指纹
	// 与当前池代际一致；不一致拒绝启动（重新发起即重新规划）。
	if credRecheckErr := a.recheckClaudeCredentialsClaim(claudeCredentials); credRecheckErr != nil {
		a.Sessions.MarkFailed(sess.ID, credRecheckErr.Error())
		return "", credRecheckErr
	}
	startGeneration, startErr := a.captureExternalProcessStartGeneration()
	if startErr != nil {
		a.Sessions.MarkFailed(sess.ID, startErr.Error())
		return "", startErr
	}
	a.externalOwnershipMu.Lock()
	if a.externalShutdown.Load() {
		a.externalOwnershipMu.Unlock()
		err := remote.ErrSharedCoordinatorClosed
		a.Sessions.MarkFailed(sess.ID, err.Error())
		return "", err
	}
	defer a.externalOwnershipMu.Unlock()
	ownershipKind := remote.SharedServiceKind(0) // no shared dependency
	if headroomAdmission != nil {
		ownershipKind = remote.SharedServiceClaudeHeadroom
	}
	attempt := a.beginExternalOwnershipAttempt(sess.ID, ownershipKind, startGeneration)
	defer a.endExternalOwnershipAttempt(attempt)
	var reservation cleanupstore.Reservation
	if headroomAdmission != nil {
		var reserveErr error
		reservation, reserveErr = a.reserveExternalProcessOwnership(sess.ID, remote.SharedServiceClaudeHeadroom)
		if reserveErr != nil {
			a.Sessions.MarkFailed(sess.ID, reserveErr.Error())
			return "", fmt.Errorf("prepare durable external Claude ownership: %w", reserveErr)
		}
		a.markExternalOwnershipReservation(attempt)
	}
	if !a.commitExternalProcessStart(attempt, startGeneration) {
		startErr = a.rejectExternalProcessStartAfterFence(sess.ID, ownershipKind, reservation)
		a.Sessions.MarkFailed(sess.ID, startErr.Error())
		return "", startErr
	}
	// F07（design §6.3 R3）：受管 OAuth 外部终端启动同样在最终执行边界清除
	// 身份/认证覆盖变量（与 embedded 的 managedClaudeIdentityBoundarySpec 同
	// 一键集）；旧 ComputeEnvOverrides 只清 ANTHROPIC_API_KEY/AUTH_TOKEN/
	// BASE_URL，不清 CLAUDE_CODE_* 身份变量。
	externalIdentityStrip := []string(nil)
	if claudeCredentials != nil && !claudeCredentials.absent && provider.IsOAuthMode() {
		externalIdentityStrip = managedClaudeIdentityEnvKeys
	}
	result, err := external.LaunchGuarded(
		sess.ID, *provider, presetName, apiKey, agentTeams, launchMode, workDir,
		func() error { return a.authorizeExternalRawStart(attempt, startGeneration) },
		externalIdentityStrip,
	)
	if err != nil {
		if errors.Is(err, remote.ErrSharedCoordinatorClosed) {
			startErr = a.rejectExternalProcessStartAfterFence(sess.ID, ownershipKind, reservation)
			a.Sessions.MarkFailed(sess.ID, startErr.Error())
			return "", startErr
		}
		completionErr := a.completeExternalProcessReservation(reservation)
		a.releaseSharedLeases(sess.ID)
		launchErr := fmt.Errorf("launch claude: %w", err)
		if completionErr != nil {
			launchErr = errors.Join(launchErr, completionErr)
		}
		a.Sessions.MarkFailed(sess.ID, launchErr.Error())
		a.Log.Error("session", "进程启动失败", fmt.Sprintf("id=%s err=%v", sess.ID, launchErr))
		return "", launchErr
	}
	// Register the concrete launcher binding immediately after successful start;
	// all later compensation uses this exact capability, never PID/session lookup.
	externalBindingKey, externalBindingRegistered, bindingErr := a.registerExternalProcessEvidence(result)
	if bindingErr != nil {
		closeUnregisteredExternalEvidence(context.Background(), result)
		a.Sessions.MarkFailed(sess.ID, bindingErr.Error())
		return "", bindingErr
	}
	externalAuthorityCommitted := false
	defer func() {
		if !externalAuthorityCommitted {
			a.compensateExternalProcessEvidence(context.Background(), result, externalBindingKey, externalBindingRegistered)
		}
	}()
	// Record PID and fsync OS identity immediately after start, before lease
	// promotion. Successful runs and failed promotions therefore share one
	// durable ownership chain across graceful Shutdown.
	a.markExternalOwnershipStarted(attempt)
	a.Sessions.SetPID(sess.ID, result.PID)
	if a.externalStartGeneration.Load() != startGeneration || a.externalShutdown.Load() {
		cleanupErr := a.handoffPostCommitShutdownStart(sess.ID, result.PID, ownershipKind, reservation, headroomAdmission, external)
		headroomAdmission = nil
		a.Sessions.MarkFailed(sess.ID, cleanupErr.Error())
		return "", cleanupErr
	}
	if headroomAdmission != nil {
		durableRecord, ownershipErr := a.persistExternalProcessOwnership(
			sess.ID, result.PID, remote.SharedServiceClaudeHeadroom, external,
		)
		if ownershipErr != nil {
			cleanup := a.registerExternalCleanup(durableRecord, reservation, false, headroomAdmission, external)
			headroomAdmission = nil
			stopErr := a.compensateExternalCleanup(cleanup)
			a.recordExternalCleanupAbandonment(remote.ExternalCleanupAbandonmentEvent{
				SessionID:          sess.ID,
				Kind:               remote.SharedServiceClaudeHeadroom,
				Reason:             remote.ExternalCleanupAbandonmentDurabilityHandoff,
				DurableReservation: true,
			})
			activationErr := fmt.Errorf("persist external Claude process ownership: %w", ownershipErr)
			if stopErr != nil {
				activationErr = errors.Join(activationErr, fmt.Errorf("compensate external Claude process: %w", stopErr))
			}
			a.Sessions.MarkFailed(sess.ID, activationErr.Error())
			return "", activationErr
		}
		if leaseErr := a.acquireAndRememberExternalSharedLease(sess.ID, externalRun, remote.SharedServiceClaudeHeadroom, headroomAdmission); leaseErr != nil {
			cleanup := a.registerExternalCleanup(durableRecord, cleanupstore.Reservation{}, true, headroomAdmission, external)
			headroomAdmission = nil
			stopErr := a.compensateExternalCleanup(cleanup)
			activationErr := fmt.Errorf("activate external Claude headroom lease: %w", leaseErr)
			if stopErr != nil {
				activationErr = errors.Join(activationErr, fmt.Errorf("compensate external Claude process: %w", stopErr))
			}
			a.Sessions.MarkFailed(sess.ID, activationErr.Error())
			return "", activationErr
		}
		a.rememberExternalDurableRun(durableRecord)
	}

	// Claude 订阅账号轮换（design §6.3）：外部会话成功启动后原子提升凭据
	// admission 为运行 lease（与 headroom 同一记账惯例；凭据无 durable
	// cleanup 需求——CodeBox 外存活进程的静止门禁由进程观察器兑底）。提升
	// 失败 → 刚启动的进程持有 canonical 凭据但租约未成立：保守停止进程。
	if claudeCredentials != nil {
		if leaseErr := a.acquireAndRememberExternalSharedLease(sess.ID, externalRun, remote.SharedServiceClaudeCredentials, claudeCredentials.admission); leaseErr != nil {
			stopErr := external.Stop(sess.ID)
			claudeCredentials.release(a)
			activationErr := fmt.Errorf("activate external Claude credentials lease: %w", leaseErr)
			if stopErr != nil {
				activationErr = errors.Join(activationErr, fmt.Errorf("compensate external Claude process: %w", stopErr))
			}
			a.Sessions.MarkFailed(sess.ID, activationErr.Error())
			return "", activationErr
		}
		claudeCredentials.release(a)
	}

	externalRecipe := stableDesktopRecipe(session.AppTypeClaudeCode, workDir, providerName, presetName, model, shellPath, useHeadroom)
	if err := a.finalizeExternalAuthority(sess.ID, result, externalBindingKey, externalBindingRegistered, externalRecipe); err != nil {
		a.Sessions.MarkFailed(sess.ID, err.Error())
		return "", err
	}
	externalAuthorityCommitted = true
	a.Log.Info("session", "进程已启动", fmt.Sprintf("id=%s pid=%d", sess.ID, result.PID))

	// 方案 R 降级（external 模式）：Launcher.Launch 不支持注入 --session-id，
	// ClaudeSessionID 留空，tracker 走方案 P（FindLatestActiveJSONL 取最新 mtime）。
	// 副作用：external 模式下同 workDir 多会话仍会指向同一最新 jsonl（边缘场景，主上已知）。
	a.startTitleTracker(sess.ID, workDir)

	// 监控进程退出。Release happens only after the Launcher reports terminal;
	// delayed observation is conservative (blocks mutation longer, never shorter).
	monitorCtx := a.ctx
	if monitorCtx == nil {
		monitorCtx = context.Background()
	}
	go func(id string, lifecycle externalLauncherPort) {
		for lifecycle.IsRunning(id) {
			select {
			case <-monitorCtx.Done():
				return
			case <-time.After(a.externalSessionPollInterval()):
			}
		}
		a.Sessions.MarkExited(id)
		a.releaseSharedLeases(id)
		a.Log.Info("session", "进程已退出", "id="+id)
	}(sess.ID, external)

	return sess.ID, nil
}

// StopSession 停止指定会话
// M-005: embedded PTY sessions are the control gate's domain — route through
// the gate and FAIL-CLOSED (never bypass to raw Pty.Close). When the control
// runtime is nil/not-ready, or the gate denies (including DenySessionNotFound
// for an unknown PTY session), the stop is rejected rather than falling back to
// a raw PTY close. External Launcher sessions (terminal/VSCode/Zed — no embedded
// PTY) remain Launcher-owned: Launcher is their legitimate authority, not a
// gate bypass.
func (a *App) StopSession(sessionID string) error {
	a.Log.Info("session", "停止会话", "id="+sessionID)

	if record, getErr := a.Sessions.Get(sessionID); getErr == nil && (record.Mode == session.ModeEmbedded || record.Mode == session.ModeTerminal) && a.sessionAdapter != nil {
		if a.control == nil || !a.control.IsReady() {
			return remote.ErrControlNotReady
		}
		ctx := a.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		if err := a.sessionAdapter.DesktopStopAuthoritative(ctx, contract.SessionID(sessionID)); err != nil {
			return err
		}
		a.releaseSharedLeases(sessionID)
		return nil
	}
	if a.Pty.IsRunning(sessionID) {
		if a.control == nil || !a.control.IsReady() {
			return remote.ErrControlNotReady
		}
		ctx := a.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		if err := a.control.DesktopStop(ctx, contract.SessionID(sessionID)); err != nil {
			return err
		}
		a.Sessions.MarkStopped(sessionID)
		a.releaseSharedLeases(sessionID)
		return nil
	}

	// Non-PTY external Launcher session: Launcher owns the process (not a gate
	// bypass). A failed stop retains the lease because the dependency may still
	// be live; successful/already-terminal stop releases it exactly once.
	external := a.externalSessionLauncher()
	if external != nil && external.IsRunning(sessionID) {
		if err := external.Stop(sessionID); err != nil {
			a.Log.Error("session", "停止会话失败", fmt.Sprintf("id=%s err=%v", sessionID, err))
			return err
		}
		a.Sessions.MarkStopping(sessionID)
		if external.IsRunning(sessionID) {
			// Stop was accepted but Launcher Wait has not proven terminal. Stopping
			// remains active/non-removable; monitor or cleanup reaper owns the lease.
			return nil
		}
	}
	a.completeExternalCleanupForSession(sessionID)
	a.Sessions.MarkStopped(sessionID)
	a.releaseSharedLeases(sessionID)
	return nil
}

// startTitleTracker 启动方案 P 的标题跟踪 goroutine。
//
// 仅用于 claudecode 会话（opencode/codex 不写 ~/.claude/projects jsonl）。
// tracker 退出条件双重保险：
//  1. a.ctx.Done()（app 关闭）；
//  2. mgr.GetStatus != Running（会话已被 MarkStopped/MarkExited/MarkFailed）。
//
// 不持有外部 cancel 句柄：依赖条件 2 在最多一个轮询周期内自动退出，避免泄漏。
// homeDir 由 os.UserHomeDir 获取；获取失败时记录日志但不启动 tracker（功能降级，非崩溃）。
func (a *App) startTitleTracker(amagiSessionID, workDir string) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		a.Log.Warn("session", "标题跟踪跳过：无法获取用户主目录", "err="+err.Error())
		return
	}
	go session.TrackTitle(a.ctx, a.Sessions, amagiSessionID, homeDir, workDir, a.Log)
}

// stopAllSessionsForShutdown is the unexported shutdown cleanup helper (design
// §10.3). It is called ONLY from App.Shutdown and never appears in the Wails
// Bind manifest. The authoritative shutdown fence is
// ControlRuntime.CloseForShutdown (called from Shutdown).
func (a *App) stopAllSessionsForShutdown() {
	ids := a.Sessions.GetRunning()
	external := a.externalSessionLauncher()
	for _, id := range ids {
		if external != nil {
			if err := external.Stop(id); err != nil || external.IsRunning(id) {
				continue // terminal owner/lease remains until monitor or reaper receipt
			}
		}
		a.completeExternalCleanupForSession(id)
		a.Sessions.MarkStopped(id)
		a.releaseSharedLeases(id)
	}
}

// GetSessions 获取所有会话列表
func (a *App) GetSessions() []session.SessionInfo {
	return a.Sessions.List()
}

// RemoveSession 删除已结束的会话记录
// M-005: route through the control gate so a stopped/running gate-managed
// session has its PTY closed AND its control tombstone cleaned by the desktop
// authority (DesktopRemove). FAIL-CLOSED: a running PTY session whose control
// runtime is unavailable is rejected rather than bypassed to the raw manager.
// A DenySessionNotFound (the gate does not manage the session) is the signal
// that the session is external/legacy → manager record cleanup.
func (a *App) RemoveSession(sessionID string) error {
	if record, getErr := a.Sessions.Get(sessionID); getErr == nil && (record.Mode == session.ModeEmbedded || record.Mode == session.ModeTerminal) && a.sessionAdapter != nil {
		if a.control == nil || !a.control.IsReady() {
			return remote.ErrControlNotReady
		}
		ctx := a.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		if err := a.sessionAdapter.DesktopRemoveAuthoritative(ctx, contract.SessionID(sessionID)); err != nil {
			return err
		}
		a.removeWebUITracker(sessionID) // Minor6：Remove commit 点同步清理 webui tracker
		return nil
	}
	if a.sessionAdapter == nil && a.control != nil && a.control.IsReady() {
		ctx := a.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		if err := a.control.DesktopRemove(ctx, contract.SessionID(sessionID)); err != nil {
			if !isControlUnknownSession(err) {
				return err
			}
		} else {
			a.Remote.DestroySessionInputLedger(contract.SessionID(sessionID))
			a.releaseSharedLeases(sessionID)
			a.removeWebUITracker(sessionID) // Minor6
			return nil
		}
	} else if a.Pty.IsRunning(sessionID) {
		return remote.ErrControlNotReady
	}
	// External Launcher / legacy record (PTY not running here): manager cleanup.
	// Release only after Manager.Remove confirms the record is non-running; a
	// failed remove must not drop a dependency from a potentially active run.
	if err := a.Sessions.Remove(sessionID); err != nil {
		return err
	}
	a.Remote.DestroySessionInputLedger(contract.SessionID(sessionID)) // M3-005: manager remove committed
	a.releaseSharedLeases(sessionID)
	a.removeWebUITracker(sessionID) // Minor6
	return nil
}

// removeWebUITracker 在会话删除 commit 点清理 webui 探测 tracker（Minor6）。
// WebUI 服务未接线（测试构造）时为空操作。claude Web 平面（design §5.3-4）
// 同 commit 点收掉进程内数据面 server（Stop + Invalidate；RemoveSession 随后
// 彻底摘除 tracker，双调用幂等）。
func (a *App) removeWebUITracker(sessionID string) {
	a.stopClaudeWebPlane(sessionID)
	if a.WebUI != nil {
		a.WebUI.RemoveSession(sessionID)
	}
}

// ClearStoppedSessions 清除所有已结束的会话
// M-005 + R3-005: route control-tombstone cleanup through the desktop authority
// (desktop authoritative batch semantics) before clearing the session manager,
// so the batch clear is gate-authorized rather than a raw manager mutation.
// Stopped sessions have no live PTY; this only removes their control entries.
//
// R3-005 fail-closed: control-MANAGED (embedded) stopped sessions are cleared
// through the gate ONLY when control is ready; if control is nil/not-ready they
// are SKIPPED (neither control entry nor manager record is touched) so the two
// stores can never diverge with a dangling control entry. When the Authority
// adapter is wired, both embedded and external sessions use exact process
// capabilities and receipt-keyed GC; only isolated legacy mode falls back.
// Returns the count of records authoritatively cleared.
func (a *App) ClearStoppedSessions() int {
	return a.clearStoppedSessionsDetailed().Cleared
}

// ClearStoppedSessionFailure is one per-ID failure from either the control Gate
// or Manager.Remove. Reason is diagnostic text and contains no session content.
type ClearStoppedSessionFailure struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// ClearStoppedSessionsResult is the truthful partial result of a batch clear.
// Cleared/ClearedIDs reflect actual successful Manager.Remove calls only.
// RetainedIDs includes gate-skipped and manager-failed records; Failed separates
// operational errors from ordinary skips such as a concurrent restart.
type ClearStoppedSessionsResult struct {
	Cleared     int                          `json:"cleared"`
	ClearedIDs  []string                     `json:"clearedIds"`
	RetainedIDs []string                     `json:"retainedIds"`
	Failed      []ClearStoppedSessionFailure `json:"failed"`
}

// removeStoppedSessionRecord calls the production Manager.Remove unless a test
// injects the narrow per-ID failure seam above.
func (a *App) removeStoppedSessionRecord(id string) error {
	var err error
	if a.sessionRemove != nil {
		err = a.sessionRemove(id)
	} else {
		err = a.Sessions.Remove(id)
	}
	if err != nil {
		return err
	}
	a.Remote.DestroySessionInputLedger(contract.SessionID(id)) // M3-005: manager remove committed (batch clear path)
	a.releaseSharedLeases(id)
	a.removeWebUITracker(id) // Minor6：批量清理 commit 点同步清理 webui tracker
	return nil
}

// clearStoppedSessionsDetailed clears stopped sessions and returns a typed
// partial result. With SessionAuthority wired, every embedded or external ID
// uses DesktopRemoveAuthoritative; only legacy fallback IDs call Manager.Remove
// directly. Failures are retained and never counted as cleared.
func (a *App) clearStoppedSessionsDetailed() ClearStoppedSessionsResult {
	result := ClearStoppedSessionsResult{}
	var eligible []string
	var controlManaged []string
	for _, s := range a.Sessions.List() {
		switch s.Status {
		case session.StatusRunning:
			continue
		case session.StatusStopping:
			result.RetainedIDs = append(result.RetainedIDs, s.ID)
			result.Failed = append(result.Failed, ClearStoppedSessionFailure{ID: s.ID, Reason: session.ErrSessionStopping.Error()})
			continue
		}
		if a.sessionAdapter != nil && (s.Mode == session.ModeEmbedded || s.Mode == session.ModeTerminal) {
			controlManaged = append(controlManaged, s.ID)
		} else if s.Mode == session.ModeEmbedded {
			controlManaged = append(controlManaged, s.ID)
		} else {
			eligible = append(eligible, s.ID)
		}
	}

	controlReady := a.control != nil && a.control.IsReady()
	if len(controlManaged) > 0 {
		if !controlReady {
			result.RetainedIDs = append(result.RetainedIDs, controlManaged...)
		} else {
			ctx := a.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			if a.sessionAdapter != nil {
				for _, id := range controlManaged {
					if err := a.sessionAdapter.DesktopRemoveAuthoritative(ctx, contract.SessionID(id)); err != nil {
						result.RetainedIDs = append(result.RetainedIDs, id)
						result.Failed = append(result.Failed, ClearStoppedSessionFailure{ID: id, Reason: err.Error()})
						continue
					}
					a.removeWebUITracker(id) // Minor6：adapter 批量成功路径同样清理 webui tracker
					result.ClearedIDs = append(result.ClearedIDs, id)
				}
			} else {
				ids := make([]contract.SessionID, len(controlManaged))
				for i, id := range controlManaged {
					ids[i] = contract.SessionID(id)
				}
				controlResult := a.control.DesktopClearStopped(ctx, ids)
				for _, perID := range controlResult.Results {
					id := string(perID.ID)
					switch perID.Status {
					case remote.DesktopClearCleared:
						eligible = append(eligible, id)
					case remote.DesktopClearErrored:
						result.RetainedIDs = append(result.RetainedIDs, id)
						result.Failed = append(result.Failed, ClearStoppedSessionFailure{ID: id, Reason: perID.Reason})
					default:
						result.RetainedIDs = append(result.RetainedIDs, id)
					}
				}
			}
		}
	}

	// R4-005 residual: Manager.Remove is authoritative for the returned count.
	// A control tombstone may already be gone, but a manager failure is reported
	// as retained/failed and never converted into a false success.
	for _, id := range eligible {
		if err := a.removeStoppedSessionRecord(id); err != nil {
			result.RetainedIDs = append(result.RetainedIDs, id)
			result.Failed = append(result.Failed, ClearStoppedSessionFailure{ID: id, Reason: err.Error()})
			if a.Log != nil {
				a.Log.Warn("session", "会话管理器清理失败", fmt.Sprintf("id=%s err=%v", id, err))
			}
			continue
		}
		result.ClearedIDs = append(result.ClearedIDs, id)
	}
	result.Cleared = len(result.ClearedIDs)
	return result
}

// LaunchCodexSession 启动 Codex CLI 终端会话
// modelName 非空时通过 -m 参数指定模型；providerID 非空时注入对应服务商的 API Key
// Codex 进程直接继承用户原始环境中的 Codex home，不做任何隔离或改写。
func (a *App) LaunchCodexSession(modelName string, providerID string, mode string, workDir string, shellPath string) (string, error) {
	if err := a.Config.RejectPiLoginLaunchRefs(providerID, modelName); err != nil {
		return "", err
	}
	a.Log.Info("session", "启动 Codex 会话请求", fmt.Sprintf("model=%s provider=%s mode=%s workDir=%s shell=%s", modelName, providerID, mode, workDir, shellPath))

	// ---- terminal_presets 桥接 ----
	// modelName 可能是 terminal_preset 的 stable key（形如 "provider/presetName"）
	var selectedPresetParams *config.Parameters
	tpProvider, tp, tpErr := a.Config.ResolveTerminalPreset(string(config.TerminalPresetOpenAI), modelName)
	tpFound := tpErr == nil && tp != nil
	if tpFound {
		if tpProvider != "" {
			providerID = tpProvider
		}
		modelName = tp.Model
		params := tp.Parameters
		selectedPresetParams = &params
		a.Log.Info("session", "Codex 命中 terminal_preset", fmt.Sprintf("key=%s provider=%s model=%s", modelName, tpProvider, tp.Model))
	}

	// ---- legacy provider preset fallback ----
	// 若未命中新体系，且 providerID 非空，检查是否是旧的 provider.Presets key。
	// 旧 key 如 "default" 不是模型名，需要从 preset.Model 中解析真实模型名。
	if !tpFound && providerID != "" {
		if provider, pErr := a.Config.GetProvider(providerID); pErr == nil {
			if preset, ok := provider.Presets[modelName]; ok {
				params := preset.Parameters
				selectedPresetParams = &params
				resolvedModel := preset.Model
				if resolvedModel == "" {
					resolvedModel = provider.DefaultModel
				}
				a.Log.Info("session", "Codex 命中旧 provider preset", fmt.Sprintf("key=%s presetModel=%s defaultModel=%s -> resolved=%s", modelName, preset.Model, provider.DefaultModel, resolvedModel))
				modelName = resolvedModel
			}
		}
	}

	// 确定启动模式
	launchMode := embeddedDefaultLaunchMode(mode)
	if err := a.validateLaunchMode(string(launchMode)); err != nil {
		return "", err
	}

	// 工作目录单一校验/回退 choke point（launch_workdir.go）：
	// Clean+Abs+Stat 验证；无效时回退默认目录 → 用户 Home（原生目标/会话
	// 记录/外部终端模式的权威值）。WSL 目标的最终目录不在此定谳：原始请求
	// 随 spec 交给 resolveEmbeddedLaunchSpec，由 spec.BootstrapMode 后置定谳
	//（纯目标判据，零探测）。
	requestedWorkDir := workDir
	resolvedWorkDir, wdErr := a.resolveLaunchWorkDir(workDir)
	if wdErr != nil {
		return "", wdErr
	}
	workDir = resolvedWorkDir

	launchSettings := codexLaunchSettings{
		Model: normalizeCodexModelName(modelName),
	}

	// R5-002: when Codex is configured to use global Headroom, acquire the
	// uninstall admission before config sync, session creation, resolution, or
	// PTY startup. Persisted Enabled remains true while uninstall has stopped the
	// process but has not yet removed the marker, closing that drain window too.
	var codexHeadroomAdmission *remote.SharedLaunchAdmission
	codexUsesGlobalHeadroom := a.CodexHeadroom != nil && a.CodexHeadroom.IsRunning()
	if a.Settings != nil && a.Settings.GetCodexGlobalHeadroom().Enabled {
		codexUsesGlobalHeadroom = true
	}
	if codexUsesGlobalHeadroom {
		// D6（桌面链 advisory）：codex 全局压缩的路由载体是宿主侧
		// ~/.codex/config.toml 的 openai_base_url 标记块，WSL codex 会话读取
		// 发行版侧独立配置，压缩不会生效——会话照常直连，但必须让「压缩未
		// 生效」可见，不默默让用户以为 8788 在压缩。不新增 Codex Headroom
		// 路由体系；remote 链显式选择压缩的拒绝在 buildCodexPlan。
		if platform.EmbeddedLaunchTargetsWSL(string(launchMode), shellPath, nil) && platform.DefaultWSLDistro(nil) != "" {
			a.Log.Warn("codex", "codex 全局压缩对 WSL 会话不生效", codexHeadroomWSLIneffectiveMsg())
		}
		if a.isExternalCleanupRecoveryBlocked() {
			return "", remote.ErrSharedServiceInUse
		}
		if a.sharedCoord == nil {
			return "", remote.ErrSharedServiceInUse
		}
		var admissionErr error
		codexHeadroomAdmission, admissionErr = a.sharedCoord.AcquireLaunchAdmission(remote.SharedServiceCodexHeadroom)
		if admissionErr != nil {
			return "", fmt.Errorf("codex headroom launch admission: %w", admissionErr)
		}
		defer func() {
			a.sharedCoord.ReleaseLaunchAdmission(codexHeadroomAdmission)
		}()
	}

	// 构建环境变量注入：若指定了 providerID，根据 Provider 的 Type 注入对应的环境变量。
	envOverrides := map[string]string{}
	codexProviderBaseURL := ""
	injectProviderEnv := func(pid string, provider *config.Provider) {
		apiKey, _ := a.getProviderAPIKey(pid, *provider)
		if apiKey == "" {
			return
		}
		if isOpenAIProvider(*provider) {
			envOverrides["OPENAI_API_KEY"] = apiKey
			if baseURL := provider.EffectiveBaseURL("openai"); baseURL != "" {
				envOverrides["OPENAI_BASE_URL"] = baseURL
				if isCustomCodexOpenAIBaseURL(baseURL) {
					codexProviderBaseURL = baseURL
				}
			}
		} else {
			envOverrides["ANTHROPIC_API_KEY"] = apiKey
			if baseURL := provider.EffectiveBaseURL("anthropic"); baseURL != "" {
				envOverrides["ANTHROPIC_BASE_URL"] = baseURL
			}
		}
	}
	if providerID != "" {
		// v1.3.13 重构时此处 Lock 后从未 Unlock（且持锁提前 return），一次
		// Codex 启动即永久持有 providerSyncMu，后续 Pi/OMP 启动与配置保存
		// 全部死锁。改为闭包 + defer 确保所有路径（含校验失败提前返回）解锁。
		if err := func() error {
			a.providerSyncMu.Lock()
			defer a.providerSyncMu.Unlock()
			provider, err := a.Config.GetProvider(providerID)
			if err != nil {
				return fmt.Errorf("resolve Codex provider %q: %w", providerID, err)
			}
			if !isOpenAIProvider(*provider) {
				return fmt.Errorf("Codex provider %q is not OpenAI-compatible", providerID)
			}
			launchSettings = resolveCodexLaunchSettings(*provider, launchSettings.Model)
			injectProviderEnv(providerID, provider)
			if baseURL := provider.EffectiveBaseURL("openai"); isCustomCodexOpenAIBaseURL(baseURL) && codexProviderBaseURL == "" {
				return fmt.Errorf("Codex provider %q requires an API key for custom endpoint %s", providerID, baseURL)
			}
			return nil
		}(); err != nil {
			return "", err
		}
	}
	if selectedPresetParams != nil {
		applyCodexPresetParameters(&launchSettings, *selectedPresetParams)
	}
	launchSettings.ProviderBaseURL = codexProviderBaseURL

	// Embedded identity stays hidden until PTY, Control and H1 activation finish.
	sess, authorityReservation, err := a.reserveLaunchSession(session.AppTypeCodex, "codex", providerID, launchSettings.Model, launchMode, workDir)
	if err != nil {
		return "", err
	}
	authorityCommitted := authorityReservation == nil
	defer func() {
		if !authorityCommitted && authorityReservation != nil {
			a.Sessions.AbortCreate(authorityReservation)
		}
	}()
	a.Log.Info("session", "Codex 会话身份已预留", fmt.Sprintf("id=%s model=%s mode=%s", sess.ID, launchSettings.Model, launchMode))

	// 调试日志：输出 envOverrides 注入情况
	envKeys := make([]string, 0, len(envOverrides))
	for k := range envOverrides {
		envKeys = append(envKeys, k)
	}
	a.Log.Info("codex", "envOverrides keys", fmt.Sprintf("%v", envKeys))

	// 内嵌终端模式：使用 ConPTY
	if launchMode == session.ModeEmbedded {
		// 注入自定义环境变量（自定义 > 系统，再被 envOverrides 覆盖）
		baseEnv := a.EnvVars.MergeWithSystem()
		env := launcher.BuildEnv(baseEnv, envOverrides)

		args := buildCodexCLIArgs(launchSettings)
		spec, err := a.resolveEmbeddedLaunchSpec(session.AppTypeCodex, string(launchMode), shellPath, workDir, requestedWorkDir, env, args)
		if err != nil {
			a.Sessions.MarkFailed(sess.ID, err.Error())
			return "", err
		}
		workDir = spec.WorkDir

		// M-006: a Codex session that routes through the global headroom (8788)
		// depends on it; acquire a lease so the toggle/uninstall coordinator guard is
		// non-empty for the run lifetime.
		var sharedKinds []remote.SharedServiceKind
		if codexHeadroomAdmission != nil {
			sharedKinds = append(sharedKinds, remote.SharedServiceCodexHeadroom)
		}
		recipe := stableDesktopRecipe(session.AppTypeCodex, workDir, providerID, "", launchSettings.Model, shellPath, codexHeadroomAdmission != nil)
		pid, err := a.launchEmbeddedPTYWithAdmission(authorityReservation, recipe, spec, codexHeadroomAdmission, sharedKinds...)
		if err != nil {
			a.Sessions.MarkFailed(sess.ID, err.Error())
			a.Log.Error("session", "Codex PTY启动失败", fmt.Sprintf("id=%s err=%v", sess.ID, err))
			return "", fmt.Errorf("start codex pty: %w", err)
		}
		authorityCommitted = true
		a.Sessions.SetPID(sess.ID, pid)
		a.Log.Info("session", "Codex PTY进程已启动", fmt.Sprintf("id=%s pid=%d", sess.ID, pid))

		go func(id string) {
			for a.Pty.IsRunning(id) {
				select {
				case <-a.ctx.Done():
					return
				case <-time.After(time.Second):
				}
			}
			a.Sessions.MarkExited(id)
			a.releaseSharedLeases(id) // M-006: release Codex headroom lease on natural exit
			a.Log.Info("session", "Codex PTY进程已退出", "id="+id)
		}(sess.ID)

		return sess.ID, nil
	}

	// 外部终端/VSCode/Zed 模式：使用 Launcher。Keep the Codex
	// Headroom admission through startup, then promote before returning success.
	external := a.externalSessionLauncher()
	if external == nil {
		err := errors.New("external launcher is not initialized")
		a.Sessions.MarkFailed(sess.ID, err.Error())
		return "", err
	}
	var externalRun *remote.ExternalRunIdentity
	if codexHeadroomAdmission != nil {
		if storeErr := a.requireExternalCleanupStore(); storeErr != nil {
			a.Sessions.MarkFailed(sess.ID, storeErr.Error())
			return "", fmt.Errorf("prepare durable external Codex cleanup: %w", storeErr)
		}
		identity, identityErr := a.sharedCoord.MintExternalRunIdentity(contract.SessionID(sess.ID))
		if identityErr != nil {
			a.Sessions.MarkFailed(sess.ID, identityErr.Error())
			return "", fmt.Errorf("prepare external Codex run: %w", identityErr)
		}
		externalRun = identity
	}
	startGeneration, startErr := a.captureExternalProcessStartGeneration()
	if startErr != nil {
		a.Sessions.MarkFailed(sess.ID, startErr.Error())
		return "", startErr
	}
	a.externalOwnershipMu.Lock()
	if a.externalShutdown.Load() {
		a.externalOwnershipMu.Unlock()
		err := remote.ErrSharedCoordinatorClosed
		a.Sessions.MarkFailed(sess.ID, err.Error())
		return "", err
	}
	defer a.externalOwnershipMu.Unlock()
	ownershipKind := remote.SharedServiceKind(0) // no shared dependency
	if codexHeadroomAdmission != nil {
		ownershipKind = remote.SharedServiceCodexHeadroom
	}
	attempt := a.beginExternalOwnershipAttempt(sess.ID, ownershipKind, startGeneration)
	defer a.endExternalOwnershipAttempt(attempt)
	var reservation cleanupstore.Reservation
	if codexHeadroomAdmission != nil {
		var reserveErr error
		reservation, reserveErr = a.reserveExternalProcessOwnership(sess.ID, remote.SharedServiceCodexHeadroom)
		if reserveErr != nil {
			a.Sessions.MarkFailed(sess.ID, reserveErr.Error())
			return "", fmt.Errorf("prepare durable external Codex ownership: %w", reserveErr)
		}
		a.markExternalOwnershipReservation(attempt)
	}
	if !a.commitExternalProcessStart(attempt, startGeneration) {
		startErr = a.rejectExternalProcessStartAfterFence(sess.ID, ownershipKind, reservation)
		a.Sessions.MarkFailed(sess.ID, startErr.Error())
		return "", startErr
	}
	beforeRawStart := func() error { return a.authorizeExternalRawStart(attempt, startGeneration) }
	var result *launcher.LaunchResult
	if argsLauncher, ok := external.(codexArgsExternalLauncherPort); ok {
		result, err = argsLauncher.LaunchCodexArgsGuarded(
			sess.ID, buildCodexCLIArgs(launchSettings), launchMode, workDir, envOverrides, beforeRawStart,
		)
	} else {
		result, err = external.LaunchCodexGuarded(
			sess.ID, launchSettings.Model, launchMode, workDir, envOverrides, beforeRawStart,
		)
	}
	if err != nil {
		if errors.Is(err, remote.ErrSharedCoordinatorClosed) {
			startErr = a.rejectExternalProcessStartAfterFence(sess.ID, ownershipKind, reservation)
			a.Sessions.MarkFailed(sess.ID, startErr.Error())
			return "", startErr
		}
		completionErr := a.completeExternalProcessReservation(reservation)
		a.releaseSharedLeases(sess.ID)
		launchErr := fmt.Errorf("launch codex: %w", err)
		if completionErr != nil {
			launchErr = errors.Join(launchErr, completionErr)
		}
		a.Sessions.MarkFailed(sess.ID, launchErr.Error())
		a.Log.Error("session", "Codex 进程启动失败", fmt.Sprintf("id=%s err=%v", sess.ID, launchErr))
		return "", launchErr
	}
	externalBindingKey, externalBindingRegistered, bindingErr := a.registerExternalProcessEvidence(result)
	if bindingErr != nil {
		closeUnregisteredExternalEvidence(context.Background(), result)
		a.Sessions.MarkFailed(sess.ID, bindingErr.Error())
		return "", bindingErr
	}
	externalAuthorityCommitted := false
	defer func() {
		if !externalAuthorityCommitted {
			a.compensateExternalProcessEvidence(context.Background(), result, externalBindingKey, externalBindingRegistered)
		}
	}()
	a.markExternalOwnershipStarted(attempt)
	a.Sessions.SetPID(sess.ID, result.PID)
	if a.externalStartGeneration.Load() != startGeneration || a.externalShutdown.Load() {
		cleanupErr := a.handoffPostCommitShutdownStart(sess.ID, result.PID, ownershipKind, reservation, codexHeadroomAdmission, external)
		codexHeadroomAdmission = nil
		a.Sessions.MarkFailed(sess.ID, cleanupErr.Error())
		return "", cleanupErr
	}
	if codexHeadroomAdmission != nil {
		durableRecord, ownershipErr := a.persistExternalProcessOwnership(
			sess.ID, result.PID, remote.SharedServiceCodexHeadroom, external,
		)
		if ownershipErr != nil {
			cleanup := a.registerExternalCleanup(durableRecord, reservation, false, codexHeadroomAdmission, external)
			codexHeadroomAdmission = nil
			stopErr := a.compensateExternalCleanup(cleanup)
			a.recordExternalCleanupAbandonment(remote.ExternalCleanupAbandonmentEvent{
				SessionID:          sess.ID,
				Kind:               remote.SharedServiceCodexHeadroom,
				Reason:             remote.ExternalCleanupAbandonmentDurabilityHandoff,
				DurableReservation: true,
			})
			activationErr := fmt.Errorf("persist external Codex process ownership: %w", ownershipErr)
			if stopErr != nil {
				activationErr = errors.Join(activationErr, fmt.Errorf("compensate external Codex process: %w", stopErr))
			}
			a.Sessions.MarkFailed(sess.ID, activationErr.Error())
			return "", activationErr
		}
		if leaseErr := a.acquireAndRememberExternalSharedLease(sess.ID, externalRun, remote.SharedServiceCodexHeadroom, codexHeadroomAdmission); leaseErr != nil {
			cleanup := a.registerExternalCleanup(durableRecord, cleanupstore.Reservation{}, true, codexHeadroomAdmission, external)
			codexHeadroomAdmission = nil
			stopErr := a.compensateExternalCleanup(cleanup)
			activationErr := fmt.Errorf("activate external Codex headroom lease: %w", leaseErr)
			if stopErr != nil {
				activationErr = errors.Join(activationErr, fmt.Errorf("compensate external Codex process: %w", stopErr))
			}
			a.Sessions.MarkFailed(sess.ID, activationErr.Error())
			return "", activationErr
		}
		a.rememberExternalDurableRun(durableRecord)
	}

	externalRecipe := stableDesktopRecipe(session.AppTypeCodex, workDir, providerID, "", launchSettings.Model, shellPath, codexHeadroomAdmission != nil)
	if err := a.finalizeExternalAuthority(sess.ID, result, externalBindingKey, externalBindingRegistered, externalRecipe); err != nil {
		a.Sessions.MarkFailed(sess.ID, err.Error())
		return "", err
	}
	externalAuthorityCommitted = true
	a.Log.Info("session", "Codex 进程已启动", fmt.Sprintf("id=%s pid=%d", sess.ID, result.PID))

	monitorCtx := a.ctx
	if monitorCtx == nil {
		monitorCtx = context.Background()
	}
	go func(id string, lifecycle externalLauncherPort) {
		for lifecycle.IsRunning(id) {
			select {
			case <-monitorCtx.Done():
				return
			case <-time.After(a.externalSessionPollInterval()):
			}
		}
		a.Sessions.MarkExited(id)
		a.releaseSharedLeases(id)
		a.Log.Info("session", "Codex 进程已退出", "id="+id)
	}(sess.ID, external)

	return sess.ID, nil
}

// LaunchPiSession 启动 Pi 会话：API 服务商写入托管 models.json；
// pi-login 命名空间的专用预设则使用标准 auth.json 与原生 provider ID，
// 不生成托管配置或注入认证 token。启动时再检查登录来源，退出登录不回退。
func (a *App) LaunchPiSession(modelName string, providerID string, mode string, workDir string, shellPath string) (string, error) {
	loginPresetRef := modelName
	login, loginErr := resolvePiLoginLaunch(a.Config, modelName, providerID)
	if loginErr != nil {
		return "", loginErr
	}
	var loginPreset *config.PiLoginPreset
	if login != nil {
		loginPreset = login.Preset
	}
	if loginPreset != nil {
		providerID = config.PiLoginProviderID(loginPreset.Provider)
		modelName = loginPreset.Model
	}
	a.Log.Info("session", "启动 Pi 会话请求", fmt.Sprintf("model=%s provider=%s mode=%s workDir=%s shell=%s", modelName, providerID, mode, workDir, shellPath))

	// ---- terminal_presets 桥接 ----
	// modelName 可能是 terminal_preset 的 stable key（形如 "provider/presetName"）。
	// 回退语义：先查 openai 桶（pi 原生桶，撞名优先），miss 后仅当 anthropic 桶
	// 预设带 HarnessSync 标记（opt-in）才命中；unmarked anthropic 预设不解析。
	tpProvider, tp, tpErr := a.Config.ResolvePiOmpTerminalPreset(modelName)
	tpFound := loginPreset == nil && tpErr == nil && tp != nil
	// presetParams 收集命中的预设参数（contextWindow/thinking/effort/maxTokens），
	// 供后续写入 pi models.json 的 model 配置与解析 --thinking 级别。
	var presetParams config.Parameters
	if tpFound {
		if tpProvider != "" {
			providerID = tpProvider
		}
		modelName = tp.Model
		presetParams = tp.Parameters
		a.Log.Info("session", "Pi 命中 terminal_preset", fmt.Sprintf("key=%s provider=%s model=%s", modelName, tpProvider, tp.Model))
	}

	// ---- legacy provider preset fallback ----
	// 若未命中新体系，且 providerID 非空，检查是否是旧的 provider.Presets key。
	if loginPreset == nil && !tpFound && providerID != "" {
		if provider, pErr := a.Config.GetProvider(providerID); pErr == nil {
			if preset, ok := provider.Presets[modelName]; ok {
				resolvedModel := preset.Model
				if resolvedModel == "" {
					resolvedModel = provider.DefaultModel
				}
				a.Log.Info("session", "Pi 命中旧 provider preset", fmt.Sprintf("key=%s presetModel=%s defaultModel=%s -> resolved=%s", modelName, preset.Model, provider.DefaultModel, resolvedModel))
				modelName = resolvedModel
				presetParams = preset.Parameters
			}
		}
	}

	// 确定启动模式
	launchMode := embeddedDefaultLaunchMode(mode)
	if err := a.validateLaunchMode(string(launchMode)); err != nil {
		return "", err
	}

	// 工作目录单一校验/回退 choke point（launch_workdir.go）：
	// Clean+Abs+Stat 验证；无效时回退默认目录 → 用户 Home（原生目标/会话
	// 记录/外部终端模式的权威值）。WSL 目标的最终目录不在此定谳：原始请求
	// 随 spec 交给 resolveEmbeddedLaunchSpec，由 spec.BootstrapMode 后置定谳
	//（纯目标判据，零探测）。
	requestedWorkDir := workDir
	resolvedWorkDir, wdErr := a.resolveLaunchWorkDir(workDir)
	if wdErr != nil {
		return "", wdErr
	}
	workDir = resolvedWorkDir

	// WSL 终端模式判定（Bug B）：embedded 模式下解析器会把 WSL shell 的会话
	// 跑在 distro 内（resolver.go 的 WSL 分支，镜像判定 platform.EmbeddedLaunchTargetsWSL）。
	// 命中时后续把 models.json 写到 WSL 侧 agent root、剥离 Windows 路径值的
	// PI_* 环境变量、并探测 fd/ripgrep；非 WSL 路径行为不变。
	wslDistro := ""
	if platform.EmbeddedLaunchTargetsWSL(string(launchMode), shellPath, nil) {
		wslDistro = platform.DefaultWSLDistro(nil)
		if wslDistro == "" {
			a.Log.Warn("pi", "WSL shell 会话但无可用发行版，配置仍写 Windows 侧", "")
		}
	}

	// Claude 订阅账号轮换（design §6.3 + 任务 F）：Pi 会话使用 canonical
	// ~/.pi/agent 根（启动时 provider 无关，运行中可 /login），受管时在认证
	// 读取/配置写入与进程副作用之前取得共享凭据 admission（不阻塞运行，
	// 只阻塞换号）。WSL Pi 登录会话消费 canonical 凭据的 distro 副本，v1
	// 不支持：返回明确错误，不动现有 WSL 同步逻辑。
	claudeCredentials, credErr := a.beginClaudeCredentialsClaim("launch-pi")
	if credErr != nil {
		return "", credErr
	}
	defer func() { claudeCredentials.release(a) }()
	// WSL 内 pi 共享 Windows agent 根（WSL 只提供运行环境）：登录凭据与 Windows 侧
	// 同一份 auth.json，Claude 订阅账号管理与宿主侧 Pi 登录同语义，不再拒绝。

	launchSettings := piLaunchSettings{
		Model: strings.TrimSpace(modelName),
	}

	// 构建环境变量注入 + pi models.json 配置写入。
	//
	// Pi 从标准用户目录 ~/.pi/agent 读取 models.json 的 providers 配置。
	// amagi 把当前选中的 provider 翻译成 pi 的一个自定义命名 provider（"amagi-<name>"），
	// 合并写入 ~/.pi/agent/models.json。启动时显式移除 PI_CODING_AGENT_DIR，
	// 避免系统或 CodeBox 自定义环境里的旧值又把 Pi 导向独立副本。
	// 这样 baseURL/api/apiKey 都正确注入，第三方 Anthropic/OpenAI 兼容 provider（如 GLM）
	// 会正确路由到其真实 endpoint，不再误打 pi 内置 anthropic/openai 的官方地址。
	//
	// 与 Claude Code（ANTHROPIC_BASE_URL env）/ Codex（config.toml）/ OpenCode
	//（OPENCODE_CONFIG_CONTENT env）同构：amagi 的 provider 配置作为通用底层，
	// 各引擎各自翻译消费。
	envOverrides := map[string]string{
		// BuildEnv 约定：空值表示从子进程环境中删除该变量。
		"PI_CODING_AGENT_DIR": "",
	}
	// 内嵌/外部 Pi 会话默认跳过 pi 启动期网络操作（pi.dev 模型目录刷新、
	// 版本检查、包管理网络）。这些操作在网络或本地代理抖动时会把启动拖到
	// 数十秒甚至挂起（用户表现为"终端一直加载不完"）；PI_OFFLINE 不影响
	// 模型推理与 amagi MCP。用户在环境变量面板显式配置 PI_OFFLINE 时尊重其值。
	if _, explicit := a.EnvVars.Get("PI_OFFLINE"); !explicit {
		envOverrides["PI_OFFLINE"] = "1"
	}
	if loginPreset != nil {
		launchSettings = piLaunchSettings{Provider: loginPreset.Provider, Model: loginPreset.Model, Thinking: loginPreset.Thinking}
		// R2：显式钉住 canonical agent root（而非删除 PI_CODING_AGENT_DIR），
		// 自定义 HOME/USERPROFILE 无法再把 Pi 导向另一份 auth.json。该 overrides
		// 同时覆盖 embedded（BuildEnv）与 external（Launcher.LaunchPi）两条路径。
		// A2 解除（主上裁定 2026-10-16）：WSL spec 生成后由
		// piLoginWSLReAnchorSpec 统一剥离该钉定（Windows 路径跨界是历史垃圾
		// 目录事故形态），不在 env 构建期分叉——保持镜像/spec 判定不一致时的
		// 语义确定性（spec 为唯一权威）。
		for key, value := range piLoginEnvOverrides(login.AgentDir, claudeLoginManagedExtraKeys(claudeCredentials)...) {
			envOverrides[key] = value
		}
		// 登录预设 contextWindow 覆写：Pi 登录模型没有预设参数面，窗口只能经
		// models.json 的 modelOverrides 注入（providers.<provider>.modelOverrides
		// .<model>.contextWindow，深合并保留兄弟配置）。写 canonical root 后，
		// Leader 与 amagi 子 agent（PI_CODING_AGENT_DIR 透传）读同一份生效。
		// 与后台 provider sync 互斥（同一 models.json 读改写）；失败仅告警不阻断
		//（回退 Pi 内置目录窗口，启动语义不受影响）。
		a.providerSyncMu.Lock()
		overrideErr := launcher.UpsertPiLoginModelOverride(login.AgentDir, loginPreset.Provider, loginPreset.Model, loginPreset.ContextWindow, loginPreset.MaxTokens)
		a.providerSyncMu.Unlock()
		if overrideErr != nil {
			a.Log.Warn("pi", "写入登录预设 contextWindow 覆写失败（回退 Pi 内置窗口）", overrideErr.Error())
		} else if loginPreset.ContextWindow > 0 || loginPreset.MaxTokens > 0 {
			a.Log.Info("pi", "登录预设模型额度覆写已写入", fmt.Sprintf("provider=%s model=%s contextWindow=%d maxTokens=%d -> %s/models.json",
				loginPreset.Provider, loginPreset.Model, loginPreset.ContextWindow, loginPreset.MaxTokens, login.AgentDir))
		}
	}
	// WSL 内 pi 共享 Windows agent 根：models.json 直接写 Windows 侧，不向 distro 分发。
	if loginPreset == nil && providerID != "" {
		// 与 LaunchCodexSession/LaunchOmpSession 对称：models.json 写入与后台
		// provider sync（syncProvidersToHarnessesLocked）互斥，防止并发改写。
		// 锁纪律（S1 diting F1）：临界区整体收进 writePiProviderModelsAtLaunch
		//（内部 defer Unlock）——任何 return 路径不再
		// 泄漏锁；v1.3.13 事故同位置的结构性防再犯。
		var writeErr error
		launchSettings, writeErr = a.writePiProviderModelsAtLaunch(providerID, launchSettings, presetParams, envOverrides)
		if writeErr != nil {
			return "", writeErr
		}
	}

	// Preserve the login stable key for desktop restart, not just its resolved model.
	if loginPreset != nil {
		modelName = loginPresetRef
	}
	// Embedded identity stays hidden until its exact PTY run is active.
	sess, authorityReservation, err := a.reserveLaunchSession(session.AppTypePi, "pi", providerID, launchSettings.Model, launchMode, workDir)
	if err != nil {
		return "", err
	}
	authorityCommitted := authorityReservation == nil
	defer func() {
		if !authorityCommitted && authorityReservation != nil {
			a.Sessions.AbortCreate(authorityReservation)
		}
	}()
	a.Log.Info("session", "Pi 会话身份已预留", fmt.Sprintf("id=%s provider=%s model=%s mode=%s", sess.ID, launchSettings.Provider, launchSettings.Model, launchMode))

	// 调试日志：输出 envOverrides 注入情况
	envKeys := make([]string, 0, len(envOverrides))
	for k := range envOverrides {
		envKeys = append(envKeys, k)
	}
	a.Log.Info("pi", "envOverrides keys", fmt.Sprintf("%v", envKeys))

	// 内嵌终端模式：使用 ConPTY
	if launchMode == session.ModeEmbedded {
		// T-1.5：为 embedded pi 注入 webui 端口（契约 v1.0.2 §7.2 通道 1）。
		// 仅 appType=pi 的本路径注入，不影响其他 CLI 与外部终端模式。
		// 分配失败不阻断启动：扩展无 env 时自选端口并写注册表（§7.2 通道 2），
		// codebox 探测侧走注册表回退发现。
		webuiPort := 0
		if p, pErr := webui.AllocateFreePort(); pErr == nil {
			webuiPort = p
			envOverrides["AMAGI_WEBUI_PORT"] = strconv.Itoa(p)
		} else {
			a.Log.Warn("webui", "webui 空闲端口分配失败，走注册表回退发现", pErr.Error())
		}

		// v1.0.2：生成每会话 capability token 并经 AMAGI_WEBUI_TOKEN 注入；
		// 与端口注入解耦（端口分配失败也注入 token——扩展自选端口仍强制校验，
		// 探测侧可从注册表条目补读）。生成失败不阻断启动，探测退化为无 token。
		webuiToken := ""
		if tok, tErr := webui.GenerateToken(); tErr == nil {
			webuiToken = tok
			envOverrides["AMAGI_WEBUI_TOKEN"] = tok
		} else {
			a.Log.Warn("webui", "webui capability token 生成失败，探测不带 token", tErr.Error())
		}

		// 注入自定义环境变量（自定义 > 系统，再被 envOverrides 覆盖）
		baseEnv := a.EnvVars.MergeWithSystem()
		env := launcher.BuildEnv(baseEnv, envOverrides)
		if wslDistro != "" {
			// Windows 路径值变量的剥离已上移到 resolveEmbeddedLaunchSpec（全引擎
			// 统一 WSL 防线，覆盖五前缀 + 精确键 + 自定义键）。
			// fd/ripgrep 一次探测 + WARN（PI_OFFLINE=1 保留转发，避免 pi 启动时联网
			// 下载工具卡住；缺工具时给出安装引导）。
			a.warnWSLSearchToolsOnce(wslDistro)
		}

		args := []string{}
		if launchSettings.Provider != "" {
			args = append(args, "--provider", launchSettings.Provider)
		}
		if launchSettings.Model != "" {
			args = append(args, "--model", launchSettings.Model)
		}
		if launchSettings.Thinking != "" {
			args = append(args, "--thinking", launchSettings.Thinking)
		}
		spec, err := a.resolveEmbeddedLaunchSpec(session.AppTypePi, string(launchMode), shellPath, workDir, requestedWorkDir, env, args)
		if err != nil {
			a.Sessions.MarkFailed(sess.ID, err.Error())
			return "", err
		}
		workDir = spec.WorkDir

		// WSL 内 pi 共享 Windows agent 根：凭据 lease、auth.json 与 Windows 侧同一份，
		// 不再按 spec 目标释放 lease，也不再做 distro 凭据副本同步。
		if loginPreset != nil {
			if spec.BootstrapMode == platform.BootstrapWSL {
				// WSL：PI_CODING_AGENT_DIR 已由 resolveEmbeddedLaunchSpec 钉到 Windows 根
				// （/p 翻译）；最终边界只剥离会被登录会话误用的凭据变量。
				spec = piLoginWSLReAnchorSpec(spec, login.AgentDir, claudeLoginManagedExtraKeys(claudeCredentials)...)
			} else {
				// R3：POSIX shell startup 文件在 CLI 之前运行，可能重导出覆盖变量；
				// 在最终执行边界重新锚定 canonical root 并剥离凭据。受管时追加
				// 剥离 Claude 身份/认证覆盖变量（design §6.3）。
				spec = piLoginReAnchorSpec(spec, login.AgentDir, claudeLoginManagedExtraKeys(claudeCredentials)...)
			}
		}
		recipe := stableDesktopRecipe(session.AppTypePi, workDir, providerID, modelName, launchSettings.Model, shellPath, false)
		var piClaims []*remote.SharedLaunchAdmission
		var piSharedKinds []remote.SharedServiceKind
		if claudeCredentials != nil {
			piSharedKinds = append(piSharedKinds, remote.SharedServiceClaudeCredentials)
			piClaims = append(piClaims, claudeCredentials.admission)
		}
		pid, err := a.launchEmbeddedPTYWithClaims(authorityReservation, recipe, spec, piClaims, piSharedKinds...)
		if err != nil {
			a.Sessions.MarkFailed(sess.ID, err.Error())
			a.Log.Error("session", "Pi PTY启动失败", fmt.Sprintf("id=%s err=%v", sess.ID, err))
			return "", fmt.Errorf("start pi pty: %w", err)
		}
		claudeCredentials.release(a)
		authorityCommitted = true
		a.Sessions.SetPID(sess.ID, pid)
		a.Log.Info("session", "Pi PTY进程已启动", fmt.Sprintf("id=%s pid=%d", sess.ID, pid))
		// T-1.5：注册 webui 探测（携带注入端口、pid 与 v1.0.2 token，供
		// /api/info 探测与注册表回退匹配；webuiPort=0 表示纯注册表发现）。
		a.WebUI.RegisterSession(sess.ID, pid, webuiPort, webuiToken)

		go func(id string) {
			for a.Pty.IsRunning(id) {
				select {
				case <-a.ctx.Done():
					return
				case <-time.After(time.Second):
				}
			}
			a.Sessions.MarkExited(id)
			a.WebUI.Invalidate(id)
			a.releaseSharedLeases(id) // 凭据/headroom lease 随进程树结束释放
			a.Log.Info("session", "Pi PTY进程已退出", "id="+id)
		}(sess.ID)

		return sess.ID, nil
	}

	// 外部终端/VSCode/Zed 模式：使用 Launcher
	// Claude 订阅账号轮换（design §6.3）：真正进程启动前复核代际；凭据租约
	// 需要外部运行身份（无 Control 写权限）。提升失败 → 保守停止进程。
	var piExternalRun *remote.ExternalRunIdentity
	if claudeCredentials != nil {
		if recheckErr := a.recheckClaudeCredentialsClaim(claudeCredentials); recheckErr != nil {
			a.Sessions.MarkFailed(sess.ID, recheckErr.Error())
			return "", recheckErr
		}
		run, mintErr := a.sharedCoord.MintExternalRunIdentity(contract.SessionID(sess.ID))
		if mintErr != nil {
			a.Sessions.MarkFailed(sess.ID, mintErr.Error())
			return "", fmt.Errorf("prepare external Pi run: %w", mintErr)
		}
		piExternalRun = run
	}
	result, err := a.Launcher.LaunchPi(sess.ID, launchSettings.Provider, launchSettings.Model, launchSettings.Thinking, launchMode, workDir, envOverrides)
	if err != nil {
		a.Sessions.MarkFailed(sess.ID, err.Error())
		a.Log.Error("session", "Pi 进程启动失败", fmt.Sprintf("id=%s err=%v", sess.ID, err))
		return "", fmt.Errorf("launch pi: %w", err)
	}
	externalBindingKey, externalBindingRegistered, err := a.registerExternalProcessEvidence(result)
	if err != nil {
		closeUnregisteredExternalEvidence(context.Background(), result)
		a.Sessions.MarkFailed(sess.ID, err.Error())
		return "", err
	}
	if claudeCredentials != nil {
		if leaseErr := a.acquireAndRememberExternalSharedLease(sess.ID, piExternalRun, remote.SharedServiceClaudeCredentials, claudeCredentials.admission); leaseErr != nil {
			stopErr := a.Launcher.Stop(sess.ID)
			claudeCredentials.release(a)
			closeUnregisteredExternalEvidence(context.Background(), result)
			launchErr := fmt.Errorf("activate external Pi credentials lease: %w", leaseErr)
			if stopErr != nil {
				launchErr = errors.Join(launchErr, fmt.Errorf("compensate external Pi process: %w", stopErr))
			}
			a.Sessions.MarkFailed(sess.ID, launchErr.Error())
			return "", launchErr
		}
		claudeCredentials.release(a)
	}
	externalRecipe := stableDesktopRecipe(session.AppTypePi, workDir, providerID, modelName, launchSettings.Model, shellPath, false)
	if err := a.finalizeExternalAuthority(sess.ID, result, externalBindingKey, externalBindingRegistered, externalRecipe); err != nil {
		a.compensateExternalProcessEvidence(context.Background(), result, externalBindingKey, externalBindingRegistered)
		a.Sessions.MarkFailed(sess.ID, err.Error())
		return "", err
	}

	a.Sessions.SetPID(sess.ID, result.PID)
	a.Log.Info("session", "Pi 进程已启动", fmt.Sprintf("id=%s pid=%d", sess.ID, result.PID))

	go func(id string) {
		for a.Launcher.IsRunning(id) {
			select {
			case <-a.ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
		a.Sessions.MarkExited(id)
		a.releaseSharedLeases(id) // 凭据 lease 随进程树结束释放
		a.Log.Info("session", "Pi 进程已退出", "id="+id)
	}(sess.ID)

	return sess.ID, nil
}

// LaunchOmpSession 启动一个新的 Oh My Pi (omp) 会话（完整复刻 LaunchPiSession）。
//
// omp 与 pi 同源：同样的 --provider/--model/--thinking CLI 契约、同样的
// PI_CODING_AGENT_DIR agent 根重定位、同样的会话 JSONL 结构。差异：
//   - agent 根为 ~/.omp/agent（defaultOmpAgentDir），models.yml 为 YAML 格式；
//   - 模型选择走 --model provider/model（--provider 为 legacy 仍可用）；
//   - 插件管理由 internal/ompplugin 服务承载（omp plugin list --json + 写操作
//     CLI，npm 与 marketplace 双源，接线见 bind_list.go）。
//
// 主链路：terminal_presets 桥接（type "omp"）→ 写 ~/.omp/agent/models.yml
// （BuildOmpModelsConfig + MergeOmpModelsConfig + WriteOmpAgentConfig，成功则
// Provider=amagi-<name>，失败回退 ompProviderMapping）→ embedded/terminal 双模式。
func (a *App) LaunchOmpSession(modelName string, providerID string, mode string, workDir string, shellPath string) (string, error) {
	if err := a.Config.RejectPiLoginLaunchRefs(providerID, modelName); err != nil {
		return "", err
	}
	a.Log.Info("session", "启动 omp 会话请求", fmt.Sprintf("model=%s provider=%s mode=%s workDir=%s shell=%s", modelName, providerID, mode, workDir, shellPath))

	// ---- terminal_presets 桥接 ----
	// modelName 可能是 terminal_preset 的 stable key（形如 "provider/presetName"）。
	// 回退语义：先查 openai 桶（omp 原生桶，撞名优先），miss 后仅当 anthropic 桶
	// 预设带 HarnessSync 标记（opt-in）才命中；unmarked anthropic 预设不解析。
	tpProvider, tp, tpErr := a.Config.ResolvePiOmpTerminalPreset(modelName)
	tpFound := tpErr == nil && tp != nil
	// presetParams 收集命中的预设参数（contextWindow/thinking/effort/maxTokens），
	// 供后续写入 omp models.yml 的 model 配置与解析 --thinking 级别。
	var presetParams config.Parameters
	if tpFound {
		if tpProvider != "" {
			providerID = tpProvider
		}
		modelName = tp.Model
		presetParams = tp.Parameters
		a.Log.Info("session", "omp 命中 terminal_preset", fmt.Sprintf("key=%s provider=%s model=%s", modelName, tpProvider, tp.Model))
	}

	// ---- legacy provider preset fallback ----
	// 若未命中新体系，且 providerID 非空，检查是否是旧的 provider.Presets key。
	if !tpFound && providerID != "" {
		if provider, pErr := a.Config.GetProvider(providerID); pErr == nil {
			if preset, ok := provider.Presets[modelName]; ok {
				resolvedModel := preset.Model
				if resolvedModel == "" {
					resolvedModel = provider.DefaultModel
				}
				a.Log.Info("session", "omp 命中旧 provider preset", fmt.Sprintf("key=%s presetModel=%s defaultModel=%s -> resolved=%s", modelName, preset.Model, provider.DefaultModel, resolvedModel))
				modelName = resolvedModel
				presetParams = preset.Parameters
			}
		}
	}

	// 确定启动模式
	launchMode := embeddedDefaultLaunchMode(mode)
	if err := a.validateLaunchMode(string(launchMode)); err != nil {
		return "", err
	}

	// 工作目录单一校验/回退 choke point（launch_workdir.go）：
	// Clean+Abs+Stat 验证；无效时回退默认目录 → 用户 Home（原生目标/会话
	// 记录/外部终端模式的权威值）。WSL 目标的最终目录不在此定谳：原始请求
	// 随 spec 交给 resolveEmbeddedLaunchSpec，由 spec.BootstrapMode 后置定谳
	//（纯目标判据，零探测）。
	requestedWorkDir := workDir
	resolvedWorkDir, wdErr := a.resolveLaunchWorkDir(workDir)
	if wdErr != nil {
		return "", wdErr
	}
	workDir = resolvedWorkDir

	// WSL 终端模式判定（与 LaunchPiSession 对称，见其注释）。
	wslDistro := ""
	if platform.EmbeddedLaunchTargetsWSL(string(launchMode), shellPath, nil) {
		wslDistro = platform.DefaultWSLDistro(nil)
		if wslDistro == "" {
			a.Log.Warn("omp", "WSL shell 会话但无可用发行版，配置仍写 Windows 侧", "")
		}
	}

	launchSettings := ompLaunchSettings{
		Model: strings.TrimSpace(modelName),
	}

	// 构建环境变量注入 + omp models.yml 配置写入。
	//
	// omp 从标准用户目录 ~/.omp/agent 读取 models.yml 的 providers 配置。
	// amagi 把当前选中的 provider 翻译成 omp 的一个自定义命名 provider（"amagi-<name>"），
	// 合并写入 ~/.omp/agent/models.yml。启动时显式移除 PI_CODING_AGENT_DIR，
	// 避免系统或 CodeBox 自定义环境里的旧值又把 omp 导向独立副本
	//（实测：PI_CODING_AGENT_DIR 只重定位会话目录，models.yml 始终从默认根读取）。
	envOverrides := map[string]string{
		// BuildEnv 约定：空值表示从子进程环境中删除该变量。
		"PI_CODING_AGENT_DIR": "",
	}
	// 与 Pi 会话同理：跳过 omp 启动期网络操作（目录刷新/版本检查），避免网络
	// 抖动时启动挂起；不影响模型推理。omp 与 pi 同源，读取同名环境变量。
	if _, explicit := a.EnvVars.Get("PI_OFFLINE"); !explicit {
		envOverrides["PI_OFFLINE"] = "1"
	}
	if providerID != "" {
		// 临界区整体收进 writeOmpProviderModelsAtLaunch（与 Pi 的
		// writePiProviderModelsAtLaunch 对称，D5）：WSL 路径构建/写入失败均阻断，
		// 不静默回退丢自定义 baseURL 的 ompProviderMapping 兜底。
		var ompWriteErr error
		launchSettings, ompWriteErr = a.writeOmpProviderModelsAtLaunch(providerID, launchSettings, presetParams, wslDistro, envOverrides)
		if ompWriteErr != nil {
			return "", ompWriteErr
		}
	}

	// Embedded identity stays hidden until its exact PTY run is active.
	sess, authorityReservation, err := a.reserveLaunchSession(session.AppTypeOhMyPi, "omp", providerID, launchSettings.Model, launchMode, workDir)
	if err != nil {
		return "", err
	}
	authorityCommitted := authorityReservation == nil
	defer func() {
		if !authorityCommitted && authorityReservation != nil {
			a.Sessions.AbortCreate(authorityReservation)
		}
	}()
	a.Log.Info("session", "omp 会话身份已预留", fmt.Sprintf("id=%s provider=%s model=%s mode=%s", sess.ID, launchSettings.Provider, launchSettings.Model, launchMode))

	// 调试日志：输出 envOverrides 注入情况
	envKeys := make([]string, 0, len(envOverrides))
	for k := range envOverrides {
		envKeys = append(envKeys, k)
	}
	a.Log.Info("omp", "envOverrides keys", fmt.Sprintf("%v", envKeys))

	// 内嵌终端模式：使用 ConPTY
	if launchMode == session.ModeEmbedded {
		// 注入自定义环境变量（自定义 > 系统，再被 envOverrides 覆盖）
		baseEnv := a.EnvVars.MergeWithSystem()
		env := launcher.BuildEnv(baseEnv, envOverrides)
		if wslDistro != "" {
			// 与 LaunchPiSession 对称：Windows 路径值剥离已上移到
			// resolveEmbeddedLaunchSpec；此处保留 fd/rg 探测。
			a.warnWSLSearchToolsOnce(wslDistro)
		}

		args := []string{}
		if launchSettings.Provider != "" {
			args = append(args, "--provider", launchSettings.Provider)
		}
		if launchSettings.Model != "" {
			args = append(args, "--model", launchSettings.Model)
		}
		if launchSettings.Thinking != "" {
			args = append(args, "--thinking", launchSettings.Thinking)
		}
		spec, err := a.resolveEmbeddedLaunchSpec(session.AppTypeOhMyPi, string(launchMode), shellPath, workDir, requestedWorkDir, env, args)
		if err != nil {
			a.Sessions.MarkFailed(sess.ID, err.Error())
			return "", err
		}
		workDir = spec.WorkDir

		recipe := stableDesktopRecipe(session.AppTypeOhMyPi, workDir, providerID, modelName, launchSettings.Model, shellPath, false)
		pid, err := a.launchEmbeddedPTY(authorityReservation, recipe, spec)
		if err != nil {
			a.Sessions.MarkFailed(sess.ID, err.Error())
			a.Log.Error("session", "omp PTY启动失败", fmt.Sprintf("id=%s err=%v", sess.ID, err))
			return "", fmt.Errorf("start omp pty: %w", err)
		}
		authorityCommitted = true
		a.Sessions.SetPID(sess.ID, pid)
		a.Log.Info("session", "omp PTY进程已启动", fmt.Sprintf("id=%s pid=%d", sess.ID, pid))

		go func(id string) {
			for a.Pty.IsRunning(id) {
				select {
				case <-a.ctx.Done():
					return
				case <-time.After(time.Second):
				}
			}
			a.Sessions.MarkExited(id)
			a.Log.Info("session", "omp PTY进程已退出", "id="+id)
		}(sess.ID)

		return sess.ID, nil
	}

	// 外部终端/VSCode/Zed 模式：使用 Launcher
	result, err := a.Launcher.LaunchOmp(sess.ID, launchSettings.Provider, launchSettings.Model, launchSettings.Thinking, launchMode, workDir, envOverrides)
	if err != nil {
		a.Sessions.MarkFailed(sess.ID, err.Error())
		a.Log.Error("session", "omp 进程启动失败", fmt.Sprintf("id=%s err=%v", sess.ID, err))
		return "", fmt.Errorf("launch omp: %w", err)
	}
	externalBindingKey, externalBindingRegistered, err := a.registerExternalProcessEvidence(result)
	if err != nil {
		closeUnregisteredExternalEvidence(context.Background(), result)
		a.Sessions.MarkFailed(sess.ID, err.Error())
		return "", err
	}
	externalRecipe := stableDesktopRecipe(session.AppTypeOhMyPi, workDir, providerID, modelName, launchSettings.Model, shellPath, false)
	if err := a.finalizeExternalAuthority(sess.ID, result, externalBindingKey, externalBindingRegistered, externalRecipe); err != nil {
		a.compensateExternalProcessEvidence(context.Background(), result, externalBindingKey, externalBindingRegistered)
		a.Sessions.MarkFailed(sess.ID, err.Error())
		return "", err
	}

	a.Sessions.SetPID(sess.ID, result.PID)
	a.Log.Info("session", "omp 进程已启动", fmt.Sprintf("id=%s pid=%d", sess.ID, result.PID))

	go func(id string) {
		for a.Launcher.IsRunning(id) {
			select {
			case <-a.ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
		a.Sessions.MarkExited(id)
		a.Log.Info("session", "omp 进程已退出", "id="+id)
	}(sess.ID)

	return sess.ID, nil
}

// wslSearchToolsWarnOnce 保证 fd/ripgrep 缺失提示每个进程生命周期只打一次
// （探测本身在 platform.WSLSearchToolStatus 内按 distro 缓存，不会重复 fork
// wsl.exe）。
var wslSearchToolsWarnOnce sync.Once

// warnWSLSearchToolsOnce 在 WSL 模式启动 pi/omp 会话时探测 distro 内的
// fd/fdfind 与 ripgrep（PI_OFFLINE=1 下 pi 不会联网自下载，缺失即文件搜索
// 能力降级）。缺失时打 WARN 给出安装引导；不做 apt 安装（需 sudo，范围外）。
// 无会话启动期 warning UI 通道，日志 WARN 为本仓库既有可感知手段。
func (a *App) warnWSLSearchToolsOnce(distro string) {
	distro = strings.TrimSpace(distro)
	if distro == "" {
		return
	}
	tools := platform.WSLSearchToolStatus(distro)
	if tools.FD && tools.Ripgrep {
		return
	}
	missing := make([]string, 0, 2)
	if !tools.FD {
		missing = append(missing, "fd")
	}
	if !tools.Ripgrep {
		missing = append(missing, "ripgrep")
	}
	wslSearchToolsWarnOnce.Do(func() {
		a.Log.Warn("wsl", "WSL 内缺少 fd/ripgrep，pi/omp 的文件搜索能力受限",
			fmt.Sprintf("[CodeBox] distro=%s 缺少 %s。可在 WSL 内执行: sudo apt install fd-find ripgrep", distro, strings.Join(missing, "/")))
	})
}

// LaunchOpenCode 启动 OpenCode 终端会话。
// 双轨兼容：优先查 opencode_presets（新模型），回退到 terminal_presets.opencode（旧模型）。
func (a *App) LaunchOpenCode(providerName string, presetName string, mode string, workDir string, shellPath string) (string, error) {
	if err := a.Config.RejectPiLoginLaunchRefs(providerName, presetName); err != nil {
		return "", err
	}
	a.Log.Info("session", "启动 OpenCode 会话请求", fmt.Sprintf("provider=%s preset=%s mode=%s workDir=%s shell=%s", providerName, presetName, mode, workDir, shellPath))

	envOverrides := map[string]string{}
	var provider *config.Provider

	// ============================================================
	// 轨道 1（新模型）：opencode_presets
	// ============================================================
	if presetName != "" {
		if ocPreset, err := a.Config.GetOpenCodePreset(presetName); err == nil && ocPreset != nil {
			a.Log.Info("session", "OpenCode 命中 opencode_preset（新模型）", fmt.Sprintf("key=%s name=%s bindings=%d", presetName, ocPreset.Name, len(ocPreset.Bindings)))

			// 构建 getAPIKey 函数：按 binding 的 local_provider + format 读取 secrets
			getAPIKey := func(localProvider, _ string) (string, error) {
				if local, err := a.Config.GetProvider(localProvider); err == nil && local != nil {
					key, _ := a.getProviderAPIKey(localProvider, *local)
					return key, nil
				}
				key, _ := a.getProviderAPIKeyWithLegacyCandidates(localProvider, legacyProviderAPIKeyCandidatesForFormat(""))
				return key, nil
			}

			// 构建 getProvider 函数：读取本地 Provider 配置（用于推导格式和注入 baseURL/organization）
			getProvider := func(providerName string) (*config.Provider, error) {
				return a.Config.GetProvider(providerName)
			}

			// 用新模型构建运行时配置
			ocOverrides, err := launcher.BuildOpenCodeEnvOverridesFromPreset(*ocPreset, getAPIKey, getProvider)
			if err != nil {
				a.Log.Error("session", "构建 OpenCode 配置失败（新模型）", err.Error())
				return "", fmt.Errorf("build opencode config from preset: %w", err)
			}
			envOverrides = ocOverrides

			// 新模型下 providerName 无意义，session 中记录 preset key
			providerName = "opencode-preset:" + presetName

			// 验证 bindings 中本地 provider 是否存在
			for ocProvID, binding := range ocPreset.Bindings {
				if binding.LocalProvider != "" {
					if _, pErr := a.Config.GetProvider(binding.LocalProvider); pErr != nil {
						a.Log.Warn("session", "binding 引用的本地 provider 不存在", fmt.Sprintf("binding=%s localProvider=%s err=%v", ocProvID, binding.LocalProvider, pErr))
						// 不直接阻断，但记录警告
					}
				}
			}

			goto launchCommon
		}
	}

	// ============================================================
	// 轨道 2（旧模型回退）：terminal_presets.opencode
	// ============================================================
	{
		// 当 presetName 为空时，不注入任何 preset 配置，让 OpenCode 读取全局配置
		if presetName == "" {
			a.Log.Info("session", "OpenCode 使用全局配置（preset 为空）", fmt.Sprintf("provider=%s", providerName))
		} else {
			// presetName 可能是 terminal_preset 的 stable key
			tpProvider, tp, tpErr := a.Config.ResolveTerminalPreset("opencode", presetName)
			tpFound := tpErr == nil && tp != nil
			if tpFound {
				if tpProvider != "" {
					providerName = tpProvider
				}
				a.Log.Info("session", "OpenCode 命中 terminal_preset（旧模型回退）", fmt.Sprintf("key=%s provider=%s model=%s hasCfg=%v", presetName, tpProvider, tp.Model, len(tp.OpenCodeCfg) > 0))
			}

			if providerName != "" {
				// SnapshotProvider：单次读锁内 provider+presets 同代快照（同
				// LaunchSession 桥接，谛听终审 Minor-2），Presets 为非 nil 副本。
				loadedProvider, err := a.Config.SnapshotProvider(providerName)
				if err != nil {
					a.Log.Error("session", "获取 OpenCode 提供商失败", err.Error())
					return "", fmt.Errorf("get opencode provider: %w", err)
				}
				provider = loadedProvider

				// 若命中 terminal preset，桥接为旧 config.Preset 注入 provider 快照
				if tpFound {
					converted := config.Preset{
						Name:           tp.Name,
						Model:          tp.Model,
						Parameters:     tp.Parameters,
						OpenCodeConfig: tp.OpenCodeCfg,
					}
					provider.Presets[presetName] = converted
					a.Log.Info("session", "OpenCode 已桥接 terminal_preset 到 provider.Presets", fmt.Sprintf("key=%s model=%s", presetName, tp.Model))
				}

				apiKey, keySource := a.getProviderAPIKey(providerName, *provider)
				if apiKey == "" {
					a.Log.Error("session", "未找到 OpenCode API 密钥", "provider="+providerName)
					return "", fmt.Errorf("no API key found for provider %q", providerName)
				}

				// 基于 Provider + Preset（含桥接后的 terminal preset）生成 OPENCODE_CONFIG_CONTENT 注入
				ocOverrides, err := launcher.BuildOpenCodeEnvOverrides(providerName, *provider, presetName, apiKey)
				if err != nil {
					a.Log.Error("session", "构建 OpenCode 配置失败", err.Error())
					return "", fmt.Errorf("build opencode config: %w", err)
				}
				envOverrides = ocOverrides

				a.Log.Info("session", "OpenCode API 密钥已获取",
					fmt.Sprintf("provider=%s source=%s key=%s len=%d",
						providerName, keySource, secrets.MaskKey(apiKey), len(apiKey)))
			}
		} // else 结束：presetName != "" 的情况
	} // 轨道2 结束

launchCommon:
	// 确定启动模式
	launchMode := embeddedDefaultLaunchMode(mode)
	if err := a.validateLaunchMode(string(launchMode)); err != nil {
		return "", err
	}

	// 工作目录单一校验/回退 choke point（launch_workdir.go）：
	// Clean+Abs+Stat 验证；无效时回退默认目录 → 用户 Home（原生目标/会话
	// 记录/外部终端模式的权威值）。WSL 目标的最终目录不在此定谳：原始请求
	// 随 spec 交给 resolveEmbeddedLaunchSpec，由 spec.BootstrapMode 后置定谳
	//（纯目标判据，零探测）。
	requestedWorkDir := workDir
	resolvedWorkDir, wdErr := a.resolveLaunchWorkDir(workDir)
	if wdErr != nil {
		return "", wdErr
	}
	workDir = resolvedWorkDir

	sessionProvider := "opencode"
	if providerName != "" {
		sessionProvider = providerName
	}

	// Embedded identity stays hidden until its exact PTY run is active.
	sess, authorityReservation, err := a.reserveLaunchSession(session.AppTypeOpenCode, sessionProvider, presetName, "", launchMode, workDir)
	if err != nil {
		return "", err
	}
	authorityCommitted := authorityReservation == nil
	defer func() {
		if !authorityCommitted && authorityReservation != nil {
			a.Sessions.AbortCreate(authorityReservation)
		}
	}()
	a.Log.Info("session", "OpenCode 会话身份已预留", fmt.Sprintf("id=%s mode=%s", sess.ID, launchMode))

	// 根据模式选择启动方式
	if launchMode == session.ModeEmbedded {
		// 内嵌终端模式：使用 ConPTY
		baseEnv := a.EnvVars.MergeWithSystem()
		env := launcher.BuildEnv(baseEnv, envOverrides)
		// WSL 模式（证据 T4）：opencode 跑在 distro 内，OPENCODE_CONFIG_CONTENT
		// 不在 WSLENV 转发面——env 载体天然缺席，会话会以无 provider 的默认配置
		// 启动。修复：把同一份 runtime config 写入 distro 内
		// ~/.config/opencode/amagi-codebox.json，并经 OPENCODE_CONFIG（Linux 路径值，
		// 在转发面内，必须在 resolveEmbeddedLaunchSpec 之前进入 env 才会被拼进
		// WSLENV 清单）指向它；opencode 以附加配置层加载该文件（实测 v1.17.18：
		// global → OPENCODE_CONFIG → 项目级 → CONTENT，不顶替用户全局配置）。
		// P3-S1 失败语义变更（已批准）：选中模型配置不可安全落地 → 启动失败
		//（不再无 provider 静默启动）；写入前对 mcp 段做确定性形态检查
		//（npx.cmd→npx 仅验证过才映射；Windows exe/ps1/盘符参数不生成假可用
		// 条目；HTTP/远程保留 URL；distro 全局同名默认保留本地）。
		if platform.EmbeddedLaunchTargetsWSL(string(launchMode), shellPath, nil) {
			wslDistro := platform.DefaultWSLDistro(nil)
			cfgJSON := strings.TrimSpace(envOverrides["OPENCODE_CONFIG_CONTENT"])
			if cfgJSON != "" {
				launchErr := fmt.Errorf("OpenCode 模型配置无法写入 WSL：未找到可用发行版（OPENCODE_CONFIG_CONTENT 不跨界）；本次未启动，不再以无 provider 配置静默启动")
				if wslDistro == "" {
					a.Sessions.MarkFailed(sess.ID, launchErr.Error())
					return "", launchErr
				}
				var runtimeCfg map[string]any
				if uErr := json.Unmarshal([]byte(cfgJSON), &runtimeCfg); uErr != nil {
					launchErr = fmt.Errorf("解析 OpenCode 运行时配置失败，无法安全写入 WSL：%v；本次未启动，不再以无 provider 配置静默启动", uErr)
					a.Sessions.MarkFailed(sess.ID, launchErr.Error())
					return "", launchErr
				}
				linuxPath, prepErr := a.prepareWSLOpenCodeLayer(wslDistro, runtimeCfg)
				if prepErr != nil {
					a.Sessions.MarkFailed(sess.ID, prepErr.Error())
					return "", prepErr
				}
				env = launcher.BuildEnv(env, map[string]string{"OPENCODE_CONFIG": linuxPath})
				a.Log.Info("session", "已写入 OpenCode 配置 (WSL)", fmt.Sprintf("distro=%s -> %s", wslDistro, linuxPath))
			}
		}

		spec, err := a.resolveEmbeddedLaunchSpec(session.AppTypeOpenCode, string(launchMode), shellPath, workDir, requestedWorkDir, env, nil)
		if err != nil {
			a.Sessions.MarkFailed(sess.ID, err.Error())
			return "", err
		}
		workDir = spec.WorkDir

		recipe := stableDesktopRecipe(session.AppTypeOpenCode, workDir, sessionProvider, presetName, "", shellPath, false)
		pid, err := a.launchEmbeddedPTY(authorityReservation, recipe, spec)
		if err != nil {
			a.Sessions.MarkFailed(sess.ID, err.Error())
			a.Log.Error("session", "OpenCode PTY启动失败", fmt.Sprintf("id=%s err=%v", sess.ID, err))
			return "", fmt.Errorf("start opencode pty: %w", err)
		}
		authorityCommitted = true
		a.Sessions.SetPID(sess.ID, pid)
		a.Log.Info("session", "OpenCode PTY进程已启动", fmt.Sprintf("id=%s pid=%d", sess.ID, pid))

		go func(id string) {
			for a.Pty.IsRunning(id) {
				select {
				case <-a.ctx.Done():
					return
				case <-time.After(time.Second):
				}
			}
			a.Sessions.MarkExited(id)
			a.Log.Info("session", "OpenCode PTY进程已退出", "id="+id)
		}(sess.ID)

		return sess.ID, nil
	}

	// 外部终端/VSCode/Zed 模式：使用 Launcher
	var apiKey string
	if provider != nil {
		apiKey, _ = a.getProviderAPIKey(sessionProvider, *provider)
	}
	result, err := a.Launcher.LaunchOpenCode(sess.ID, launchMode, workDir, envOverrides, "", provider, presetName, apiKey)
	if err != nil {
		a.Sessions.MarkFailed(sess.ID, err.Error())
		a.Log.Error("session", "OpenCode 进程启动失败", fmt.Sprintf("id=%s err=%v", sess.ID, err))
		return "", fmt.Errorf("launch opencode: %w", err)
	}
	externalBindingKey, externalBindingRegistered, err := a.registerExternalProcessEvidence(result)
	if err != nil {
		closeUnregisteredExternalEvidence(context.Background(), result)
		a.Sessions.MarkFailed(sess.ID, err.Error())
		return "", err
	}
	externalRecipe := stableDesktopRecipe(session.AppTypeOpenCode, workDir, sessionProvider, presetName, "", shellPath, false)
	if err := a.finalizeExternalAuthority(sess.ID, result, externalBindingKey, externalBindingRegistered, externalRecipe); err != nil {
		a.compensateExternalProcessEvidence(context.Background(), result, externalBindingKey, externalBindingRegistered)
		a.Sessions.MarkFailed(sess.ID, err.Error())
		return "", err
	}

	a.Sessions.SetPID(sess.ID, result.PID)
	a.Log.Info("session", "OpenCode 进程已启动", fmt.Sprintf("id=%s pid=%d", sess.ID, result.PID))

	go func(id string) {
		for a.Launcher.IsRunning(id) {
			select {
			case <-a.ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
		a.Sessions.MarkExited(id)
		a.Log.Info("session", "OpenCode 进程已退出", "id="+id)
	}(sess.ID)

	return sess.ID, nil
}

// --- PTY 终端 API ---

// PtyWrite 向内嵌终端写入数据（前端键盘输入）。data 为 base64 编码。
// M-005: fail-closed — when the control runtime is not ready the write is
// rejected rather than falling back to the raw PTY service (design §6.3 C-01:
// every mutation must go through the Gate). The control runtime is created in
// the App constructor and MarkReady runs in Startup before any embedded PTY is
// created, so this only rejects writes during the startup/shutdown window.
func (a *App) PtyWrite(sessionID string, data string) error {
	if a.control == nil || !a.control.IsReady() {
		return remote.ErrControlNotReady
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return fmt.Errorf("decode base64: %w", err)
	}
	return a.control.DesktopInput(a.ctx, contract.SessionID(sessionID), raw)
}

// PtyWriteLarge 向内嵌终端分块写入大量数据（用于长文本粘贴）。data 为 base64 编码。
// 内部将数据拆分为 1KB 小块逐步写入，避免 ConPTY 缓冲区溢出截断。
// M-005: fail-closed (see PtyWrite).
func (a *App) PtyWriteLarge(sessionID string, data string) error {
	if a.control == nil || !a.control.IsReady() {
		return remote.ErrControlNotReady
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return fmt.Errorf("decode base64: %w", err)
	}
	return a.control.DesktopPasteChunk(a.ctx, contract.SessionID(sessionID), raw)
}

// SaveClipboardImage 将 base64 编码的 PNG 保存为私有临时文件，返回文件绝对路径。
// 用于处理 Windows 截图工具截图后粘贴到终端的场景。
func (a *App) SaveClipboardImage(base64Data string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return "", fmt.Errorf("decode base64: %w", err)
	}
	filePath, err := writeClipboardImage(raw)
	if err != nil {
		return "", err
	}
	a.Log.Info("app", "剪贴板图片已保存", filePath)
	return filePath, nil
}

func writeClipboardImage(raw []byte) (string, error) {
	if len(raw) == 0 {
		return "", errors.New("clipboard image is empty")
	}
	if len(raw) > maxClipboardImageBytes {
		return "", fmt.Errorf("clipboard image exceeds %d MiB limit", maxClipboardImageBytes/(1024*1024))
	}
	if len(raw) < len(pngSignature) || !bytes.Equal(raw[:len(pngSignature)], pngSignature) {
		return "", errors.New("clipboard image must be a PNG")
	}

	file, err := os.CreateTemp("", "amagi-codebox-clipboard-*.png")
	if err != nil {
		return "", fmt.Errorf("create temp image: %w", err)
	}
	filePath := file.Name()
	succeeded := false
	defer func() {
		if !succeeded {
			_ = file.Close()
			_ = os.Remove(filePath)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return "", fmt.Errorf("set temp image permissions: %w", err)
	}
	if _, err := file.Write(raw); err != nil {
		return "", fmt.Errorf("write temp image: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close temp image: %w", err)
	}
	succeeded = true
	return filePath, nil
}

// PtyResize 调整内嵌终端尺寸
// M-005: fail-closed (see PtyWrite).
func (a *App) PtyResize(sessionID string, cols, rows int) error {
	if a.control == nil || !a.control.IsReady() {
		return remote.ErrControlNotReady
	}
	return a.control.DesktopPassiveResize(a.ctx, contract.SessionID(sessionID), cols, rows)
}

// GetOutputHistorySnapshot returns a JSON-encoded snapshot of the output history
// along with the emitSeq and the current run token/version at snapshot time.
// The JSON structure is:
//
//	{"data": "<base64-encoded bytes>", "seq": <uint64>, "runToken": "<opaque>", "runVersion": "<decimal>"}
//
// Frontend uses the seq to deduplicate live events: any live event with
// seq <= the returned seq is already contained in the history snapshot.
// runToken/runVersion enable A3's strict run-scoped filtering (design §8.6.1).
func (a *App) GetOutputHistorySnapshot(sessionID string) (string, error) {
	data, seq, err := a.Pty.GetOutputHistoryWithSeq(sessionID)
	if err != nil {
		return "", err
	}
	if a.control != nil {
		return a.control.Projector().FormatSnapshotJSON(contract.SessionID(sessionID), data, seq)
	}
	result := struct {
		Data string `json:"data"`
		Seq  uint64 `json:"seq"`
	}{
		Data: base64.StdEncoding.EncodeToString(data),
		Seq:  seq,
	}
	b, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("marshal history snapshot: %w", err)
	}
	return string(b), nil
}

// GetPtyDimensions 返回指定 PTY 会话的当前尺寸。
// 实现 remote.DimensionsProvider 接口。
func (a *App) GetPtyDimensions(sessionID string) (cols, rows int, err error) {
	return a.Pty.GetPtyDimensions(sessionID)
}

// OpenFileInEditor 使用系统默认程序打开指定文件。
// filePath 可以是绝对路径或相对路径；line 参数保留兼容但不使用（系统默认程序通常不支持行号定位）。
func (a *App) OpenFileInEditor(filePath string, line int) error {
	_ = line
	// 先验证文件是否存在，避免打开不存在的路径时创建空文件
	if _, err := os.Stat(filePath); err != nil {
		a.Log.Debug("app", "文件不存在，跳过打开", filePath)
		return fmt.Errorf("file not found: %s", filePath)
	}
	if err := a.fileOpener().Open(filePath); err != nil {
		a.Log.Warn("app", "打开文件失败", fmt.Sprintf("file=%s err=%v", filePath, err))
		return fmt.Errorf("open file %q: %w", filePath, err)
	}
	a.Log.Info("app", "系统默认程序打开文件", filePath)
	return nil
}
