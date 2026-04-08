package amagi

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Service manages settings_amagi.json configuration file.
// Methods are public for Wails binding.
type Service struct {
	configPath string
	config     *AmagiSettings
	mu         sync.RWMutex
	dirty      bool // Track if config has unsaved changes
}

// NewService creates a new AmagiService.
// configPath should be the directory containing settings_amagi.json.
func NewService(configDir string) *Service {
	return &Service{
		configPath: filepath.Join(configDir, "settings_amagi.json"),
		config:     nil,
	}
}

// deepCopyPreset creates a deep copy of AmagiModelPreset, including reference-type fields.
func deepCopyPreset(p AmagiModelPreset) AmagiModelPreset {
	cp := p
	if p.Thinking != nil {
		t := *p.Thinking
		cp.Thinking = &t
	}
	if p.ProtocolOptions != nil {
		cp.ProtocolOptions = make(map[string]interface{}, len(p.ProtocolOptions))
		for k, v := range p.ProtocolOptions {
			cp.ProtocolOptions[k] = v
		}
	}
	if p.ProviderOptions != nil {
		cp.ProviderOptions = make(map[string]interface{}, len(p.ProviderOptions))
		for k, v := range p.ProviderOptions {
			cp.ProviderOptions[k] = v
		}
	}
	return cp
}

// Load reads settings_amagi.json from disk.
// Returns default empty config if file doesn't exist (no error).
// Supports backward-compatible migration from flat preset structure to grouped structure.
func (s *Service) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := os.ReadFile(s.configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.config = DefaultAmagiSettings()
			s.dirty = false
			return nil
		}
		return fmt.Errorf("read amagi config: %w", err)
	}

	// First parse the full config with raw model_presets
	type rawSettings struct {
		Model                    string                             `json:"model"`
		Providers                map[string]AmagiProvider           `json:"providers"`
		AvailableModels          []string                           `json:"available_models,omitempty"`
		ModelOverrides           map[string]string                  `json:"model_overrides,omitempty"`
		ModelCapabilityOverrides map[string]AmagiCapabilityOverride `json:"model_capability_overrides,omitempty"`
		RawModelPresets          json.RawMessage                    `json:"model_presets"`
		AlwaysThinkingEnabled    *bool                              `json:"always_thinking_enabled,omitempty"`
		EffortLevel              string                             `json:"effort_level,omitempty"`
		AdvisorModel             string                             `json:"advisor_model,omitempty"`
	}

	var raw rawSettings
	if err := json.Unmarshal(b, &raw); err != nil {
		return fmt.Errorf("parse amagi config json: %w", err)
	}

	// Migrate model_presets from old format if needed
	modelPresets, oldModel, migrated, err := migrateModelPresets(raw.RawModelPresets, raw.Model)
	if err != nil {
		return fmt.Errorf("migrate model presets: %w", err)
	}

	// Build final config
	cfg := AmagiSettings{
		Model:                    raw.Model,
		Providers:                raw.Providers,
		AvailableModels:          raw.AvailableModels,
		ModelOverrides:           raw.ModelOverrides,
		ModelCapabilityOverrides: raw.ModelCapabilityOverrides,
		ModelPresets:             modelPresets,
		AlwaysThinkingEnabled:    raw.AlwaysThinkingEnabled,
		EffortLevel:              raw.EffortLevel,
		AdvisorModel:             raw.AdvisorModel,
	}

	// Initialize nil maps to avoid panics
	if cfg.Providers == nil {
		cfg.Providers = map[string]AmagiProvider{}
	}
	if cfg.AvailableModels == nil {
		cfg.AvailableModels = []string{}
	}
	if cfg.ModelOverrides == nil {
		cfg.ModelOverrides = map[string]string{}
	}
	if cfg.ModelCapabilityOverrides == nil {
		cfg.ModelCapabilityOverrides = map[string]AmagiCapabilityOverride{}
	}
	if cfg.ModelPresets == nil {
		cfg.ModelPresets = map[string]ModelPresetGroup{}
	}

	s.config = &cfg

	// Persist migration immediately to avoid data loss on abnormal exit
	if migrated {
		if oldModel != "" {
			if defaultGroup, ok := s.config.ModelPresets["Default"]; ok {
				defaultGroup.DefaultPreset = oldModel
				s.config.ModelPresets["Default"] = defaultGroup
			}
		}
		s.config.Model = "Default"
		if err := s.saveLocked(); err != nil {
			return fmt.Errorf("persist migrated config: %w", err)
		}
	} else {
		s.dirty = false
	}
	return nil
}

