// Package agentcli 是 agent-CLI 的服务端：桌面应用内运行的本地命令服务
// （独立回环 listener、随机端口、端点文件发现），面向 AI agent 的 CLI 客户端
// （DESIGN.md §1/§2）。契约见 internal/agentcli/spec；命令表内容由 cmds 提供，
// Handler 由 root 包经 agentCLIHost 注册。
//
// 安全模型（DESIGN §3）：端点文件 0600 与令牌阻断的是"其他本机用户、浏览器
// 页面与 DNS rebinding"；同 uid 恶意进程与明文密钥配置同属一个信任域，不宣称
// 更强。三重校验（回环对端 + Host 白名单 + 拒 Origin）+ 常量时间令牌比较见
// auth.go；审计见 audit.go；端点文件生命周期见 endpoint.go。
package agentcli

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"amagi-codebox/internal/agentcli/spec"
	"amagi-codebox/internal/platform"
)

// Logger 是 agentcli 服务端的最小日志接口（root 包直接以 *logging.Service
// 适配，方法签名一致）。nil 安全：不注入则静默。
type Logger interface {
	Info(source, message string, detail ...string)
	Warn(source, message string, detail ...string)
	Error(source, message string, detail ...string)
}

// Status 是服务状态快照。刻意不含令牌：URL 本身不含令牌，令牌只存在于
// 0600 端点文件与请求头中（DESIGN §3.1——令牌不进任何命令输出/日志）。
// LastError 携带最近一次生命周期失败（如端点被其它实例占用拒绝发布），
// 成功启动/轮换后清空；供 UI 如实展示，不沉默。
type Status struct {
	Enabled      bool   `json:"enabled"`
	Running      bool   `json:"running"`
	URL          string `json:"url"` // 形如 http://127.0.0.1:53211；未运行为空
	EndpointPath string `json:"endpointPath"`
	PID          int    `json:"pid"`
	StartedAt    string `json:"startedAt"` // RFC3339；未运行为空
	LastError    string `json:"lastError"` // 最近一次启动/发布失败原因；空 = 无异常
}

const (
	// maxBodyBytes 是单个命令请求体上限（DESIGN §3.2）。
	maxBodyBytes = 1 << 20 // 1 MiB
	// defaultCommandTimeout 是 Command.Timeout==0 时的命令执行超时（DESIGN §2）。
	defaultCommandTimeout = 120 * time.Second
	// auditSource 是服务端日志的 source 标识。
	auditSource = "agent-cli"
)

// Server 承载 agent-CLI 的本地 HTTP 服务。构造后 table/handlers 不可变
// （并发读无锁）；运行态（token/listener/enabled）由 mu 保护。
//
// 超时/取消语义（评审 D-06）：请求 ctx 会传给 Handler 并随客户端断开/命令
// 超时取消；invokeHandler 前检查 ctx（已取消则不执行、不触发副作用）。
// 但服务端不强行中止 Handler：不可取消的 Handler（不消费 ctx 的实现）在
// 客户端超时后仍会执行到完成——其结果如实写入响应（若连接尚在），并在
// 审计行标注 ctxCancelled；客户端超时错误因此提示“操作可能已生效，请先
// 查询状态再重试”。
type Server struct {
	configDir  string
	appVersion string
	table      []spec.Command
	tableHash  string
	handlers   map[string]spec.Handler
	audit      *auditLog
	log        Logger

	// processAlive 用于跨进程端点所有权检测（评审 D-04）；测试可注入模拟
	// 两实例。nil 视为 platform.ProcessAlive。
	processAlive func(pid int) bool

	mu        sync.Mutex // 保护以下运行态字段（快路径读）
	enabled   bool       // false 时所有请求 503 cli_disabled（listener 保留存活）
	running   bool
	token     string // 32 字节 base64url；空 = 无有效令牌（禁用/停止态）
	ln        net.Listener
	httpSrv   *http.Server
	url       string
	port      int // listener 实际端口（Host 头精确校验用，评审 D-09）
	startedAt time.Time
	lastErr   string // 最近一次启动/发布失败原因（Status.LastError）

	// lifeMu 串行化生命周期提交边界（评审 D-03）：Start/Stop/SetEnabled/
	// RotateToken 的状态变更+端点发布+失败回滚作为整体在统一互斥下串行
	// 执行，避免并发轮换时“回滚了别次操作已替换的旧令牌”或共用临时文件
	// 互相改名失败。不保护快路径（gate/Status 只取 mu）。
	lifeMu sync.Mutex
}

