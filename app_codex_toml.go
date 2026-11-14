package main

// app_codex_toml.go — Codex config.toml pure rendering/syncing, TOML line helpers,
// global Headroom marker block management, atomic backup writes, and uninstall stopper.

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/envcheck"
	"amagi-codebox/internal/fsutil"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/remote"
)

const (
	// codexGlobalHeadroomMarkerStart / End delimit the amagi-managed
	// openai_base_url block in ~/.codex/config.toml. They are intentionally
	// distinct from the amagi-codebox-inject markers so the provider-section
	// cleanup (removeCodexManagedProviderSection / cleanupCodexManagedProviderConfig)
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

// syncCodexConfigModel updates the top-level model in an existing Codex config.toml
// and removes amagi-managed provider state so official Codex login can recover.
func syncCodexConfigModel(model string) error {
	codexHome, err := platform.CodexHomeDir(os.Environ())
	if err != nil {
		return fmt.Errorf("get Codex home: %w", err)
	}
	configPath := filepath.Join(codexHome, "config.toml")
	return syncCodexConfigFile(configPath, codexConfigSyncOptions{Model: model, CleanupManagedConfig: true})
}

func syncCodexCustomProviderConfig(model, baseURL, wireAPI string) error {
	codexHome, err := platform.CodexHomeDir(os.Environ())
	if err != nil {
		return fmt.Errorf("get Codex home: %w", err)
	}
	configPath := filepath.Join(codexHome, "config.toml")
	return syncCodexCustomProviderConfigFile(configPath, model, baseURL, wireAPI)
}

func syncCodexCustomProviderConfigFile(configPath, model, baseURL, wireAPI string) error {
	if strings.TrimSpace(model) == "" {
		return fmt.Errorf("codex model is empty")
	}
	if strings.TrimSpace(baseURL) == "" {
		return fmt.Errorf("codex provider base_url is empty")
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return fmt.Errorf("create codex config dir: %w", err)
	}
	return syncCodexConfigFile(configPath, codexConfigSyncOptions{
		Model:                model,
		ModelProvider:        codexModelProviderName,
		ProviderBaseURL:      baseURL,
		WireAPI:              wireAPI,
		EnsureCustomProvider: true,
		ForceAPILogin:        true,
	})
}

func syncCodexConfigFile(configPath string, opts codexConfigSyncOptions) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) || !opts.EnsureCustomProvider {
			return fmt.Errorf("read config.toml: %w", err)
		}
	}
	rendered, err := renderCodexConfig(data, configPath, opts)
	if err != nil {
		return err
	}
	return fsutil.WritePrivateFile(configPath, rendered)
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

// --- Codex global headroom openai_base_url marker block ---
//
// syncCodexGlobalHeadroomConfig writes or removes the top-level openai_base_url
// assignment that routes codex desktop traffic through the independent
// codex-global headroom proxy (port 8788). It is intentionally separate from
// syncCodexConfigFile / syncCodexCustomProviderConfigFile:
//
//   - It uses a distinct marker pair (amagi-headroom-global-start/-end) so the
//     provider-section cleanup (removeCodexManagedProviderSection /
//     cleanupCodexManagedProviderConfig) never strips it, and so a model-only
//     sync (syncCodexConfigModel) leaves the openai_base_url intact.
//   - It edits the file surgically (line-based) to preserve every other config
//     key (plugins / marketplaces / mcp_servers / profiles / ...).
//   - It backs up the previous content to config.toml.bak.<ts> before writing.
//
// enabled=true inserts/refreshes the block; enabled=false removes it. Both paths
// are idempotent: re-running with the same state is a no-op (apart from a fresh
// backup when content actually changes).
func syncCodexGlobalHeadroomConfig(enabled bool, port int) error {
	codexHome, err := platform.CodexHomeDir(os.Environ())
	if err != nil {
		return fmt.Errorf("get Codex home: %w", err)
	}
	configPath := filepath.Join(codexHome, "config.toml")

	previous, err := os.ReadFile(configPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read codex config.toml: %w", err)
		}
		// No config yet. Only create one when enabling; disabling is a no-op.
		if !enabled {
			return nil
		}
		previous = nil
	}

	content := string(previous)
	lines := splitTomlLinesSafe(content)
	originalJoined := strings.Join(lines, "\n")

	lines = removeCodexGlobalHeadroomBlock(lines)
	if enabled {
		if port <= 0 {
			port = CodexGlobalHeadroomDefaultPort
		}
		lines = insertCodexGlobalHeadroomBlock(lines, port)
	}

	updatedJoined := strings.Join(lines, "\n")
	if updatedJoined == originalJoined {
		// Idempotent no-op: avoid touching mtimes / writing a backup when the
		// desired state is already present.
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return fmt.Errorf("create codex config dir: %w", err)
	}
	return writeCodexConfigWithBackup(configPath, []byte(updatedJoined), previous)
}

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