// hasValidGroupStructure checks if the groups map contains valid new-format structure.
// Returns true if at least one group has non-empty presets, description, or default_preset.
func hasValidGroupStructure(groups map[string]ModelPresetGroup) bool {
	for _, g := range groups {
		if g.Presets != nil || g.Description != "" || g.DefaultPreset != "" {
			return true
		}
	}
	return false
}

// migrateModelPresets attempts to parse model_presets as new format first,
// then falls back to old flat format if needed.
// Returns (presets, oldModelValue, wasMigrated, error).
func migrateModelPresets(rawJSON json.RawMessage, oldModelValue string) (map[string]ModelPresetGroup, string, bool, error) {
	if len(rawJSON) == 0 || string(rawJSON) == "null" {
		return map[string]ModelPresetGroup{}, "", false, nil
	}

	// Try new format first
	var groups map[string]ModelPresetGroup
	if err := json.Unmarshal(rawJSON, &groups); err == nil {
		// Check if it's actually new format using structure validation
		if hasValidGroupStructure(groups) {
			return groups, "", false, nil
		}
	}

	// Try old format (flat presets)
	var flat map[string]AmagiModelPreset
	if err := json.Unmarshal(rawJSON, &flat); err != nil {
		// Neither format worked, return empty
		return map[string]ModelPresetGroup{}, "", false, nil
	}

	if len(flat) == 0 {
		return map[string]ModelPresetGroup{}, "", false, nil
	}

	// Migrate: wrap all flat presets into a "Default" group
	defaultGroup := ModelPresetGroup{
		Description:   "从旧版格式自动迁移",
		DefaultPreset: "", // Will be set by caller based on old "model" field
		Presets:       flat,
	}
	return map[string]ModelPresetGroup{"Default": defaultGroup}, oldModelValue, true, nil
}

// Save writes settings_amagi.json to disk using atomic write.
// Only writes if dirty flag is true.
// Uses write lock throughout to ensure atomicity of check-write-clear cycle.
func (s *Service) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.config == nil {
		return errors.New("config not loaded")
	}
	if !s.dirty {
		return nil // No changes to save
	}

	path := s.configPath
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir amagi config dir: %w", err)
	}

	b, err := json.MarshalIndent(s.config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal amagi config: %w", err)
	}
	b = append(b, '\n')

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write temp amagi config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace amagi config: %w", err)
	}

	s.dirty = false
	return nil
}

// saveLocked marks config as dirty and saves.
// Caller must hold write lock.
func (s *Service) saveLocked() error {
	s.dirty = true
	cfg := s.config
	if cfg == nil {
		return errors.New("config not loaded")
	}
	path := s.configPath

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir amagi config dir: %w", err)
	}

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal amagi config: %w", err)
	}
	b = append(b, '\n')

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write temp amagi config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace amagi config: %w", err)
	}

	s.dirty = false
	return nil
}

// GetSettings returns a copy of current settings.
func (s *Service) GetSettings() *AmagiSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.config == nil {
		return DefaultAmagiSettings()
	}
	// Return a deep copy to avoid external modification
	copy := *s.config
	if copy.Providers != nil {
		copy.Providers = make(map[string]AmagiProvider, len(s.config.Providers))
		for k, v := range s.config.Providers {
			copy.Providers[k] = v
		}
	}
	if copy.ModelOverrides != nil {
		copy.ModelOverrides = make(map[string]string, len(s.config.ModelOverrides))
		for k, v := range s.config.ModelOverrides {
			copy.ModelOverrides[k] = v
		}
	}
	if copy.ModelCapabilityOverrides != nil {
		copy.ModelCapabilityOverrides = make(map[string]AmagiCapabilityOverride, len(s.config.ModelCapabilityOverrides))
		for k, v := range s.config.ModelCapabilityOverrides {
			copy.ModelCapabilityOverrides[k] = v
		}
	}
	if copy.ModelPresets != nil {
		copy.ModelPresets = make(map[string]ModelPresetGroup, len(s.config.ModelPresets))
		for groupName, group := range s.config.ModelPresets {
			groupCopy := group
			// Deep copy the presets map within each group
			if group.Presets != nil {
				groupCopy.Presets = make(map[string]AmagiModelPreset, len(group.Presets))
				for presetName, preset := range group.Presets {
					groupCopy.Presets[presetName] = deepCopyPreset(preset)
				}
			}
			copy.ModelPresets[groupName] = groupCopy
		}
	}
	if copy.AvailableModels != nil {
		models := make([]string, len(s.config.AvailableModels))
		for i, v := range s.config.AvailableModels {
			models[i] = v
		}
		copy.AvailableModels = models
	}
	return &copy
}

