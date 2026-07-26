package opencodeplugin

import (
	"amagi-codebox/internal/platform"
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

func (s *Service) executeOpenCodeCommand(ctx context.Context, args ...string) (*CommandResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	resolver := s.resolver
	if resolver == nil {
		resolver = platform.NewCLIResolver(platform.CurrentCapabilities())
	}
	env := platform.BuildEffectiveEnv(os.Environ())
	cli, _, err := resolver.ResolveExecutable("opencode", append([]string(nil), args...), env)
	if err != nil {
		return nil, fmt.Errorf("未找到 OpenCode CLI，请先安装或检查 PATH: %w", err)
	}

	runner := s.runner
	if runner == nil {
		runner = platform.NewProcessRunner()
	}
	joinedArgs := strings.Join(args, " ")
	if s.log != nil {
		s.log.Info("opencodeplugin", "执行 OpenCode 插件命令", joinedArgs)
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
			result.Error = "OpenCode 插件命令执行超时，请稍后重试"
		}
		return result, fmt.Errorf("OpenCode 插件命令执行超时: %s", joinedArgs)
	}
	if err != nil {
		if result.Error == "" {
			result.Error = err.Error()
		}
		if s.log != nil {
			s.log.Error("opencodeplugin", "OpenCode 插件命令执行失败", fmt.Sprintf("args=%s error=%s", joinedArgs, result.Error))
		}
		return result, fmt.Errorf("OpenCode 插件命令执行失败：%s", result.Error)
	}
	if s.log != nil {
		s.log.Info("opencodeplugin", "OpenCode 插件命令执行完成", joinedArgs)
	}
	return result, nil
}