// insertCodexGlobalHeadroomBlock inserts a fresh marker block containing the
// top-level openai_base_url assignment. The block is placed at the end of the
// top-level region (immediately before the first [section] header) so the
// assignment stays top-level in TOML terms; when the file has no section it is
// appended at the end. An existing stray top-level openai_base_url outside any
// marker block is removed first to avoid a duplicate key (TOML forbids
// duplicate top-level keys).
func insertCodexGlobalHeadroomBlock(lines []string, port int) []string {
	baseURL := fmt.Sprintf("http://127.0.0.1:%d/v1", port)
	block := []string{
		codexGlobalHeadroomMarkerStart,
		"openai_base_url = " + strconv.Quote(baseURL),
		codexGlobalHeadroomMarkerEnd,
	}

	// Drop any pre-existing top-level openai_base_url so we never emit a
	// duplicate top-level key (which would make the TOML invalid).
	cleaned := make([]string, 0, len(lines))
	inTopLevel := true
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if inTopLevel && isTomlTableHeader(trimmed) {
			inTopLevel = false
		}
		if inTopLevel {
			if key, ok := tomlAssignmentKey(trimmed); ok && key == "openai_base_url" {
				continue
			}
		}
		cleaned = append(cleaned, line)
	}

	// Find the index of the first section header and insert the block just
	// before it; fall back to appending at the end when there is no section.
	insertAt := len(cleaned)
	inTopLevel = true
	for i, line := range cleaned {
		trimmed := strings.TrimSpace(line)
		if inTopLevel && isTomlTableHeader(trimmed) {
			insertAt = i
			break
		}
		if isTomlTableHeader(trimmed) {
			inTopLevel = false
		}
	}

	out := make([]string, 0, len(cleaned)+len(block)+2)
	out = append(out, cleaned[:insertAt]...)
	// Separate the block from preceding top-level content with a blank line when
	// there is content above and it is not already blank.
	if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
		out = append(out, "")
	}
	out = append(out, block...)
	out = append(out, cleaned[insertAt:]...)
	return out
}

// splitTomlLinesSafe normalizes CR/CRLF to LF and splits into lines. Unlike the
// codexplugin variant it never returns nil for empty input (returns an empty
// slice) so downstream join logic is uniform.
func splitTomlLinesSafe(content string) []string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	return strings.Split(content, "\n")
}

// writeCodexConfigWithBackup writes next to path, backing up previous to
// path.bak.<YYYYMMDDHHMMSS_msec>_<rand4> first (only when previous is non-empty).
// The timestamp carries millisecond precision plus a short random suffix so two
// writes within the same second (or even same millisecond) do not silently
// overwrite each other's backup. It matches the codexplugin.writeConfigWithBackup
// contract but lives in package main so the global-headroom config layer does
// not depend on internal/codexplugin internals. Mode preserves the existing file
// mode (default 0600).
func writeCodexConfigWithBackup(path string, next []byte, previous []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create codex config dir: %w", err)
	}
	if len(previous) > 0 {
		backupPath := fmt.Sprintf("%s.bak.%s.%s", path, time.Now().Format("20060102150405.000"), randHexSuffix(4))
		if err := os.WriteFile(backupPath, previous, 0600); err != nil {
			return fmt.Errorf("backup codex config.toml: %w", err)
		}
	}
	mode := os.FileMode(0600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config.toml.*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary codex config: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(next); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary codex config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temporary codex config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary codex config: %w", err)
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return fmt.Errorf("chmod temporary codex config: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace codex config.toml: %w", err)
	}
	cleanup = false
	return nil
}

// randHexSuffix returns a short random hex string of byteLen*2 hex characters
// (e.g. byteLen=4 -> 8 hex chars). It is used to disambiguate config backups
// that land within the same clock millisecond. On any read error it falls back
// to a fixed string so the caller never fails solely because the suffix could
// not be generated.
func randHexSuffix(byteLen int) string {
	if byteLen <= 0 {
		byteLen = 4
	}
	b := make([]byte, byteLen)
	if _, err := rand.Read(b); err != nil {
		return "00000000"[:byteLen*2]
	}
	return hex.EncodeToString(b)
}

