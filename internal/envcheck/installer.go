package envcheck

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"amagi-codebox/internal/platform"
)

const (
	installCommandTimeout = 120 * time.Second

	installOperationInstall installOperation = "install"
	installOperationUpdate  installOperation = "update"
)

type installOperation string

type installCommand struct {
	description string
	path        string
	args        []string
}

func (s *Service) installOrUpdate(tool CLITool, operation installOperation) (*InstallResult, error) {
	if !IsValidCLITool(tool) {
		return nil, fmt.Errorf("unsupported CLI tool: %s", tool)
	}

	before, checkErr := s.CheckOne(tool)
	if checkErr != nil && before == nil {
		return installFailure(tool, fmt.Sprintf("pre-%s check failed", operation), checkErr), checkErr
	}

	if operation == installOperationInstall && isHealthyAndCurrent(before) {
		return &InstallResult{
			Success: true,
			Message: fmt.Sprintf("%s is already installed and up to date", displayToolName(tool)),
			Tool:    tool,
			Version: before.Version,
		}, nil
	}
	if operation == installOperationUpdate && isHealthyAndCurrent(before) {
		return &InstallResult{
			Success: true,
			Message: fmt.Sprintf("%s is already up to date", displayToolName(tool)),
			Tool:    tool,
			Version: before.Version,
		}, nil
	}

	commands, err := s.installCommands(tool, operation, before)
	if err != nil {
		return installFailure(tool, fmt.Sprintf("prepare %s command failed", operation), err), err
	}

	var lastErr error
	var attempts []string
	for _, command := range commands {
		attempts = append(attempts, command.description)
		if err := s.runInstallCommand(command); err != nil {
			lastErr = err
			continue
		}
		lastErr = nil
		break
	}
	if lastErr != nil {
		message := fmt.Sprintf("%s %s failed after trying %s", displayToolName(tool), operation, strings.Join(attempts, ", "))
		return installFailure(tool, message, lastErr), lastErr
	}

	after, verifyErr := s.CheckOne(tool)
	if verifyErr != nil && after == nil {
		return installFailure(tool, fmt.Sprintf("%s %s completed but verification failed", displayToolName(tool), operation), verifyErr), verifyErr
	}
	if after == nil || !after.Installed || !after.PATHOk || strings.TrimSpace(after.Error) != "" {
		verifyMessage := verificationErrorMessage(after)
		err := errors.New(verifyMessage)
		return installFailure(tool, fmt.Sprintf("%s %s completed but verification failed", displayToolName(tool), operation), err), err
	}

	// For update operations, verify the version actually changed.
	if operation == installOperationUpdate {
		beforeVersion := ""
		if before != nil {
			beforeVersion = strings.TrimSpace(before.Version)
		}
		afterVersion := strings.TrimSpace(after.Version)
		if beforeVersion != "" && afterVersion == beforeVersion {
			errMsg := fmt.Sprintf(
				"update command ran but version was unchanged (%s) - the CLI might be running or locked",
				afterVersion,
			)
			err := errors.New(errMsg)
			return installFailure(tool, errMsg, err), err
		}
	}

	return &InstallResult{
		Success: true,
		Message: fmt.Sprintf("%s %s completed successfully", displayToolName(tool), operation),
		Tool:    tool,
		Version: after.Version,
	}, nil
}

func (s *Service) installCommands(tool CLITool, operation installOperation, current *CheckStatus) ([]installCommand, error) {
	switch tool {
	case ToolClaudeCode:
		baseCmds := claudeInstallCommands(operation, current)
		npmCmd := npmClaudeCommand(operation)

		// If installed via NPM, prefer NPM command first.
		if current != nil && current.InstallMethod == InstallMethodNPM {
			if err := s.ensureNPMAvailable(); err != nil {
				return nil, err
			}
			return append([]installCommand{npmCmd}, baseCmds...), nil
		}
		// For other install methods, try NPM as last resort (best-effort).
		if s.ensureNPMAvailable() == nil {
			baseCmds = append(baseCmds, npmCmd)
		}
		return baseCmds, nil
	case ToolOpenCode:
		if err := s.ensureNPMAvailable(); err != nil {
			return nil, err
		}
		return []installCommand{npmOpenCodeCommand(operation)}, nil
	case ToolCodex:
		if err := s.ensureNPMAvailable(); err != nil {
			return nil, err
		}
		return []installCommand{npmCodexCommand(operation)}, nil
	default:
		return nil, fmt.Errorf("unsupported CLI tool: %s", tool)
	}
}

// npmClaudeCommand returns the npm install or update command for Claude Code.
func npmClaudeCommand(operation installOperation) installCommand {
	if operation == installOperationUpdate {
		return installCommand{
			description: "npm global update @anthropic-ai/claude-code",
			path:        "npm",
			args:        []string{"update", "-g", "@anthropic-ai/claude-code"},
		}
	}
	return installCommand{
		description: "npm global install @anthropic-ai/claude-code",
		path:        "npm",
		args:        []string{"install", "-g", "@anthropic-ai/claude-code"},
	}
}

