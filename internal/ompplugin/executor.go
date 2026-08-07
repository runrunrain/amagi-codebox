package ompplugin

import (
	"amagi-codebox/internal/platform"
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// omp 命令超时档位（读快写慢）：
//   - list：仅查询本地注册表，15s 足够；
//   - enable/disable/uninstall：本地状态变更，30s；
//   - install/upgrade：可能触发 npm install / git clone，给到 3 分钟。
const (
	ompListTimeout        = 15 * time.Second
	ompPluginWriteTimeout = 30 * time.Second
	ompInstallTimeout     = 3 * time.Minute
)

// executeOmpCommand 运行 omp CLI（读与写操作统一走这里，piplugin 同款模板）。
// timeout 由调用方按操作档位传入；超时与退出错误均落入 CommandResult 后返回。
//
// 环境差异（与 piplugin 的关键区别）：piplugin 在 fork pi CLI 时显式注入
// PI_CODING_AGENT_DIR 指向标准 agent 根；omp 不消费该变量——它只重定位
// LaunchOmpSession 的会话目录（且启动时显式删除旧值，避免把 omp 导向独立
// 副本），插件 CLI 天然操作 ~/.omp/plugins（实测 plugin list 不受其影响）。
// 因此这里仅做 BuildEffectiveEnv 的 PATH 归一化，不注入 PI_CODING_AGENT_DIR。
func (s *Service) executeOmpCommand(ctx context.Context, timeout time.Duration, args ...string) (*CommandResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resolver := s.resolver
	if resolver == nil {
		resolver = platform.NewCLIResolver(platform.CurrentCapabilities())
	}
	env := platform.BuildEffectiveEnv(os.Environ())
	cli, _, err := resolver.ResolveExecutable(ompExecutable, append([]string(nil), args...), env)
	if err != nil {
		return nil, fmt.Errorf("未找到 omp CLI，请先安装或检查 PATH: %w", err)
	}

	runner := s.runner
	if runner == nil {
		runner = platform.NewProcessRunner()
	}
	joinedArgs := strings.Join(args, " ")
	if s.log != nil {
		s.log.Info("ompplugin", "执行 omp 插件命令", joinedArgs)
	}

	processResult, err := runner.Run(runCtx, platform.CommandSpec{
		Path:   cli.Path,
		Args:   cli.Args,
		Env:    env,
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
			result.Error = "omp 插件命令执行超时，请稍后重试"
		}
		return result, fmt.Errorf("omp 插件命令执行超时: %s", joinedArgs)
	}
	if err != nil {
		if result.Error == "" {
			result.Error = err.Error()
		}
		if s.log != nil {
			s.log.Error("ompplugin", "omp 插件命令执行失败", fmt.Sprintf("args=%s error=%s", joinedArgs, result.Error))
		}
		return result, fmt.Errorf("omp 插件命令执行失败：%s", result.Error)
	}
	if s.log != nil {
		s.log.Info("ompplugin", "omp 插件命令执行完成", joinedArgs)
	}
	return result, nil
}