// GetModelPresets returns all model preset groups.
func (s *Service) GetModelPresets() map[string]ModelPresetGroup {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.config == nil || s.config.ModelPresets == nil {
		return map[string]ModelPresetGroup{}
	}
	out := make(map[string]ModelPresetGroup, len(s.config.ModelPresets))
	for k, v := range s.config.ModelPresets {
		groupCopy := v
		// Deep copy the presets map within each group
		if v.Presets != nil {
			groupCopy.Presets = make(map[string]AmagiModelPreset, len(v.Presets))
			for presetName, preset := range v.Presets {
				groupCopy.Presets[presetName] = deepCopyPreset(preset)
			}
		}
		out[k] = groupCopy
	}
	return out
}

// GetModelPreset returns a specific preset group by name.
func (s *Service) GetModelPreset(name string) (*ModelPresetGroup, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.config == nil {
		return nil, errors.New("config not loaded")
	}
	group, ok := s.config.ModelPresets[name]
	if !ok {
		return nil, fmt.Errorf("preset group not found: %s", name)
	}
	groupCopy := group
	// Deep copy the presets map
	if group.Presets != nil {
		groupCopy.Presets = make(map[string]AmagiModelPreset, len(group.Presets))
		for presetName, preset := range group.Presets {
			groupCopy.Presets[presetName] = deepCopyPreset(preset)
		}
	}
	return &groupCopy, nil
}

