package proxy

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ProxyService 代理注入服务：拦截 Anthropic API 请求，
// 根据关键字匹配规则向最后一条用户消息注入 Prompt。
type ProxyService struct {
	rules             []InjectionRule
	configDir         string // 配置文件目录，用于自动保存规则
	server            *http.Server
	listener          net.Listener
	backendURL        string
	backendURLHistory []string // 后端URL历史记录
	port              int
	running           bool
	logs              []InjectionLog
	mu                sync.RWMutex
	logsMu            sync.Mutex

	// === 新增：usage 钩子（设计 9.1） ===
	// onUsage 是注入的 usage sink；nil 时跳过（解耦，proxy 包不 import usage 包）。
	onUsage func(UsageEvent)
	// currentSession 是 LaunchSession 注入的当前会话上下文（用于把 usage 关联到 amagi session）。
	currentSession *currentSessionCtx
	sessMu         sync.RWMutex
}

// currentSessionCtx 携带当前活跃会话的关联信息。
type currentSessionCtx struct {
	SessionID string
	Provider  string
	Preset    string
	AppType   string // claudecode / codex / opencode
}

// SetUsageSink 由 app.go 在 Startup 注入。
//
// 接受一个 func(UsageEvent) 回调；nil 时禁用 usage 钩子。
// 设计要求 proxy 包不 import usage 包，故此回调由 app.go 适配
// usage.Service.Record(UsageEvent) → 接受 proxy.UsageEvent 的闭包实现。
func (s *ProxyService) SetUsageSink(fn func(UsageEvent)) {
	s.mu.Lock()
	s.onUsage = fn
	s.mu.Unlock()
}

// SetCurrentSession 由 LaunchSession 在创建会话后注入（仅 useProxy 时）。
func (s *ProxyService) SetCurrentSession(sessionID, provider, preset, appType string) {
	s.sessMu.Lock()
	s.currentSession = &currentSessionCtx{
		SessionID: sessionID,
		Provider:  provider,
		Preset:    preset,
		AppType:   appType,
	}
	s.sessMu.Unlock()
}

// ClearCurrentSession 在会话停止时调用（第一期可选，新会话启动会覆盖）。
func (s *ProxyService) ClearCurrentSession() {
	s.sessMu.Lock()
	s.currentSession = nil
	s.sessMu.Unlock()
}

func NewProxyService() *ProxyService {
	return &ProxyService{
		rules:             []InjectionRule{},
		logs:              []InjectionLog{},
		backendURLHistory: []string{},
	}
}

// ========== 规则 CRUD ==========

func (s *ProxyService) GetRules() []InjectionRule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]InjectionRule, len(s.rules))
	copy(out, s.rules)
	return out
}

func (s *ProxyService) SetRules(rules []InjectionRule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rules = make([]InjectionRule, len(rules))
	copy(s.rules, rules)
}

