package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/envvars"
	"amagi-codebox/internal/paths"
	"amagi-codebox/internal/plugin"
	"amagi-codebox/internal/secrets"
	"amagi-codebox/internal/settings"
)

var Version = "dev"

type cliState struct {
	configDir   string
	claudeDir   string
	configSvc   *config.ConfigService
	secretsSvc  *secrets.SecretsService
	pathsSvc    *paths.PathsService
	settingsSvc *settings.Service
	envVarsSvc  *envvars.EnvVarsService
	pluginSvc   *plugin.Service
}

type namedProvider struct {
	Name string `json:"name"`
	config.Provider
}

type secretInfo struct {
	Provider     string            `json:"provider"`
	Source       string            `json:"source"`
	MaskedKey    string            `json:"maskedKey"`
	KeyLength    int               `json:"keyLength"`
	HasStored    bool              `json:"hasStored"`
	StoredMasked string            `json:"storedMasked,omitempty"`
	Environment  map[string]string `json:"environment,omitempty"`
}

func main() {
	pretty, args := stripPrettyFlag(os.Args[1:])
	if len(args) == 0 {
		printUsage()
		os.Exit(1)
	}

	state := &cliState{
		configDir: defaultConfigDir(),
		claudeDir: defaultClaudeDir(),
	}

	var err error
	switch args[0] {
	case "config":
		err = handleConfig(state, args[1:], pretty)
	case "secrets":
		err = handleSecrets(state, args[1:], pretty)
	case "paths":
		err = handlePaths(state, args[1:], pretty)
	case "plugin":
		err = handlePlugin(state, args[1:], pretty)
	case "settings":
		err = handleSettings(state, args[1:], pretty)
	case "envvars":
		err = handleEnvVars(state, args[1:], pretty)
	case "info":
		err = handleInfo(state, pretty)
	case "version":
		err = writeJSON(map[string]any{"version": resolvedVersion()}, pretty)
	case "help", "-h", "--help":
		printUsage()
		return
	default:
		printUsage()
		fatalf(pretty, "unknown command: %s", args[0])
	}

	if err != nil {
		fatal(pretty, err)
	}
}

func handleConfig(state *cliState, args []string, pretty bool) error {
	if len(args) == 0 {
		return fmt.Errorf("missing config subcommand")
	}

	svc, err := state.getConfigService()
	if err != nil {
		return err
	}

	switch args[0] {
	case "list":
		names := svc.GetProviderNames()
		sort.Strings(names)
		providers := make([]namedProvider, 0, len(names))
		for _, name := range names {
			provider, err := svc.GetProvider(name)
			if err != nil {
				return err
			}
			providers = append(providers, namedProvider{Name: name, Provider: *provider})
		}
		return writeJSON(map[string]any{"providers": providers}, pretty)
	case "get":
		if len(args) < 2 {
			return fmt.Errorf("usage: codebox config get <provider>")
		}
		provider, err := svc.GetProvider(args[1])
		if err != nil {
			return err
		}
		return writeJSON(namedProvider{Name: args[1], Provider: *provider}, pretty)
	case "export":
		fs := newFlagSet("config export")
		output := fs.String("output", "", "write export JSON to file")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if len(fs.Args()) != 0 {
			return fmt.Errorf("usage: codebox config export [--output file]")
		}

		exportCfg, err := buildExportConfig(state)
		if err != nil {
			return err
		}

		if *output != "" {
			if err := writeJSONFile(*output, exportCfg); err != nil {
				return err
			}
			return writeJSON(map[string]any{
				"written":   *output,
				"providers": len(exportCfg.Providers),
			}, pretty)
		}

		return writeJSON(exportCfg, pretty)
	case "import":
		if len(args) != 2 {
			return fmt.Errorf("usage: codebox config import <file>")
		}
		result, err := importConfigFile(state, args[1])
		if err != nil {
			return err
		}
		return writeJSON(result, pretty)
	default:
		return fmt.Errorf("unknown config subcommand: %s", args[0])
	}
}

