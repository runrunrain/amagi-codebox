package config

import "testing"

// ResolvePiOmpTerminalPreset：pi/omp 启动链专用 resolver 的回退语义——
// openai 公共桶（pi/omp 原生桶）优先，miss 后仅当 anthropic 桶预设带
// HarnessSync 标记（opt-in）才返回；unmarked anthropic 与两桶均 miss
// 返回 ("", nil, nil)（与 ResolveTerminalPreset 的 not-found 形态一致）。

func TestResolvePiOmpTerminalPreset_OpenAIBucketWinsOnKeyCollision(t *testing.T) {
	svc := newTestConfigService(t)

	// 两桶同名 key：openai 桶条目必须胜出。
	if err := svc.SaveTerminalPreset("openai", "glm/dual", TerminalPreset{
		Name: "OpenAI Side", Provider: "glm", Model: "glm-openai-model",
	}); err != nil {
		t.Fatalf("SaveTerminalPreset(openai): %v", err)
	}
	if err := svc.SaveTerminalPreset("anthropic", "glm/dual", TerminalPreset{
		Name: "Anthropic Side", Provider: "glm", Model: "glm-anthropic-model", HarnessSync: true,
	}); err != nil {
		t.Fatalf("SaveTerminalPreset(anthropic): %v", err)
	}

	prov, tp, err := svc.ResolvePiOmpTerminalPreset("glm/dual")
	if err != nil {
		t.Fatalf("ResolvePiOmpTerminalPreset: %v", err)
	}
	if prov != "glm" || tp == nil {
		t.Fatalf("resolve = (%q, %v), want (glm, preset)", prov, tp)
	}
	if tp.Model != "glm-openai-model" {
		t.Fatalf("collision resolved to %q, want openai bucket entry glm-openai-model", tp.Model)
	}
}

func TestResolvePiOmpTerminalPreset_MarkedAnthropicOptIn(t *testing.T) {
	svc := newTestConfigService(t)

	if err := svc.SaveTerminalPreset("claude_code", "glm/marked", TerminalPreset{
		Name: "Marked", Provider: "glm", Model: "glm-5.3", HarnessSync: true,
		Parameters: Parameters{ReasoningEffort: "max"},
	}); err != nil {
		t.Fatalf("SaveTerminalPreset(anthropic): %v", err)
	}

	prov, tp, err := svc.ResolvePiOmpTerminalPreset("glm/marked")
	if err != nil {
		t.Fatalf("ResolvePiOmpTerminalPreset: %v", err)
	}
	if prov != "glm" || tp == nil {
		t.Fatalf("resolve = (%q, %v), want (glm, preset)", prov, tp)
	}
	if tp.Model != "glm-5.3" {
		t.Fatalf("model = %q, want glm-5.3", tp.Model)
	}
	if tp.Parameters.ReasoningEffort != "max" {
		t.Fatalf("parameters not carried: %+v", tp.Parameters)
	}
}

func TestResolvePiOmpTerminalPreset_UnmarkedAnthropicReturnsNotFound(t *testing.T) {
	svc := newTestConfigService(t)

	if err := svc.SaveTerminalPreset("anthropic", "glm/plain", TerminalPreset{
		Name: "Plain", Provider: "glm", Model: "glm-4",
	}); err != nil {
		t.Fatalf("SaveTerminalPreset(anthropic): %v", err)
	}

	prov, tp, err := svc.ResolvePiOmpTerminalPreset("glm/plain")
	if err != nil {
		t.Fatalf("ResolvePiOmpTerminalPreset: %v", err)
	}
	if prov != "" || tp != nil {
		t.Fatalf("unmarked anthropic resolved to (%q, %v), want empty not-found result", prov, tp)
	}
}

func TestResolvePiOmpTerminalPreset_BothBucketsMiss(t *testing.T) {
	svc := newTestConfigService(t)

	if err := svc.SaveTerminalPreset("openai", "openai/only", TerminalPreset{
		Name: "OpenAI Only", Provider: "openai", Model: "gpt-x",
	}); err != nil {
		t.Fatalf("SaveTerminalPreset(openai): %v", err)
	}

	prov, tp, err := svc.ResolvePiOmpTerminalPreset("nonexistent/key")
	if err != nil {
		t.Fatalf("ResolvePiOmpTerminalPreset: %v", err)
	}
	if prov != "" || tp != nil {
		t.Fatalf("resolve = (%q, %v), want empty not-found result", prov, tp)
	}
}