func (s *ProxyService) AddRule(rule InjectionRule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rule.ID == "" {
		rule.ID = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	s.rules = append(s.rules, rule)

	// 自动保存规则到文件
	if s.configDir != "" {
		if err := s.saveRulesUnlocked(); err != nil {
			// 保存失败不影响添加操作，仅记录错误
			fmt.Printf("[amagi-codebox proxy] warning: failed to save rules after add: %v\n", err)
		}
	}

	return nil
}

func (s *ProxyService) UpdateRule(rule InjectionRule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.rules {
		if r.ID == rule.ID {
			s.rules[i] = rule

			// 自动保存规则到文件
			if s.configDir != "" {
				if err := s.saveRulesUnlocked(); err != nil {
					// 保存失败不影响更新操作，仅记录错误
					fmt.Printf("[amagi-codebox proxy] warning: failed to save rules after update: %v\n", err)
				}
			}

			return nil
		}
	}
	return fmt.Errorf("rule not found: %s", rule.ID)
}

func (s *ProxyService) DeleteRule(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.rules {
		if r.ID == id {
			s.rules = append(s.rules[:i], s.rules[i+1:]...)

			// 自动保存规则到文件
			if s.configDir != "" {
				if err := s.saveRulesUnlocked(); err != nil {
					// 保存失败不影响删除操作，仅记录错误
					fmt.Printf("[amagi-codebox proxy] warning: failed to save rules after delete: %v\n", err)
				}
			}

			return nil
		}
	}
	return fmt.Errorf("rule not found: %s", id)
}

// LoadRules 从文件加载规则
func (s *ProxyService) LoadRules(configDir string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 记录 configDir，以便后续自动保存
	s.configDir = configDir

	rulesPath := filepath.Join(configDir, "injection-rules.json")

	// 检查文件是否存在
	_, err := os.Stat(rulesPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// 文件不存在，返回 nil（不是错误）
			return nil
		}
		return fmt.Errorf("stat rules file: %w", err)
	}

	// 读取文件
	b, err := os.ReadFile(rulesPath)
	if err != nil {
		return fmt.Errorf("read rules file: %w", err)
	}

	// 解析 JSON
	var rules []InjectionRule
	if err := json.Unmarshal(b, &rules); err != nil {
		return fmt.Errorf("parse rules json: %w", err)
	}

	// 更新规则（已在锁内）
	s.rules = rules

	return nil
}

// LoadBackendURLHistory 从配置文件加载后端URL历史记录
func (s *ProxyService) LoadBackendURLHistory(configDir string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.configDir = configDir
	historyPath := filepath.Join(configDir, "proxy-backend-url-history.json")

	// 检查文件是否存在
	_, err := os.Stat(historyPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // 文件不存在不是错误
		}
		return fmt.Errorf("stat history file: %w", err)
	}

	// 读取文件
	b, err := os.ReadFile(historyPath)
	if err != nil {
		return fmt.Errorf("read history file: %w", err)
	}

	// 解析 JSON
	var history []string
	if err := json.Unmarshal(b, &history); err != nil {
		return fmt.Errorf("parse history json: %w", err)
	}

	s.backendURLHistory = history
	return nil
}

// SaveBackendURLHistory 保存后端URL历史记录到文件
func (s *ProxyService) SaveBackendURLHistory(configDir string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.configDir = configDir
	historyPath := filepath.Join(configDir, "proxy-backend-url-history.json")

	// 创建配置目录（如不存在）
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}

	// 序列化为 JSON（带缩进）
	history := make([]string, len(s.backendURLHistory))
	copy(history, s.backendURLHistory)
	b, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal history: %w", err)
	}
	b = append(b, '\n')

	// 原子写入
	tmpPath := historyPath + ".tmp"
	if err := os.WriteFile(tmpPath, b, 0o644); err != nil {
		return fmt.Errorf("write temp history: %w", err)
	}
	if err := os.Rename(tmpPath, historyPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace history: %w", err)
	}

	return nil
}

// GetBackendURLHistory 获取后端URL历史记录（返回副本）
func (s *ProxyService) GetBackendURLHistory() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.backendURLHistory == nil {
		return []string{}
	}

	history := make([]string, len(s.backendURLHistory))
	copy(history, s.backendURLHistory)
	return history
}

