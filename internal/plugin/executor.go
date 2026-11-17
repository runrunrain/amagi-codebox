package plugin

import (
	"amagi-codebox/internal/platform"
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// runCLI 是只读 CLI 查询的统一入口：测试通过 execCLI 字段注入替身，
// 生产环境（execCLI 为 nil）走真实 executeClaudeCommand。写操作不经此入口。
func (s *Service) runCLI(args ...string) (*CommandResult, error) {
	if s.execCLI != nil {
		return s.execCLI(args...)
	}
	return s.executeClaudeCommand(args...)
}

func (s *Service) executeClaudeCommand(args ...string) (*CommandResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resolver := platform.NewCLIResolver(platform.CurrentCapabilities())
	env := platform.BuildEffectiveEnv(os.Environ())
	cli, _, err := resolver.ResolveExecutable("claude", append([]string(nil), args...), env)
	if err != nil {
		return nil, fmt.Errorf("未找到 Claude CLI，请先安装或检查 PATH: %w", err)
	}

	runner := platform.NewProcessRunner()

	joinedArgs := strings.Join(args, " ")
	if s.log != nil {
		s.log.Info("plugin", "执行 Claude 插件命令", joinedArgs)
	}

	resultSpec, err := runner.Run(ctx, platform.CommandSpec{
		Path:   cli.Path,
		Args:   cli.Args,
		Env:    env,
		Policy: platform.DefaultProcessPolicy(),
	})
	if resultSpec == nil {
		resultSpec = &platform.ProcessResult{}
	}
	result := &CommandResult{
		Success: err == nil,
		Output:  strings.TrimSpace(resultSpec.Stdout),
		Error:   strings.TrimSpace(resultSpec.Stderr),
	}

	if ctx.Err() == context.DeadlineExceeded {
		if result.Error == "" {
			result.Error = "command timed out after 60 seconds"
		} else {
			result.Error += "\ncommand timed out after 60 seconds"
		}
		if s.log != nil {
			s.log.Error("plugin", "Claude 插件命令超时", joinedArgs)
		}
		return result, fmt.Errorf("claude command timed out: %s", joinedArgs)
	}

	if err != nil {
		if result.Error == "" {
			result.Error = err.Error()
		}
		if s.log != nil {
			s.log.Error("plugin", "Claude 插件命令执行失败", fmt.Sprintf("args=%s error=%s", joinedArgs, result.Error))
		}
		return result, fmt.Errorf("Claude 插件命令执行失败：%s", result.Error)
	}

	if s.log != nil {
		s.log.Info("plugin", "Claude 插件命令执行完成", joinedArgs)
	}

	return result, nil
}
