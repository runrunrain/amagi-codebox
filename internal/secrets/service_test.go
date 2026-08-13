package secrets

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
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

type blockingLoadStore struct {
	started chan struct{}
	release chan struct{}
}

func (s *blockingLoadStore) Load(path string) (map[string]string, error) {
	_ = path
	close(s.started)
	<-s.release
	return map[string]string{"openai": "sk-loaded"}, nil
}

func (s *blockingLoadStore) Save(path string, values map[string]string) error {
	_ = path
	_ = values
	return nil
}

func (s *blockingLoadStore) Kind() string { return "blocking" }

func (s *blockingLoadStore) LegacyImportPath(path string) string { return path }

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

func TestSlowLoadDoesNotBlockSnapshotOrMutation(t *testing.T) {
	store := &blockingLoadStore{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	svc := NewSecretsServiceWithStore(t.TempDir(), store)
	loadDone := make(chan error, 1)
	go func() { loadDone <- svc.Load() }()

	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("secret store load did not start")
	}

	assertReturnsQuickly := func(name string, fn func() error, target error) {
		t.Helper()
		done := make(chan error, 1)
		go func() { done <- fn() }()
		select {
		case err := <-done:
			if !errors.Is(err, target) {
				t.Fatalf("%s error = %v, want %v", name, err, target)
			}
		case <-time.After(250 * time.Millisecond):
			t.Fatalf("%s blocked behind the credential store load", name)
		}
	}

	assertReturnsQuickly("snapshot", func() error {
		_, err := svc.Snapshot()
		return err
	}, ErrSecretsLoading)
	assertReturnsQuickly("set API key", func() error {
		return svc.SetAPIKey("openai", "sk-new")
	}, ErrSecretsLoading)

	close(store.release)
	select {
	case err := <-loadDone:
		if err != nil {
			t.Fatalf("load: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("secret store load did not finish")
	}

	snapshot, err := svc.Snapshot()
	if err != nil {
		t.Fatalf("snapshot after load: %v", err)
	}
	if snapshot["openai"] != "sk-loaded" {
		t.Fatalf("snapshot = %#v, want loaded secret", snapshot)
	}
}
