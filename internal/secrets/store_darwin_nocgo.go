//go:build darwin && !cgo

package secrets

type darwinSecretStore struct{}

func NewSecretStore() SecretStore {
	return darwinSecretStore{}
}

func (darwinSecretStore) Load(path string) (map[string]string, error) {
	_ = path
	return nil, ErrSecretStoreNotReady
}

func (darwinSecretStore) Save(path string, values map[string]string) error {
	_, _ = path, values
	return ErrSecretStoreNotReady
}

func (darwinSecretStore) Kind() string { return "keychain" }

func (darwinSecretStore) LegacyImportPath(path string) string { return path }
