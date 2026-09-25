//go:build !windows

package dockerwsl

import (
	"errors"
	"time"
)

// 非 Windows 平台桩：Docker Desktop 仅存在于 Windows（macOS 版不管理 WSL 集成）。
// 方法返回「不支持」，与 wslsetup 的 service_other.go 同口径。

func newRealRunner() runner {
	unsupported := func() (string, error) { return "", errors.New("dockerwsl: 仅支持 Windows") }
	return runner{
		desktopExePath: func() string { return "" },
		engineExePath:  func() string { return "" },
		defaultDistro:  func() string { return "" },
		wslStates:      func() map[string]string { return map[string]string{} },
		wsl: func(args ...string) (string, error) {
			return unsupported()
		},
		tasklistCount: func(image string) (int, error) { return 0, nil },
		taskkillTree:  func(image string) error { return nil },
		startDesktop:  func(path string) error { return errors.New("dockerwsl: 仅支持 Windows") },
		engineReady: func(engineExe string) (bool, string, error) {
			return false, "", errors.New("dockerwsl: 仅支持 Windows")
		},
		sleep: time.Sleep,
		now:   time.Now,
	}
}