// NewServer 构造服务。table 与 handlers 的键（Command.Name()）必须 1:1，
// 由 root 包测试锚定；此处不做复制，调用方保证构造后不再修改。
func NewServer(configDir, appVersion string, table []spec.Command, handlers map[string]spec.Handler, log Logger) *Server {
	return &Server{
		configDir:  configDir,
		appVersion: appVersion,
		table:      table,
		tableHash:  hashTable(table),
		handlers:   handlers,
		audit:        newAuditLog(configDir, log),
		log:          log,
		processAlive: platform.ProcessAlive,
		enabled:      true,
	}
}

// alivePID 返回 pid 存活判定（注入优先）。
func (s *Server) alivePID(pid int) bool {
	if s.processAlive != nil {
		return s.processAlive(pid)
	}
	return platform.ProcessAlive(pid)
}

// Start 启动服务：跨进程所有权检测（评审 D-04：端点被另一存活实例占用则
// 拒绝发布，不覆盖、不影响应用其余功能）→ 清理陈旧端点文件 → 生成新令牌
//（每次启动重新生成，DESIGN §3.1）→ 回环随机端口监听 → 发布端点文件。
// 幂等：已在运行且启用时直接返回；已在监听但被禁用时恢复放行并轮换令牌
// 重发布端点。端点文件发布失败则回滚监听，保持"运行 = 端点已发布"不变量。
// 生命周期由 Stop 显式控制（不接管调用方 ctx：Shutdown 已在保存配置前
// 显式停止，避免 wails ctx 取消时序依赖）。
//
// 状态变更、发布与回滚在 lifeMu 下整体串行（评审 D-03）：并发 Start/
// Rotate/SetEnabled/Stop 不会交错出"内存令牌与磁盘端点不一致"的中间态。
func (s *Server) Start() error {
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()
	return s.startLocked()
}

