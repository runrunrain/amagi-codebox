// Package opencodeconfig manages the global OpenCode configuration file
// located at ~/.config/opencode/opencode.json.
//
// It provides read/write operations for the frontend Settings page,
// with automatic directory creation, default content generation,
// and JSON validation on save.
package opencodeconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// defaultConfigContent is returned when the config file does not exist.
// It is a valid empty JSON object so front-end editors can parse it immediately.
const defaultConfigContent = "{\n}\n"

// Service provides read/write access to the global OpenCode configuration file.
// It is stateless: each method resolves the file path from the current user's
// home directory at call time, ensuring the path is always up to date.
type Service struct{}

// NewService creates a new OpenCode config service.
func NewService() *Service {
	return &Service{}
}

// configFilePath returns the absolute path to the global OpenCode config file.
// Path: $HOME/.config/opencode/opencode.json
func configFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home dir: %w", err)
	}
	return filepath.Join(home, ".config", "opencode", "opencode.json"), nil
}

// ensureDir creates the parent directory of the given path if it does not exist.
func ensureDir(path string) error {
	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	} else if err != nil {
		return fmt.Errorf("stat directory %s: %w", dir, err)
	}
	return nil
}

// GetOpenCodeConfig reads the global OpenCode config file and returns its
// content as a JSON string. If the file does not exist, it returns a default
// empty configuration. If the file exists but contains invalid JSON or a
// non-object root (array, string, number, etc.), the raw content is still
// returned so the user can see and fix it in the editor.
func (s *Service) GetOpenCodeConfig() (string, error) {
	path, err := configFilePath()
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return defaultConfigContent, nil
		}
		return "", fmt.Errorf("read opencode config: %w", err)
	}

	// Validate JSON; if invalid, still return raw content so user can fix it.
	if !json.Valid(data) {
		return string(data), nil
	}

	// Require the root node to be a JSON object.
	// json.Unmarshal into map[string]any silently accepts null (produces nil),
	// so treat nil the same as non-object: return raw content for the user to fix.
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil || obj == nil {
		return string(data), nil
	}

	// Re-format with consistent indentation for display.
	formatted, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return "", fmt.Errorf("format opencode config: %w", err)
	}
	if string(formatted) == "{}" {
		formatted = []byte("{\n}")
	}
	return string(formatted) + "\n", nil
}

// SaveOpenCodeConfig validates and saves the given JSON string to the global
// OpenCode config file. The root node must be a JSON object; arrays, strings,
// numbers, and other non-object types are rejected. It returns an error if the
// content is not valid JSON, is not a JSON object, or if the file cannot be
// written.
//
// The file is written atomically (write to .tmp then rename) to avoid
// corruption from partial writes. Parent directories are created automatically.
func (s *Service) SaveOpenCodeConfig(content string) error {
	// Validate JSON before writing.
	if !json.Valid([]byte(content)) {
		return fmt.Errorf("invalid JSON: content is not valid JSON")
	}

	// Require root node to be a JSON object.
	// json.Unmarshal into map[string]any silently accepts null (produces nil),
	// so we must detect that case explicitly.
	var obj map[string]any
	if err := json.Unmarshal([]byte(content), &obj); err != nil || obj == nil {
		return fmt.Errorf("invalid config: root must be a JSON object, not %s", jsonRootType(content))
	}

	// Re-format for consistent output.
	// json.MarshalIndent serialises an empty map as "{}", but we prefer "{\n}\n"
	// to keep a consistent multi-line layout that editors handle well.
	formatted, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return fmt.Errorf("format JSON: %w", err)
	}
	if string(formatted) == "{}" {
		formatted = []byte("{\n}")
	}
	formatted = append(formatted, '\n')

	path, err := configFilePath()
	if err != nil {
		return err
	}

	if err := ensureDir(path); err != nil {
		return err
	}

	// Atomic write: write to temp file then rename.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, formatted, 0o644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace config file: %w", err)
	}

	return nil
}

// jsonRootType returns a human-readable description of the root JSON type.
func jsonRootType(content string) string {
	// Trim leading whitespace to find the first meaningful character.
	for _, r := range content {
		switch r {
		case ' ', '\t', '\n', '\r':
			continue
		case '{':
			return "object"
		case '[':
			return "array"
		case '"':
			return "string"
		case 't', 'f':
			return "boolean"
		case 'n':
			return "null"
		default:
			return "number"
		}
	}
	return "empty"
}

// GetOpenCodeConfigPath returns the absolute path to the global OpenCode config
// file. Useful for front-end display purposes (e.g., showing the user where
// the file is located on disk).
func (s *Service) GetOpenCodeConfigPath() (string, error) {
	return configFilePath()
}
