package launcher

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/envvars"
	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/session"
)

// LaunchResult 启动结果（返回给 app 层，不暴露给前端）
type LaunchResult struct {
	SessionID string `json:"-"`
	PID       int    `json:"-"`
}

// LauncherService 管理 Claude Code 进程的启动（支持多实例）。
type LauncherService struct {
	processes map[string]*exec.Cmd // sessionID -> cmd
	mu        sync.Mutex
	proxyPort int
	log       *logging.Service
	envVars   *envvars.EnvVarsService
}

func NewLauncherService(log *logging.Service, envVars *envvars.EnvVarsService) *LauncherService {
	return &LauncherService{
		processes: make(map[string]*exec.Cmd),
		log:       log,
		envVars:   envVars,
	}
}

func (s *LauncherService) baseEnv() []string {
	if s.envVars != nil {
		return s.envVars.MergeWithSystem()
	}
	return os.Environ()
}

func (s *LauncherService) SetProxyPort(port int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.proxyPort = port
}

// BuildOverrides 根据提供商配置和预设参数构建环境变量覆盖映射。
// 返回的 map 可直接传给 BuildEnv。
func (s *LauncherService) BuildOverrides(
	provider config.Provider,
	presetName string,
	apiKey string,
	agentTeams config.AgentTeamsConfig,
) map[string]string {
	s.mu.Lock()
	proxyPort := s.proxyPort
	s.mu.Unlock()

	preset, ok := provider.Presets[presetName]

	overrides := map[string]string{}
	if strings.EqualFold(provider.Type, "openai") || provider.AuthKey == "OPENAI_API_KEY" {
		overrides["ANTHROPIC_BASE_URL"] = ""
		overrides["ANTHROPIC_API_KEY"] = ""
		overrides["ANTHROPIC_AUTH_TOKEN"] = ""
		return overrides
	}

	// Base URL
	if provider.AuthKey == config.AuthTypeOAuth {
		// OAuth 模式必须直连官方端点，不能继承代理或 API Key 环境。
		overrides["ANTHROPIC_BASE_URL"] = ""
	} else if proxyPort > 0 {
		overrides["ANTHROPIC_BASE_URL"] = fmt.Sprintf("http://localhost:%d", proxyPort)
	} else {
		overrides["ANTHROPIC_BASE_URL"] = provider.BaseURL
	}

	// Auth key
	switch provider.AuthKey {
	case config.AuthTypeOAuth:
		// OAuth 模式：清除所有 API Key 环境变量，让 Claude Code 使用原生 OAuth 凭据
		overrides["ANTHROPIC_API_KEY"] = ""
		overrides["ANTHROPIC_AUTH_TOKEN"] = ""
	case config.AuthTypeAuthToken:
		overrides["ANTHROPIC_AUTH_TOKEN"] = apiKey
		overrides["ANTHROPIC_API_KEY"] = ""
	default:
		overrides["ANTHROPIC_API_KEY"] = apiKey
		overrides["ANTHROPIC_AUTH_TOKEN"] = ""
	}

	// Model
	model := provider.DefaultModel
	if ok && strings.TrimSpace(preset.Model) != "" {
		model = preset.Model
	}
	overrides["ANTHROPIC_MODEL"] = model

	// Preset parameters
	if ok {
		p := preset.Parameters
		if p.Temperature != 0 {
			overrides["ANTHROPIC_TEMPERATURE"] = strconv.FormatFloat(p.Temperature, 'f', -1, 64)
		}
		if p.TopP != 0 {
			overrides["ANTHROPIC_TOP_P"] = strconv.FormatFloat(p.TopP, 'f', -1, 64)
		}
		if p.MaxTokens != 0 {
			overrides["ANTHROPIC_MAX_TOKENS"] = strconv.Itoa(p.MaxTokens)
		}
		if p.MaxContextLength != 0 {
			overrides["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] = strconv.Itoa(p.MaxContextLength)
		}
		if p.DoSample != nil {
			overrides["ANTHROPIC_DO_SAMPLE"] = strconv.FormatBool(*p.DoSample)
		}
		if p.Stream != nil {
			overrides["ANTHROPIC_STREAM"] = strconv.FormatBool(*p.Stream)
		}
		if p.Thinking != nil {
			b, err := json.Marshal(p.Thinking)
			if err == nil {
				overrides["ANTHROPIC_THINKING"] = string(b)
			}
		}
	}

	if agentTeams.Enabled {
		overrides["CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS"] = "1"
		overrides["CLAUDE_TEAMMATE_MODE"] = agentTeams.TeammateMode
	}

	return overrides
}

// Launch 启动一个新的 Claude Code 进程，返回启动结果。
// 支持两种模式：terminal（独立终端）、embedded（内嵌终端）
func (s *LauncherService) Launch(
	sessionID string,
	provider config.Provider,
	presetName string,
	apiKey string,
	agentTeams config.AgentTeamsConfig,
	mode session.LaunchMode,
	workDir string,
) (*LaunchResult, error) {
	overrides := s.BuildOverrides(provider, presetName, apiKey, agentTeams)
	env := BuildEnv(s.baseEnv(), overrides)

	// ============================================================
	// 启动策略：复刻原始验证可行的方式
	//
	// 原始版本使用 exec.Command("claude") + os.Stdin/Stdout/Stderr，
	// 在 Wails GUI 进程中稳定运行。Wails 是 GUI 子系统应用（无控制台），
	// Windows 会自动为子控制台进程分配新控制台窗口。
	//
	// 在原始基础上增加：
	//   - cmd.Dir 设置工作目录（无需 cd /D 命令链）
	//   - 多实例支持（每次 exec.Command 各自获得独立控制台）
	//
	// 注意：不使用 CmdLine + CREATE_NEW_CONSOLE，该方式在 Wails 环境下
	// 会导致 claude 在约 5 秒后退出（原因尚未完全确认，疑与 Wails 运行时
	// 的进程管理机制有关）。
	// ============================================================

	cmd := s.buildClaudeCmd(workDir, env)

	s.log.Info("launcher", "正在启动进程", fmt.Sprintf("sessionID=%s mode=%s", sessionID, mode))

	if err := cmd.Start(); err != nil {
		s.log.Error("launcher", "进程启动失败", err.Error())
		return nil, fmt.Errorf("start process: %w", err)
	}

	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}

	s.log.Info("launcher", "进程已启动", fmt.Sprintf("sessionID=%s pid=%d", sessionID, pid))

	s.mu.Lock()
	s.processes[sessionID] = cmd
	s.mu.Unlock()

	// 监控进程退出
	go func(id string, c *exec.Cmd) {
		err := c.Wait()
		s.log.Info("launcher", "进程已退出", fmt.Sprintf("sessionID=%s exitErr=%v", id, err))
		s.mu.Lock()
		delete(s.processes, id)
		s.mu.Unlock()
	}(sessionID, cmd)

	return &LaunchResult{
		SessionID: sessionID,
		PID:       pid,
	}, nil
}

