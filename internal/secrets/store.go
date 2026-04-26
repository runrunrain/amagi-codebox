package secrets

import "errors"

var ErrSecretStoreNotReady = errors.New("secret store backend is not implemented on this platform")

type SecretStore interface {
	Load(path string) (map[string]string, error)
	Save(path string, values map[string]string) error
	Kind() string
	LegacyImportPath(path string) string
}