// AddBackendURL 添加后端URL到历史记录（去重、限制20条、最近在前）
func (s *ProxyService) AddBackendURL(url string) error {
	if url == "" {
		return errors.New("URL cannot be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 初始化历史记录
	if s.backendURLHistory == nil {
		s.backendURLHistory = []string{}
	}

	// 标准化URL（去除末尾斜杠）
	normalizedURL := strings.TrimRight(url, "/")

	// 去重：如果URL已存在，先移除旧的
	newHistory := []string{}
	for _, existingURL := range s.backendURLHistory {
		if strings.TrimRight(existingURL, "/") == normalizedURL {
			continue // 跳过已存在的URL
		}
		newHistory = append(newHistory, existingURL)
	}

	// 将新URL添加到最前面
	newHistory = append([]string{normalizedURL}, newHistory...)

	// 限制最多20条
	if len(newHistory) > 20 {
		newHistory = newHistory[:20]
	}

	s.backendURLHistory = newHistory

	// 自动保存到文件
	if s.configDir != "" {
		if err := s.saveBackendURLHistoryUnlocked(); err != nil {
			fmt.Printf("[amagi-codebox proxy] warning: failed to save backend URL history: %v\n", err)
		}
	}

	return nil
}

// RemoveBackendURL 从历史记录中删除指定URL
func (s *ProxyService) RemoveBackendURL(url string) error {
	if url == "" {
		return errors.New("URL cannot be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.backendURLHistory == nil {
		return nil // 空历史记录，无需删除
	}

	// 标准化URL
	normalizedURL := strings.TrimRight(url, "/")

	// 过滤掉要删除的URL
	newHistory := []string{}
	for _, existingURL := range s.backendURLHistory {
		if strings.TrimRight(existingURL, "/") != normalizedURL {
			newHistory = append(newHistory, existingURL)
		}
	}

	s.backendURLHistory = newHistory

	// 自动保存到文件
	if s.configDir != "" {
		if err := s.saveBackendURLHistoryUnlocked(); err != nil {
			fmt.Printf("[amagi-codebox proxy] warning: failed to save backend URL history: %v\n", err)
		}
	}

	return nil
}

// SetBackendURL 设置当前使用的后端URL，并自动添加到历史记录
func (s *ProxyService) SetBackendURL(url string) error {
	if url == "" {
		return errors.New("URL cannot be empty")
	}

	normalizedURL := strings.TrimRight(url, "/")

	s.mu.Lock()
	s.backendURL = normalizedURL
	s.mu.Unlock()

	// 添加到历史记录
	return s.AddBackendURL(normalizedURL)
}

// saveBackendURLHistoryUnlocked 保存后端URL历史记录到文件（必须在已持有 mu.Lock 的情况下调用）
func (s *ProxyService) saveBackendURLHistoryUnlocked() error {
	if s.configDir == "" {
		return fmt.Errorf("configDir not set")
	}

	historyPath := filepath.Join(s.configDir, "proxy-backend-url-history.json")

	// 创建配置目录（如不存在）
	if err := os.MkdirAll(s.configDir, 0o755); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}

	// 序列化为 JSON（带缩进）
	history := make([]string, len(s.backendURLHistory))
	copy(history, s.backendURLHistory)
	b, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal history: %w", err)
	}
	b = append(b, '\n')

	// 原子写入
	tmpPath := historyPath + ".tmp"
	if err := os.WriteFile(tmpPath, b, 0o644); err != nil {
		return fmt.Errorf("write temp history: %w", err)
	}
	if err := os.Rename(tmpPath, historyPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace history: %w", err)
	}

	return nil
}

// SaveRules 保存规则到文件
func (s *ProxyService) SaveRules(configDir string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 更新 configDir，以便后续自动保存
	s.configDir = configDir

	rulesPath := filepath.Join(configDir, "injection-rules.json")

	// 创建配置目录（如不存在）
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}

	// 序列化为 JSON（带缩进）
	rules := make([]InjectionRule, len(s.rules))
	copy(rules, s.rules)
	b, err := json.MarshalIndent(rules, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal rules: %w", err)
	}
	b = append(b, '\n')

	// 原子写入：先写 .tmp 文件，再 rename
	tmpPath := rulesPath + ".tmp"
	if err := os.WriteFile(tmpPath, b, 0o644); err != nil {
		return fmt.Errorf("write temp rules: %w", err)
	}
	if err := os.Rename(tmpPath, rulesPath); err != nil {
		_ = os.Remove(tmpPath) // 清理临时文件
		return fmt.Errorf("replace rules: %w", err)
	}

	return nil
}

