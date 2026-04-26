//go:build !windows && !darwin

package secrets

type unsupportedSecretStore struct{}

func NewSecretStore() SecretStore {
	return unsupportedSecretStore{}
}

func (unsupportedSecretStore) Load(path string) (map[string]string, error) {
	return map[string]string{}, nil
}

func (unsupportedSecretStore) Save(path string, values map[string]string) error {
	_ = path
	_ = values
	return nil
}

func (unsupportedSecretStore) Kind() string { return "unsupported" }

func (unsupportedSecretStore) LegacyImportPath(path string) string { return path }