// SaveModelPreset saves or updates a model preset group.
func (s *Service) SaveModelPreset(name string, group ModelPresetGroup) error {
	if name == "" {
		return errors.New("preset group name is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil {
		return errors.New("config not loaded")
	}
	if s.config.ModelPresets == nil {
		s.config.ModelPresets = map[string]ModelPresetGroup{}
	}
	s.config.ModelPresets[name] = group
	return s.saveLocked()
}

// DeleteModelPreset deletes a model preset group.
func (s *Service) DeleteModelPreset(name string) error {
	if name == "" {
		return errors.New("preset group name is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil {
		return errors.New("config not loaded")
	}
	if s.config.ModelPresets == nil {
		return nil
	}
	delete(s.config.ModelPresets, name)
	return s.saveLocked()
}

// RenameModelPreset renames a model preset group atomically under write lock.
// If the active model references the old name, it is updated to the new name.
func (s *Service) RenameModelPreset(oldName, newName string) error {
	if oldName == "" || newName == "" {
		return errors.New("both old and new preset group names are required")
	}
	if oldName == newName {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil {
		return errors.New("config not loaded")
	}

	group, ok := s.config.ModelPresets[oldName]
	if !ok {
		return fmt.Errorf("preset group not found: %s", oldName)
	}
	if _, exists := s.config.ModelPresets[newName]; exists {
		return fmt.Errorf("preset group already exists: %s", newName)
	}

	s.config.ModelPresets[newName] = group
	delete(s.config.ModelPresets, oldName)

	// Update active model reference if it was pointing to the old name
	if s.config.Model == oldName {
		s.config.Model = newName
	}

	return s.saveLocked()
}

// GetSubPreset returns a specific sub-preset within a group.
func (s *Service) GetSubPreset(groupName, presetName string) (*AmagiModelPreset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.config == nil {
		return nil, errors.New("config not loaded")
	}
	group, ok := s.config.ModelPresets[groupName]
	if !ok {
		return nil, fmt.Errorf("preset group not found: %s", groupName)
	}
	preset, ok := group.Presets[presetName]
	if !ok {
		return nil, fmt.Errorf("sub-preset not found: %s in group %s", presetName, groupName)
	}
	copy := preset
	return &copy, nil
}

// SaveSubPreset saves or updates a sub-preset within a group.
func (s *Service) SaveSubPreset(groupName, presetName string, preset AmagiModelPreset) error {
	if groupName == "" {
		return errors.New("group name is required")
	}
	if presetName == "" {
		return errors.New("preset name is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil {
		return errors.New("config not loaded")
	}
	if s.config.ModelPresets == nil {
		s.config.ModelPresets = map[string]ModelPresetGroup{}
	}

	group, ok := s.config.ModelPresets[groupName]
	if !ok {
		// Create new group if it doesn't exist
		group = ModelPresetGroup{
			Presets: map[string]AmagiModelPreset{},
		}
	}
	if group.Presets == nil {
		group.Presets = map[string]AmagiModelPreset{}
	}
	group.Presets[presetName] = preset
	s.config.ModelPresets[groupName] = group
	return s.saveLocked()
}

// DeleteSubPreset deletes a sub-preset from a group.
func (s *Service) DeleteSubPreset(groupName, presetName string) error {
	if groupName == "" {
		return errors.New("group name is required")
	}
	if presetName == "" {
		return errors.New("preset name is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil {
		return errors.New("config not loaded")
	}
	if s.config.ModelPresets == nil {
		return nil
	}

	group, ok := s.config.ModelPresets[groupName]
	if !ok {
		return nil
	}
	if group.Presets == nil {
		return nil
	}
	delete(group.Presets, presetName)
	s.config.ModelPresets[groupName] = group
	return s.saveLocked()
}

// SetModel sets the default model (now: current active group name).
func (s *Service) SetModel(model string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil {
		return errors.New("config not loaded")
	}
	s.config.Model = model
	return s.saveLocked()
}

// SetEffortLevel sets the effort level.
func (s *Service) SetEffortLevel(level string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil {
		return errors.New("config not loaded")
	}
	s.config.EffortLevel = level
	return s.saveLocked()
}

// SetAvailableModels sets the available models list.
func (s *Service) SetAvailableModels(models []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil {
		return errors.New("config not loaded")
	}
	s.config.AvailableModels = models
	return s.saveLocked()
}

// SetAlwaysThinkingEnabled sets the always thinking enabled flag.
func (s *Service) SetAlwaysThinkingEnabled(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil {
		return errors.New("config not loaded")
	}
	s.config.AlwaysThinkingEnabled = &enabled
	return s.saveLocked()
}

// SetAdvisorModel sets the advisor model.
func (s *Service) SetAdvisorModel(model string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil {
		return errors.New("config not loaded")
	}
	s.config.AdvisorModel = model
	return s.saveLocked()
}

// GetSettingsJSON returns the settings as a JSON string.
// For frontend JSON view.
func (s *Service) GetSettingsJSON() (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.config == nil {
		cfg := DefaultAmagiSettings()
		b, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return "", fmt.Errorf("marshal settings: %w", err)
		}
		return string(b), nil
	}
	b, err := json.MarshalIndent(s.config, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal settings: %w", err)
	}
	return string(b), nil
}

// SaveSettingsJSON saves settings from a JSON string.
// Only writes whitelist fields, filters out unknown fields.
// For frontend JSON view.
func (s *Service) SaveSettingsJSON(jsonStr string) error {
	var cfg AmagiSettings
	if err := json.Unmarshal([]byte(jsonStr), &cfg); err != nil {
		return fmt.Errorf("parse settings JSON: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil {
		s.config = DefaultAmagiSettings()
	}

	// Only update whitelist fields
	s.config.Model = cfg.Model
	s.config.Providers = cfg.Providers
	s.config.AvailableModels = cfg.AvailableModels
	s.config.ModelOverrides = cfg.ModelOverrides
	s.config.ModelCapabilityOverrides = cfg.ModelCapabilityOverrides
	s.config.ModelPresets = cfg.ModelPresets
	s.config.AlwaysThinkingEnabled = cfg.AlwaysThinkingEnabled
	s.config.EffortLevel = cfg.EffortLevel
	s.config.AdvisorModel = cfg.AdvisorModel

	// Ensure maps are initialized
	if s.config.Providers == nil {
		s.config.Providers = map[string]AmagiProvider{}
	}
	if s.config.AvailableModels == nil {
		s.config.AvailableModels = []string{}
	}
	if s.config.ModelOverrides == nil {
		s.config.ModelOverrides = map[string]string{}
	}
	if s.config.ModelCapabilityOverrides == nil {
		s.config.ModelCapabilityOverrides = map[string]AmagiCapabilityOverride{}
	}
	if s.config.ModelPresets == nil {
		s.config.ModelPresets = map[string]ModelPresetGroup{}
	}

	return s.saveLocked()
}