// saveRulesUnlocked 保存规则到文件（必须在已持有 mu.Lock 的情况下调用）
func (s *ProxyService) saveRulesUnlocked() error {
	if s.configDir == "" {
		return fmt.Errorf("configDir not set")
	}

	rulesPath := filepath.Join(s.configDir, "injection-rules.json")

	// 创建配置目录（如不存在）
	if err := os.MkdirAll(s.configDir, 0o755); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}

	// 序列化为 JSON（带缩进）
	rules := make([]InjectionRule, len(s.rules))
	copy(rules, s.rules)
	b, err := json.MarshalIndent(rules, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal rules: %w", err)
	}
	b = append(b, '\n')

	// 原子写入：先写 .tmp 文件，再 rename
	tmpPath := rulesPath + ".tmp"
	if err := os.WriteFile(tmpPath, b, 0o644); err != nil {
		return fmt.Errorf("write temp rules: %w", err)
	}
	if err := os.Rename(tmpPath, rulesPath); err != nil {
		_ = os.Remove(tmpPath) // 清理临时文件
		return fmt.Errorf("replace rules: %w", err)
	}

	return nil
}

// ========== 代理生命周期 ==========

func (s *ProxyService) Start(port int, backendURL string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("proxy already running on port %d", s.port)
	}
	if backendURL == "" {
		return fmt.Errorf("backendURL cannot be empty")
	}

	s.port = port
	s.backendURL = strings.TrimRight(backendURL, "/")

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.proxyHandler)

	s.server = &http.Server{
		Handler: mux,
	}

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("listen on port %d: %w", port, err)
	}
	s.listener = ln
	s.running = true

	go func() {
		if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			fmt.Printf("[amagi-codebox proxy] serve error: %v\n", err)
		}
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	fmt.Printf("[amagi-codebox proxy] started on :%d → %s\n", port, s.backendURL)
	return nil
}

func (s *ProxyService) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running || s.server == nil {
		return nil
	}

	err := s.server.Close()
	s.server = nil
	s.listener = nil
	s.running = false
	fmt.Println("[amagi-codebox proxy] stopped")
	return err
}

func (s *ProxyService) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

func (s *ProxyService) GetStatus() ProxyStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return ProxyStatus{
		Running:    s.running,
		Port:       s.port,
		BackendURL: s.backendURL,
		RuleCount:  len(s.rules),
	}
}

func (s *ProxyService) GetLogs() []InjectionLog {
	s.logsMu.Lock()
	defer s.logsMu.Unlock()
	out := make([]InjectionLog, len(s.logs))
	copy(out, s.logs)
	return out
}

func (s *ProxyService) GetPort() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.port
}

// ========== HTTP 处理 ==========

