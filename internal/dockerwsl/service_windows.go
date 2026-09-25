//go:build windows

package dockerwsl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"

	"amagi-codebox/internal/platform"
)

// Windows 真实现：runner 各接缝映射到系统调用。全部经 platform.SuppressConsoleWindow
// 静默化，wsl.exe 输出按 UTF-16（带 BOM）解码（platform/wsl.go 同口径）。

// dockerDesktopCandidates / dockerEngineCandidates 按安装惯例排序探测。
var (
	dockerDesktopCandidates = []string{
		`C:\Program Files\Docker\Docker\Docker Desktop.exe`,
		`C:\Program Files\Docker\Docker\frontend\Docker Desktop.exe`,
	}
	dockerEngineCandidates = []string{
		`C:\Program Files\Docker\Docker\resources\bin\docker.exe`,
	}
)

// wslOutputCacheMu 保护下缓存 wsl -l -v 解码结果？——不需要：platform.WSLDistroStates
// 自带进程级缓存；runner 直接转发。

func exists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// decodeUTF16BestEffort 对 wsl.exe / tasklist.exe 输出做 UTF-16LE（BOM 开头）解码；
// 非 UTF-16（ASCII/ANSI）时原样返回（GBK 中文乱码不在此层处理——本包不解析中文消息，
// tasklist 只计数 CSV 行）。
func decodeUTF16BestEffort(out []byte) string {
	if len(out) >= 2 && ((out[0] == 0xFF && out[1] == 0xFE) || (len(out)%2 == 0 && out[1] == 0x00 && out[0] != 0x00 && hasNulPairs(out))) {
		u16 := make([]uint16, 0, len(out)/2)
		for i := 0; i+1 < len(out); i += 2 {
			u16 = append(u16, uint16(out[i])|uint16(out[i+1])<<8)
		}
		return string(utf16.Decode(u16))
	}
	return string(out)
}

// hasNulPairs 快速判定偶数位全零的 UTF-16LE 特征（纯 ASCII 输出不会命中）。
func hasNulPairs(out []byte) bool {
	hits := 0
	for i := 1; i+1 < len(out) && hits < 4; i += 2 {
		if out[i] == 0x00 {
			hits++
		}
	}
	return hits >= 4
}

func runCombined(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	platform.SuppressConsoleWindow(cmd)
	out, err := cmd.CombinedOutput()
	return decodeUTF16BestEffort(out), err
}

func newRealRunner() runner {
	return runner{
		desktopExePath: func() string {
			for _, p := range dockerDesktopCandidates {
				if exists(p) {
					return p
				}
			}
			return ""
		},
		engineExePath: func() string {
			for _, p := range dockerEngineCandidates {
				if exists(p) {
					return p
				}
			}
			return ""
		},
		defaultDistro: func() string {
			return platform.DefaultWSLDistro(nil)
		},
		wslStates: func() map[string]string {
			return platform.WSLDistroStates(nil)
		},
		wsl: func(args ...string) (string, error) {
			// 发行版内 stat/test 不产生 UTF-16；wsl.exe 控制台输出按需解码。
			out, err := runCombined("wsl.exe", args...)
			return strings.TrimSpace(strings.TrimRight(out, "\r\n\x00")), err
		},
		tasklistCount: func(image string) (int, error) {
			out, err := runCombined("tasklist.exe", "/FI", fmt.Sprintf("IMAGENAME eq %s", image), "/FO", "CSV", "/NH")
			if err != nil {
				return 0, err
			}
			count := 0
			for _, line := range strings.Split(out, "\n") {
				line = strings.TrimSpace(strings.Trim(line, "\r\x00"))
				if line == "" || strings.Contains(line, "INFO:") {
					continue
				}
				if strings.HasPrefix(line, "\"") {
					count++
				}
			}
			return count, nil
		},
		taskkillTree: func(image string) error {
			out, err := runCombined("taskkill.exe", "/IM", image, "/T", "/F")
			_ = out
			return err
		},
		startDesktop: func(path string) error {
			if path == "" || !exists(path) {
				return errors.New("Docker Desktop.exe 不存在")
			}
			cmd := exec.Command(path)
			platform.SuppressConsoleWindow(cmd)
			cmd.Dir = filepath.Dir(path)
			return cmd.Start()
		},
		engineReady: func(engineExe string) (bool, string, error) {
			if engineExe == "" {
				return false, "", errors.New("docker.exe 未找到")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, engineExe, "info", "--format", "{{.ServerVersion}}")
			platform.SuppressConsoleWindow(cmd)
			out, err := cmd.Output()
			if err != nil {
				return false, "", err
			}
			ver := strings.TrimSpace(decodeUTF16BestEffort(out))
			if ver == "" {
				return false, "", nil
			}
			return true, ver, nil
		},
		sleep: time.Sleep,
		now:   time.Now,
	}
}
