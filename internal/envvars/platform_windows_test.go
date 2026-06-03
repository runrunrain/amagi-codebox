//go:build windows

package envvars

import "testing"

func TestIsKeyNotFoundError(t *testing.T) {
	tests := []struct {
		output string
		want   bool
	}{
		// 英文：Windows 典型 not found 输出
		{"ERROR: The system was unable to find the specified registry key or value.", true},
		{"The system was unable to find the specified registry key or value.", true},
		{"unable to find the specified registry key", true},
		// 中文：Windows 中文版典型输出
		{"系统找不到指定的注册表项或值。", true},
		{"错误: 系统找不到指定的注册表项或值。", true},
		// 非 not found 的错误
		{"Access is denied.", false},
		{"", false},
		{"some other error", false},
	}
	for _, tt := range tests {
		got := isKeyNotFoundError(tt.output)
		if got != tt.want {
			t.Errorf("isKeyNotFoundError(%q) = %v, want %v", tt.output, got, tt.want)
		}
	}
}
