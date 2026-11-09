package platform

import (
	"os"
	"strings"
)

// WSL 会话的 Windows 根目录事实（供 amagi-pi `win_exec` 等工具定位 Windows 原生
// 程序，不依赖 WSL PATH 里是否带 Windows 目录——CodeBox 刻意不启用全局 interop
// PATH 混合）。值取自 CodeBox 所在的 Windows 宿主环境，经 WSLENV `/p` 由 wsl.exe
// 翻译成 /mnt/<盘符>/…。键名是 AMAGI_WIN_ 精确键，不开放 AMAGI_ 前缀。

// wslWindowsRootKeys 把转发键映射到宿主环境变量名。
var wslWindowsRootKeys = []struct{ key, hostVar string }{
	{"AMAGI_WIN_SYSTEMROOT", "SystemRoot"},
	{"AMAGI_WIN_PROGRAMFILES", "ProgramFiles"},
	{"AMAGI_WIN_LOCALAPPDATA", "LOCALAPPDATA"},
	{"AMAGI_WIN_USERPROFILE", "USERPROFILE"},
}

// wslWindowsRootLookup 是宿主环境读取接缝（测试替换）。
var wslWindowsRootLookup = os.Getenv

// WSLWindowsRootKeys 返回 AMAGI_WIN_* 转发键（均需 /p 路径翻译）。
func WSLWindowsRootKeys() []string {
	out := make([]string, 0, len(wslWindowsRootKeys))
	for _, k := range wslWindowsRootKeys {
		out = append(out, k.key)
	}
	return out
}

// appendWSLWindowsRoots 向 env 追加缺失的 AMAGI_WIN_* 条目（仅当宿主值是盘符
// 绝对路径；env 已显式给出同名键则不覆盖）。返回新切片与实际追加的键（调用方
// 把它们并入转发清单与 /p 标志集）。
func appendWSLWindowsRoots(env []string) ([]string, []string) {
	have := map[string]struct{}{}
	for _, kv := range env {
		if k, _, ok := strings.Cut(kv, "="); ok {
			have[strings.ToUpper(k)] = struct{}{}
		}
	}
	out := append([]string(nil), env...)
	var added []string
	for _, k := range wslWindowsRootKeys {
		if _, dup := have[k.key]; dup {
			continue
		}
		v := strings.TrimSpace(wslWindowsRootLookup(k.hostVar))
		if !isWindowsDriveAbs(v) {
			continue
		}
		out = append(out, k.key+"="+v)
		added = append(added, k.key)
	}
	return out, added
}

func isWindowsDriveAbs(v string) bool {
	if len(v) < 3 || v[1] != ':' || (v[2] != '\\' && v[2] != '/') {
		return false
	}
	c := v[0]
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}
