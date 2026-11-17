package platform

import "testing"

func TestWSLToUNCPath(t *testing.T) {
	cases := []struct {
		distro, linuxPath, want string
		wantErr                 bool
	}{
		{"Ubuntu-24.04", "/home/u/.claude", `\\wsl.localhost\Ubuntu-24.04\home\u\.claude`, false},
		{"Ubuntu-24.04", "/", `\\wsl.localhost\Ubuntu-24.04\`, false},
		{"Debian", "/mnt/d/WorkPace/amagi-codebox", `\\wsl.localhost\Debian\mnt\d\WorkPace\amagi-codebox`, false},
		{"Ubuntu", "/home/u/.claude/projects/-mnt-d-x-y/abc.jsonl", `\\wsl.localhost\Ubuntu\home\u\.claude\projects\-mnt-d-x-y\abc.jsonl`, false},
		// fail closed：空 distro / 相对路径 / 分隔符污染 / NUL。
		{"", "/home/u", "", true},
		{"Ubuntu", "home/u", "", true},
		{"Ubuntu", "", "", true},
		{"bad/distro", "/home/u", "", true},
		{`bad\distro`, "/home/u", "", true},
		{"Ubuntu", "/home/\x00u", "", true},
	}
	for _, tc := range cases {
		got, err := WSLToUNCPath(tc.distro, tc.linuxPath)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("WSLToUNCPath(%q, %q) 应失败，got %q", tc.distro, tc.linuxPath, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("WSLToUNCPath(%q, %q) 报错: %v", tc.distro, tc.linuxPath, err)
		}
		if got != tc.want {
			t.Fatalf("WSLToUNCPath(%q, %q) = %q, want %q", tc.distro, tc.linuxPath, got, tc.want)
		}
	}
}