func handleSecrets(state *cliState, args []string, pretty bool) error {
	if len(args) == 0 {
		return fmt.Errorf("missing secrets subcommand")
	}

	secretsSvc, err := state.getSecretsService()
	if err != nil {
		return err
	}

	switch args[0] {
	case "list":
		names, err := state.secretProviderNames()
		if err != nil {
			return err
		}
		items := make([]secretInfo, 0, len(names))
		for _, name := range names {
			items = append(items, state.secretInfo(name))
		}
		return writeJSON(map[string]any{"secrets": items}, pretty)
	case "get":
		if len(args) != 2 {
			return fmt.Errorf("usage: codebox secrets get <provider>")
		}
		return writeJSON(state.secretInfo(args[1]), pretty)
	case "set":
		if len(args) < 3 {
			return fmt.Errorf("usage: codebox secrets set <provider> <key>")
		}
		provider := args[1]
		key := strings.Join(args[2:], " ")
		if err := secretsSvc.SetAPIKey(provider, key); err != nil {
			return err
		}
		if err := secretsSvc.Save(); err != nil {
			return err
		}
		return writeJSON(state.secretInfo(provider), pretty)
	default:
		return fmt.Errorf("unknown secrets subcommand: %s", args[0])
	}
}

func handlePaths(state *cliState, args []string, pretty bool) error {
	if len(args) == 0 {
		return fmt.Errorf("missing paths subcommand")
	}

	pathsSvc, err := state.getPathsService()
	if err != nil {
		return err
	}

	switch args[0] {
	case "list":
		return writeJSON(map[string]any{
			"defaultPath": pathsSvc.GetDefaultPath(),
			"paths":       pathsSvc.GetPaths(),
		}, pretty)
	case "default":
		return writeJSON(map[string]any{"defaultPath": pathsSvc.GetDefaultPath()}, pretty)
	case "set-default":
		if len(args) < 2 {
			return fmt.Errorf("usage: codebox paths set-default <path>")
		}
		resolved, err := filepath.Abs(strings.Join(args[1:], " "))
		if err != nil {
			return fmt.Errorf("resolve path: %w", err)
		}
		if !pathsSvc.ValidatePath(resolved) {
			return fmt.Errorf("path is not an existing directory: %s", resolved)
		}
		if err := pathsSvc.SetDefaultPath(resolved); err != nil {
			return err
		}
		if err := pathsSvc.Save(); err != nil {
			return err
		}
		return writeJSON(map[string]any{"defaultPath": resolved}, pretty)
	default:
		return fmt.Errorf("unknown paths subcommand: %s", args[0])
	}
}

func handlePlugin(state *cliState, args []string, pretty bool) error {
	if len(args) == 0 {
		return fmt.Errorf("missing plugin subcommand")
	}

	pluginSvc := state.getPluginService()

	switch args[0] {
	case "list":
		items, err := pluginSvc.GetInstalledPlugins()
		if err != nil {
			return err
		}
		return writeJSON(map[string]any{"plugins": items}, pretty)
	case "detail":
		if len(args) != 2 {
			return fmt.Errorf("usage: codebox plugin detail <id>")
		}
		item, err := pluginSvc.GetPluginDetail(args[1])
		if err != nil {
			return err
		}
		return writeJSON(item, pretty)
	case "install":
		if len(args) != 2 {
			return fmt.Errorf("usage: codebox plugin install <name>")
		}
		result, err := pluginSvc.InstallPlugin(args[1])
		if err != nil {
			return err
		}
		return writeJSON(result, pretty)
	case "uninstall":
		if len(args) != 2 {
			return fmt.Errorf("usage: codebox plugin uninstall <id>")
		}
		result, err := pluginSvc.UninstallPlugin(args[1])
		if err != nil {
			return err
		}
		return writeJSON(result, pretty)
	case "enable":
		if len(args) != 2 {
			return fmt.Errorf("usage: codebox plugin enable <id>")
		}
		result, err := pluginSvc.EnablePlugin(args[1])
		if err != nil {
			return err
		}
		return writeJSON(result, pretty)
	case "disable":
		if len(args) != 2 {
			return fmt.Errorf("usage: codebox plugin disable <id>")
		}
		result, err := pluginSvc.DisablePlugin(args[1])
		if err != nil {
			return err
		}
		return writeJSON(result, pretty)
	case "update":
		if len(args) != 2 {
			return fmt.Errorf("usage: codebox plugin update <id>")
		}
		result, err := pluginSvc.UpdatePlugin(args[1])
		if err != nil {
			return err
		}
		return writeJSON(result, pretty)
	case "marketplace":
		return handlePluginMarketplace(pluginSvc, args[1:], pretty)
	default:
		return fmt.Errorf("unknown plugin subcommand: %s", args[0])
	}
}

