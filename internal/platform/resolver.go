package platform

import (
	"fmt"
	"path/filepath"
	"strings"
)

type LaunchBootstrapMode string

const (
	BootstrapDirectCommand LaunchBootstrapMode = "direct-command"
	BootstrapShellInline   LaunchBootstrapMode = "shell-inline"
	BootstrapShellAttach   LaunchBootstrapMode = "shell-attach"
)

type ResolvedCLI struct {
	Name string   `json:"name"`
	Path string   `json:"path"`
	Args []string `json:"args"`
}

type ResolvedShell struct {
	Key          string `json:"key"`
	Path         string `json:"path"`
	LoginStyle   string `json:"loginStyle"`
	BootstrapArg string `json:"bootstrapArg"`
}

type ResolvedEnv struct {
	Variables        []string `json:"variables"`
	EffectivePATH    string   `json:"effectivePath"`
	AddedPATHEntries []string `json:"addedPathEntries"`
}

type ProcessPolicy struct {
	HideConsoleWindow bool `json:"hideConsoleWindow"`
	Detached          bool `json:"detached"`
}

type LaunchDiagnostics struct {
	ShellSource       string   `json:"shellSource"`
	CLISource         string   `json:"cliSource"`
	PATHSources       []string `json:"pathSources"`
	Warnings          []string `json:"warnings"`
	MissingCandidates []string `json:"missingCandidates"`
}

type ResolvedLaunchSpec struct {
	AppType        string              `json:"appType"`
	LaunchMode     string              `json:"launchMode"`
	WorkDir        string              `json:"workDir"`
	CLI            ResolvedCLI         `json:"cli"`
	Shell          *ResolvedShell      `json:"shell,omitempty"`
	BootstrapMode  LaunchBootstrapMode `json:"bootstrapMode"`
	StartupCommand string              `json:"startupCommand,omitempty"`
	Env            ResolvedEnv         `json:"env"`
	PTYCols        int                 `json:"ptyCols"`
	PTYRows        int                 `json:"ptyRows"`
	ProcessPolicy  ProcessPolicy       `json:"processPolicy"`
	Diagnostics    LaunchDiagnostics   `json:"diagnostics"`
}

type ResolveRequest struct {
	AppType            string
	LaunchMode         string
	RequestedShellPath string
	WorkDir            string
	Env                []string
	CLIArgs            []string
	PTYCols            int
	PTYRows            int
}

type CLIResolver interface {
	Resolve(request ResolveRequest) (ResolvedLaunchSpec, error)
	ResolveExecutable(command string, args []string, env []string) (ResolvedCLI, LaunchDiagnostics, error)
}

type defaultCLIResolver struct {
	capabilities PlatformCapabilities
}

func NewCLIResolver(capabilities PlatformCapabilities) CLIResolver {
	return &defaultCLIResolver{capabilities: capabilities}
}

func (r *defaultCLIResolver) Resolve(request ResolveRequest) (ResolvedLaunchSpec, error) {
	resolvedEnv, effectivePATH, addedEntries, pathSources := buildEffectiveEnvForOS(r.capabilities.OS, request.Env)
	requestedShell := strings.TrimSpace(request.RequestedShellPath)
	var resolvedShell *ResolvedShell
	shellSource := "default"
	shellWarnings := []string{}
	if requestedShell != "" {
		shell, source, warnings := resolveRequestedShell(requestedShell, resolvedEnv, r.capabilities)
		resolvedShell = &shell
		shellSource = source
		shellWarnings = warnings
	}

	cliName, err := cliNameForAppType(request.AppType)
	if err != nil {
		return ResolvedLaunchSpec{}, err
	}

	cli, diagnostics, err := r.resolveCLIForRequest(cliName, request.CLIArgs, resolvedEnv, resolvedShell)
	if err != nil {
		return ResolvedLaunchSpec{}, err
	}
	if len(pathSources) > 0 {
		diagnostics.PATHSources = append([]string(nil), pathSources...)
	}

	spec := ResolvedLaunchSpec{
		AppType:    request.AppType,
		LaunchMode: request.LaunchMode,
		WorkDir:    request.WorkDir,
		CLI:        cli,
		Env: ResolvedEnv{
			Variables:        resolvedEnv,
			EffectivePATH:    effectivePATH,
			AddedPATHEntries: addedEntries,
		},
		PTYCols:       request.PTYCols,
		PTYRows:       request.PTYRows,
		ProcessPolicy: DefaultProcessPolicy(),
		Diagnostics:   diagnostics,
	}

	// Windows OpenCode/Codex embedded: BootstrapShellAttach for historical compatibility.
	// ConPTY starts shell only; CLI command sent via PTY input stream (equivalent to user
	// typing "opencode" or "codex -m gpt-5"). This avoids OpenCode/Codex detecting an
	// inline/complete-path command line and opening an external terminal window.
	if r.capabilities.OS == "windows" && isAttachEligible(request.LaunchMode, request.AppType) {
		if resolvedShell == nil {
			shell := defaultShellForCapabilities(resolvedEnv, r.capabilities)
			resolvedShell = &shell
			shellSource = "default-attach"
		}
		spec.Shell = resolvedShell
		spec.BootstrapMode = BootstrapShellAttach
		startupCmd, err := buildAttachStartupCommandForShell(resolvedShell, cliName, request.CLIArgs)
		if err != nil {
			return ResolvedLaunchSpec{}, err
		}
		spec.StartupCommand = startupCmd
		spec.Diagnostics.ShellSource = shellSource
		spec.Diagnostics.Warnings = append(spec.Diagnostics.Warnings, shellWarnings...)
		return spec, nil
	}

	if requestedShell == "" && shouldInlineWindowsScriptWrapper(r.capabilities.OS, cli.Path) {
		shell := defaultShellForCapabilities(resolvedEnv, r.capabilities)
		resolvedShell = &shell
		shellSource = "default"
	}

	if resolvedShell == nil {
		spec.BootstrapMode = BootstrapDirectCommand
		spec.Diagnostics.ShellSource = shellSource
		spec.Diagnostics.Warnings = append(spec.Diagnostics.Warnings, shellWarnings...)
		return spec, nil
	}

	spec.Shell = resolvedShell
	spec.BootstrapMode = BootstrapShellInline
	spec.StartupCommand = buildCommandString(cli.Path, cli.Args)
	spec.Diagnostics.ShellSource = shellSource
	spec.Diagnostics.Warnings = append(spec.Diagnostics.Warnings, shellWarnings...)
	return spec, nil
}