// startLocked 假定 lifeMu 已持有（发布/回滚与状态变更同一临界区）。
func (s *Server) startLocked() error {
	s.mu.Lock()
	if s.running {
		enabled := s.enabled
		s.mu.Unlock()
		if enabled {
			return nil
		}
		// 已在监听但处于禁用态（运行中被关闭后再次开启）：恢复放行并轮换
		// 令牌（旧令牌在禁用时已作废）+ 重发布端点。
		s.mu.Lock()
		s.enabled = true
		s.mu.Unlock()
		return s.rotateLocked()
	}
	s.mu.Unlock()

	// 跨进程所有权（评审 D-04）：现有端点文件属于另一个存活进程 → 拒绝
	// 发布，不清理、不覆盖；Status 携带原因，应用日志告警。
	if ownerErr := checkEndpointOwner(s.configDir, s.alivePID); ownerErr != nil {
		s.mu.Lock()
		s.lastErr = ownerErr.Error()
		s.mu.Unlock()
		s.logWarn("agent-CLI 服务未启动（端点被其它实例占用）", ownerErr.Error())
		return ownerErr
	}

	CleanStaleEndpoint(s.configDir)

	token, err := newToken()
	if err != nil {
		return fmt.Errorf("agent-cli: 生成令牌失败: %w", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("agent-cli: 监听回环端口失败: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	now := time.Now()

	httpSrv := &http.Server{
		Handler:           s.buildHandler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// WriteTimeout 必须覆盖最长命令执行（长命令由 Command.Timeout 自行
		// 约束；这里取宽裕上限，避免截断 env check 一类实时探测）。
		WriteTimeout: 15 * time.Minute,
		IdleTimeout:  2 * time.Minute,
	}

	s.mu.Lock()
	if s.running { // 防御：lifeMu 已串行化，正常不可达。
		s.mu.Unlock()
		_ = ln.Close()
		return nil
	}
	s.token = token
	s.ln = ln
	s.httpSrv = httpSrv
	s.url = url
	s.port = port
	s.startedAt = now
	s.running = true
	s.enabled = true
	s.mu.Unlock()

	go func() {
		if serveErr := httpSrv.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			s.logError("HTTP 服务异常退出", serveErr.Error())
		}
	}()

	if err := publishEndpoint(s.configDir, spec.Endpoint{
		SchemaVersion: spec.EndpointSchemaVersion,
		URL:           url,
		PID:           os.Getpid(),
		StartedAt:     now.Format(time.RFC3339),
		AppVersion:    s.appVersion,
		CLIToken:      token,
	}); err != nil {
		// 发布失败：在同一 lifeMu 临界区内回滚监听与内存态（stopLocked 不
		// 再取 lifeMu，不会自锁），保持"运行 = 端点已发布"不变量。
		s.stopLocked()
		s.mu.Lock()
		s.lastErr = err.Error()
		s.mu.Unlock()
		s.logError("agent-CLI 端点发布失败，服务已回滚", err.Error())
		return fmt.Errorf("agent-cli: 发布端点文件失败: %w", err)
	}
	s.mu.Lock()
	s.lastErr = ""
	s.mu.Unlock()
	s.logInfo("agent-CLI 服务已启动", url)
	return nil
}

// Stop 停止服务：关闭监听与全部连接、令牌作废、撤销本进程发布的端点文件
// （仅 pid 匹配才删，防误删其它实例的文件）。幂等。与其它生命周期操作在
// lifeMu 下串行（评审 D-03）。
func (s *Server) Stop() {
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()
	s.stopLocked()
}

// stopLocked 假定 lifeMu 已持有。
func (s *Server) stopLocked() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	httpSrv := s.httpSrv
	s.running = false
	s.enabled = false
	s.token = ""
	s.url = ""
	s.port = 0
	s.startedAt = time.Time{}
	s.httpSrv = nil
	s.ln = nil
	s.mu.Unlock()

	if httpSrv != nil {
		_ = httpSrv.Close()
	}
	removeOwnEndpoint(s.configDir)
	s.logInfo("agent-CLI 服务已停止", "")
}

// SetEnabled 切换命令行访问开关（DESIGN §3.7）。开启 = Start 语义（必要时
// 冷启动，或恢复已监听实例的放行 + 轮换令牌 + 重发布端点）；关闭 = 令牌立即
// 作废 + 撤销端点文件，listener 保留存活并对所有请求返回 503 cli_disabled
// （CLI 客户端据此区分"应用未运行"与"命令行访问被关闭"）。持久化由调用方
// （App 门面 → settings.Service）负责。开关与轮换/启停在 lifeMu 下串行
//（评审 D-03）。
func (s *Server) SetEnabled(enabled bool) error {
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()
	if enabled {
		return s.startLocked()
	}
	s.mu.Lock()
	s.enabled = false
	s.token = "" // 旧令牌立即作废
	running := s.running
	s.mu.Unlock()
	if running {
		removeOwnEndpoint(s.configDir)
		s.logInfo("agent-CLI 命令行访问已关闭（listener 保留 503）", "")
	}
	return nil
}

// RotateToken 轮换令牌：生成新令牌重写端点文件，旧令牌立即失效（DESIGN
// §3.1）。仅在运行且启用时有意义。与其它生命周期操作在 lifeMu 下串行
//（评审 D-03：并发轮换不会共用临时文件或回滚别次操作已替换的令牌）。
func (s *Server) RotateToken() error {
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()
	s.mu.Lock()
	running, enabled := s.running, s.enabled
	s.mu.Unlock()
	if !running || !enabled {
		return errors.New("agent-cli: 服务未运行或已禁用，无法轮换令牌")
	}
	return s.rotateLocked()
}

// rotateLocked 生成新令牌并重写端点文件；失败只回滚自己的版本（评审 D-03：
// 仅当内存令牌仍是本次未发布成功的值时才恢复旧值，绝不恢复别次操作已
// 替换的令牌）。假定 lifeMu 已持有，发布在临界区内串行。
func (s *Server) rotateLocked() error {
	token, err := newToken()
	if err != nil {
		return fmt.Errorf("agent-cli: 生成令牌失败: %w", err)
	}
	s.mu.Lock()
	if !s.running || !s.enabled {
		s.mu.Unlock()
		return errors.New("agent-cli: 服务未运行或已禁用")
	}
	old, url, startedAt := s.token, s.url, s.startedAt
	s.token = token
	s.mu.Unlock()

	if err := publishEndpoint(s.configDir, spec.Endpoint{
		SchemaVersion: spec.EndpointSchemaVersion,
		URL:           url,
		PID:           os.Getpid(),
		StartedAt:     startedAt.Format(time.RFC3339),
		AppVersion:    s.appVersion,
		CLIToken:      token,
	}); err != nil {
		s.mu.Lock()
		// 新令牌未发布即未生效：恢复旧值保证磁盘文件与内存一致。仅当内存
		// 令牌仍是自己设的值才回滚（lifeMu 串行下必真，防御保留）。
		if s.token == token {
			s.token = old
			s.lastErr = err.Error()
		}
		s.mu.Unlock()
		return fmt.Errorf("agent-cli: 重写端点文件失败: %w", err)
	}
	s.mu.Lock()
	s.lastErr = ""
	s.mu.Unlock()
	s.logInfo("agent-CLI 令牌已轮换", "")
	return nil
}

// Status 返回状态快照（不含令牌）。
func (s *Server) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{
		Enabled:      s.enabled,
		Running:      s.running,
		EndpointPath: endpointPath(s.configDir),
		PID:          os.Getpid(),
		LastError:    s.lastErr,
	}
	if s.running {
		st.URL = s.url
		st.StartedAt = s.startedAt.Format(time.RFC3339)
	}
	return st
}

