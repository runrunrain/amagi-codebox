// Package claudewebui 是 Claude Code 会话的 per-session 回环 Web 数据面
// server（design §5.2，2026-11）。
//
// 与 pi 会话的本质差别（design §3/D2）：pi 的数据面 server 由 amagi-pi 扩展
// 进程实现，而 Claude Code 没有等价扩展宿主——数据面 server 因此移入 CodeBox
// 进程内（本包）：结构化流来自 Claude Code 落盘的会话转录 jsonl
// （internal/appmeta/claude 增量解析，tailer.go），输入面经控制门写回 PTY
// （input.go 双车道 InputPort，App 侧适配），与 TUI 平面共享同一进程同一
// 上下文。
//
// 职责分片：
//   - server.go  Manager（App 持有，非 Wails 绑定）+ plane 路由 + info/history/draft
//   - auth.go    Bearer 强制（constant-time）、CORS/预检、错误信封 {v:1,error}
//   - input.go   双车道输入端口 + /api/input /api/interrupt（车道分流单点）
//   - tailer.go  转录 jsonl 尾随（mtime 去重、增量、busy 派生）
//   - ws.go      /ws/events（子协议鉴权、seq 单调信封、环形缓冲重放）
//   - assets.go  //go:embed all:dist 的 SPA 静态面
//
// 安全红线（继承 pi 契约纪律，design §6）：token 视同会话权限——只经 URL
// fragment `#/t=<token>` 下发，绝不进 query/path/请求行/日志/错误体；server
// 只 bind 127.0.0.1；Host 校验 ^127\.0\.0\.1(:\d+)?$（remote 反代出向已按此
// 重写）；错误体统一 {v:1,error:"<code>"}（错误响应也带 v）。
package claudewebui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"amagi-codebox/internal/logging"
)

// PlaneConfig 一次平面的全部材料（App/executor 组装，design §5.2）。
type PlaneConfig struct {
	// SessionID 是 CodeBox 会话 ID（tracker 键 / info.sessionId）。
	SessionID string
	// ClaudeSessionID 是 --session-id 注入值（锁定转录文件名）；空 = 尾随
	// 目录内 mtime 最新的 jsonl（方案 P 语义，design §5.2 tailer）。
	ClaudeSessionID string
	// WorkDir 是 spec.WorkDir（最终生效目录，转录路径编码用）。
	WorkDir string
	// TranscriptRoot 是转录根——由调用方用 claude.ClaudeConfigRoot 解析后
	// 传入（CLAUDE_CONFIG_DIR 优先 → HOME/USERPROFILE → 缺省 homeDir，
	// design §5.1/R12）。
	TranscriptRoot string
	// PID 是 claude PTY pid（info.pid；ended 存活校验由宿主 tracker 负责）。
	PID int
	// Port 是预分配端口（webui.AllocateFreePort）；0 = 自选。
	Port int
	// Token 是 webui.GenerateToken 生成的 capability token（必填）。
	Token string
	// Settings 是运行参数（轮询周期/环形缓冲上限/历史分页上限；测试 seam）。
	Settings RuntimeSettings
}

// RuntimeSettings 是 per-plane 的运行参数（零值 = 全部取默认）。
type RuntimeSettings struct {
	// PollInterval 是转录尾随轮询周期（默认 500ms，design §5.2 tailer）。
	PollInterval time.Duration
	// RingBuffer 是 WS 环形缓冲上限（默认 4096，?since 重放窗口）。
	RingBuffer int
	// HistoryLimit 是历史缓冲/分页上限（默认 500；超限丢最旧置
	// truncatedHead，design §5.2 history）。
	HistoryLimit int
}

const (
	defaultPollInterval = 500 * time.Millisecond
	defaultRingBuffer   = 4096
	defaultHistoryLimit = 500
)

// normalize 把零值/非法值落回默认。
func (st RuntimeSettings) normalize() RuntimeSettings {
	if st.PollInterval <= 0 {
		st.PollInterval = defaultPollInterval
	}
	if st.RingBuffer <= 0 {
		st.RingBuffer = defaultRingBuffer
	}
	if st.HistoryLimit <= 0 {
		st.HistoryLimit = defaultHistoryLimit
	}
	return st
}

// Manager 管理全部 per-session 回环 server（App 持有；非 Wails 绑定，
// design §5.2/§10）。同一会话重复 Start 幂等替换（先 Stop 旧平面，防
// listener 泄漏，design R9）。
type Manager struct {
	log *logging.Service

	mu     sync.Mutex
	planes map[string]*plane
}

// NewManager 创建 Manager。log 为 nil 时静默（不记日志，行为不变）。
func NewManager(log *logging.Service) *Manager {
	return &Manager{log: log, planes: make(map[string]*plane)}
}