func shouldInlineWindowsScriptWrapper(osName string, cliPath string) bool {
	if osName != "windows" {
		return false
	}
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(cliPath))) {
	case ".cmd", ".bat":
		return true
	default:
		return false
	}
}

func (r *defaultCLIResolver) resolveCLIForRequest(command string, args []string, env []string, shell *ResolvedShell) (ResolvedCLI, LaunchDiagnostics, error) {
	if r.capabilities.OS == "darwin" && shell != nil && strings.TrimSpace(shell.Path) != "" {
		if resolvedPath := resolveCommandViaShellFallback(command, env, shell); resolvedPath != "" {
			return ResolvedCLI{Name: command, Path: resolvedPath, Args: append([]string(nil), args...)}, LaunchDiagnostics{
				CLISource:   "shell-assisted",
				PATHSources: []string{"app-env", "controlled-additions", "inherited", "shell-fallback"},
			}, nil
		}
	}
	return r.ResolveExecutable(command, args, env)
}

func (r *defaultCLIResolver) ResolveExecutable(command string, args []string, env []string) (ResolvedCLI, LaunchDiagnostics, error) {
	resolvedPath, source := resolveExecutableWithEnvForOS(r.capabilities.OS, command, env)
	diagnostics := LaunchDiagnostics{
		CLISource:   source,
		PATHSources: []string{"merged-env"},
	}
	if resolvedPath == "" {
		diagnostics.MissingCandidates = []string{command}
		return ResolvedCLI{}, diagnostics, &CapabilityViolation{
			Code:             "cli_not_found",
			Message:          fmt.Sprintf("failed to resolve CLI %q", command),
			PlatformID:       r.capabilities.PlatformID,
			RequestedFeature: command,
			SuggestedAction:  "ensure the CLI is installed or configure an absolute path",
		}
	}
	return ResolvedCLI{Name: command, Path: resolvedPath, Args: append([]string(nil), args...)}, diagnostics, nil
}

func cliNameForAppType(appType string) (string, error) {
	switch strings.TrimSpace(strings.ToLower(appType)) {
	case "claudecode":
		return "claude", nil
	case "opencode":
		return "opencode", nil
	case "codex":
		return "codex", nil
	case "amagicode":
		return "amagicode", nil
	default:
		return "", fmt.Errorf("unsupported app type: %s", appType)
	}
}

