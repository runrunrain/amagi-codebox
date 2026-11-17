package main

// app_codex_toml.go — Codex config.toml pure rendering/syncing, TOML line helpers,
// legacy global-proxy marker block cleanup, and atomic backup writes.

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/fsutil"
	"amagi-codebox/internal/platform"
)

const (
	// codexGlobalHeadroomMarkerStart / End delimit a retired amagi-managed
	// openai_base_url block that legacy versions wrote into ~/.codex/config.toml.
	// They are kept only so startup cleanup can strip the leftover block from
	// existing user files, and are intentionally distinct from the
	// amagi-codebox-inject markers so the provider-section cleanup
	// (removeCodexManagedProviderSection / cleanupCodexManagedProviderConfig)
	// never touches this block.
	codexGlobalHeadroomMarkerStart = "# === amagi-headroom-global-start ==="
	codexGlobalHeadroomMarkerEnd   = "# === amagi-headroom-global-end ==="
)

type codexConfigSyncOptions struct {
	Model           string
	ModelProvider   string
	ProviderBaseURL string
	// WireAPI 为托管 provider 段写入的 wire_api 值（经 codexWireAPI 归一化：
	// "chat" -> chat，其余 -> responses legacy 默认）。空值维持既有行为。
	WireAPI              string
	EnsureCustomProvider bool
	ForceAPILogin        bool
	CleanupManagedConfig bool
}

// renderCodexConfig is the pure Codex TOML transform shared by desktop writes
// and the transactional remote executor. The caller owns durable file I/O.
func renderCodexConfig(data []byte, configPath string, opts codexConfigSyncOptions) ([]byte, error) {
	content := string(data)
	lines := []string{}
	if content != "" {
		lines = strings.Split(content, "\n")
	}

	if opts.EnsureCustomProvider {
		lines = removeCodexManagedProviderSection(lines, opts.ModelProvider)
		topLevelAssignments := []string{
			"model = " + strconv.Quote(opts.Model),
			"model_provider = " + strconv.Quote(opts.ModelProvider),
		}
		if opts.ForceAPILogin {
			topLevelAssignments = append(topLevelAssignments, "forced_login_method = "+strconv.Quote("api"))
		}
		lines = syncCodexTopLevelAssignments(lines, topLevelAssignments, true)
		lines = appendCodexCustomProviderSection(lines, opts.ModelProvider, opts.ProviderBaseURL, opts.WireAPI)
	} else {
		if opts.CleanupManagedConfig {
			lines = cleanupCodexManagedProviderConfig(lines, codexModelProviderName)
		}
		updated := syncCodexTopLevelAssignments(lines, []string{"model = " + strconv.Quote(opts.Model)}, false)
		if countTopLevelAssignment(updated, "model") == 0 {
			return nil, fmt.Errorf("top-level model field not found in %s", configPath)
		}
		lines = updated
	}
	return []byte(strings.Join(lines, "\n")), nil
}

func syncCodexTopLevelAssignments(lines []string, assignmentLines []string, insertMissing bool) []string {
	wanted := make(map[string]string, len(assignmentLines))
	order := make([]string, 0, len(assignmentLines))
	seen := make(map[string]bool, len(assignmentLines))
	for _, assignment := range assignmentLines {
		key, ok := tomlAssignmentKey(strings.TrimSpace(assignment))
		if !ok {
			continue
		}
		wanted[key] = assignment
		order = append(order, key)
	}

	updated := make([]string, 0, len(lines)+len(assignmentLines)+1)
	inTopLevel := true
	insertMissingBeforeSection := func() {
		if !insertMissing {
			return
		}
		for _, key := range order {
			if !seen[key] {
				updated = append(updated, wanted[key])
				seen[key] = true
			}
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if inTopLevel && isTomlTableHeader(trimmed) {
			insertMissingBeforeSection()
			inTopLevel = false
		}

		if inTopLevel {
			if key, ok := tomlAssignmentKey(trimmed); ok {
				if replacement, exists := wanted[key]; exists {
					if seen[key] {
						continue
					}
					updated = append(updated, replacement)
					seen[key] = true
					continue
				}
			}
		}

		updated = append(updated, line)
	}

	insertMissingBeforeSection()
	return trimTrailingEmptyLines(updated)
}

func removeCodexManagedProviderSection(lines []string, modelProvider string) []string {
	if modelProvider == "" {
		return lines
	}
	providerHeader := "[model_providers." + modelProvider + "]"
	providerSubHeaderPrefix := "[model_providers." + modelProvider + "."
	updated := make([]string, 0, len(lines))
	skipOwnedBlock := false
	skipProviderSection := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "# === amagi-codebox-inject-start ===" {
			skipOwnedBlock = true
			continue
		}
		if skipOwnedBlock {
			if trimmed == "# === amagi-codebox-inject-end ===" {
				skipOwnedBlock = false
			}
			continue
		}
		if isTomlTableHeader(trimmed) {
			if trimmed == providerHeader || strings.HasPrefix(trimmed, providerSubHeaderPrefix) {
				skipProviderSection = true
				continue
			}
			skipProviderSection = false
		}
		if skipProviderSection {
			continue
		}
		updated = append(updated, line)
	}
	return trimTrailingEmptyLines(updated)
}