// ---- HTTP 路由 ----

func (s *Server) buildHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(spec.PathPing, s.handlePing)
	mux.HandleFunc(spec.PathCommand, s.handleCommand)
	// 前缀内其余路径统一走校验 + 404，不泄漏路由差异。
	mux.HandleFunc(spec.PathPrefix, s.handleUnknown)
	return mux
}

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	if !s.gate(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusBadRequest, spec.Errorf(spec.ErrBadArgs, "ping 只接受 GET"))
		return
	}
	s.writeEnvelope(w, http.StatusOK, spec.Envelope{OK: true, Data: spec.PingData{
		AppVersion: s.appVersion,
		PID:        os.Getpid(),
		TableHash:  s.tableHash,
	}})
}

func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request) {
	if !s.gate(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusBadRequest, spec.Errorf(spec.ErrBadArgs, "命令只接受 POST"))
		return
	}
	name := strings.TrimPrefix(r.URL.Path, spec.PathCommand)
	cmd, ok := s.findCommand(name)
	if !ok {
		s.writeError(w, http.StatusNotFound, spec.Errorf(spec.ErrUnknownCommand, "未知命令 %q", name))
		return
	}
	handler := s.handlers[name] // 表 ↔ Handler 1:1 由 root parity 测试锚定；防御缺失
	if handler == nil {
		s.writeError(w, http.StatusInternalServerError, spec.Errorf(spec.ErrInternal, "命令 %q 未注册处理器", name))
		return
	}

	// 审计收口（DESIGN §3.4）：非 read 命令的任何终态（参数校验失败/确认门
	// 拒绝/执行结果）都记一行；只记参数名，绝不记参数值。ctxCancelled 标注
	// 请求取消后仍完成/未执行的场景（评审 D-06）。
	auditIfMutation := func(paramNames []string, code string, duration time.Duration, ctxCancelled bool) {
		if cmd.Risk != spec.RiskRead {
			s.audit.record(cmd, paramNames, code, duration, ctxCancelled)
		}
	}

	var req spec.Request
	// 请求体：上限（413）+ 恰好一个 JSON 值（评审 D-11）+ UseNumber 保留
	// 整数字面量精度（评审 D-10）。
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.UseNumber()
	if err := dec.Decode(&req); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			auditIfMutation(sortedKeys(req.Args), spec.ErrBadArgs, 0, false)
			s.writeError(w, http.StatusRequestEntityTooLarge, spec.Errorf(spec.ErrBadArgs, "请求体超过 %d 字节上限", maxBodyBytes))
			return
		}
		auditIfMutation(sortedKeys(req.Args), spec.ErrBadArgs, 0, false)
		s.writeError(w, http.StatusBadRequest, spec.Errorf(spec.ErrBadArgs, "请求体解析失败: %v", err))
		return
	}
	// 整个 body 必须恰好是一个 JSON 值：尾随第二 JSON 值或非空白垃圾一律拒
	// 绝（只 Decode 一次会静默忽略尾随内容，评审 D-11）。
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		auditIfMutation(sortedKeys(req.Args), spec.ErrBadArgs, 0, false)
		s.writeError(w, http.StatusBadRequest, spec.Errorf(spec.ErrBadArgs, "请求体必须恰好是一个 JSON 值（检测到尾随内容）"))
		return
	}

	args, argErr := normalizeArgs(cmd, req.Args)
	if argErr != nil {
		auditIfMutation(sortedKeys(req.Args), spec.ErrBadArgs, 0, false)
		s.writeError(w, http.StatusBadRequest, argErr)
		return
	}

	// 确认门：服务端强制（DESIGN §2）。缺失/不符 → 409 confirmation_required。
	if cmd.Confirm != "" && req.Confirm != cmd.Confirm {
		auditIfMutation(sortedKeys(args), spec.ErrConfirmationRequired, 0, false)
		s.writeError(w, http.StatusConflict, &spec.Error{
			Code:    spec.ErrConfirmationRequired,
			Message: fmt.Sprintf("命令 %s 需要确认", cmd.Name()),
			Hint:    "请加 --yes，或在 confirm 字段传入 " + cmd.Confirm,
		})
		return
	}

	timeout := time.Duration(cmd.Timeout) * time.Second
	if timeout <= 0 {
		timeout = defaultCommandTimeout
	}
	// ctx 随客户端断开取消 + 命令级超时（spec.Handler 契约）。
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	start := time.Now()
	// ctx 预检（评审 D-06）：客户端已断开/超时的请求不再调用 Handler，
	// 不触发副作用；结果如实（未执行）写入响应与审计。
	if ctxErr := ctx.Err(); ctxErr != nil {
		e := spec.Errorf(spec.ErrInternal, "命令未执行：请求已取消或超时（%v）", ctxErr)
		auditIfMutation(sortedKeys(args), e.Code, 0, true)
		s.writeError(w, statusForCode(e.Code), e)
		return
	}
	data, herr := s.invokeHandler(ctx, handler, args)
	duration := time.Since(start)

	resultCode := "ok"
	if herr != nil {
		var serr *spec.Error
		if errors.As(herr, &serr) {
			resultCode = serr.Code
		} else {
			resultCode = spec.ErrInternal
		}
	}
	// Handler 完成后 ctx 已取消：结果仍如实返回（不篡改成功语义），审计
	// 标注 ctxCancelled（不可取消的 Handler 在客户端超时后仍会完成，评审 D-06）。
	auditIfMutation(sortedKeys(args), resultCode, duration, ctx.Err() != nil)

	if herr == nil {
		s.writeEnvelope(w, http.StatusOK, spec.Envelope{OK: true, Data: data})
		return
	}
	var serr *spec.Error
	if errors.As(herr, &serr) {
		s.writeError(w, statusForCode(serr.Code), serr)
		return
	}
	s.writeError(w, http.StatusInternalServerError, spec.Errorf(spec.ErrInternal, "%v", herr))
}

