package settings

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestSystemProxyEndpoint_DefaultsAndLoadNormalize：新装（零值）与老文件
// （无 systemProxy 键）读入后回落默认端点；Load 路径对非法值宽容回填。
func TestSystemProxyEndpoint_DefaultsAndLoadNormalize(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "settings")
	svc := NewService(dir)

	if got := svc.GetSystemProxyEndpoint(); got.Host != "127.0.0.1" || got.Port != 5800 {
		t.Fatalf("fresh endpoint = %+v, want 127.0.0.1:5800", got)
	}
}

// TestSystemProxyEndpoint_SetPersistsAcrossReload：Set → 新 Service 实例 Load
// 同目录，端点应原样恢复。
func TestSystemProxyEndpoint_SetPersistsAcrossReload(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "settings")
	svc := NewService(dir)

	if err := svc.SetSystemProxyEndpoint("127.0.0.1", 7890); err != nil {
		t.Fatalf("SetSystemProxyEndpoint: %v", err)
	}

	svc2 := NewService(dir)
	if err := svc2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := svc2.GetSystemProxyEndpoint(); got.Host != "127.0.0.1" || got.Port != 7890 {
		t.Fatalf("reloaded endpoint = %+v, want 127.0.0.1:7890", got)
	}
}

// TestSystemProxyEndpoint_SetValidates：空主机、越界端口、内嵌端口冲突都应
// 报错且不改变现有值（内存与磁盘都不动）。
func TestSystemProxyEndpoint_SetValidates(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "settings")
	svc := NewService(dir)
	if err := svc.SetSystemProxyEndpoint("127.0.0.1", 7890); err != nil {
		t.Fatalf("seed endpoint: %v", err)
	}

	cases := []struct {
		name string
		host string
		port int
		want string
	}{
		{"empty host", "  ", 7890, "host is empty"},
		{"port zero", "127.0.0.1", 0, "out of valid range"},
		{"port too large", "127.0.0.1", 65536, "out of valid range"},
		{"embedded port conflict", "127.0.0.1:1080", 7890, "conflicts"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := svc.SetSystemProxyEndpoint(tc.host, tc.port)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
			if got := svc.GetSystemProxyEndpoint(); got.Port != 7890 {
				t.Fatalf("endpoint mutated on validation failure: %+v", got)
			}
		})
	}

	// 合法规整：误粘 scheme 与内嵌端口（与显式 port 一致）应被拆净。
	if err := svc.SetSystemProxyEndpoint(" http://192.168.1.2:1080/", 1080); err != nil {
		t.Fatalf("normalize endpoint: %v", err)
	}
	if got := svc.GetSystemProxyEndpoint(); got.Host != "192.168.1.2" || got.Port != 1080 {
		t.Fatalf("normalized endpoint = %+v, want 192.168.1.2:1080", got)
	}
}
