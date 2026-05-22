package codexplugin

import (
	"amagi-codebox/internal/platform"
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

func (s *Service) executeCodexCommand(ctx context.Context, args ...string) (*CommandResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	resolver := s.resolver
	if resolver == nil {
		resolver = platform.NewCLIResolver(platform.CurrentCapabilities())
	}
	cli, _, err := resolver.ResolveExecutable("codex", append([]string(nil), args...), os.Environ())
	if err != nil {
		return nil, fmt.Errorf("未找到 Codex CLI，请先安装或检查 PATH: %w", err)
	}

	runner := s.runner
	if runner == nil {
		runner = platform.NewProcessRunner()
	}

	joinedArgs := strings.Join(args, " ")
	if s.log != nil {
		s.log.Info("codexplugin", "执行 Codex 插件命令", joinedArgs)
	}

	processResult, err := runner.Run(runCtx, platform.CommandSpec{
		Path:   cli.Path,
		Args:   cli.Args,
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
		if result.Error == "" {
			result.Error = "Codex 插件命令执行超时，请稍后重试"
		}
		if s.log != nil {
			s.log.Error("codexplugin", "Codex 插件命令超时", joinedArgs)
		}
		return result, fmt.Errorf("codex command timed out: %s", joinedArgs)
	}

	if err != nil {
		if result.Error == "" {
			result.Error = err.Error()
		}
		if s.log != nil {
			s.log.Error("codexplugin", "Codex 插件命令执行失败", fmt.Sprintf("args=%s error=%s", joinedArgs, result.Error))
		}
		return result, fmt.Errorf("Codex 插件命令执行失败：%s", result.Error)
	}

	if s.log != nil {
		s.log.Info("codexplugin", "Codex 插件命令执行完成", joinedArgs)
	}
	return result, nil
}