func (s *ProxyService) proxyHandler(w http.ResponseWriter, r *http.Request) {
	// GET → 简单状态页
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		status := s.GetStatus()
		fmt.Fprintf(w, `{"running":%t,"port":%d,"backendURL":"%s","ruleCount":%d}`,
			status.Running, status.Port, status.BackendURL, status.RuleCount)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.writeError(w, err)
		return
	}

	// 获取后端 URL
	s.mu.RLock()
	backendURL := s.backendURL
	s.mu.RUnlock()

	// 改写超长工具名（适配百炼等有工具名长度限制的 API）
	body, toolMapping := rewriteRequestToolNames(body)

	modified, matchedNames := s.injectPrompts(body)

	// 构造转发请求：backendURL + 原始路径
	targetURL := backendURL + r.URL.Path

	req, err := http.NewRequest(http.MethodPost, targetURL, strings.NewReader(string(modified)))
	if err != nil {
		s.writeError(w, err)
		return
	}

	// 复制请求头
	for h, v := range r.Header {
		lower := strings.ToLower(h)
		if lower != "host" && lower != "content-length" {
			req.Header[h] = v
		}
	}
	req.Header.Set("Content-Length", fmt.Sprintf("%d", len(modified)))

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		s.writeError(w, err)
		return
	}
	defer resp.Body.Close()

	// 提取 preview 用于日志
	preview := extractPreview(body)
	s.addLog(matchedNames, preview, resp.StatusCode)

	// 响应处理：若有工具名映射，需要恢复原名
	if !toolMapping.isEmpty() {
		writeResponseWithRestore(w, resp, toolMapping, nil)
		return
	}

	// 无映射：检测 SSE 或普通 JSON
	isSSE := strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream")

	for h, v := range resp.Header {
		lower := strings.ToLower(h)
		if lower != "connection" && lower != "transfer-encoding" && lower != "content-length" {
			w.Header()[h] = v
		}
	}

	if isSSE {
		w.WriteHeader(resp.StatusCode)
		flusher, canFlush := w.(http.Flusher)
		accumulator := NewSSEUsageAccumulator()

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			accumulator.ProcessLine(line)
			w.Write([]byte(line))
			w.Write([]byte("\n"))
			if canFlush {
				flusher.Flush()
			}
		}
		// === usage 钩子：SSE 流结束后取累积 usage（设计 9.2，原 640 行）===
		if u := accumulator.GetUsage(); u != nil {
			s.emitUsageEvent(u, body, r)
		}
	} else {
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprintf(w, `{"error":"read response: %s"}`, err.Error())
			return
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(respBody)))
		w.WriteHeader(resp.StatusCode)
		w.Write(respBody)
		// === usage 钩子：非 SSE 从 body 提取 usage（设计 9.2，原 650 行）===
		if u := parseUsageFromJSON(respBody); u != nil {
			s.emitUsageEvent(u, body, r)
		}
	}
}

// emitUsageEvent 把 ProxyService.UsageData 转换为 UsageEvent 并调用 sink（设计 9.2）。
//
// 调用方：service.go proxyHandler 的 SSE 与非 SSE 分支。
// 行为：
//   - 若 onUsage 未注入（nil）则跳过（解耦）
//   - model 从请求 body 提取；provider 优先用 LaunchSession 注入的，否则从 backendURL 推断
//   - sessionID/preset/appType 从 currentSession 读取
//   - 在独立 goroutine 中调用 sink，避免阻塞响应链
func (s *ProxyService) emitUsageEvent(data *UsageData, reqBody []byte, r *http.Request) {
	s.mu.RLock()
	sink := s.onUsage
	s.mu.RUnlock()
	if sink == nil {
		return
	}

	s.sessMu.RLock()
	ctx := s.currentSession
	s.sessMu.RUnlock()

	s.mu.RLock()
	backendURL := s.backendURL
	s.mu.RUnlock()

	model := extractModelFromRequest(reqBody)
	provider := inferProviderFromURL(backendURL)
	if ctx != nil && ctx.Provider != "" {
		provider = ctx.Provider
	}

	evt := UsageEvent{
		Provider:                 provider,
		Model:                    model,
		OccurredAt:               time.Now(),
		RequestID:                r.Header.Get("x-request-id"),
		InputTokens:              data.InputTokens,
		OutputTokens:             data.OutputTokens,
		CacheReadInputTokens:     data.CacheReadInputTokens,
		CacheCreationInputTokens: data.CacheCreationInputTokens,
	}
	if ctx != nil {
		evt.SessionID = ctx.SessionID
		evt.Preset = ctx.Preset
		evt.AppType = ctx.AppType
	}

	// 在 goroutine 里跑避免阻塞响应链（设计 9.2 末尾）
	go sink(evt)
}

// ========== 关键字注入引擎 ==========