// LaunchOpenCode 启动一个新的 OpenCode 进程，返回启动结果。
// 支持两种模式：terminal（独立终端）、embedded（内嵌终端）。
// envOverrides 中的键值对会注入到进程环境变量，用于传递 OpenCode 配置和认证信息。
func (s *LauncherService) LaunchOpenCode(
	sessionID string,
	mode session.LaunchMode,
	workDir string,
	envOverrides map[string]string,
) (*LaunchResult, error) {
	env := BuildEnv(s.baseEnv(), envOverrides)

	cmd := s.buildOpenCodeCmd(workDir, env)

	s.log.Info("launcher", "正在启动 OpenCode 进程", fmt.Sprintf("sessionID=%s mode=%s", sessionID, mode))

	if err := cmd.Start(); err != nil {
		s.log.Error("launcher", "OpenCode 进程启动失败", err.Error())
		return nil, fmt.Errorf("start opencode process: %w", err)
	}

	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}

	s.log.Info("launcher", "OpenCode 进程已启动", fmt.Sprintf("sessionID=%s pid=%d", sessionID, pid))

	s.mu.Lock()
	s.processes[sessionID] = cmd
	s.mu.Unlock()

	// 监控进程退出
	go func(id string, c *exec.Cmd) {
		err := c.Wait()
		s.log.Info("launcher", "OpenCode 进程已退出", fmt.Sprintf("sessionID=%s exitErr=%v", id, err))
		s.mu.Lock()
		delete(s.processes, id)
		s.mu.Unlock()
	}(sessionID, cmd)

	return &LaunchResult{
		SessionID: sessionID,
		PID:       pid,
	}, nil
}

