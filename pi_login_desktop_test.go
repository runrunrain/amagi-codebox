package main

import (
	"strings"
	"testing"
)

// 普通 Pi 桌面入口的登录解析正常返回 nil，不能解引用或劫持普通选择。
// 使用明确非法模式停在已有校验处，无需启动真实 CLI/访问任何认证文件。
func TestPiLoginDesktopOrdinarySelectionReachesModeValidation(t *testing.T) {
	isolatedHome(t)
	app, _ := newTestAppWithConfigDir(t)
	for _, tc := range []struct {
		provider string
		model    string
	}{
		{"", ""},
		{"api-provider", "api-model"},
		{"pi-login", "pi-login/Zm9v"},
	} {
		t.Run(tc.provider+"/"+tc.model, func(t *testing.T) {
			_, err := app.LaunchPiSession(tc.model, tc.provider, "invalid-test-mode", "", "")
			if err == nil || !strings.Contains(err.Error(), "unsupported launch mode: invalid-test-mode") {
				t.Fatalf("ordinary selection did not reach mode validation: %v", err)
			}
		})
	}
}