func handlePluginMarketplace(pluginSvc *plugin.Service, args []string, pretty bool) error {
	if len(args) == 0 {
		return fmt.Errorf("missing plugin marketplace subcommand")
	}

	switch args[0] {
	case "list":
		items, err := pluginSvc.GetMarketplaces()
		if err != nil {
			return err
		}
		return writeJSON(map[string]any{"marketplaces": items}, pretty)
	case "update":
		if len(args) == 2 {
			result, err := pluginSvc.UpdateMarketplace(args[1])
			if err != nil {
				return err
			}
			return writeJSON(map[string]any{"results": []map[string]any{{"name": args[1], "result": result}}}, pretty)
		}
		if len(args) > 2 {
			return fmt.Errorf("usage: codebox plugin marketplace update [name]")
		}

		marketplaces, err := pluginSvc.GetMarketplaces()
		if err != nil {
			return err
		}
		results := make([]map[string]any, 0, len(marketplaces))
		for _, marketplace := range marketplaces {
			result, err := pluginSvc.UpdateMarketplace(marketplace.Name)
			if err != nil {
				return fmt.Errorf("update marketplace %q: %w", marketplace.Name, err)
			}
			results = append(results, map[string]any{"name": marketplace.Name, "result": result})
		}
		return writeJSON(map[string]any{"results": results}, pretty)
	default:
		return fmt.Errorf("unknown plugin marketplace subcommand: %s", args[0])
	}
}

func handleSettings(state *cliState, args []string, pretty bool) error {
	if len(args) == 0 {
		return fmt.Errorf("missing settings subcommand")
	}

	settingsSvc, err := state.getSettingsService()
	if err != nil {
		return err
	}

	switch args[0] {
	case "get":
		return writeJSON(settingsSvc.GetSettings(), pretty)
	case "set":
		if len(args) < 3 {
			return fmt.Errorf("usage: codebox settings set <key> <value>")
		}
		if err := setSettingValue(settingsSvc, args[1], strings.Join(args[2:], " ")); err != nil {
			return err
		}
		return writeJSON(settingsSvc.GetSettings(), pretty)
	default:
		return fmt.Errorf("unknown settings subcommand: %s", args[0])
	}
}

func handleEnvVars(state *cliState, args []string, pretty bool) error {
	if len(args) == 0 {
		return fmt.Errorf("missing envvars subcommand")
	}

	envSvc, err := state.getEnvVarsService()
	if err != nil {
		return err
	}

	switch args[0] {
	case "list":
		vars := envSvc.GetAll()
		sort.Slice(vars, func(i, j int) bool { return vars[i].Key < vars[j].Key })
		return writeJSON(map[string]any{"envVars": vars}, pretty)
	case "set":
		if len(args) < 3 {
			return fmt.Errorf("usage: codebox envvars set <key> <value>")
		}
		key := args[1]
		value := strings.Join(args[2:], " ")
		if err := envSvc.Set(key, value); err != nil {
			return err
		}
		return writeJSON(map[string]any{"key": key, "value": value}, pretty)
	case "delete":
		if len(args) != 2 {
			return fmt.Errorf("usage: codebox envvars delete <key>")
		}
		if err := envSvc.Delete(args[1]); err != nil {
			return err
		}
		return writeJSON(map[string]any{"deleted": args[1]}, pretty)
	default:
		return fmt.Errorf("unknown envvars subcommand: %s", args[0])
	}
}

func handleInfo(state *cliState, pretty bool) error {
	configSvc, err := state.getConfigService()
	if err != nil {
		return err
	}
	pathsSvc, err := state.getPathsService()
	if err != nil {
		return err
	}
	settingsSvc, err := state.getSettingsService()
	if err != nil {
		return err
	}
	envSvc, err := state.getEnvVarsService()
	if err != nil {
		return err
	}

	cwd, _ := os.Getwd()
	return writeJSON(map[string]any{
		"version":       resolvedVersion(),
		"configDir":     state.configDir,
		"claudeDir":     state.claudeDir,
		"cwd":           cwd,
		"goos":          runtime.GOOS,
		"goarch":        runtime.GOARCH,
		"providerCount": len(configSvc.GetProviders()),
		"defaultPath":   pathsSvc.GetDefaultPath(),
		"remotePort":    settingsSvc.GetRemotePort(),
		"envVarCount":   len(envSvc.GetAll()),
		"files": map[string]string{
			"models":   filepath.Join(state.configDir, "models.json"),
			"secrets":  filepath.Join(state.configDir, "secrets.enc"),
			"paths":    filepath.Join(state.configDir, "paths.json"),
			"settings": filepath.Join(state.configDir, "settings.json"),
			"envvars":  filepath.Join(state.configDir, "envvars.json"),
		},
	}, pretty)
}