func resolveRequestedShell(requestedShell string, env []string, capabilities PlatformCapabilities) (ResolvedShell, string, []string) {
	trimmed := strings.TrimSpace(requestedShell)
	warnings := []string{}
	if trimmed == "" {
		resolved := defaultShellForCapabilities(env, capabilities)
		return resolved, "default", warnings
	}

	for _, candidate := range shellCandidates(capabilities.OS) {
		if strings.EqualFold(candidate.key, trimmed) || strings.EqualFold(candidate.label, trimmed) {
			if resolvedPath := resolveCommandPathForOS(capabilities.OS, trimmed, env); resolvedPath != "" {
				return buildResolvedShell(candidate.key, resolvedPath, capabilities), "explicit", warnings
			}
			resolvedPath := resolveBinaryFromCandidatesForOS(capabilities.OS, candidate.candidates, env)
			if resolvedPath != "" {
				return buildResolvedShell(candidate.key, resolvedPath, capabilities), "explicit", warnings
			}
			break
		}
	}

	resolvedPath := resolveCommandPathForOS(capabilities.OS, trimmed, env)
	if resolvedPath != "" {
		key := strings.TrimSuffix(strings.ToLower(filepath.Base(resolvedPath)), filepath.Ext(resolvedPath))
		return buildResolvedShell(key, resolvedPath, capabilities), "explicit", warnings
	}

	defaultShell := defaultShellForCapabilities(env, capabilities)
	if defaultShell.Path != "" {
		warnings = append(warnings, fmt.Sprintf("requested shell %q was not found; falling back to %s", trimmed, defaultShell.Path))
		return defaultShell, "fallback", warnings
	}

	key := strings.TrimSuffix(strings.ToLower(filepath.Base(trimmed)), filepath.Ext(trimmed))
	return buildResolvedShell(key, trimmed, capabilities), "explicit", warnings
}

func defaultShellForCapabilities(env []string, capabilities PlatformCapabilities) ResolvedShell {
	for _, candidate := range shellCandidates(capabilities.OS) {
		if !strings.EqualFold(candidate.key, capabilities.DefaultShellKey) {
			continue
		}
		if resolvedPath := resolveBinaryFromCandidatesForOS(capabilities.OS, candidate.candidates, env); resolvedPath != "" {
			return buildResolvedShell(candidate.key, resolvedPath, capabilities)
		}
	}
	for _, candidate := range shellCandidates(capabilities.OS) {
		if resolvedPath := resolveBinaryFromCandidatesForOS(capabilities.OS, candidate.candidates, env); resolvedPath != "" {
			return buildResolvedShell(candidate.key, resolvedPath, capabilities)
		}
	}
	return ResolvedShell{}
}

func buildResolvedShell(key string, resolvedPath string, capabilities PlatformCapabilities) ResolvedShell {
	bootstrapArg := "/K"
	loginStyle := "interactive"
	if capabilities.OS != "windows" {
		bootstrapArg = "-lc"
		loginStyle = "login"
	}
	switch key {
	case "pwsh", "powershell":
		bootstrapArg = "-Command"
		loginStyle = "interactive"
	case "cmd":
		bootstrapArg = "/K"
		loginStyle = "interactive"
	case "bash", "zsh":
		bootstrapArg = "-ilc"
		loginStyle = "login"
	case "fish", "sh":
		bootstrapArg = "-lc"
		loginStyle = "login"
	}
	return ResolvedShell{Key: key, Path: resolvedPath, LoginStyle: loginStyle, BootstrapArg: bootstrapArg}
}

func buildCommandString(command string, args []string) string {
	parts := make([]string, 0, 1+len(args))
	parts = append(parts, quoteCommandPart(command))
	for _, arg := range args {
		parts = append(parts, quoteCommandPart(arg))
	}
	return strings.Join(parts, " ")
}

