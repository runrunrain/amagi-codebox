//go:build darwin

package secrets

import (
	"errors"
	"os"
)

type darwinSecretStore struct{}

func NewSecretStore() SecretStore {
	return darwinSecretStore{}
}

func (darwinSecretStore) Load(path string) (map[string]string, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	return nil, ErrSecretStoreNotReady
}

func (darwinSecretStore) Save(path string, values map[string]string) error {
	_ = path
	_ = values
	return ErrSecretStoreNotReady
}

func (darwinSecretStore) Kind() string { return "keychain" }

func (darwinSecretStore) LegacyImportPath(path string) string { return path }
