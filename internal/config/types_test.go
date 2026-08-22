package config

import "testing"

// TestNormalizeOpenAIBaseURL 表驱动测试 OpenAI base URL 归一化。
//
// 归一化语义（审核 Major-3 修正后）：用 net/url 解析，仅处理 URL.Path 的
// /chat/completions 后缀与尾斜杠，保留 query/fragment；对 hostless、解析失败、
// scheme-only、非 URL 字符串保守返回 TrimSpace 后原值，不做破坏性裁剪。
func TestNormalizeOpenAIBaseURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		// --- 基本剥离 ---
		{
			name: "plain base url unchanged",
			raw:  "https://api.example.com/v1",
			want: "https://api.example.com/v1",
		},
		{
			name: "strip chat/completions suffix",
			raw:  "https://api.example.com/v1/chat/completions",
			want: "https://api.example.com/v1",
		},
		{
			name: "strip suffix with trailing slash",
			raw:  "https://api.example.com/v1/chat/completions/",
			want: "https://api.example.com/v1",
		},
		{
			name: "real target endpoint opencode zen",
			raw:  "https://opencode.ai/zen/go/v1/chat/completions",
			want: "https://opencode.ai/zen/go/v1",
		},
		{
			name: "trailing slash on plain base stripped",
			raw:  "https://api.example.com/v1/",
			want: "https://api.example.com/v1",
		},
		{
			name: "multiple trailing slashes after base stripped",
			raw:  "https://api.example.com/v1///",
			want: "https://api.example.com/v1",
		},
		{
			name: "duplicate suffix fully stripped by loop (idempotent)",
			raw:  "https://api.example.com/v1/chat/completions/chat/completions",
			want: "https://api.example.com/v1",
		},
		{
			name: "suffix stripped with port",
			raw:  "https://host:8080/v1/chat/completions",
			want: "https://host:8080/v1",
		},
		{
			name: "host root with single slash collapses to bare host",
			raw:  "https://api.example.com/",
			want: "https://api.example.com",
		},
		{
			name: "bare host unchanged",
			raw:  "https://api.example.com",
			want: "https://api.example.com",
		},

		// --- query / fragment 完整保留（审核 Major-3 反例）---
		{
			name: "query preserved when path has no suffix",
			raw:  "https://host/v1?redirect=/",
			want: "https://host/v1?redirect=/",
		},
		{
			name: "suffix stripped but query with path-like value preserved",
			raw:  "https://host/v1/chat/completions?target=/chat/completions/",
			want: "https://host/v1?target=/chat/completions/",
		},
		{
			name: "fragment preserved when stripping suffix",
			raw:  "https://host/v1/chat/completions#section",
			want: "https://host/v1#section",
		},
		{
			name: "query and fragment both preserved",
			raw:  "https://host/v1/chat/completions?x=1#frag",
			want: "https://host/v1?x=1#frag",
		},

		// --- 大小写 / 中段后缀 ---
		{
			name: "uppercase suffix NOT stripped (case-sensitive, preserved as-is)",
			raw:  "https://api.example.com/v1/Chat/Completions",
			want: "https://api.example.com/v1/Chat/Completions",
		},
		{
			name: "suffix in middle not stripped (only trailing form handled)",
			raw:  "https://gateway.example/chat/completions/extra",
			want: "https://gateway.example/chat/completions/extra",
		},

		// --- 空 / 空白 ---
		{
			name: "empty string returns empty",
			raw:  "",
			want: "",
		},
		{
			name: "whitespace only returns empty",
			raw:  "   ",
			want: "",
		},
		{
			name: "leading and trailing whitespace trimmed then suffix stripped",
			raw:  "  https://api.example.com/v1/chat/completions  ",
			want: "https://api.example.com/v1",
		},

		// --- hostless / scheme-only / 非 URL 保守返回原值（审核 Major-3 反例）---
		{
			name: "only slashes conservative (hostless, preserved as-is)",
			raw:  "///",
			want: "///",
		},
		{
			name: "hostless bare suffix conservative (NOT collapsed to empty)",
			raw:  "/chat/completions",
			want: "/chat/completions",
		},
		{
			name: "hostless relative path with suffix conservative",
			raw:  "/v1/chat/completions",
			want: "/v1/chat/completions",
		},
		{
			name: "scheme only conservative (NOT corrupted to scheme colon)",
			raw:  "https://",
			want: "https://",
		},
		{
			name: "non url string conservative",
			raw:  "not-a-url",
			want: "not-a-url",
		},
		{
			name: "no scheme hostless conservative",
			raw:  "api.example.com/v1/chat/completions",
			want: "api.example.com/v1/chat/completions",
		},

		// --- 转义路径保守处理（增量复审新 Major：基于 EscapedPath 不破坏 %2F）---
		// 转义斜杠 %2F 不是真实分隔符，不得被解码后误当 /chat/completions 后缀裁剪。
		{
			name: "escaped slash uppercase suffix NOT stripped (single segment preserved)",
			raw:  "https://host/v1%2Fchat%2Fcompletions",
			want: "https://host/v1%2Fchat%2Fcompletions",
		},
		{
			name: "escaped slash lowercase suffix NOT stripped",
			raw:  "https://host/v1%2fchat%2fcompletions",
			want: "https://host/v1%2fchat%2fcompletions",
		},
		{
			name: "escaped suffix with trailing slash strips only the literal slash (escapes kept)",
			raw:  "https://host/v1%2Fchat%2Fcompletions/",
			want: "https://host/v1%2Fchat%2Fcompletions",
		},
		{
			name: "literal suffix stripped even when followed by escaped tilde (escapes kept after strip)",
			raw:  "https://host/v1/chat/completions%7E",
			want: "https://host/v1/chat/completions%7E",
		},
		{
			name: "mixed real and escaped segments strip real suffix keep escapes",
			raw:  "https://host/v1%2Fseg/chat/completions",
			want: "https://host/v1%2Fseg",
		},

		// --- /responses 后缀剥离（与 /chat/completions 同一循环/语义，wire_api 协议选择）---
		{
			name: "strip responses suffix",
			raw:  "https://api.example.com/v1/responses",
			want: "https://api.example.com/v1",
		},
		{
			name: "strip responses suffix with trailing slash",
			raw:  "https://api.example.com/v1/responses/",
			want: "https://api.example.com/v1",
		},
		{
			name: "duplicate responses suffix fully stripped by loop (idempotent)",
			raw:  "https://api.example.com/v1/responses/responses",
			want: "https://api.example.com/v1",
		},
		{
			name: "mixed suffixes stripped by loop",
			raw:  "https://api.example.com/v1/chat/completions/responses",
			want: "https://api.example.com/v1",
		},
		{
			name: "responses suffix stripped but query with path-like value preserved",
			raw:  "https://host/v1/responses?target=/responses/",
			want: "https://host/v1?target=/responses/",
		},
		{
			name: "responses suffix stripped with fragment preserved",
			raw:  "https://host/v1/responses#frag",
			want: "https://host/v1#frag",
		},
		{
			name: "uppercase Responses NOT stripped (case-sensitive, preserved as-is)",
			raw:  "https://api.example.com/v1/Responses",
			want: "https://api.example.com/v1/Responses",
		},
		{
			name: "responses in middle not stripped (only trailing form handled)",
			raw:  "https://gateway.example/responses/extra",
			want: "https://gateway.example/responses/extra",
		},
		{
			name: "hostless responses suffix conservative (NOT collapsed)",
			raw:  "/v1/responses",
			want: "/v1/responses",
		},
		{
			name: "escaped slash responses suffix NOT stripped (escapes kept)",
			raw:  "https://host/v1%2Fresponses",
			want: "https://host/v1%2Fresponses",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeOpenAIBaseURL(tt.raw)
			if got != tt.want {
				t.Fatalf("NormalizeOpenAIBaseURL(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

// TestNormalizeOpenAIBaseURL_Idempotent 验证对已归一化输出再次归一化结果不变。
func TestNormalizeOpenAIBaseURL_Idempotent(t *testing.T) {
	raw := "https://opencode.ai/zen/go/v1/chat/completions"
	first := NormalizeOpenAIBaseURL(raw)
	second := NormalizeOpenAIBaseURL(first)
	if first != second {
		t.Fatalf("not idempotent: first=%q second=%q", first, second)
	}
	if first != "https://opencode.ai/zen/go/v1" {
		t.Fatalf("first normalization = %q, want https://opencode.ai/zen/go/v1", first)
	}
}

// TestNormalizeOpenAIBaseURL_ResponsesSuffixIdempotent 验证 /responses 剥离幂等。
func TestNormalizeOpenAIBaseURL_ResponsesSuffixIdempotent(t *testing.T) {
	raw := "https://api.openai.com/v1/responses"
	first := NormalizeOpenAIBaseURL(raw)
	second := NormalizeOpenAIBaseURL(first)
	if first != second {
		t.Fatalf("not idempotent: first=%q second=%q", first, second)
	}
	if first != "https://api.openai.com/v1" {
		t.Fatalf("first normalization = %q, want https://api.openai.com/v1", first)
	}
}

// TestOpenAIFormatEffectiveWireAPI 表驱动测试 wire_api 归一化：
// trim+小写后仅接受 "chat"/"responses"，其余（未设置、空白、非法值）归一化为 ""。
// nil 接收者安全（OpenAI 子块缺失的 legacy provider）。
func TestOpenAIFormatEffectiveWireAPI(t *testing.T) {
	tests := []struct {
		name   string
		format *OpenAIFormat
		want   string
	}{
		{"nil receiver returns empty", nil, ""},
		{"unset returns empty", &OpenAIFormat{}, ""},
		{"empty string returns empty", &OpenAIFormat{WireAPI: ""}, ""},
		{"whitespace only returns empty", &OpenAIFormat{WireAPI: "   "}, ""},
		{"chat passthrough", &OpenAIFormat{WireAPI: "chat"}, "chat"},
		{"responses passthrough", &OpenAIFormat{WireAPI: "responses"}, "responses"},
		{"uppercase chat normalized to lowercase", &OpenAIFormat{WireAPI: "CHAT"}, "chat"},
		{"mixed case responses normalized", &OpenAIFormat{WireAPI: "Responses"}, "responses"},
		{"surrounding whitespace trimmed", &OpenAIFormat{WireAPI: "  chat  "}, "chat"},
		{"tab and newline trimmed", &OpenAIFormat{WireAPI: "\tchat\n"}, "chat"},
		{"illegal value normalized to empty", &OpenAIFormat{WireAPI: "websocket"}, ""},
		{"protocol path value rejected", &OpenAIFormat{WireAPI: "/chat/completions"}, ""},
		{"pi api value rejected", &OpenAIFormat{WireAPI: "openai-responses"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.format.EffectiveWireAPI(); got != tt.want {
				t.Fatalf("EffectiveWireAPI() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestNormalizeOpenAIBaseURL_EscapedPathIdempotent 验证转义路径再次归一化结果不变
// （增量复审新 Major：基于 EscapedPath 的剥离必须保持转义无损）。
func TestNormalizeOpenAIBaseURL_EscapedPathIdempotent(t *testing.T) {
	cases := []string{
		"https://host/v1%2Fchat%2Fcompletions",
		"https://host/v1%2fchat%2fcompletions",
		"https://host/v1%2Fchat%2Fcompletions/",
		"https://host/v1%2Fseg/chat/completions",
	}
	for _, raw := range cases {
		first := NormalizeOpenAIBaseURL(raw)
		second := NormalizeOpenAIBaseURL(first)
		if first != second {
			t.Fatalf("escaped path not idempotent for %q: first=%q second=%q", raw, first, second)
		}
	}
}

// TestIsOfficialOpenAIBaseURL 验证官方 OpenAI 判定使用精确 host 比较，
// 不被欺骗性 host/path 误判（审核 Major-4）。
func TestIsOfficialOpenAIBaseURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{"official https with path", "https://api.openai.com/v1", true},
		{"official https no path", "https://api.openai.com", true},
		{"official uppercase host", "https://API.OpenAI.com/v1", true},
		{"official no scheme", "api.openai.com/v1", true},
		{"official with port", "https://api.openai.com:443/v1", true},
		{"official FQDN trailing dot", "https://api.openai.com./v1", true},
		{"official FQDN trailing dot no scheme", "api.openai.com./v1", true},
		{"official with userinfo", "https://user:pass@api.openai.com/v1", true},
		{"deceptive trailing subdomain", "https://api.openai.com.evil.example/v1", false},
		{"deceptive path contains host", "https://gateway.example/proxy/api.openai.com/v1", false},
		{"deceptive path equals host", "https://evil.example/api.openai.com", false},
		{"deceptive userinfo hosts official domain", "https://api.openai.com@evil.example/v1", false},
		{"third party openai compatible", "https://opencode.ai/zen/go/v1", false},
		{"empty", "", false},
		{"hostless path", "/v1/chat/completions", false},
		{"scheme only", "https://", false},
		{"non url string", "not-a-url", false},
		{"whitespace only", "   ", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsOfficialOpenAIBaseURL(tt.url); got != tt.want {
				t.Fatalf("IsOfficialOpenAIBaseURL(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

// TestEffectiveBaseURL_OpenAINormalization 验证 openai 格式下 base URL 被归一化。
func TestEffectiveBaseURL_OpenAINormalization(t *testing.T) {
	// 新字段 OpenAI.BaseURL 带后缀 -> 归一化剥离
	p := Provider{
		OpenAI: &OpenAIFormat{
			Enabled: true,
			BaseURL: "https://opencode.ai/zen/go/v1/chat/completions",
		},
	}
	got := p.EffectiveBaseURL("openai")
	if got != "https://opencode.ai/zen/go/v1" {
		t.Fatalf("openai EffectiveBaseURL = %q, want https://opencode.ai/zen/go/v1 (normalized)", got)
	}

	// 带 trailing slash 同样归一化
	p2 := Provider{
		OpenAI: &OpenAIFormat{
			Enabled: true,
			BaseURL: "https://api.example.com/v1/chat/completions/",
		},
	}
	if got := p2.EffectiveBaseURL("openai"); got != "https://api.example.com/v1" {
		t.Fatalf("openai EffectiveBaseURL with trailing slash = %q, want https://api.example.com/v1", got)
	}
}

// TestEffectiveBaseURL_AnthropicNotNormalized 验证 anthropic 格式下同样的
// 后缀字符串原样透传，不被归一化。
func TestEffectiveBaseURL_AnthropicNotNormalized(t *testing.T) {
	const urlWithSuffix = "https://example.com/v1/chat/completions"
	p := Provider{
		Anthropic: &AnthropicFormat{
			Enabled: true,
			BaseURL: urlWithSuffix,
		},
	}
	got := p.EffectiveBaseURL("anthropic")
	if got != urlWithSuffix {
		t.Fatalf("anthropic EffectiveBaseURL = %q, want %q (must NOT be normalized)", got, urlWithSuffix)
	}
}

// TestEffectiveBaseURL_EmptyFormatOpenAINormalized 验证空 format 经
// PreferredFormat() 推导为 openai 时同样走归一化（opencode 旧轨道用空 format 调用）。
func TestEffectiveBaseURL_EmptyFormatOpenAINormalized(t *testing.T) {
	p := Provider{
		OpenAI: &OpenAIFormat{
			Enabled: true,
			BaseURL: "https://opencode.ai/zen/go/v1/chat/completions",
		},
	}
	if p.PreferredFormat() != "openai" {
		t.Fatalf("PreferredFormat = %q, want openai", p.PreferredFormat())
	}
	got := p.EffectiveBaseURL("")
	if got != "https://opencode.ai/zen/go/v1" {
		t.Fatalf("empty-format openai EffectiveBaseURL = %q, want https://opencode.ai/zen/go/v1 (normalized)", got)
	}
}

// TestEffectiveBaseURL_EmptyFormatAnthropicNotNormalized 验证空 format 推导为
// anthropic 时不归一化。
func TestEffectiveBaseURL_EmptyFormatAnthropicNotNormalized(t *testing.T) {
	const urlWithSuffix = "https://example.com/v1/chat/completions"
	p := Provider{
		Anthropic: &AnthropicFormat{
			Enabled: true,
			BaseURL: urlWithSuffix,
		},
	}
	if p.PreferredFormat() != "anthropic" {
		t.Fatalf("PreferredFormat = %q, want anthropic", p.PreferredFormat())
	}
	if got := p.EffectiveBaseURL(""); got != urlWithSuffix {
		t.Fatalf("empty-format anthropic EffectiveBaseURL = %q, want %q (not normalized)", got, urlWithSuffix)
	}
}

// TestEffectiveBaseURL_LegacyFallbackOpenAINormalized 验证 openai 格式下
// 当新字段为空、回退到 legacy p.BaseURL 时同样归一化。
func TestEffectiveBaseURL_LegacyFallbackOpenAINormalized(t *testing.T) {
	p := Provider{
		Type:    "openai",
		BaseURL: "https://legacy.example.com/v1/chat/completions",
		AuthKey: "OPENAI_API_KEY",
	}
	if !p.IsOpenAICompatible() {
		t.Fatal("provider should be OpenAI compatible")
	}
	got := p.EffectiveBaseURL("openai")
	if got != "https://legacy.example.com/v1" {
		t.Fatalf("legacy fallback openai EffectiveBaseURL = %q, want https://legacy.example.com/v1 (normalized)", got)
	}
}

// TestEffectiveBaseURL_LegacyFallbackAnthropicNotNormalized 验证 anthropic
// 格式下回退 legacy p.BaseURL 不归一化。
func TestEffectiveBaseURL_LegacyFallbackAnthropicNotNormalized(t *testing.T) {
	const urlWithSuffix = "https://legacy.example.com/v1/chat/completions"
	p := Provider{
		BaseURL: urlWithSuffix,
		AuthKey: "ANTHROPIC_API_KEY",
	}
	got := p.EffectiveBaseURL("anthropic")
	if got != urlWithSuffix {
		t.Fatalf("legacy fallback anthropic EffectiveBaseURL = %q, want %q (not normalized)", got, urlWithSuffix)
	}
}

// TestEffectiveBaseURLRaw_NotNormalized 验证 EffectiveBaseURLRaw 返回原始值
// （未经归一化），供存储/导出路径使用（审核 Major-2）。
func TestEffectiveBaseURLRaw_NotNormalized(t *testing.T) {
	const rawWithSuffix = "https://opencode.ai/zen/go/v1/chat/completions"
	p := Provider{
		OpenAI: &OpenAIFormat{Enabled: true, BaseURL: rawWithSuffix},
	}
	// 显式 openai format
	if got := p.EffectiveBaseURLRaw("openai"); got != rawWithSuffix {
		t.Fatalf("EffectiveBaseURLRaw(openai) = %q, want %q (raw, not normalized)", got, rawWithSuffix)
	}
	// 空 format 推导为 openai 同样返回原始值
	if got := p.EffectiveBaseURLRaw(""); got != rawWithSuffix {
		t.Fatalf("EffectiveBaseURLRaw('') = %q, want raw (not normalized)", got)
	}
	// 对照：EffectiveBaseURL 仍归一化
	if got := p.EffectiveBaseURL("openai"); got != "https://opencode.ai/zen/go/v1" {
		t.Fatalf("EffectiveBaseURL(openai) = %q, want normalized https://opencode.ai/zen/go/v1", got)
	}
}

// TestEffectiveBaseURLRaw_LegacyFallback 验证 EffectiveBaseURLRaw 在 legacy
// 字段回退时也返回原始值。
func TestEffectiveBaseURLRaw_LegacyFallback(t *testing.T) {
	const rawWithSuffix = "https://legacy.example.com/v1/chat/completions"
	p := Provider{
		Type:    "openai",
		BaseURL: rawWithSuffix,
		AuthKey: "OPENAI_API_KEY",
	}
	if got := p.EffectiveBaseURLRaw("openai"); got != rawWithSuffix {
		t.Fatalf("legacy fallback EffectiveBaseURLRaw = %q, want %q (raw)", got, rawWithSuffix)
	}
}

// TestSyncLegacyFields_PreservesRawBaseURL 验证 SyncLegacyFields 镜像 legacy
// BaseURL 时使用原始值，不被运行时归一化污染（审核 Major-2：存储原样）。
func TestSyncLegacyFields_PreservesRawBaseURL(t *testing.T) {
	const rawWithSuffix = "https://opencode.ai/zen/go/v1/chat/completions"
	p := Provider{
		OpenAI: &OpenAIFormat{
			Enabled: true,
			BaseURL: rawWithSuffix,
		},
	}
	synced := p.SyncLegacyFields()
	// legacy BaseURL 必须保留用户原始输入值
	if synced.BaseURL != rawWithSuffix {
		t.Fatalf("SyncLegacyFields BaseURL = %q, want %q (raw, not normalized)", synced.BaseURL, rawWithSuffix)
	}
	// 嵌套 OpenAI.BaseURL 也保留原始值（不应被改动）
	if synced.OpenAI == nil || synced.OpenAI.BaseURL != rawWithSuffix {
		t.Fatalf("nested OpenAI.BaseURL = %v, want %q (raw)", synced.OpenAI, rawWithSuffix)
	}
	// 运行时 EffectiveBaseURL 仍归一化（归一化只发生在消费端）
	if got := synced.EffectiveBaseURL("openai"); got != "https://opencode.ai/zen/go/v1" {
		t.Fatalf("runtime EffectiveBaseURL = %q, want normalized https://opencode.ai/zen/go/v1", got)
	}
}

// TestBuildExportProvider_PreservesRawBaseURL 验证导出的 legacy base_url 与
// 嵌套 openai.base_url 都保留用户原始输入值（审核 Major-2：导出原样）。
func TestBuildExportProvider_PreservesRawBaseURL(t *testing.T) {
	const rawWithSuffix = "https://opencode.ai/zen/go/v1/chat/completions"
	provider := Provider{
		OpenAI: &OpenAIFormat{
			Enabled: true,
			BaseURL: rawWithSuffix,
		},
		DefaultModel: "deepseek-v4-flash",
	}
	ep := BuildExportProvider(provider, "sk-test")
	// 导出的 legacy base_url 必须保留原始值
	if ep.BaseURL != rawWithSuffix {
		t.Fatalf("export BaseURL = %q, want %q (raw, not normalized)", ep.BaseURL, rawWithSuffix)
	}
	// 嵌套 openai.base_url 同样保留原始值
	if ep.OpenAI == nil || ep.OpenAI.BaseURL != rawWithSuffix {
		t.Fatalf("export openai.base_url = %v, want %q (raw)", ep.OpenAI, rawWithSuffix)
	}
	// 对照：运行时 EffectiveBaseURL 仍归一化
	if got := provider.EffectiveBaseURL("openai"); got != "https://opencode.ai/zen/go/v1" {
		t.Fatalf("runtime EffectiveBaseURL = %q, want normalized https://opencode.ai/zen/go/v1", got)
	}
}