// removeCodexGlobalHeadroomConfig is the package-level disable entry used by the
// envcheck uninstall hook (stopAllHeadroomForUninstall) which does not have
// access to the App-bound wrapper. It removes the marker block best-effort and
// is idempotent.
func removeCodexGlobalHeadroomConfig() error {
	return syncCodexGlobalHeadroomConfig(false, 0)
}

// stopAllHeadroomForUninstall is the headroom stopper injected into envcheck so
// CleanHeadroom terminates BOTH proxy child processes before the shared venv is
// removed. On Windows a running headroom.exe inside the venv is locked by the OS
// and os.RemoveAll would fail; stopping both instances first avoids that. When
// the codex-global switch was persisted on, it also clears the openai_base_url
// marker block and persistence so codex does not keep pointing at the removed
// proxy (a dead openai_base_url would break codex until manually fixed).
//
// R3-002: when the coordinator rejects because active sessions hold headroom
// leases, this returns envcheck.ErrHeadroomInUse (wrapping the coordinator
// detail) so CleanHeadroom treats it as a FATAL rejection and aborts BEFORE the
// venv is removed. Plain stop failures (process already dead, etc.) remain
// best-effort and non-fatal. Individual failures are logged.
func (a *App) stopAllHeadroomForUninstall() (error, func()) {
	if a.isExternalCleanupRecoveryBlocked() {
		return fmt.Errorf("%w: %w", envcheck.ErrHeadroomInUse, remote.ErrSharedServiceInUse), func() {}
	}
	// M-006 / R3-002: consult the coordinator BEFORE tearing down shared headroom.
	// R4-002 (TOCTOU): instead of two instantaneous LeaseCount checks (which left a
	// window for a concurrent launch to acquire a lease before RemoveAll), this
	// enters a SINGLE install-drain critical section on the SharedServiceCoordinator
	// that blocks new AcquireForRun for BOTH headroom kinds, confirms both are
	// lease-free, and stays held until CleanHeadroom finishes the venv removal. It
	// returns (stopErr, releaseDrain): releaseDrain releases the drain and is
	// invoked by CleanHeadroom via defer after RemoveAll (or on abort). When
	// existing leases are present, it returns ErrHeadroomInUse (fatal) plus the
	// releaseDrain (still set so the caller releases the drain it entered).
	release := func() {} // default no-op when coordinator is absent
	if a.sharedCoord != nil {
		empty := a.sharedCoord.BeginHeadroomUninstallDrain()
		release = a.sharedCoord.EndHeadroomUninstallDrain
		if !empty {
			cn := a.sharedCoord.LeaseCount(remote.SharedServiceClaudeHeadroom)
			xn := a.sharedCoord.LeaseCount(remote.SharedServiceCodexHeadroom)
			if a.Log != nil {
				a.Log.Warn("headroom", "卸载联动拒绝：仍有活跃会话占用 headroom", fmt.Sprintf("claude=%d codex=%d", cn, xn))
			}
			return fmt.Errorf("%w: %w", envcheck.ErrHeadroomInUse, remote.ErrSharedServiceInUse), release
		}
	}
	var firstErr error
	logErr := func(scope string, err error) {
		if err == nil {
			return
		}
		if a.Log != nil {
			a.Log.Warn("headroom", scope, err.Error())
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if a.Headroom != nil {
		logErr("卸载联动：停止 claude 会话级 headroom 失败", a.Headroom.Stop())
	}
	if a.CodexHeadroom != nil {
		logErr("卸载联动：停止 codex 全局 headroom 失败", a.CodexHeadroom.Stop())
	}
	// Only tear down the codex-global config/persistence when the toggle was on;
	// otherwise this is a plain uninstall of the claude-side headroom.
	if a.Settings != nil && a.Settings.GetCodexGlobalHeadroom().Enabled {
		logErr("卸载联动：清理 codex 全局 openai_base_url 失败", removeCodexGlobalHeadroomConfig())
		logErr("卸载联动：清除 codex 全局 headroom 持久化失败", a.Settings.SetCodexGlobalHeadroom(false, "", 0))
	}
	return firstErr, release
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
