//go:build windows

package platform

import "testing"

// TestParseRegProxyOverride 覆盖注册表 ProxyOverride 例外列表解析：
// 分号分隔、<local> 控制标记丢弃、缺失返回 nil。
func TestParseRegProxyOverride(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "semicolon list with local token",
			in: "\r\nHKEY_CURRENT_USER\\Software\\Microsoft\\Windows\\CurrentVersion\\Internet Settings\r\n" +
				"    ProxyOverride    REG_SZ    localhost;127.*;10.*;192.168.*;<local>\r\n",
			want: []string{"localhost", "127.*", "10.*", "192.168.*"},
		},
		{
			name: "single entry",
			in:   "    ProxyOverride    REG_SZ    *.vx.net\r\n",
			want: []string{"*.vx.net"},
		},
		{
			name: "missing value",
			in:   "    ProxyServer    REG_SZ    127.0.0.1:5800\r\n",
			want: nil,
		},
	}
	for _, c := range cases {
		got := parseRegProxyOverride(c.in)
		if len(got) != len(c.want) {
			t.Errorf("%s: parseRegProxyOverride = %v, want %v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: parseRegProxyOverride[%d] = %q, want %q", c.name, i, got[i], c.want[i])
			}
		}
	}
}