// LaunchCodex 启动一个新的 Codex CLI 进程，返回启动结果。
// 支持两种模式：terminal（独立终端）、embedded（内嵌终端）
// modelName 非空时附加 -m modelName 参数；
// envOverrides 中的键值对会注入到进程环境变量。
func (s *LauncherService) LaunchCodex(
	sessionID string,
	modelName string,
	mode session.LaunchMode,
	workDir string,
	envOverrides map[string]string,
) (*LaunchResult, error) {
	env := BuildEnv(s.baseEnv(), envOverrides)

	cmd := s.buildCodexCmd(modelName, workDir, env)

	s.log.Info("launcher", "正在启动 Codex 进程", fmt.Sprintf("sessionID=%s mode=%s model=%s", sessionID, mode, modelName))

	if err := cmd.Start(); err != nil {
		s.log.Error("launcher", "Codex 进程启动失败", err.Error())
		return nil, fmt.Errorf("start codex process: %w", err)
	}

	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}

	s.log.Info("launcher", "Codex 进程已启动", fmt.Sprintf("sessionID=%s pid=%d", sessionID, pid))

	s.mu.Lock()
	s.processes[sessionID] = cmd
	s.mu.Unlock()

	// 监控进程退出
	go func(id string, c *exec.Cmd) {
		err := c.Wait()
		s.log.Info("launcher", "Codex 进程已退出", fmt.Sprintf("sessionID=%s exitErr=%v", id, err))
		s.mu.Lock()
		delete(s.processes, id)
		s.mu.Unlock()
	}(sessionID, cmd)

	return &LaunchResult{
		SessionID: sessionID,
		PID:       pid,
	}, nil
}

// buildCodexCmd 构建 codex 进程命令。
// modelName 非空时附加 -m modelName 参数。
func (s *LauncherService) buildCodexCmd(modelName, workDir string, env []string) *exec.Cmd {
	args := []string{}
	if modelName != "" {
		args = append(args, "-m", modelName)
	}
	cmd := exec.Command("codex", args...)
	cmd.Dir = workDir
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

// buildClaudeCmd 构建 claude 进程命令。
// 复刻原始验证可行的方式：直接 exec.Command("claude")，
// 传递 os.Stdin/Stdout/Stderr，由 Windows 自动分配新控制台。
func (s *LauncherService) buildClaudeCmd(workDir string, env []string) *exec.Cmd {
	cmd := exec.Command("claude")
	cmd.Dir = workDir
	cmd.Env = env
	// 关键：必须设置 Stdin/Stdout/Stderr 为 os 句柄。
	// 在 Wails GUI 应用中这些是无效句柄（0x0），
	// 但 Windows 为子控制台进程分配新控制台后，
	// cmd.exe 会正确使用控制台 I/O。
	// 如果留 nil，Go 会打开 DevNull，可能干扰 TTY 检测。
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

// buildOpenCodeCmd 构建 opencode 进程命令。
// 与 buildClaudeCmd 类似，但启动的是 opencode 命令。
func (s *LauncherService) buildOpenCodeCmd(workDir string, env []string) *exec.Cmd {
	cmd := exec.Command("opencode")
	cmd.Dir = workDir
	cmd.Env = env
	// 关键：必须设置 Stdin/Stdout/Stderr 为 os 句柄。
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

// buildAmagiCmd 构建 amagicode 进程命令。
// 与 buildClaudeCmd 类似，但启动的是 amagicode 命令。
func (s *LauncherService) buildAmagiCmd(workDir string, env []string) *exec.Cmd {
	cmd := exec.Command("amagicode")
	cmd.Dir = workDir
	cmd.Env = env
	// 关键：必须设置 Stdin/Stdout/Stderr 为 os 句柄。
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

// LaunchAmagiCode 启动一个新的 AmagiCode 进程，返回启动结果。
// 支持两种模式：terminal（独立终端）、embedded（内嵌终端）。
// envOverrides 中的键值对会注入到进程环境变量，用于传递 AmagiCode 配置和认证信息。
func (s *LauncherService) LaunchAmagiCode(
	sessionID string,
	mode session.LaunchMode,
	workDir string,
	envOverrides map[string]string,
) (*LaunchResult, error) {
	env := BuildEnv(s.baseEnv(), envOverrides)

	cmd := s.buildAmagiCmd(workDir, env)

	s.log.Info("launcher", "正在启动 AmagiCode 进程", fmt.Sprintf("sessionID=%s mode=%s", sessionID, mode))

	if err := cmd.Start(); err != nil {
		s.log.Error("launcher", "AmagiCode 进程启动失败", err.Error())
		return nil, fmt.Errorf("start amagicode process: %w", err)
	}

	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}

	s.log.Info("launcher", "AmagiCode 进程已启动", fmt.Sprintf("sessionID=%s pid=%d", sessionID, pid))

	s.mu.Lock()
	s.processes[sessionID] = cmd
	s.mu.Unlock()

	// 监控进程退出
	go func(id string, c *exec.Cmd) {
		err := c.Wait()
		s.log.Info("launcher", "AmagiCode 进程已退出", fmt.Sprintf("sessionID=%s exitErr=%v", id, err))
		s.mu.Lock()
		delete(s.processes, id)
		s.mu.Unlock()
	}(sessionID, cmd)

	return &LaunchResult{
		SessionID: sessionID,
		PID:       pid,
	}, nil
}

// Stop 停止指定会话的进程
func (s *LauncherService) Stop(sessionID string) error {
	s.log.Info("launcher", "停止进程", "sessionID="+sessionID)
	s.mu.Lock()
	cmd, ok := s.processes[sessionID]
	if !ok {
		s.mu.Unlock()
		s.log.Debug("launcher", "进程已不存在", "sessionID="+sessionID)
		return nil
	}
	delete(s.processes, sessionID)
	s.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := cmd.Process.Kill(); err != nil {
		s.log.Error("launcher", "杀死进程失败", fmt.Sprintf("sessionID=%s err=%v", sessionID, err))
		return fmt.Errorf("kill process: %w", err)
	}
	_ = cmd.Wait()
	s.log.Info("launcher", "进程已停止", "sessionID="+sessionID)
	return nil
}

// StopAll 停止所有进程
func (s *LauncherService) StopAll() {
	s.log.Info("launcher", "停止所有进程")
	s.mu.Lock()
	cmds := make(map[string]*exec.Cmd, len(s.processes))
	for k, v := range s.processes {
		cmds[k] = v
	}
	s.processes = make(map[string]*exec.Cmd)
	s.mu.Unlock()

	for id, cmd := range cmds {
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			s.log.Info("launcher", "进程已停止", "sessionID="+id)
		}
	}
}

// IsRunning 检查指定会话是否在运行
func (s *LauncherService) IsRunning(sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.processes[sessionID]
	return ok
}

// RunningCount 返回运行中的进程数
func (s *LauncherService) RunningCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.processes)
}