// Start 为 cfg.SessionID 启动一个 per-session 回环 server。同 sessionID
// 幂等替换（先 Stop 旧平面）；成功返回实际 bind 的端口。
func (m *Manager) Start(cfg PlaneConfig, input InputPort) (int, error) {
	if cfg.SessionID == "" {
		return 0, errors.New("claudewebui: SessionID 必填")
	}
	if cfg.Token == "" {
		return 0, errors.New("claudewebui: Token 必填")
	}
	if input == nil {
		return 0, errors.New("claudewebui: InputPort 必填")
	}
	if cfg.Port < 0 || cfg.Port > 65535 {
		return 0, fmt.Errorf("claudewebui: 非法端口 %d", cfg.Port)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.planes[cfg.SessionID]; ok {
		old.stop() // 幂等替换：先停旧（listener 立即释放，R9）
	}
	p, err := newPlane(m.log, cfg, input)
	if err != nil {
		return 0, err
	}
	m.planes[cfg.SessionID] = p
	return p.port, nil
}

// Stop 停止并摘除 sessionID 的平面（幂等；未注册时零操作）。
func (m *Manager) Stop(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.planes[sessionID]; ok {
		p.stop()
		delete(m.planes, sessionID)
	}
}

// plane 是单个会话的回环数据面（HTTP + WS + 转录尾随）。
type plane struct {
	cfg    PlaneConfig
	input  InputPort
	log    *logging.Service
	set    RuntimeSettings
	port   int
	assets *assetsHost

	ctx    context.Context
	cancel context.CancelFunc
	ln     net.Listener
	srv    *http.Server

	tailer *tailer
	hub    *hub

	draftMu sync.Mutex
	draft   string // 平面草稿（内存态；空串 = 无草稿。pi 实证需求：sandbox iframe 无 localStorage）

	stopOnce sync.Once
}

// newPlane 组装并启动一个 plane（listen 成功即开始服务）。
func newPlane(log *logging.Service, cfg PlaneConfig, input InputPort) (*plane, error) {
	set := cfg.Settings.normalize()
	ctx, cancel := context.WithCancel(context.Background())

	p := &plane{cfg: cfg, input: input, log: log, set: set, ctx: ctx, cancel: cancel}

	// 只 bind 127.0.0.1（安全红线；Port 优先、0 自选）。
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Port)))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("claudewebui: bind 127.0.0.1:%d: %w", cfg.Port, err)
	}
	p.ln = ln
	p.port = ln.Addr().(*net.TCPAddr).Port

	p.tailer = newTailer(cfg, set, log, func(kind string, event any) { p.hub.publish(kind, event) })
	p.hub = newHub(set, log, func() helloState { return p.tailer.helloSnapshot() })
	p.assets = newAssetsHost(log)

	p.srv = &http.Server{Handler: http.HandlerFunc(p.handleHTTP)}
	go func() {
		if err := p.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			p.warnf("回环 server 退出", fmt.Sprintf("port=%d err=%v", p.port, err))
		}
	}()
	go p.tailer.run(ctx)
	return p, nil
}

// stop 幂等收尾：hub 尽力而为 bye → ctx 取消（尾随/清理）→ Shutdown 收尾。
func (p *plane) stop() {
	p.stopOnce.Do(func() {
		p.hub.shutdown() // 先广播 bye（尽力而为）再关连接
		p.cancel()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = p.srv.Shutdown(ctx)
	})
}

func (p *plane) warnf(message, detail string) {
	if p.log != nil {
		p.log.Warn("claudewebui", message, detail)
	}
}

// handleHTTP 是 plane 的总路由：Host 校验 → OPTIONS 预检本地 204 →
// /api/*（Bearer）→ /ws/*（子协议）→ 静态 SPA（无鉴权）。
func (p *plane) handleHTTP(w http.ResponseWriter, r *http.Request) {
	// Host 校验 ^127\.0\.0\.1(:\d+)?$（对齐 pi；remote 反代出向已按此重写）。
	if !validLoopbackHost(r.Host) {
		writeAPIError(w, r, http.StatusForbidden, errBadRequest)
		return
	}

	path := r.URL.Path
	if strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/ws/") {
		// CORS 预检本地 204（Authorization, Content-Type），不携 token——
		// sandbox opaque iframe（Origin:null）的预检不带凭据（CORS 规范），
		// 镜像 remote 反代与 v1 派发器纪律（webui_proxy.go）。
		if r.Method == http.MethodOptions {
			writePreflight(w, r)
			return
		}
	}

	switch {
	case path == "/api/info" && r.Method == http.MethodGet:
		p.authAPI(w, r, p.handleInfo)
	case path == "/api/history" && r.Method == http.MethodGet:
		p.authAPI(w, r, p.handleHistory)
	case path == "/api/input" && r.Method == http.MethodPost:
		p.authAPI(w, r, p.handleInput)
	case path == "/api/interrupt" && r.Method == http.MethodPost:
		p.authAPI(w, r, p.handleInterrupt)
	case path == "/api/draft":
		p.authAPI(w, r, func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				p.handleDraftGet(w, r)
			case http.MethodPut:
				p.handleDraftPut(w, r)
			default:
				w.Header().Set("Allow", "GET, PUT, OPTIONS")
				writeAPIError(w, r, http.StatusMethodNotAllowed, errMethodNotAllowed)
			}
		})
	case path == "/ws/events":
		p.hub.serveWS(w, r, p.cfg.Token)
	default:
		// / 与静态资源：无鉴权、ACAO:*（design §6.1）。未知 /api、/ws 路径
		// 也走静态兜底（404/SPA），不给探测面。
		p.assets.serve(w, r)
	}
}