func (s *cliState) getConfigService() (*config.ConfigService, error) {
	if s.configSvc != nil {
		return s.configSvc, nil
	}
	svc := config.NewConfigService(s.configDir)
	if err := svc.Load(); err != nil {
		return nil, err
	}
	s.configSvc = svc
	return svc, nil
}

func (s *cliState) getSecretsService() (*secrets.SecretsService, error) {
	if s.secretsSvc != nil {
		return s.secretsSvc, nil
	}
	svc := secrets.NewSecretsService(s.configDir)
	if err := svc.Load(); err != nil {
		return nil, err
	}
	s.secretsSvc = svc
	return svc, nil
}

func (s *cliState) getPathsService() (*paths.PathsService, error) {
	if s.pathsSvc != nil {
		return s.pathsSvc, nil
	}
	svc := paths.NewPathsService(s.configDir)
	if err := svc.Load(); err != nil {
		return nil, err
	}
	s.pathsSvc = svc
	return svc, nil
}

func (s *cliState) getSettingsService() (*settings.Service, error) {
	if s.settingsSvc != nil {
		return s.settingsSvc, nil
	}
	svc := settings.NewService(s.configDir)
	if err := svc.Load(); err != nil {
		return nil, err
	}
	s.settingsSvc = svc
	return svc, nil
}

func (s *cliState) getEnvVarsService() (*envvars.EnvVarsService, error) {
	if s.envVarsSvc != nil {
		return s.envVarsSvc, nil
	}
	svc := envvars.NewEnvVarsService(s.configDir)
	if err := svc.Load(); err != nil {
		return nil, err
	}
	s.envVarsSvc = svc
	return svc, nil
}

func (s *cliState) getPluginService() *plugin.Service {
	if s.pluginSvc != nil {
		return s.pluginSvc
	}
	s.pluginSvc = plugin.NewService(s.claudeDir, nil)
	return s.pluginSvc
}

