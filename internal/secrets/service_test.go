package secrets

import (
	"errors"
	"path/filepath"
	"testing"
)

type stubStore struct {
	saveErr error
}

func (s stubStore) Load(path string) (map[string]string, error) {
	_ = path
	return map[string]string{}, nil
}

func (s stubStore) Save(path string, values map[string]string) error {
	_ = path
	_ = values
	return s.saveErr
}

func (s stubStore) Kind() string { return "stub" }

func (s stubStore) LegacyImportPath(path string) string { return path }

func TestSaveIgnoresUnreadyStoreWhenSecretsEmpty(t *testing.T) {
	svc := &SecretsService{
		secretsPath: filepath.Join(t.TempDir(), "secrets.enc"),
		store:       stubStore{saveErr: ErrSecretStoreNotReady},
		cache:       map[string]string{},
	}

	if err := svc.Save(); err != nil {
		t.Fatalf("expected empty secret save to skip unready store, got %v", err)
	}
}

func TestSaveReturnsUnreadyStoreErrorWhenSecretsExist(t *testing.T) {
	svc := &SecretsService{
		secretsPath: filepath.Join(t.TempDir(), "secrets.enc"),
		store:       stubStore{saveErr: ErrSecretStoreNotReady},
		cache: map[string]string{
			"openai": "sk-live",
		},
	}

	err := svc.Save()
	if !errors.Is(err, ErrSecretStoreNotReady) {
		t.Fatalf("expected ErrSecretStoreNotReady, got %v", err)
	}
}
