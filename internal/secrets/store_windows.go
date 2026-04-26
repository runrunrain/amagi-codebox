//go:build windows

package secrets

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/billgraziano/dpapi"
)

type windowsSecretStore struct{}

func NewSecretStore() SecretStore {
	return windowsSecretStore{}
}

func (windowsSecretStore) Load(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return map[string]string{}, nil
	}
	plaintext, err := dpapi.Decrypt(string(b))
	if err != nil {
		return nil, fmt.Errorf("dpapi decrypt: %w", err)
	}
	var values map[string]string
	if err := json.Unmarshal([]byte(plaintext), &values); err != nil {
		return nil, fmt.Errorf("parse secrets json: %w", err)
	}
	if values == nil {
		values = map[string]string{}
	}
	return values, nil
}

func (windowsSecretStore) Save(path string, values map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir secrets dir: %w", err)
	}
	plaintext, err := json.Marshal(values)
	if err != nil {
		return fmt.Errorf("marshal secrets: %w", err)
	}
	ciphertext, err := dpapi.Encrypt(string(plaintext))
	if err != nil {
		return fmt.Errorf("dpapi encrypt: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(ciphertext), 0o600); err != nil {
		return fmt.Errorf("write temp secrets: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace secrets: %w", err)
	}
	return nil
}

func (windowsSecretStore) Kind() string { return "dpapi" }

func (windowsSecretStore) LegacyImportPath(path string) string { return path }