func (s *cliState) secretProviderNames() ([]string, error) {
	configSvc, err := s.getConfigService()
	if err != nil {
		return nil, err
	}
	secretsSvc, err := s.getSecretsService()
	if err != nil {
		return nil, err
	}

	seen := map[string]struct{}{}
	for _, name := range configSvc.GetProviderNames() {
		seen[name] = struct{}{}
	}
	for _, name := range secretsSvc.GetAllProviders() {
		seen[name] = struct{}{}
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func (s *cliState) secretInfo(provider string) secretInfo {
	secretsSvc, err := s.getSecretsService()
	if err != nil {
		return secretInfo{Provider: provider, Source: "error", MaskedKey: "***"}
	}

	diagnostics := secretsSvc.GetKeyDiagnostics([]string{provider})
	entry := diagnostics[provider]
	info := secretInfo{
		Provider:    provider,
		Source:      entry["source"],
		MaskedKey:   entry["masked_key"],
		Environment: map[string]string{},
	}
	if info.MaskedKey == "" {
		info.MaskedKey = "***"
	}
	if v, err := strconv.Atoi(entry["key_length"]); err == nil {
		info.KeyLength = v
	}
	info.HasStored = entry["has_stored"] == "true"
	info.StoredMasked = entry["stored_masked"]
	for key, value := range entry {
		if strings.HasPrefix(key, "env_") {
			info.Environment[strings.TrimPrefix(key, "env_")] = value
		}
	}
	if len(info.Environment) == 0 {
		info.Environment = nil
	}
	return info
}

func buildExportConfig(state *cliState) (*config.ExportConfig, error) {
	configSvc, err := state.getConfigService()
	if err != nil {
		return nil, err
	}
	secretsSvc, err := state.getSecretsService()
	if err != nil {
		return nil, err
	}

	providers := configSvc.GetProviders()
	exportProviders := make(map[string]config.ExportProvider, len(providers))
	for name, provider := range providers {
		apiKey := getExportProviderAPIKey(secretsSvc, name)
		exportProviders[name] = config.BuildExportProvider(provider, apiKey)
	}

	return &config.ExportConfig{
		Version:    "1.0",
		ExportedAt: time.Now().Format(time.RFC3339),
		Source:     "amagi-codebox",
		Providers:  exportProviders,
		AgentTeams: configSvc.GetAgentTeams(),
	}, nil
}

func getExportProviderAPIKey(secretsSvc *secrets.SecretsService, providerName string) string {
	for _, candidate := range exportProviderAPIKeyCandidates(providerName) {
		if candidate == providerName {
			if key, _ := secretsSvc.GetAPIKeyWithFallback(candidate); key != "" {
				return key
			}
			continue
		}

		key, err := secretsSvc.GetAPIKey(candidate)
		if err != nil {
			continue
		}
		if trimmed := strings.TrimSpace(key); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func exportProviderAPIKeyCandidates(providerName string) []string {
	return []string{
		providerName,
		providerName + ":anthropic",
		providerName + ":openai",
	}
}

func importConfigFile(state *cliState, filePath string) (map[string]any, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read config import file: %w", err)
	}

	var exportCfg config.ExportConfig
	if err := json.Unmarshal(data, &exportCfg); err != nil {
		return nil, fmt.Errorf("parse config import json: %w", err)
	}
	if exportCfg.Version == "" || exportCfg.Source == "" {
		return nil, fmt.Errorf("invalid config import: missing version or source")
	}

	configSvc, err := state.getConfigService()
	if err != nil {
		return nil, err
	}
	secretsSvc, err := state.getSecretsService()
	if err != nil {
		return nil, err
	}

	importedProviders := make([]string, 0, len(exportCfg.Providers))
	secretsUpdated := false
	for name, provider := range exportCfg.Providers {
		item := provider.ToProvider()
		if err := configSvc.SaveProvider(name, item); err != nil {
			return nil, fmt.Errorf("save provider %q: %w", name, err)
		}
		if apiKey := provider.UnifiedAPIKey(); apiKey != "" {
			if err := secretsSvc.SetAPIKey(name, apiKey); err != nil {
				return nil, fmt.Errorf("save provider %q api key: %w", name, err)
			}
			_ = secretsSvc.DeleteAPIKey(name + ":anthropic")
			_ = secretsSvc.DeleteAPIKey(name + ":openai")
			secretsUpdated = true
		}
		importedProviders = append(importedProviders, name)
	}
	sort.Strings(importedProviders)

	agentTeamsUpdated := false
	if exportCfg.AgentTeams.Enabled || exportCfg.AgentTeams.TeammateMode != "" {
		if err := configSvc.SetAgentTeams(exportCfg.AgentTeams); err != nil {
			return nil, fmt.Errorf("save agent teams config: %w", err)
		}
		agentTeamsUpdated = true
	}
	if secretsUpdated {
		if err := secretsSvc.Save(); err != nil {
			return nil, fmt.Errorf("persist secrets: %w", err)
		}
	}

	return map[string]any{
		"file":              filePath,
		"importedProviders": importedProviders,
		"providerCount":     len(importedProviders),
		"agentTeamsUpdated": agentTeamsUpdated,
	}, nil
}

func setSettingValue(svc *settings.Service, key string, value string) error {
	dashboard := svc.GetDashboardDefaults()
	terminal := svc.GetTerminalSettings()

	switch normalizeSettingKey(key) {
	case "dashboard.provider":
		dashboard.Provider = value
		return svc.SetDashboardDefaults(dashboard)
	case "dashboard.preset":
		dashboard.Preset = value
		return svc.SetDashboardDefaults(dashboard)
	case "dashboard.opencodeprovider":
		dashboard.OpenCodeProvider = value
		return svc.SetDashboardDefaults(dashboard)
	case "dashboard.mode":
		dashboard.Mode = value
		return svc.SetDashboardDefaults(dashboard)
	case "dashboard.shell":
		dashboard.Shell = value
		return svc.SetDashboardDefaults(dashboard)
	case "dashboard.claudemode":
		dashboard.ClaudeMode = value
		return svc.SetDashboardDefaults(dashboard)
	case "dashboard.claudeshell":
		dashboard.ClaudeShell = value
		return svc.SetDashboardDefaults(dashboard)
	case "dashboard.opencodemode":
		dashboard.OpenCodeMode = value
		return svc.SetDashboardDefaults(dashboard)
	case "dashboard.opencodeshell":
		dashboard.OpenCodeShell = value
		return svc.SetDashboardDefaults(dashboard)
	case "dashboard.codexmode":
		dashboard.CodexMode = value
		return svc.SetDashboardDefaults(dashboard)
	case "dashboard.codexshell":
		dashboard.CodexShell = value
		return svc.SetDashboardDefaults(dashboard)
	case "dashboard.useproxy":
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("parse bool for %s: %w", key, err)
		}
		dashboard.UseProxy = parsed
		return svc.SetDashboardDefaults(dashboard)
	case "terminal.scrollback":
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("parse int for %s: %w", key, err)
		}
		terminal.Scrollback = parsed
		return svc.SetTerminalSettings(terminal)
	case "remoteport":
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("parse int for %s: %w", key, err)
		}
		return svc.SetRemotePort(parsed)
	case "mobilewebroot":
		return svc.SetMobileWebRoot(value)
	case "githubtoken":
		return svc.SetGitHubToken(value)
	default:
		return fmt.Errorf("unsupported setting key %q", key)
	}
}

