package main

import (
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/settings"
)

// TestGetSystemProxyStatusSettingsNotInitialized：Settings 未初始化时状态安全
// 返回零值（不 panic），configured 字段为空。
func TestGetSystemProxyStatusSettingsNotInitialized(t *testing.T) {
	app := &App{}
	status := app.GetSystemProxyStatus()
	if status.ConfiguredHost != "" || status.ConfiguredPort != 0 {
		t.Fatalf("configured endpoint = %+v, want zero values when Settings is nil", status)
	}
}

// TestGetSystemProxyStatusReportsPersistedEndpoint：带默认设置时快照应带出
// 持久化端点（127.0.0.1:5800）。enabled/host 等实时字段随设备而变，不参与断言。
func TestGetSystemProxyStatusReportsPersistedEndpoint(t *testing.T) {
	svc := settings.NewService(t.TempDir())
	app := &App{Settings: svc}

	status := app.GetSystemProxyStatus()
	if status.ConfiguredHost != "127.0.0.1" || status.ConfiguredPort != 5800 {
		t.Fatalf("configured endpoint = %s:%d, want 127.0.0.1:5800", status.ConfiguredHost, status.ConfiguredPort)
	}
}

// TestSetSystemProxyEnabledSettingsNotInitialized：Settings 未初始化时直接报错，
// 不得触达平台写入路径。
func TestSetSystemProxyEnabledSettingsNotInitialized(t *testing.T) {
	app := &App{}
	_, err := app.SetSystemProxyEnabled(false)
	if err == nil || !strings.Contains(err.Error(), "settings service is not initialized") {
		t.Fatalf("err = %v, want settings service is not initialized", err)
	}
}

// TestSystemProxyStatusMatchesSettingsServiceType：App 层快照的 configured 字段
// 与 settings.Service 返回值同源（防两端点字段漂移）。
func TestSystemProxyStatusMatchesSettingsServiceType(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "settings")
	svc := settings.NewService(dir)
	if err := svc.SetSystemProxyEndpoint("192.168.1.9", 1080); err != nil {
		t.Fatalf("seed endpoint: %v", err)
	}
	app := &App{Settings: svc}
	status := app.GetSystemProxyStatus()
	if status.ConfiguredHost != "192.168.1.9" || status.ConfiguredPort != 1080 {
		t.Fatalf("configured endpoint = %s:%d, want 192.168.1.9:1080", status.ConfiguredHost, status.ConfiguredPort)
	}
}
