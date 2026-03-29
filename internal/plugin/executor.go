package plugin

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

func (s *Service) executeClaudeCommand(args ...string) (*CommandResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	joinedArgs := strings.Join(args, " ")
	if s.log != nil {
		s.log.Info("plugin", "执行 Claude 插件命令", joinedArgs)
	}

	err := cmd.Run()
	result := &CommandResult{
		Success: err == nil,
		Output:  strings.TrimSpace(stdout.String()),
		Error:   strings.TrimSpace(stderr.String()),
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