func (s *Server) handleUnknown(w http.ResponseWriter, r *http.Request) {
	if !s.gate(w, r) {
		return
	}
	s.writeError(w, http.StatusNotFound, spec.Errorf(spec.ErrUnknownCommand, "未知路径 %q", r.URL.Path))
}

// invokeHandler 调用 Handler 并把 panic 恢复为 internal 错误（DESIGN A1），
// 防止单条命令崩溃拖垮整个 HTTP 服务。
func (s *Server) invokeHandler(ctx context.Context, handler spec.Handler, args spec.Args) (data any, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			data = nil
			err = spec.Errorf(spec.ErrInternal, "命令处理器崩溃: %v", rec)
		}
	}()
	return handler(ctx, args)
}

func (s *Server) findCommand(name string) (spec.Command, bool) {
	for _, c := range s.table {
		if c.Name() == name {
			return c, true
		}
	}
	return spec.Command{}, false
}

// ---- 响应与状态映射 ----

// statusForCode 把 spec.Error.Code 映射为 HTTP 状态（DESIGN §2 协议表）。
func statusForCode(code string) int {
	switch code {
	case spec.ErrBadArgs:
		return http.StatusBadRequest
	case spec.ErrUnauthorized:
		return http.StatusUnauthorized
	case spec.ErrForbidden, spec.ErrForbiddenCommand:
		return http.StatusForbidden
	case spec.ErrUnknownCommand, spec.ErrNotFound:
		return http.StatusNotFound
	case spec.ErrConfirmationRequired, spec.ErrConflict:
		return http.StatusConflict
	case spec.ErrDisabled, spec.ErrNotReady:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func (s *Server) writeEnvelope(w http.ResponseWriter, status int, env spec.Envelope) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(env)
}

func (s *Server) writeError(w http.ResponseWriter, status int, e *spec.Error) {
	s.writeEnvelope(w, status, spec.Envelope{OK: false, Error: e})
}

// ---- 参数归一与校验 ----

// normalizeArgs 按命令表声明做白名单/必填/类型/枚举校验，并把 JSON 反序列
// 化的值归一为 Handler 友好形态（int → int64、[]any → []string、json 字面量
// → 解析后的值）。缺失的非必填参数用 Param.Default 兜底（spec：服务端也需
// 自行兜底）。
func normalizeArgs(cmd spec.Command, raw map[string]any) (spec.Args, *spec.Error) {
	byName := make(map[string]spec.Param, len(cmd.Params))
	for _, p := range cmd.Params {
		byName[p.Name] = p
	}
	args := make(spec.Args, len(raw)+len(cmd.Params))
	for name, v := range raw {
		p, ok := byName[name]
		if !ok {
			return nil, spec.Errorf(spec.ErrBadArgs, "未知参数 %q", name)
		}
		norm, err := coerceParam(p, v)
		if err != nil {
			return nil, err
		}
		args[name] = norm
	}
	for _, p := range cmd.Params {
		if _, ok := args[p.Name]; ok {
			continue
		}
		if p.Required {
			return nil, spec.Errorf(spec.ErrBadArgs, "缺少必填参数 %q（--%s）", p.Name, kebabCase(p.Name))
		}
		if p.Default != nil {
			args[p.Name] = p.Default
		}
	}
	return args, nil
}

// coerceParam 校验并归一单个参数值。UseNumber 解码后只会出现 string /
// json.Number / bool / []any / map[string]any / nil（评审 D-10：整数经
// json.Number 严格解析，不经 float64 舍入）。
func coerceParam(p spec.Param, v any) (any, *spec.Error) {
	bad := func(reason string) *spec.Error {
		return spec.Errorf(spec.ErrBadArgs, "参数 %q %s", p.Name, reason)
	}
	switch p.Type {
	case spec.TypeString:
		sv, ok := v.(string)
		if !ok {
			return nil, bad("必须是字符串")
		}
		if len(p.Enum) > 0 && !containsStr(p.Enum, sv) {
			return nil, spec.Errorf(spec.ErrBadArgs, "参数 %q 取值必须是 %s 之一", p.Name, strings.Join(p.Enum, "|"))
		}
		return sv, nil
	case spec.TypeInt:
		n, ok := v.(json.Number)
		if !ok {
			return nil, bad("必须是整数")
		}
		i, err := n.Int64() // 严格整数解析：小数/指数形式/超 int64 一律拒绝
		if err != nil {
			return nil, bad("必须是整数（或超出 int64 范围）")
		}
		if int64(int(i)) != i { // 32 位平台的 int 目标范围
			return nil, bad("超出本平台 int 范围")
		}
		return i, nil
	case spec.TypeBool:
		bv, ok := v.(bool)
		if !ok {
			return nil, bad("必须是布尔值")
		}
		return bv, nil
	case spec.TypeStrings:
		items, ok := v.([]any)
		if !ok {
			return nil, bad("必须是字符串数组")
		}
		out := make([]string, 0, len(items))
		for _, it := range items {
			sv, ok := it.(string)
			if !ok {
				return nil, bad("数组元素必须是字符串")
			}
			if len(p.Enum) > 0 && !containsStr(p.Enum, sv) {
				return nil, spec.Errorf(spec.ErrBadArgs, "参数 %q 元素取值必须是 %s 之一", p.Name, strings.Join(p.Enum, "|"))
			}
			out = append(out, sv)
		}
		return out, nil
	case spec.TypeJSON:
		// 客户端两种发送策略都接受：JSON 字面量字符串（由客户端展开 @file/-
		// stdin 后仍以字符串承载），或直接发送解析后的 JSON 值。透传值内的
		// json.Number 递归还原为 float64，保持 Handler 可见形态与普通解码
		// 一致（评审 D-10：严格解析只作用于 int 参数本身）。
		if sv, ok := v.(string); ok {
			var parsed any
			if err := json.Unmarshal([]byte(sv), &parsed); err != nil {
				return nil, spec.Errorf(spec.ErrBadArgs, "参数 %q 不是合法 JSON: %v", p.Name, err)
			}
			return parsed, nil
		}
		return jsonNumbersToFloat(v), nil
	default:
		return nil, spec.Errorf(spec.ErrInternal, "命令表参数 %q 声明了未知类型 %q", p.Name, p.Type)
	}
}

func containsStr(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// jsonNumbersToFloat 把 UseNumber 解码出的 json.Number 递归还原为 float64
//（仅用于非 int 参数的透传值；int 参数在 coerceParam 里已严格解析为 int64）。
// 无法解析为 float64 的极端字面量原样保留，后续序列化自行暴露。
func jsonNumbersToFloat(v any) any {
	switch t := v.(type) {
	case json.Number:
		if f, err := t.Float64(); err == nil {
			return f
		}
		return t
	case map[string]any:
		for k, x := range t {
			t[k] = jsonNumbersToFloat(x)
		}
		return t
	case []any:
		for i, x := range t {
			t[i] = jsonNumbersToFloat(x)
		}
		return t
	default:
		return v
	}
}

// sortedKeys 返回映射的键井集（排序稳定，供审计的 paramNames 用）。
func sortedKeys[M ~map[string]V, V any](m M) []string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// kebabCase 把 camelCase 参数名转成 CLI 旗标形态（buildId → build-id），
// 仅用于错误提示。
func kebabCase(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('-')
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// hashTable 与 cmds.Hash 同算法（服务端快照独立计算，避免生产代码反向依赖
// cmds 包；测试以 cmds.Hash(cmds.Table()) 锚定两者不漂移——漂移会让客户端
// 的版本偏差提示误报）。
func hashTable(table []spec.Command) string {
	h := sha256.New()
	for _, c := range table {
		h.Write([]byte(c.Name() + "|" + string(c.Risk) + "|" + c.Confirm + "|"))
		for _, p := range c.Params {
			h.Write([]byte(p.Name + ":" + string(p.Type) + ";"))
		}
		h.Write([]byte("\n"))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *Server) logInfo(msg, detail string) {
	if s.log != nil {
		s.log.Info(auditSource, msg, detail)
	}
}

func (s *Server) logWarn(msg, detail string) {
	if s.log != nil {
		s.log.Warn(auditSource, msg, detail)
	}
}

func (s *Server) logError(msg, detail string) {
	if s.log != nil {
		s.log.Error(auditSource, msg, detail)
	}
}
