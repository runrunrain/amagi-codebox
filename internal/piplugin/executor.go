package piplugin

import (
	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/platform"
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// piCommandTimeout 是 pi install/remove/update 的最长等待时间。
// 这些命令可能触发 npm install / git clone，故给到 3 分钟。
const piCommandTimeout = 3 * time.Minute

// executePiCommand 运行 pi CLI（pi install/remove/update 等写操作走这里）。
// 读操作（list/details）不走本函数，而是直接解析 settings.json + 扫描实体目录，
// 避免 fork CLI 带来的开销与不确定性。
func (s *Service) executePiCommand(ctx context.Context, args ...string) (*CommandResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithTimeout(ctx, piCommandTimeout)
	defer cancel()

	resolver := s.resolver
	if resolver == nil {
		resolver = platform.NewCLIResolver(platform.CurrentCapabilities())
	}
	env := platform.BuildEffectiveEnv(os.Environ())
	// 强制 pi CLI 在服务指定的标准用户 agentDir（~/.pi/agent）上
	// 执行写操作，确保插件面板与普通 Pi/CodeBox Pi 共享配置。
	env = launcher.BuildEnv(env, map[string]string{"PI_CODING_AGENT_DIR": s.agentDir})
	// Dir=agentDir 要求目录存在：全新机器上 ~/.pi/agent 只会在 pi 会话首次
	// 启动时由 launcher 创建（launcher/pi_config.go），插件面板可能先于任何
	// pi 会话使用；若目录缺失，exec 会在启动 pi CLI 前就 chdir 失败。先确保
	// agent 根存在（权限对齐 launcher 写 agent 目录的 0700）。
	if err := os.MkdirAll(s.agentDir, 0o700); err != nil {
		return nil, fmt.Errorf("创建 pi agent 目录失败: %w", err)
	}
	cli, _, err := resolver.ResolveExecutable(piExecutable, append([]string(nil), args...), env)
	if err != nil {
		return nil, fmt.Errorf("未找到 pi CLI，请先安装或检查 PATH: %w", err)
	}

	runner := s.runner
	if runner == nil {
		runner = platform.NewProcessRunner()
	}
	joinedArgs := strings.Join(args, " ")
	if s.log != nil {
		s.log.Info("piplugin", "执行 pi 包命令", joinedArgs)
	}

	processResult, err := runner.Run(runCtx, platform.CommandSpec{
		Path: cli.Path,
		Args: cli.Args,
		Env:  env,
		// v1.3.23 修复：pi remove/update 对 local 源的匹配 key 输入侧按 process.cwd()
		// 解析相对路径、settings 侧按 agentDir 解析（pi package-manager.js
		// getSourceMatchKeyForInput vs ForSettings）。GUI 进程 cwd（通常 /）≠ agentDir
		// 时面板传 settings 原样字符串（如 ../../maorun-workpace/amagi-pi）必失配
		// （实战：remove/switch 报 No matching package found）。统一 cwd=agentDir 根治。
		Dir:    s.agentDir,
		Policy: platform.DefaultProcessPolicy(),
	})
	if processResult == nil {
		processResult = &platform.ProcessResult{}
	}
	result := &CommandResult{
		Success: err == nil,
		Output:  strings.TrimSpace(processResult.Stdout),
		Error:   strings.TrimSpace(processResult.Stderr),
	}
	if runCtx.Err() == context.DeadlineExceeded {
		result.Success = false
		if result.Error == "" {
			result.Error = "pi 包命令执行超时，请稍后重试"
		}
		return result, fmt.Errorf("pi 包命令执行超时: %s", joinedArgs)
	}
	if err != nil {
		if result.Error == "" {
			result.Error = err.Error()
		}
		if s.log != nil {
			s.log.Error("piplugin", "pi 包命令执行失败", fmt.Sprintf("args=%s error=%s", joinedArgs, result.Error))
		}
		return result, fmt.Errorf("pi 包命令执行失败：%s", result.Error)
	}
	if s.log != nil {
		s.log.Info("piplugin", "pi 包命令执行完成", joinedArgs)
	}
	return result, nil
}
