//go:build !darwin && !linux && !windows

package sharedmarket

import (
	"errors"
	"os"
)

func fileIdentity(*os.File) (string, error) {
	return "", errors.New("当前平台不支持持久文件身份")
}