// authAPI 给 /api/* 处理器强制套 Bearer 鉴权（auth.go）。
func (p *plane) authAPI(w http.ResponseWriter, r *http.Request, h func(http.ResponseWriter, *http.Request)) {
	if !bearerAuthorized(r, p.cfg.Token) {
		if r.Header.Get("Authorization") == "" {
			writeAPIError(w, r, http.StatusUnauthorized, errUnauthorized)
			return
		}
		writeAPIError(w, r, http.StatusForbidden, errForbidden)
		return
	}
	h(w, r)
}

// handleInfo 实现 GET /api/info——探测契约五字段 v/ready/sessionId/pid/port
// （internal/webui/probe.go 强校验 v==1、port 等值）+ 附加 appType。
func (p *plane) handleInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, http.StatusOK, infoResponse{
		V: 1, Ready: true,
		SessionID: p.cfg.SessionID,
		PID:       p.cfg.PID,
		Port:      p.port,
		AppType:   "claudecode",
	})
}

type infoResponse struct {
	V         int    `json:"v"`
	Ready     bool   `json:"ready"`
	SessionID string `json:"sessionId"`
	PID       int    `json:"pid"`
	Port      int    `json:"port"`
	AppType   string `json:"appType"`
}

// handleHistory 实现 GET /api/history?limit=N——全量快照 = tailer 当前缓冲
// 投影：最新 limit 条 + cursor + busy + title + truncatedHead。
// limit 钳制：缺省/非法/≤0 → Settings.HistoryLimit；> HistoryLimit → 钳到
// HistoryLimit（design §6.1）。
func (p *plane) handleHistory(w http.ResponseWriter, r *http.Request) {
	limit := p.set.HistoryLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > p.set.HistoryLimit {
		limit = p.set.HistoryLimit
	}

	snap := p.tailer.snapshot()
	items := snap.Items
	if len(items) > limit {
		items = items[len(items)-limit:]
	}
	writeJSON(w, r, http.StatusOK, historyResponse{
		V: 1, Items: items, Cursor: snap.Cursor, Busy: snap.Busy,
		Title: snap.Title, TruncatedHead: snap.TruncatedHead,
	})
}

type historyResponse struct {
	V             int          `json:"v"`
	Items         []wireRecord `json:"items"`
	Cursor        int64        `json:"cursor"`
	Busy          bool         `json:"busy"`
	Title         string       `json:"title"`
	TruncatedHead bool         `json:"truncatedHead"`
}

// handleDraftGet 实现 GET /api/draft → {v:1,draft}。draft 不写 PTY、无车道
// 概念（design §5.2）。
func (p *plane) handleDraftGet(w http.ResponseWriter, r *http.Request) {
	p.draftMu.Lock()
	draft := p.draft
	p.draftMu.Unlock()
	writeJSON(w, r, http.StatusOK, draftResponse{V: 1, Draft: draft})
}

// handleDraftPut 实现 PUT /api/draft {draft}——内存暂存，≤128KiB，空串清除。
func (p *plane) handleDraftPut(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r, maxBodyBytes)
	if !ok {
		return
	}
	var req draftRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, errBadRequest)
		return
	}
	if len(req.Draft) > maxTextBytes {
		writeAPIError(w, r, http.StatusBadRequest, errBadRequest)
		return
	}
	p.draftMu.Lock()
	p.draft = req.Draft // 空串即清除
	p.draftMu.Unlock()
	writeJSON(w, r, http.StatusOK, okResponse{V: 1, OK: true})
}

type draftRequest struct {
	Draft string `json:"draft"`
}

type draftResponse struct {
	V     int    `json:"v"`
	Draft string `json:"draft"`
}

type okResponse struct {
	V  int  `json:"v"`
	OK bool `json:"ok"`
}

// validLoopbackHost 校验 Host ^127\.0\.0\.1(:\d+)?$（对齐 pi 反代出向形状
// webui_proxy.go:643-652）。
func validLoopbackHost(host string) bool {
	if host == "127.0.0.1" {
		return true
	}
	rest, ok := strings.CutPrefix(host, "127.0.0.1:")
	if !ok || rest == "" {
		return false
	}
	for i := 0; i < len(rest); i++ {
		if rest[i] < '0' || rest[i] > '9' {
			return false
		}
	}
	return true
}

// readBody 读取限量请求体；超限/非 UTF-8 → 400（统一错误信封）。
func readBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, errBadRequest)
		return nil, false
	}
	if int64(len(body)) > limit {
		writeAPIError(w, r, http.StatusBadRequest, errBadRequest)
		return nil, false
	}
	return body, true
}
