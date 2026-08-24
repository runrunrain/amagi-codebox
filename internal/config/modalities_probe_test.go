package config

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// probeServer 构造可编程的 OpenAI 兼容假端点：modelsBody 为 /models 响应，
// completionStatus/completionBody 为 /chat/completions 响应；received 记录
// 实弹探测请求体（供内容断言）。
type probeServer struct {
	modelsBody        string
	completionStatus  int
	completionBody    string
	completionCalls   int
	completionPayload string
}

func (ps *probeServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, ps.modelsBody)
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		ps.completionCalls++
		body, _ := io.ReadAll(r.Body)
		ps.completionPayload = string(body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(ps.completionStatus)
		_, _ = io.WriteString(w, ps.completionBody)
	})
	return mux
}

// 正向：/models 元数据携带 input_modalities（OpenRouter 形态）→ 零实弹定论。
func TestProbeModelModalities_ModelsAPIMetadata(t *testing.T) {
	ps := &probeServer{
		modelsBody: `{"data":[{"id":"acme-v9","architecture":{"input_modalities":["text","image","video"]}}]}`,
	}
	srv := httptest.NewServer(ps.handler())
	defer srv.Close()

	mods, source, ok := ProbeModelModalities(context.Background(), srv.Client(), srv.URL+"/v1", "sk-test", "acme-v9")
	if !ok {
		t.Fatal("models-api metadata must be conclusive")
	}
	if source != ModalityProbeSourceModelsAPI {
		t.Errorf("source = %q, want %q", source, ModalityProbeSourceModelsAPI)
	}
	if !mods.Vision || !mods.Video {
		t.Errorf("mods = %+v, want vision+video from metadata", mods)
	}
	if ps.completionCalls != 0 {
		t.Errorf("completion calls = %d, want 0 (metadata conclusive, no live probe)", ps.completionCalls)
	}
}

// 负向 → 兜底：/models 条目无模态字段 → 落入实弹图片探测；2xx 确认 vision。
func TestProbeModelModalities_LiveImageProbeAccepted(t *testing.T) {
	ps := &probeServer{
		modelsBody:       `{"data":[{"id":"acme-v9"}]}`,
		completionStatus: 200,
		completionBody:   `{"choices":[{"message":{"content":"ok"}}]}`,
	}
	srv := httptest.NewServer(ps.handler())
	defer srv.Close()

	mods, source, ok := ProbeModelModalities(context.Background(), srv.Client(), srv.URL+"/v1", "sk-test", "acme-v9")
	if !ok || source != ModalityProbeSourceImageProbe {
		t.Fatalf("source=%q ok=%v, want image-probe conclusive", source, ok)
	}
	if !mods.Vision || mods.Video {
		t.Errorf("mods = %+v, want vision-only (live probe never asserts video)", mods)
	}
	// 内容断言：实弹请求确实携带了图片输入（防假探测）。
	if !strings.Contains(ps.completionPayload, "image_url") || !strings.Contains(ps.completionPayload, "data:image/png;base64,") {
		t.Errorf("probe payload missing image content: %s", ps.completionPayload)
	}
	if !strings.Contains(ps.completionPayload, `"max_tokens":1`) {
		t.Errorf("probe payload must clamp max_tokens=1: %s", ps.completionPayload)
	}
}

// 负向定论：400 + 模态拒绝关键词 → 确认不支持图片（缓存否定结论防重复实弹）。
func TestProbeModelModalities_LiveImageProbeRejected(t *testing.T) {
	ps := &probeServer{
		modelsBody:       `{"data":[]}`,
		completionStatus: 400,
		completionBody:   `{"error":{"message":"This model does not support image input"}}`,
	}
	srv := httptest.NewServer(ps.handler())
	defer srv.Close()

	mods, source, ok := ProbeModelModalities(context.Background(), srv.Client(), srv.URL+"/v1", "", "acme-text-9")
	if !ok || source != ModalityProbeSourceImageProbe {
		t.Fatalf("rejection must be conclusive: source=%q ok=%v", source, ok)
	}
	if mods.Vision || mods.Video {
		t.Errorf("mods = %+v, want zero (definitive rejection)", mods)
	}
}

// 未决场景一律不落结论：形态拒绝（data URI 不被网关接受）/ 5xx / 鉴权失败。
func TestProbeModelModalities_Inconclusive(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"data-uri 形态拒绝", 400, `{"error":{"message":"data uri scheme not supported, pass an https image url"}}`},
		{"无关 400（参数错误）", 400, `{"error":{"message":"max_tokens must be positive"}}`},
		{"500 服务端故障", 500, `{"error":"internal"}`},
		{"401 鉴权失败", 401, `{"error":"unauthorized"}`},
		{"429 限流", 429, `{"error":"rate limited"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ps := &probeServer{
				modelsBody:       `{"data":[]}`,
				completionStatus: tc.status,
				completionBody:   tc.body,
			}
			srv := httptest.NewServer(ps.handler())
			defer srv.Close()
			_, _, ok := ProbeModelModalities(context.Background(), srv.Client(), srv.URL+"/v1", "", "acme-x")
			if ok {
				t.Errorf("status %d body %q must be inconclusive", tc.status, tc.body)
			}
		})
	}
}

// 负向：空 baseURL / 空 model / nil client 直接未决，不发请求。
func TestProbeModelModalities_InvalidInput(t *testing.T) {
	if _, _, ok := ProbeModelModalities(context.Background(), nil, "http://x", "", "m"); ok {
		t.Error("nil client must be inconclusive")
	}
	if _, _, ok := ProbeModelModalities(context.Background(), http.DefaultClient, "", "", "m"); ok {
		t.Error("empty baseURL must be inconclusive")
	}
	if _, _, ok := ProbeModelModalities(context.Background(), http.DefaultClient, "http://x", "", "  "); ok {
		t.Error("blank model must be inconclusive")
	}
}

// modalities 字符串形态（"text+image->text"）解析正/负。
func TestParseModalityMetadataArrowString(t *testing.T) {
	mods := modalitiesFromArrowString("text+image->text")
	if !mods.Vision || mods.Video {
		t.Errorf("text+image->text = %+v, want vision only", mods)
	}
	mods = modalitiesFromArrowString("text->text")
	if mods.Vision || mods.Video {
		t.Errorf("text->text = %+v, want zero", mods)
	}
	// modalitiesFromList 的负向：空数组与非数组均不识别。
	if _, ok := modalitiesFromList([]any{}); ok {
		t.Error("empty list must not be recognized")
	}
	if _, ok := modalitiesFromList("image"); ok {
		t.Error("non-array must not be recognized")
	}
}

// JSON 序列化冒烟：ModalityProbeEntry 字段名是缓存文件 API。
func TestModalityProbeEntryJSON(t *testing.T) {
	b, err := json.Marshal(ModalityProbeEntry{Vision: true, Source: ModalityProbeSourceImageProbe, ProbedAt: "2026-08-23T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, key := range []string{`"vision":true`, `"source":"image-probe"`, `"probed_at"`} {
		if !strings.Contains(s, key) {
			t.Errorf("entry json %s missing %s", s, key)
		}
	}
}