// injectPrompts 扫描最后一条用户消息，按关键字匹配规则注入 Prompt。
// 注意：只有当规则有关键字且至少有一个关键字匹配时才会触发。
// keywords 为空的规则将被忽略（不会作为默认规则触发）。
func (s *ProxyService) injectPrompts(body []byte) ([]byte, []string) {
	messages := gjson.GetBytes(body, "messages")
	if !messages.Exists() || !messages.IsArray() || len(messages.Array()) == 0 {
		return body, nil
	}

	// 从后往前找最后一条 role=user 的消息
	msgArr := messages.Array()
	lastIdx := -1
	for i := len(msgArr) - 1; i >= 0; i-- {
		if msgArr[i].Get("role").String() == "user" {
			lastIdx = i
			break
		}
	}
	if lastIdx < 0 {
		return body, nil
	}

	lastMsg := msgArr[lastIdx]
	content := lastMsg.Get("content")
	contentPath := fmt.Sprintf("messages.%d.content", lastIdx)

	// 收集用户消息的全部文本用于关键字搜索
	userText := collectUserText(content)

	// 获取按优先级排序的启用规则
	sortedRules := s.getSortedEnabledRules()
	if len(sortedRules) == 0 {
		return body, nil
	}

	// 关键字匹配：只处理有关键字的规则
	var matchedRules []InjectionRule
	userTextLower := strings.ToLower(userText)

	// 调试日志：输出用户消息内容和关键字
	fmt.Printf("[amagi-codebox proxy] DEBUG: userText length=%d, content=%q\n", len(userText), userText)
	fmt.Printf("[amagi-codebox proxy] DEBUG: userTextLower=%q\n", userTextLower)

	for i := range sortedRules {
		rule := sortedRules[i]

		// 跳过没有关键字的规则（不再作为默认规则）
		if len(rule.Keywords) == 0 {
			continue
		}

		// 任一关键字匹配即命中
		for _, kw := range rule.Keywords {
			kw = strings.TrimSpace(kw)
			if kw == "" {
				continue
			}
			kwLower := strings.ToLower(kw)
			isMatched := strings.Contains(userTextLower, kwLower)
			fmt.Printf("[amagi-codebox proxy] DEBUG: checking keyword %q (lower=%q) in userText: %v\n", kw, kwLower, isMatched)
			if isMatched {
				fmt.Printf("[amagi-codebox proxy] DEBUG: rule %q matched by keyword %q\n", rule.Name, kw)
				matchedRules = append(matchedRules, rule)
				break
			}
		}
	}

	if len(matchedRules) == 0 {
		return body, nil
	}

	// 注入匹配到的规则 Prompt
	modified := body
	var matchedNames []string
	var err error

	// 如果 content 是字符串，先转换为数组格式
	if content.Type == gjson.String {
		newContent := []interface{}{
			map[string]string{"type": "text", "text": content.String()},
		}
		modified, err = sjson.SetBytes(modified, contentPath, newContent)
		if err != nil {
			return body, nil
		}
	}

	// 获取后端URL判断是否是Claude API
	s.mu.RLock()
	backendURL := s.backendURL
	s.mu.RUnlock()
	isClaude := isClaudeProvider(backendURL)

	// 逐个追加注入块
	for _, rule := range matchedRules {
		if rule.Prompt == "" {
			continue
		}

		// 判断是否应该添加 cache_control
		shouldAddCache := rule.EnableCache && isClaude && len(rule.Prompt) > 500

		block := map[string]interface{}{
			"type": "text",
			"text": rule.Prompt,
		}

		// 添加 cache_control 参数
		if shouldAddCache {
			cacheTTL := rule.CacheTTL
			if cacheTTL == "" {
				cacheTTL = "5m" // 默认5分钟
			}
			block["cache_control"] = map[string]string{
				"type": "ephemeral",
				"ttl":  cacheTTL,
			}
			fmt.Printf("[amagi-codebox proxy] added cache_control with ttl=%s for rule %q\n", cacheTTL, rule.Name)
		}

		modified, err = sjson.SetBytes(modified, contentPath+".-1", block)
		if err != nil {
			return body, nil
		}
		matchedNames = append(matchedNames, rule.Name)
	}

	if len(matchedNames) == 0 {
		return body, nil
	}

	return modified, matchedNames
}

