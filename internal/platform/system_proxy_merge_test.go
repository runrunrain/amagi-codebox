package platform

import "testing"

// TestMergeNoProxy 覆盖 NO_PROXY 合并：回环打底、例外去重、空白清洗、
// 例外列表为空/含空串时的行为。
func TestMergeNoProxy(t *testing.T) {
	cases := []struct {
		name       string
		exceptions []string
		want       string
	}{
		{
			name:       "base only",
			exceptions: nil,
			want:       "localhost,127.0.0.1,::1",
		},
		{
			name:       "append exceptions",
			exceptions: []string{"*.vx.net", "router.ai.vx.net"},
			want:       "localhost,127.0.0.1,::1,*.vx.net,router.ai.vx.net",
		},
		{
			name:       "dedupe against base and self",
			exceptions: []string{"localhost", "*.vx.net", "*.vx.net"},
			want:       "localhost,127.0.0.1,::1,*.vx.net",
		},
		{
			name:       "trim and drop empties",
			exceptions: []string{"  *.vx.net  ", "", "  "},
			want:       "localhost,127.0.0.1,::1,*.vx.net",
		},
	}
	for _, c := range cases {
		if got := mergeNoProxy(c.exceptions); got != c.want {
			t.Errorf("%s: mergeNoProxy = %q, want %q", c.name, got, c.want)
		}
	}
}