func normalizeSettingKey(key string) string {
	parts := strings.Split(strings.TrimSpace(strings.ToLower(key)), ".")
	for i, part := range parts {
		part = strings.ReplaceAll(part, "-", "")
		part = strings.ReplaceAll(part, "_", "")
		parts[i] = part
	}
	return strings.Join(parts, ".")
}

func stripPrettyFlag(args []string) (bool, []string) {
	pretty := false
	rest := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "--pretty" || arg == "-pretty" {
			pretty = true
			continue
		}
		rest = append(rest, arg)
	}
	return pretty, rest
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func writeJSON(v any, pretty bool) error {
	var (
		data []byte
		err  error
	)
	if pretty {
		data, err = json.MarshalIndent(v, "", "  ")
	} else {
		data, err = json.Marshal(v)
	}
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = os.Stdout.Write(data)
	return err
}

func writeJSONFile(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir output dir: %w", err)
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write output file: %w", err)
	}
	return nil
}

func fatal(pretty bool, err error) {
	_ = writeError(pretty, err.Error())
	os.Exit(1)
}

func fatalf(pretty bool, format string, args ...any) {
	fatal(pretty, fmt.Errorf(format, args...))
}

func writeError(pretty bool, message string) error {
	payload := map[string]any{"error": message}
	var (
		data []byte
		err  error
	)
	if pretty {
		data, err = json.MarshalIndent(payload, "", "  ")
	} else {
		data, err = json.Marshal(payload)
	}
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = os.Stderr.Write(data)
	return err
}

func resolvedVersion() string {
	if strings.TrimSpace(Version) != "" && Version != "dev" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if strings.TrimSpace(info.Main.Version) != "" && info.Main.Version != "(devel)" {
			return info.Main.Version
		}
	}
	return Version
}

func defaultConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".amagi-codebox"
	}
	return filepath.Join(home, ".amagi-codebox")
}

func defaultClaudeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claude"
	}
	return filepath.Join(home, ".claude")
}

func printUsage() {
	usage := `codebox [--pretty] <command> <subcommand> [arguments]

Commands:
  codebox config list
  codebox config get <provider>
  codebox config export [--output file]
  codebox config import <file>

  codebox secrets list
  codebox secrets get <provider>
  codebox secrets set <provider> <key>

  codebox paths list
  codebox paths default
  codebox paths set-default <path>

  codebox plugin list
  codebox plugin detail <id>
  codebox plugin install <name>
  codebox plugin uninstall <id>
  codebox plugin enable <id>
  codebox plugin disable <id>
  codebox plugin update <id>
  codebox plugin marketplace list
  codebox plugin marketplace update [name]

  codebox settings get
  codebox settings set <key> <value>

  codebox envvars list
  codebox envvars set <key> <value>
  codebox envvars delete <key>

  codebox info
  codebox version
`
	_, _ = os.Stderr.WriteString(usage)
}
