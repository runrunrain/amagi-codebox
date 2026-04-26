package plugin

import (
	"amagi-codebox/internal/platform"
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

func (s *Service) executeClaudeCommand(args ...string) (*CommandResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resolver := platform.NewCLIResolver(platform.CurrentCapabilities())
	cli, _, err := resolver.ResolveExecutable("claude", args, os.Environ())
	if err != nil {
		return nil, err
	}

	runner := platform.NewProcessRunner()

	joinedArgs := strings.Join(args, " ")
	if s.log != nil {
		s.log.Info("plugin", "执行 Claude 插件命令", joinedArgs)
	}

	resultSpec, err := runner.Run(ctx, platform.CommandSpec{
		Path:   cli.Path,
		Args:   cli.Args,
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
		return result, fmt.Errorf("run claude %s: %w", joinedArgs, err)
	}

	if s.log != nil {
		s.log.Info("plugin", "Claude 插件命令执行完成", joinedArgs)
	}

	return result, nil
}
