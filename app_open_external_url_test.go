package main

import "testing"

// ValidateExternalURL：web 平面 amagi:open-url 宿主桥的 Go 侧白名单。
// 正负成对：http/https 放行；其他 scheme、无 host、垃圾串拒绝。
func TestValidateExternalURL(t *testing.T) {
	valid := []string{
		"http://example.com",
		"https://example.com/path?query=1#frag",
		" https://example.com/docs/a b c ", // 首尾空白归一后合法
		"https://127.0.0.1:8680/webui/s1/",
	}
	for _, u := range valid {
		if err := ValidateExternalURL(u); err != nil {
			t.Errorf("ValidateExternalURL(%q) = %v, want nil", u, err)
		}
	}
	invalid := []string{
		"",
		"not a url",
		"javascript:alert(1)",
		"data:text/html,hi",
		"file:///etc/passwd",
		"mailto:someone@example.com",
		"ftp://example.com/pub",
		"/relative/path",
		"https://", // 无 host
	}
	for _, u := range invalid {
		if err := ValidateExternalURL(u); err == nil {
			t.Errorf("ValidateExternalURL(%q) = nil, want error", u)
		}
	}
}
