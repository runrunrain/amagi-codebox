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
	cliName, err := cliNameForAppType(request.AppType)
	if err != nil {
		return ResolvedLaunchSpec{}, err
	}

	cli, diagnostics, err := r.ResolveExecutable(cliName, request.CLIArgs, request.Env)
	if err != nil {
		return ResolvedLaunchSpec{}, err
	}

	spec := ResolvedLaunchSpec{
		AppType:    request.AppType,
		LaunchMode: request.LaunchMode,
		WorkDir:    request.WorkDir,
		CLI:        cli,
		Env: ResolvedEnv{
			Variables:     append([]string(nil), request.Env...),
			EffectivePATH: envValue(request.Env, "PATH"),
		},
		PTYCols:       request.PTYCols,
		PTYRows:       request.PTYRows,
		ProcessPolicy: DefaultProcessPolicy(),
		Diagnostics:   diagnostics,
	}

	requestedShell := strings.TrimSpace(request.RequestedShellPath)
	if requestedShell == "" {
		spec.BootstrapMode = BootstrapDirectCommand
		return spec, nil
	}

	resolvedShell := resolveRequestedShell(requestedShell, request.Env, r.capabilities)
	spec.Shell = &resolvedShell
	spec.BootstrapMode = BootstrapShellInline
	spec.StartupCommand = buildCommandString(cli.Path, cli.Args)
	spec.Diagnostics.ShellSource = "explicit"
	return spec, nil
}

func (r *defaultCLIResolver) ResolveExecutable(command string, args []string, env []string) (ResolvedCLI, LaunchDiagnostics, error) {
	resolvedPath, source := resolveExecutableWithEnv(command, env)
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

func resolveRequestedShell(requestedShell string, env []string, capabilities PlatformCapabilities) ResolvedShell {
	resolvedPath := resolveCommandPath(requestedShell, env)
	if resolvedPath == "" {
		resolvedPath = requestedShell
	}

	key := strings.TrimSuffix(strings.ToLower(filepath.Base(resolvedPath)), filepath.Ext(resolvedPath))
	bootstrapArg := "/K"
	loginStyle := "interactive"
	if capabilities.OS != "windows" {
		bootstrapArg = "-lc"
		loginStyle = "login"
	}
	if key == "pwsh" || key == "powershell" {
		bootstrapArg = "-Command"
		if capabilities.OS != "windows" {
			bootstrapArg = "-lc"
		}
	}

	return ResolvedShell{
		Key:          key,
		Path:         resolvedPath,
		LoginStyle:   loginStyle,
		BootstrapArg: bootstrapArg,
	}
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
