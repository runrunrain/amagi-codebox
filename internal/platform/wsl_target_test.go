package platform

// wsl_target_test.go — WSLTargetIdentity 批量身份探测的解析钉子（uid/home/
// machine-id 三行格式、异常输入 fail closed、空 distro）。

import "testing"

func TestParseWSLUserIdentity(t *testing.T) {
	cases := []struct {
		name      string
		out       string
		uid       string
		home      string
		machineID string
		ok        bool
	}{
		{"full identity", "1000\n/home/u\n6b1c1f0e8a2d4f3b9c0d5e6f7a8b9c0d\n", "1000", "/home/u", "6b1c1f0e8a2d4f3b9c0d5e6f7a8b9c0d", true},
		{"no machine-id line", "1000\n/home/u\n", "1000", "/home/u", "", true},
		{"empty machine-id line", "1000\n/home/u\n\n", "1000", "/home/u", "", true},
		{"crlf noise", "1000\r\n/home/u\r\nabc123\r\x00", "1000", "/home/u", "abc123", true},
		{"non-numeric uid", "ubuntu\n/home/u\n", "", "", "", false},
		{"relative home", "1000\nhome/u\n", "", "", "", false},
		{"missing home line", "1000\n", "", "", "", false},
		{"empty output", "", "", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseWSLUserIdentity(tc.out)
			if got.OK != tc.ok || got.UID != tc.uid || got.Home != tc.home || got.MachineID != tc.machineID {
				t.Fatalf("parseWSLUserIdentity(%q) = %+v, want uid=%q home=%q machine=%q ok=%v",
					tc.out, got, tc.uid, tc.home, tc.machineID, tc.ok)
			}
		})
	}
}

func TestWSLTargetIdentityEmptyDistro(t *testing.T) {
	orig := wslScriptRunner
	wslScriptRunner = func(distro, script string) ([]byte, error) {
		t.Fatalf("probe must not run for empty distro (got %q)", distro)
		return nil, nil
	}
	t.Cleanup(func() { wslScriptRunner = orig })
	if got := WSLTargetIdentity("   "); got.OK {
		t.Fatalf("empty distro must not probe, got %+v", got)
	}
}