func cleanupCodexManagedProviderConfig(lines []string, modelProvider string) []string {
	if modelProvider == "" {
		return lines
	}
	managedTopLevelProvider := topLevelAssignmentValueEquals(lines, "model_provider", modelProvider)

	lines = removeCodexManagedProviderSection(lines, modelProvider)
	updated := make([]string, 0, len(lines))
	inTopLevel := true
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if inTopLevel && isTomlTableHeader(trimmed) {
			inTopLevel = false
		}
		if inTopLevel {
			if key, ok := tomlAssignmentKey(trimmed); ok {
				switch key {
				case "model_provider":
					if tomlAssignmentValueEquals(trimmed, modelProvider) {
						continue
					}
				case "forced_login_method":
					if managedTopLevelProvider && tomlAssignmentValueEquals(trimmed, "api") {
						continue
					}
				}
			}
		}
		updated = append(updated, line)
	}
	return trimTrailingEmptyLines(updated)
}

func topLevelAssignmentValueEquals(lines []string, key, value string) bool {
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if isTomlTableHeader(trimmed) {
			return false
		}
		if gotKey, ok := tomlAssignmentKey(trimmed); ok && gotKey == key {
			return tomlAssignmentValueEquals(trimmed, value)
		}
	}
	return false
}

func tomlAssignmentValueEquals(trimmedLine, value string) bool {
	idx := strings.Index(trimmedLine, "=")
	if idx == -1 {
		return false
	}
	rawValue := strings.TrimSpace(trimmedLine[idx+1:])
	if rawValue == "" {
		return false
	}
	if rawValue[0] == '"' || rawValue[0] == '\'' {
		quote := rawValue[0]
		for i := 1; i < len(rawValue); i++ {
			if quote == '"' && rawValue[i] == '\\' {
				i++
				continue
			}
			if rawValue[i] == quote {
				literal := rawValue[:i+1]
				if quote == '\'' {
					return literal[1:len(literal)-1] == value
				}
				unquoted, err := strconv.Unquote(literal)
				return err == nil && unquoted == value
			}
		}
	}
	if commentIdx := strings.Index(rawValue, "#"); commentIdx != -1 {
		rawValue = strings.TrimSpace(rawValue[:commentIdx])
	}
	if unquoted, err := strconv.Unquote(rawValue); err == nil {
		return unquoted == value
	}
	return strings.Trim(rawValue, "\"'") == value
}

// codexWireAPI 把 provider 侧的 wire_api 归一化为 Codex config.toml / -c 覆盖
// 的合法值：仅 "chat"（trim+小写后）走 Chat Completions，其余（未设置、
// "responses"、非法值）一律返回 "responses" —— 与既有硬编码 wire_api="responses"
// 的 legacy 行为完全一致，保证未设置时零变化。
func codexWireAPI(wireAPI string) string {
	if strings.ToLower(strings.TrimSpace(wireAPI)) == config.WireAPIChat {
		return config.WireAPIChat
	}
	return config.WireAPIResponses
}

func appendCodexCustomProviderSection(lines []string, modelProvider, baseURL, wireAPI string) []string {
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
		lines = append(lines, "")
	}
	lines = append(lines,
		"# === amagi-codebox-inject-start ===",
		"[model_providers."+modelProvider+"]",
		"name = "+strconv.Quote(modelProvider),
		"base_url = "+strconv.Quote(baseURL),
		"env_key = "+strconv.Quote("OPENAI_API_KEY"),
		"requires_openai_auth = false",
		"wire_api = "+strconv.Quote(codexWireAPI(wireAPI)),
		"# === amagi-codebox-inject-end ===",
	)
	return lines
}