// getSortedEnabledRules 返回启用规则的副本，按 Priority 降序排列。
func (s *ProxyService) getSortedEnabledRules() []InjectionRule {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var enabled []InjectionRule
	for _, r := range s.rules {
		if r.Enabled {
			enabled = append(enabled, r)
		}
	}
	sort.Slice(enabled, func(i, j int) bool {
		return enabled[i].Priority > enabled[j].Priority
	})
	return enabled
}

// collectUserText 从 content 中收集全部文本（支持 string 和 array 两种格式）。
func collectUserText(content gjson.Result) string {
	if content.Type == gjson.String {
		return content.String()
	}
	if content.IsArray() {
		var parts []string
		for _, block := range content.Array() {
			if block.Get("type").String() == "text" {
				parts = append(parts, block.Get("text").String())
			}
		}
		return strings.Join(parts, " ")
	}
	return ""
}

// extractPreview 从请求 body 中提取最后一条用户消息的前 100 字符。
func extractPreview(body []byte) string {
	messages := gjson.GetBytes(body, "messages")
	if !messages.Exists() || !messages.IsArray() {
		return ""
	}
	msgArr := messages.Array()
	for i := len(msgArr) - 1; i >= 0; i-- {
		if msgArr[i].Get("role").String() == "user" {
			content := msgArr[i].Get("content")
			text := collectUserText(content)
			if len(text) > 100 {
				return text[:100] + "..."
			}
			return text
		}
	}
	return ""
}

// ========== 日志 ==========

func (s *ProxyService) addLog(ruleNames []string, preview string, status int) {
	if len(ruleNames) == 0 {
		return
	}
	s.logsMu.Lock()
	defer s.logsMu.Unlock()

	entry := InjectionLog{
		Time:      time.Now().Format("15:04:05"),
		RuleNames: ruleNames,
		Preview:   preview,
		Status:    status,
	}
	s.logs = append(s.logs, entry)
	// 环形缓冲区，最多保留 100 条
	if len(s.logs) > 100 {
		s.logs = s.logs[1:]
	}

	fmt.Printf("[amagi-codebox proxy] injected rules=%v status=%d preview=%q\n",
		ruleNames, status, preview)
}

func (s *ProxyService) writeError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	fmt.Fprintf(w, `{"error":"%s"}`, err.Error())
}

// isClaudeProvider 判断后端是否是 Claude API
func isClaudeProvider(backendURL string) bool {
	lower := strings.ToLower(backendURL)
	return strings.Contains(lower, "anthropic.com")
}

// inferProviderFromURL 从 backendURL 推断 provider 名称
// 注意：匹配顺序很重要，更具体的匹配应该放在前面
func inferProviderFromURL(backendURL string) string {
	lower := strings.ToLower(backendURL)
	switch {
	// 百炼（阿里云）优先匹配，因为 URL 包含 "anthropic"
	case strings.Contains(lower, "dashscope") || strings.Contains(lower, "aliyun"):
		return "dashscope"
	// GLM（智谱）
	case strings.Contains(lower, "bigmodel") || strings.Contains(lower, "glm") || strings.Contains(lower, "zhipu"):
		return "glm"
	// MiniMax
	case strings.Contains(lower, "minimax") || strings.Contains(lower, "x.ai"):
		return "minimax"
	// DeepSeek
	case strings.Contains(lower, "deepseek"):
		return "deepseek"
	// Anthropic 官方
	case strings.Contains(lower, "anthropic.com"):
		return "anthropic"
	// OpenAI
	case strings.Contains(lower, "openai"):
		return "openai"
	default:
		return "unknown"
	}
}