func quoteCommandPart(value string) string {
	if value == "" {
		return `""`
	}
	if !strings.ContainsAny(value, " \t\"") {
		return value
	}
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

// isAttachEligible returns true when the launch should use BootstrapShellAttach:
// Windows + embedded mode + OpenCode or Codex app type.
func isAttachEligible(launchMode string, appType string) bool {
	if launchMode != "" && launchMode != "embedded" {
		return false
	}
	switch strings.TrimSpace(strings.ToLower(appType)) {
	case "opencode", "codex":
		return true
	default:
		return false
	}
}

// buildAttachStartupCommandForShell builds a shell-safe startup command for
// BootstrapShellAttach mode. The command is typed into the interactive shell
// via the PTY input stream, so every token must be escaped for the target
// shell to prevent shell metacharacters from being interpreted.
// Returns an error if args contain characters that cannot be safely escaped
// for the target shell (e.g. % ! CR LF for cmd.exe, or CR LF for PowerShell).
func buildAttachStartupCommandForShell(shell *ResolvedShell, cliName string, args []string) (string, error) {
	if shell == nil {
		return buildAttachFallback(cliName, args), nil
	}
	switch shell.Key {
	case "pwsh", "powershell":
		return buildAttachPowerShell(cliName, args)
	case "cmd":
		return buildAttachCmd(cliName, args)
	default:
		return buildAttachFallback(cliName, args), nil
	}
}

// buildAttachPowerShell produces a command safe for interactive PowerShell input.
// Uses the call operator & with single-quoted tokens:
//
//	& 'codex' '-m' 'gpt&5'
//
// Single quotes prevent all PowerShell expansion ($, (), [], etc.).
// Internal single quotes are doubled ('' escape).
// Returns an error if any arg contains CR or LF which would split the command.
func buildAttachPowerShell(cliName string, args []string) (string, error) {
	if err := validateNoNewlines(append(append([]string(nil), cliName), args...)...); err != nil {
		return "", err
	}
	parts := make([]string, 0, 1+len(args)+1)
	parts = append(parts, "&")
	parts = append(parts, "'"+escapePowerShellSingleQuote(cliName)+"'")
	for _, arg := range args {
		parts = append(parts, "'"+escapePowerShellSingleQuote(arg)+"'")
	}
	return strings.Join(parts, " "), nil
}

func escapePowerShellSingleQuote(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

// buildAttachCmd produces a command safe for interactive cmd.exe input.
// Uses double-quoted tokens to prevent & | < > from acting as command
// separators. Internal double quotes are escaped as "".
// Returns an error if any arg contains %, !, CR, or LF -- these cannot be
// safely neutralised in all cmd.exe contexts and indicate a suspicious input.
func buildAttachCmd(cliName string, args []string) (string, error) {
	allTokens := append(append([]string(nil), cliName), args...)
	if err := validateSafeCmdArgs(allTokens); err != nil {
		return "", err
	}
	parts := make([]string, 0, 1+len(args))
	parts = append(parts, escapeCmdArg(cliName))
	for _, arg := range args {
		parts = append(parts, escapeCmdArg(arg))
	}
	return strings.Join(parts, " "), nil
}

// validateSafeCmdArgs rejects tokens containing characters that cannot be
// safely escaped for cmd.exe interactive input: %, !, CR, LF.
func validateSafeCmdArgs(tokens []string) error {
	for _, tok := range tokens {
		for i := 0; i < len(tok); i++ {
			switch tok[i] {
			case '%':
				return &CapabilityViolation{
					Code:             "unsafe_attach_arg",
					Message:          fmt.Sprintf("attach command arg %q contains '%%' which cannot be safely escaped for cmd.exe", tok),
					SuggestedAction:  "ensure model names do not contain percent signs",
					RequestedFeature: "shell-attach-cmd",
				}
			case '!':
				return &CapabilityViolation{
					Code:             "unsafe_attach_arg",
					Message:          fmt.Sprintf("attach command arg %q contains '!' which cannot be safely escaped for cmd.exe", tok),
					SuggestedAction:  "ensure model names do not contain exclamation marks",
					RequestedFeature: "shell-attach-cmd",
				}
			case '\r', '\n':
				return &CapabilityViolation{
					Code:             "unsafe_attach_arg",
					Message:          fmt.Sprintf("attach command arg %q contains a newline character which would split the command", tok),
					SuggestedAction:  "ensure model names do not contain newline characters",
					RequestedFeature: "shell-attach-cmd",
				}
			}
		}
	}
	return nil
}

// validateNoNewlines rejects tokens containing CR or LF characters.
func validateNoNewlines(tokens ...string) error {
	for _, tok := range tokens {
		if strings.ContainsRune(tok, '\r') || strings.ContainsRune(tok, '\n') {
			return &CapabilityViolation{
				Code:             "unsafe_attach_arg",
				Message:          fmt.Sprintf("attach command arg %q contains a newline character which would split the command", tok),
				SuggestedAction:  "ensure model names do not contain newline characters",
				RequestedFeature: "shell-attach",
			}
		}
	}
	return nil
}

// escapeCmdArg wraps a cmd.exe token in double quotes when necessary and
// escapes internal double quotes. Bare alphanumeric tokens and safe flags
// like -m pass through unquoted for readability.
// Caller must have already validated via validateSafeCmdArgs.
func escapeCmdArg(value string) string {
	if value == "" {
		return `""`
	}
	if isSafeCmdToken(value) {
		return value
	}
	escaped := strings.ReplaceAll(value, `"`, `""`)
	return `"` + escaped + `"`
}

// isSafeCmdToken returns true for tokens that contain only characters safe
// to pass unquoted on a cmd.exe interactive command line: alphanumeric,
// hyphens, underscores, dots, forward/back slashes, colons, equals, and @.
func isSafeCmdToken(value string) bool {
	for _, ch := range value {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') ||
			ch == '-' || ch == '_' || ch == '.' || ch == '/' || ch == '\\' || ch == ':' || ch == '=' || ch == '@') {
			return false
		}
	}
	return true
}

// buildAttachFallback is a conservative fallback for unknown shells.
// Uses the generic buildCommandString which applies basic quoting.
func buildAttachFallback(cliName string, args []string) string {
	return buildCommandString(cliName, args)
}