// npmOpenCodeCommand returns the npm install or update command for OpenCode.
func npmOpenCodeCommand(operation installOperation) installCommand {
	if operation == installOperationUpdate {
		return installCommand{
			description: "npm global update opencode-ai",
			path:        "npm",
			args:        []string{"update", "-g", "opencode-ai"},
		}
	}
	return installCommand{
		description: "npm global install opencode-ai@latest",
		path:        "npm",
		args:        []string{"install", "-g", "opencode-ai@latest"},
	}
}

// npmCodexCommand returns the npm install or update command for Codex.
func npmCodexCommand(operation installOperation) installCommand {
	if operation == installOperationUpdate {
		return installCommand{
			description: "npm global update @openai/codex",
			path:        "npm",
			args:        []string{"update", "-g", "@openai/codex"},
		}
	}
	return installCommand{
		description: "npm global install @openai/codex",
		path:        "npm",
		args:        []string{"install", "-g", "@openai/codex"},
	}
}

func claudeInstallCommands(operation installOperation, current *CheckStatus) []installCommand {
	native := installCommand{
		description: "Claude Code native PowerShell installer",
		path:        "powershell.exe",
		args: []string{
			"-NoProfile",
			"-NonInteractive",
			"-ExecutionPolicy", "RemoteSigned",
			"-Command", "irm https://claude.ai/install.ps1 | iex",
		},
	}

	wingetAction := "install"
	wingetDescription := "winget install Anthropic.ClaudeCode"
	if operation == installOperationUpdate {
		wingetAction = "upgrade"
		wingetDescription = "winget upgrade Anthropic.ClaudeCode"
	}
	winget := installCommand{
		description: wingetDescription,
		path:        "winget",
		args:        []string{wingetAction, "Anthropic.ClaudeCode", "--accept-source-agreements", "--accept-package-agreements"},
	}

	if operation == installOperationUpdate && current != nil && current.InstallMethod == InstallMethodWinget {
		return []installCommand{winget, native}
	}
	return []installCommand{native, winget}
}

func (s *Service) ensureNPMAvailable() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, err := s.processRunner.Run(ctx, platform.CommandSpec{
		Path:   "npm",
		Args:   []string{"--version"},
		Policy: platform.DefaultProcessPolicy(),
	})
	if err == nil {
		return nil
	}
	message := strings.TrimSpace(resultText(result))
	if message == "" {
		message = err.Error()
	}
	return fmt.Errorf("npm is required to install this tool, but npm is not available in PATH; install Node.js first: %s", message)
}

func (s *Service) runInstallCommand(command installCommand) error {
	ctx, cancel := context.WithTimeout(context.Background(), installCommandTimeout)
	defer cancel()

	result, err := s.processRunner.Run(ctx, platform.CommandSpec{
		Path:   command.path,
		Args:   append([]string(nil), command.args...),
		Policy: platform.DefaultProcessPolicy(),
	})
	if err == nil {
		return nil
	}

	message := strings.TrimSpace(resultText(result))
	if message == "" {
		message = err.Error()
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		message = fmt.Sprintf("timed out after %s: %s", installCommandTimeout, message)
	}
	if errors.Is(err, exec.ErrNotFound) {
		message = fmt.Sprintf("command %q was not found in PATH: %s", command.path, message)
	}
	return fmt.Errorf("%s: %s", command.description, message)
}

func isHealthyAndCurrent(status *CheckStatus) bool {
	return status != nil &&
		status.Installed &&
		status.PATHOk &&
		!status.HasUpdate &&
		strings.TrimSpace(status.Error) == ""
}

func installFailure(tool CLITool, message string, err error) *InstallResult {
	errorMessage := ""
	if err != nil {
		errorMessage = err.Error()
	}
	return &InstallResult{
		Success: false,
		Message: message,
		Tool:    tool,
		Error:   errorMessage,
	}
}

func verificationErrorMessage(status *CheckStatus) string {
	if status == nil {
		return "tool status is empty after installation"
	}
	if strings.TrimSpace(status.Error) != "" {
		return status.Error
	}
	if !status.Installed {
		return "tool executable was not found after installation"
	}
	if !status.PATHOk {
		return "tool was found outside PATH; restart the application or terminal after PATH changes"
	}
	return "tool verification did not report a usable installation"
}

func displayToolName(tool CLITool) string {
	switch tool {
	case ToolClaudeCode:
		return "Claude Code"
	case ToolOpenCode:
		return "OpenCode"
	case ToolCodex:
		return "Codex"
	default:
		return string(tool)
	}
}