// BuildEnv merges base env with overrides, where overrides win.
// If an override value is "", the key is DELETED from the output environment.
func BuildEnv(base []string, overrides map[string]string) []string {
	// Preserve stable ordering from base; apply overrides; append new keys.
	values := make(map[string]string, len(base)+len(overrides))
	order := make([]string, 0, len(base)+len(overrides))
	seen := make(map[string]struct{}, len(base)+len(overrides))
	keyMap := make(map[string]string, len(base)+len(overrides))
	caseInsensitive := runtime.GOOS == "windows"
	normalizeKey := func(key string) string {
		if caseInsensitive {
			return strings.ToUpper(key)
		}
		return key
	}

	for _, kv := range base {
		k, v := splitEnvKV(kv)
		if k == "" {
			continue
		}
		nk := normalizeKey(k)
		actualKey, ok := keyMap[nk]
		if !ok {
			actualKey = k
			keyMap[nk] = actualKey
		}
		values[actualKey] = v
		if _, ok := seen[actualKey]; !ok {
			seen[actualKey] = struct{}{}
			order = append(order, actualKey)
		}
	}

	for k, v := range overrides {
		nk := normalizeKey(k)
		actualKey, ok := keyMap[nk]
		if !ok {
			actualKey = k
			keyMap[nk] = actualKey
		}
		if v == "" {
			// Empty override = delete the key from the environment entirely.
			// This prevents Claude Code from seeing a defined-but-empty auth variable
			// and mistakenly using it instead of the correct one.
			delete(values, actualKey)
			continue
		}
		values[actualKey] = v
		if _, ok := seen[actualKey]; !ok {
			seen[actualKey] = struct{}{}
			order = append(order, actualKey)
		}
	}

	out := make([]string, 0, len(order))
	for _, k := range order {
		if v, ok := values[k]; ok {
			out = append(out, k+"="+v)
		}
	}
	return out
}

func splitEnvKV(kv string) (key string, val string) {
	i := strings.IndexByte(kv, '=')
	if i <= 0 {
		return "", ""
	}
	return kv[:i], kv[i+1:]
}
