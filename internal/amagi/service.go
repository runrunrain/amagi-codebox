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

// Load reads settings_amagi.json from disk.
// Returns default empty config if file doesn't exist (no error).
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

	var cfg AmagiSettings
	if err := json.Unmarshal(b, &cfg); err != nil {
		return fmt.Errorf("parse amagi config json: %w", err)
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
		cfg.ModelPresets = map[string]AmagiModelPreset{}
	}

	s.config = &cfg
	s.dirty = false
	return nil
}

// Save writes settings_amagi.json to disk using atomic write.
// Only writes if dirty flag is true.
func (s *Service) Save() error {
	s.mu.RLock()
	cfg := s.config
	path := s.configPath
	dirty := s.dirty
	s.mu.RUnlock()

	if cfg == nil {
		return errors.New("config not loaded")
	}
	if !dirty {
		return nil // No changes to save
	}

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

	s.mu.Lock()
	s.dirty = false
	s.mu.Unlock()

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
		copy.ModelPresets = make(map[string]AmagiModelPreset, len(s.config.ModelPresets))
		for k, v := range s.config.ModelPresets {
			copy.ModelPresets[k] = v
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

// GetModelPresets returns all model presets.
func (s *Service) GetModelPresets() map[string]AmagiModelPreset {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.config == nil || s.config.ModelPresets == nil {
		return map[string]AmagiModelPreset{}
	}
	out := make(map[string]AmagiModelPreset, len(s.config.ModelPresets))
	for k, v := range s.config.ModelPresets {
		out[k] = v
	}
	return out
}

// GetModelPreset returns a specific preset by name.
func (s *Service) GetModelPreset(name string) (*AmagiModelPreset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.config == nil {
		return nil, errors.New("config not loaded")
	}
	preset, ok := s.config.ModelPresets[name]
	if !ok {
		return nil, fmt.Errorf("preset not found: %s", name)
	}
	copy := preset
	return &copy, nil
}

// SaveModelPreset saves or updates a model preset.
func (s *Service) SaveModelPreset(name string, preset AmagiModelPreset) error {
	if name == "" {
		return errors.New("preset name is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil {
		return errors.New("config not loaded")
	}
	if s.config.ModelPresets == nil {
		s.config.ModelPresets = map[string]AmagiModelPreset{}
	}
	s.config.ModelPresets[name] = preset
	return s.saveLocked()
}

// DeleteModelPreset deletes a model preset.
func (s *Service) DeleteModelPreset(name string) error {
	if name == "" {
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
	delete(s.config.ModelPresets, name)
	return s.saveLocked()
}

// SetModel sets the default model.
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
		s.config.ModelPresets = map[string]AmagiModelPreset{}
	}

	return s.saveLocked()
}
