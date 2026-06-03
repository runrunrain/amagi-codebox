//go:build windows

package envvars

import (
	"context"
	"fmt"
	"strings"
	"time"

	"amagi-codebox/internal/platform"
)

// windowsPlatform Windows 平台全局环境变量操作实现
type windowsPlatform struct {
	runner platform.ProcessRunner
}

func newPlatformImpl() globalEnvPlatform {
	return &windowsPlatform{
		runner: platform.NewProcessRunner(),
	}
}

// readUserEnvVar 读取当前用户级环境变量
// 返回 (值, 是否存在, 错误)
func (p *windowsPlatform) readUserEnvVar(key string) (string, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := p.runner.Run(ctx, platform.CommandSpec{
		Path:   "reg",
		Args:   []string{"query", `HKCU\Environment`, "/v", key},
		Policy: platform.DefaultProcessPolicy(),
	})
	if err != nil {
		output := strings.TrimSpace(resultText(result))
		// reg query 在值不存在时返回错误，输出包含 "unable to find" 或 "找不到"
		if strings.Contains(output, "unable to find") || strings.Contains(output, "找不到") {
			return "", false, nil
		}
		return "", false, fmt.Errorf("reg query HKCU\\Environment\\%s: %w", key, err)
	}

	// 解析 reg query 输出
	// 格式示例:
	//   HKEY_CURRENT_USER\Environment
	//       FOO    REG_SZ    bar
	// 或
	//       FOO    REG_EXPAND_SZ    %SystemRoot%
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "REG_SZ") || strings.Contains(line, "REG_EXPAND_SZ") {
			// 找到值行，格式: KEY    REG_TYPE    VALUE
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				// 值可能包含空格，需要从第三个字段开始拼接
				// 先找到 REG_SZ 或 REG_EXPAND_SZ 的位置
				regIdx := -1
				for i, p := range parts {
					if p == "REG_SZ" || p == "REG_EXPAND_SZ" {
						regIdx = i
						break
					}
				}
				if regIdx >= 0 && regIdx+1 < len(parts) {
					value := strings.Join(parts[regIdx+1:], " ")
					return value, true, nil
				}
			}
		}
	}
	return "", false, nil
}

// writeUserEnvVar 写入当前用户级环境变量
func (p *windowsPlatform) writeUserEnvVar(key, value string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, err := p.runner.Run(ctx, platform.CommandSpec{
		Path:   "reg",
		Args:   []string{"add", `HKCU\Environment`, "/v", key, "/t", "REG_SZ", "/d", value, "/f"},
		Policy: platform.DefaultProcessPolicy(),
	})
	if err != nil {
		return fmt.Errorf("reg add HKCU\\Environment\\%s: %w", key, err)
	}
	return nil
}

// deleteUserEnvVar 删除当前用户级环境变量
// key 不存在时视为成功
func (p *windowsPlatform) deleteUserEnvVar(key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, err := p.runner.Run(ctx, platform.CommandSpec{
		Path:   "reg",
		Args:   []string{"delete", `HKCU\Environment`, "/v", key, "/f"},
		Policy: platform.DefaultProcessPolicy(),
	})
	if err != nil {
		output := strings.TrimSpace(resultText(result))
		// reg delete 在值不存在时返回错误，需要识别 not found 输出并视为成功
		if isKeyNotFoundError(output) {
			return nil
		}
		return fmt.Errorf("reg delete HKCU\\Environment\\%s: %w", key, err)
	}
	return nil
}

// isKeyNotFoundError 判断 reg delete 输出是否表示 key 不存在
// 覆盖英文/中文常见的 not found 输出
func isKeyNotFoundError(output string) bool {
	lower := strings.ToLower(output)
	// 英文: "The system was unable to find the specified registry key or value."
	if strings.Contains(lower, "unable to find") {
		return true
	}
	// 英文变体: "ERROR: The system was unable to find the specified registry key or value."
	if strings.Contains(lower, "the system was unable to find") {
		return true
	}
	// 中文: "系统找不到指定的注册表项或值"
	if strings.Contains(output, "找不到") {
		return true
	}
	return false
}

// broadcastEnvChange 广播 WM_SETTINGCHANGE 通知所有窗口环境变量已变更
func (p *windowsPlatform) broadcastEnvChange() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 使用 PowerShell 调用 Win32 SendMessageTimeout 广播 WM_SETTINGCHANGE
	_, err := p.runner.Run(ctx, platform.CommandSpec{
		Path: "powershell.exe",
		Args: []string{"-NoProfile", "-NonInteractive", "-Command",
			"Add-Type -Name Win32 -Namespace System -MemberDefinition '" +
				`[DllImport("user32.dll")]public static extern IntPtr SendMessageTimeout(IntPtr hWnd,uint Msg,UIntPtr wParam,string lParam,uint fuFlags,uint uTimeout,out UIntPtr lpdwResult);` +
				"'; $HWND_BROADCAST=0xffff; $WM_SETTINGCHANGE=0x001a; $result=0; " +
				"[System.Win32]::SendMessageTimeout($HWND_BROADCAST,$WM_SETTINGCHANGE,0,'Environment',2,5000,[ref]$result)"},
		Policy: platform.DefaultProcessPolicy(),
	})
	// 广播失败不作为致命错误
	if err != nil {
		return fmt.Errorf("broadcast WM_SETTINGCHANGE: %w", err)
	}
	return nil
}

// resultText 从 ProcessResult 中提取文本输出
func resultText(r *platform.ProcessResult) string {
	if r == nil {
		return ""
	}
	return r.Stdout + r.Stderr
}
