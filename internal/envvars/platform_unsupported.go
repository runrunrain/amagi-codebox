//go:build !windows

package envvars

import "fmt"

// unsupportedPlatform 非 Windows 平台的全局环境变量操作实现（全部返回 unsupported 错误）
type unsupportedPlatform struct{}

func newPlatformImpl() globalEnvPlatform {
	return &unsupportedPlatform{}
}

func (p *unsupportedPlatform) supportsGlobalSync() bool {
	return false
}

func (p *unsupportedPlatform) readUserEnvVar(key string) (string, bool, error) {
	return "", false, fmt.Errorf("global env sync is not supported on this platform")
}

func (p *unsupportedPlatform) writeUserEnvVar(key, value string) error {
	return fmt.Errorf("global env sync is not supported on this platform")
}

func (p *unsupportedPlatform) deleteUserEnvVar(key string) error {
	return fmt.Errorf("global env sync is not supported on this platform")
}

func (p *unsupportedPlatform) broadcastEnvChange() error {
	return fmt.Errorf("global env sync is not supported on this platform")
}