// --- Legacy codex-global proxy marker block cleanup ---
//
// Older versions routed codex desktop traffic through a local proxy by writing
// a marker-delimited top-level openai_base_url block into ~/.codex/config.toml.
// The proxy is gone; the only remaining responsibility is stripping the leftover
// block from existing user files so no dead proxy address survives.

// removeCodexGlobalHeadroomBlock strips the amagi-headroom-global marker block
// (inclusive of both marker lines and any lines between them). It is a no-op
// when the block is absent and is safe to run on config that also contains the
// amagi-codebox-inject block (the markers are distinct).
func removeCodexGlobalHeadroomBlock(lines []string) []string {
	updated := make([]string, 0, len(lines))
	skip := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == codexGlobalHeadroomMarkerStart {
			skip = true
			continue
		}
		if skip {
			if trimmed == codexGlobalHeadroomMarkerEnd {
				skip = false
			}
			continue
		}
		updated = append(updated, line)
	}
	// A removed block often leaves a doubled blank line behind; collapse only
	// runs of 2+ blanks introduced around the removed region without touching
	// intentional spacing elsewhere is overkill -- trimTrailingEmptyLines keeps
	// the tail tidy which is the only place a dangling blank survives.
	return trimTrailingEmptyLines(updated)
}

// splitTomlLinesSafe normalizes CR/CRLF to LF and splits into lines. Unlike the
// codexplugin variant it never returns nil for empty input (returns an empty
// slice) so downstream join logic is uniform.
func splitTomlLinesSafe(content string) []string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	return strings.Split(content, "\n")
}

// cleanupCodexLegacyHeadroomMarkerOnStartup strips the retired marker block
// from ~/.codex/config.toml once on every startup (idempotent). No marker or
// no file is a no-op; failures only log a warning and never block startup.
func (a *App) cleanupCodexLegacyHeadroomMarkerOnStartup() {
	if err := removeCodexLegacyHeadroomMarkerConfig(); err != nil {
		if a.Log != nil {
			a.Log.Warn("codex", "清除遗留代理标记块失败", err.Error())
		}
	}
}

// removeCodexLegacyHeadroomMarkerConfig reads ~/.codex/config.toml, strips the
// legacy marker block when present, and writes the result back with the shared
// atomic private-file writer.
func removeCodexLegacyHeadroomMarkerConfig() error {
	codexHome, err := platform.CodexHomeDir(os.Environ())
	if err != nil {
		return fmt.Errorf("get Codex home: %w", err)
	}
	configPath := filepath.Join(codexHome, "config.toml")
	previous, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read codex config.toml: %w", err)
	}
	lines := splitTomlLinesSafe(string(previous))
	updated := removeCodexGlobalHeadroomBlock(lines)
	if strings.Join(updated, "\n") == strings.Join(lines, "\n") {
		return nil
	}
	return fsutil.WritePrivateFile(configPath, []byte(strings.Join(updated, "\n")))
}

func countTopLevelAssignment(lines []string, key string) int {
	count := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if isTomlTableHeader(trimmed) {
			return count
		}
		if got, ok := tomlAssignmentKey(trimmed); ok && got == key {
			count++
		}
	}
	return count
}

func trimTrailingEmptyLines(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func tomlAssignmentKey(trimmedLine string) (string, bool) {
	if trimmedLine == "" || strings.HasPrefix(trimmedLine, "#") {
		return "", false
	}
	idx := strings.Index(trimmedLine, "=")
	if idx == -1 {
		return "", false
	}
	key := strings.TrimSpace(trimmedLine[:idx])
	if key == "" {
		return "", false
	}
	return key, true
}

func isTomlTableHeader(trimmedLine string) bool {
	return strings.HasPrefix(trimmedLine, "[")
}

func isCustomCodexOpenAIBaseURL(baseURL string) bool {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return false
	}
	parseTarget := trimmed
	if !strings.Contains(parseTarget, "://") {
		parseTarget = "https://" + parseTarget
	}
	parsed, err := url.Parse(parseTarget)
	if err != nil || parsed.Host == "" {
		normalized := strings.TrimRight(strings.ToLower(trimmed), "/")
		return normalized != "https://api.openai.com" && normalized != "https://api.openai.com/v1" && normalized != "api.openai.com" && normalized != "api.openai.com/v1"
	}
	if strings.ToLower(parsed.Hostname()) != codexOfficialOpenAIAPIHost {
		return true
	}
	path := strings.TrimRight(strings.ToLower(parsed.EscapedPath()), "/")
	return path != "" && path != "/v1"
}
