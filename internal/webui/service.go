package webui

// service.go — Wails 绑定服务（按 bind_list.go 既有模式注册）。
//
// 状态机（技术方案 §5）：unknown → probing → available | unavailable；
// 会话结束（pi 进程退出 → server 消亡 → 探测持续失败，契约 §7.4）→ ended。
//
// pi TUI 内 /resume、/new、fork、reload 会在同进程内切换会话：sessionId
// 必变而 pid 不变，故采纳粘性键为 pid——sessionId 演进视为合法会话切换，
// 跟随更新并记日志（见 validateAdoption / adoptLocked）。
//
// 每个 embedded pi 会话一条 tracker：LaunchPiSession 在 PTY 启动成功后
// RegisterSession（携带注入端口、pid 与 v1.0.2 capability token），前端按
// 0.5–1s 节奏轮询 ProbeWebUI 直到 available（契约 §4.1 建议节奏）；会话退出
// 时 app 调 Invalidate 落 ended，会话记录删除时 app 调 RemoveSession 清理。
//
// 并发纪律（R1 Major3）：网络 I/O 不持全局锁——探测前先快照 tracker 标量
// 字段，I/O 完成后再取锁提交；同一 tracker 的探测轮由 per-tracker probeMu
// 串行化，迟到结果在提交点按 tracker 同一性校验丢弃。

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"amagi-codebox/internal/logging"
)

// State 是 codebox 侧 per-session webui 状态机取值。
type State string

const (
	StateUnknown     State = "unknown"     // 未注册（非 pi 会话 / 外部终端模式）
	StateProbing     State = "probing"     // 已注册，探测中（503 或暂未连通）
	StateAvailable   State = "available"   // /api/info 200 且强校验通过
	StateUnavailable State = "unavailable" // 探测窗口耗尽仍未就绪（未装插件等，A-4 隐藏切换控件）
	StateEnded       State = "ended"       // 会话已结束（进程退出 / 可用后持续失联）
)

// Status 是暴露给前端的会话 webui 状态快照。
type Status struct {
	State State `json:"state"`
	// URL 为 iframe src（契约 v1.0.2 §6.5：${httpBase}/#/t=<token>，fragment
	// 承载 capability token，不入 HTTP 日志；token 未注入/未学到时为
	// ${httpBase}/ 裸形式）；仅 available 时非空。
	URL  string `json:"url,omitempty"`
	Port int    `json:"port,omitempty"`
}

// tracker 记录单个 codebox 会话的 webui 发现状态。
type tracker struct {
	pid          int    // pi 进程 pid（注册表回退按 pid 匹配 + 采纳强校验用）
	injectedPort int    // AMAGI_WEBUI_PORT 注入端口（0 = 注入失败，纯注册表发现）
	token        string // v1.0.2 capability token（AMAGI_WEBUI_TOKEN 注入值；空 = 未注入，探测时从注册表条目补读）
	piSessionID  string // 探测成功后从 /api/info 学到的 pi 会话 UUID（/resume 等会话切换后跟随演进；粘性键是 pid）
	state        State
	port         int // 当前确认/候选端口
	registeredAt time.Time
	failStreak   int // available 之后连续探测失败次数（ended 判定，§7.4）

	// probeMu 串行化同一 tracker 的探测轮（I/O 在全局锁外执行，靠它防止
	// 同会话并发探测交叉提交）。
	probeMu sync.Mutex
}

const (
	// defaultProbeTimeout 是单次 /api/info 请求的 HTTP 超时（每候选独立，
	// Major3：多候选扫描不再共享一个总预算）。
	defaultProbeTimeout = 1500 * time.Millisecond
	// defaultProbingWindow 是注册后等待 available 的总窗口；窗口耗尽判
	// unavailable（典型场景：pi 未装 amagi 插件，端口永不就绪）。
	defaultProbingWindow = 45 * time.Second
	// endedFailThreshold 是 available 之后判定 ended 所需的连续失败次数
	//（§7.4：连接失败持续超过探测窗口；§10 建议 ≥2 次间隔探测）。
	endedFailThreshold = 2
)

// Service 是 webui 壳集成的 Wails 绑定服务。
type Service struct {
	log         *logging.Service
	registryDir string
	client      *http.Client

	mu       sync.Mutex
	trackers map[string]*tracker

	// 测试 seam：缩小窗口/超时以编排状态机；生产取默认值。
	probingWindow time.Duration
	now           func() time.Time
}

// NewService 创建 webui 服务。registryDir 为契约 §7.3 注册表目录
// （~/.pi/agent/amagi/webui-registry）。
func NewService(log *logging.Service, registryDir string) *Service {
	return &Service{
		log:           log,
		registryDir:   registryDir,
		client:        &http.Client{Timeout: defaultProbeTimeout},
		trackers:      make(map[string]*tracker),
		probingWindow: defaultProbingWindow,
		now:           time.Now,
	}
}

// RegisterSession 注册一个 embedded pi 会话（app.go LaunchPiSession 在 PTY
// 启动成功后调用；不暴露给前端）。injectedPort<=0 表示端口分配/注入失败，
// 发现完全走注册表回退通道；token 为 AMAGI_WEBUI_TOKEN 注入值（v1.0.2），
// 空串表示未注入（探测时从注册表条目补读 token）。
func (s *Service) RegisterSession(sessionID string, pid, injectedPort int, token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trackers[sessionID] = &tracker{
		pid:          pid,
		injectedPort: injectedPort,
		token:        token,
		state:        StateProbing,
		port:         injectedPort,
		registeredAt: s.now(),
	}
}

// Invalidate 在会话退出时将状态落为 ended（app.go 退出 goroutine 调用）。
func (s *Service) Invalidate(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.trackers[sessionID]; ok && t.state != StateEnded {
		t.state = StateEnded
	}
}

// RemoveSession 彻底移除 tracker（会话被 Remove/批量清理时调用）。
// 进行中的探测轮在提交点发现 tracker 已摘除后丢弃迟到结果。
func (s *Service) RemoveSession(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.trackers, sessionID)
}

// GetWebUIStatus 返回缓存的会话 webui 状态（非阻塞，不发起探测）。
// 未注册的会话返回 unknown。
func (s *Service) GetWebUIStatus(sessionID string) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return statusOf(s.trackers[sessionID])
}

// ProbeWebUI 执行一轮探测并返回最新状态。前端按契约 §4.1 建议的
// 0.5–1s 节奏轮询；available 后转低频保活（跟随 /resume 等会话切换），
// unavailable / ended 后停止轮询。
// 网络 I/O 在全局锁外执行（Major3：不阻塞其他会话的状态读写）。
func (s *Service) ProbeWebUI(sessionID string) Status {
	s.mu.Lock()
	t, ok := s.trackers[sessionID]
	if !ok {
		s.mu.Unlock()
		return Status{State: StateUnknown}
	}
	if t.state == StateEnded {
		st := statusOf(t)
		s.mu.Unlock()
		return st
	}
	s.mu.Unlock()

	s.probe(sessionID, t)

	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.trackers[sessionID]; ok {
		return statusOf(cur)
	}
	// 探测期间会话被 Remove：按未注册上报（前端会停止轮询）。
	return Status{State: StateUnknown}
}

// OpenWebPlane 返回可加载的 Web 平面 URL（契约 v1.0.2 §6.5：
// ${httpBase}/#/t=<token>）。未 available 时先补一轮探测再判定；仍不可用
// 则返回错误。
func (s *Service) OpenWebPlane(sessionID string) (string, error) {
	s.mu.Lock()
	t, ok := s.trackers[sessionID]
	if !ok {
		s.mu.Unlock()
		return "", fmt.Errorf("webui: session %s 未注册 webui 发现（非 embedded pi 会话）", sessionID)
	}
	if t.state == StateEnded {
		s.mu.Unlock()
		return "", fmt.Errorf("webui: session %s 的 webui 已结束", sessionID)
	}
	needProbe := t.state != StateAvailable
	s.mu.Unlock()

	if needProbe {
		s.probe(sessionID, t)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.trackers[sessionID]
	if !ok {
		return "", fmt.Errorf("webui: session %s 已被移除", sessionID)
	}
	if cur.state != StateAvailable {
		return "", fmt.Errorf("webui: session %s 的 webui 当前不可用（state=%s）", sessionID, cur.state)
	}
	return statusOf(cur).URL, nil
}

// probeSnapshot 是执行网络 I/O 前对 tracker 的标量快照。
type probeSnapshot struct {
	pid         int
	token       string
	piSessionID string
	port        int
}

// probeResult 是一轮探测（锁外 I/O）的结论。
type probeResult int

const (
	resultFailed   probeResult = iota // 全部通道失败（不可达/校验不通过）
	resultNotReady                    // 注入端口 503 且 v/pid 校验通过：目标服务未就绪
	resultAdopted                     // 采纳：port/info/token 有效
)

// probe 执行一轮探测：快照（锁内）→ 网络 I/O（锁外）→ 提交（锁内）。
func (s *Service) probe(sessionID string, t *tracker) {
	t.probeMu.Lock()
	defer t.probeMu.Unlock()

	s.mu.Lock()
	if s.trackers[sessionID] != t || t.state == StateEnded {
		s.mu.Unlock()
		return
	}
	snap := probeSnapshot{
		pid:         t.pid,
		token:       t.token,
		piSessionID: t.piSessionID,
		port:        t.port,
	}
	client := s.client
	registryDir := s.registryDir
	s.mu.Unlock()

	result, port, info, token := s.runProbe(client, registryDir, &snap)

	s.mu.Lock()
	defer s.mu.Unlock()
	// 终态栅栏：探测期间 Invalidate 可已置 ended；迟到结果不得复活。
	if s.trackers[sessionID] != t || t.state == StateEnded {
		return
	}
	switch result {
	case resultAdopted:
		s.adoptLocked(t, port, info, token)
	case resultNotReady:
		// 保活探测（available 后）遇瞬时 503（会话切换/服务重建窗口的
		// ready=false，pid 已校验确属目标进程）：不降级——unavailable/ended
		// 只由持续不可达（failStreak）或会话退出（Invalidate）决定。
		if t.state == StateAvailable {
			return
		}
		s.markStillProbingLocked(t)
	default:
		// 全部通道失败。
		if t.state == StateAvailable {
			// §7.4 ended 判定：连续失败达阈值才落 ended（bye/WS 不作依据）。
			t.failStreak++
			if t.failStreak >= endedFailThreshold {
				t.state = StateEnded
				s.log.Info("webui", "webui 探测持续失败，判定 ended", "session="+sessionID)
			}
			return
		}
		s.markStillProbingLocked(t)
	}
}

// runProbe 执行锁外网络 I/O：先注入端口，失败回退注册表扫描（§7.2 双通道）。
// 每个候选独立超时（Major3）。返回采纳结论与生效 token。
func (s *Service) runProbe(client *http.Client, registryDir string, snap *probeSnapshot) (probeResult, int, *Info, string) {
	// 通道 1：注入端口探测。tokenProven（token 非空）时 200 已证归属，
	// validateAdoption 豁免 pid 等值（Windows shell-attach：node pid ≠ shell pid）。
	if snap.port > 0 {
		switch info, outcome := probeCandidate(client, snap.port, snap.token, snap.pid); outcome {
		case probeReady:
			if validateAdoption(snap, info, snap.port, snap.token != "") {
				return resultAdopted, snap.port, info, snap.token
			}
			// 强校验不通过：端口被复用/错位，转注册表回退（§7.3 陈旧条目同规则）。
			s.log.Warn("webui", "注入端口探测未通过强校验，转注册表回退",
				fmt.Sprintf("port=%d wantPid=%d wantSession=%s gotPid=%d gotSession=%s gotPort=%d",
					snap.port, snap.pid, snap.piSessionID, info.PID, info.SessionID, info.Port))
		case probeNotReady:
			// v/pid 校验通过的 503：确属目标服务但未就绪，保持 probing（Major3：
			// 不再把任意 503 当作未就绪积压等待）。
			return resultNotReady, 0, nil, ""
		}
	}

	// 通道 2：注册表回退扫描。匹配键：已学到的 piSessionID 精确匹配优先，
	// 失配但 pid 一致也采纳（/resume 等会话切换后扩展覆盖写条目，sessionId
	// 演进；per-pid 注册表天然唯一）；每个候选逐探测验证，失败即淘汰
	//（§7.3：权威判定永远是探测）；探测失败/校验不过的候选 best-effort
	// 删除其注册文件（陈旧条目清理）。
	for _, e := range scanRegistry(registryDir) {
		// 注入端口已试过则跳过；例外：未携带注入 token 且本条目带 token 时
		// 同端口也重试（独立终端场景：首次空 token 探测 403，需从条目补读）。
		if e.Port == snap.port && snap.port > 0 && !(snap.token == "" && e.Token != "") {
			continue
		}
		pidMatch := snap.pid > 0 && e.PID == snap.pid
		// token 相等入围（Windows shell-attach：条目 pid 是 node pid，与
		// tracker 的 shell pid 必然失配；注入 token 与条目 token 一致即归属）。
		tokenMatch := snap.token != "" && e.Token == snap.token
		if snap.piSessionID != "" {
			// 精确匹配优先；失配但 pid 一致（会话切换后条目覆盖写）或 token
			// 相等也入围，最终归属由 validateAdoption 强校验裁决。
			if e.SessionID != snap.piSessionID && !pidMatch && !tokenMatch {
				continue
			}
		} else if !pidMatch && !tokenMatch {
			continue
		}
		// v1.0.2：注入 token 优先；未注入时从注册表条目补读（独立终端场景）。
		token := snap.token
		if token == "" {
			token = e.Token
		}
		if info, outcome := probeCandidate(client, e.Port, token, snap.pid); outcome == probeReady {
			if validateAdoption(snap, info, e.Port, token != "") {
				return resultAdopted, e.Port, info, token
			}
		}
		// 探测失败/校验不通过 → 淘汰该候选，继续下一个；best-effort 删除注册文件。
		if e.file != "" {
			_ = os.Remove(e.file)
		}
	}

	return resultFailed, 0, nil, ""
}

// probeCandidate 对单候选执行一次带独立超时的探测（Major3：每候选独立预算）。
func probeCandidate(client *http.Client, port int, token string, expectPID int) (*Info, probeOutcome) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultProbeTimeout)
	defer cancel()
	return probeInfo(ctx, client, port, token, expectPID)
}

// validateAdoption 是采纳强校验（Major2；resume 修订：粘性键放宽为 pid；
// Windows shell-attach 修订：token 即身份）：
//   - sessionId 非空 + info.port==候选端口，齐备才采纳；
//   - pid 校验：token 探测（tokenProven，探测所用 token 非空且得到 200）时
//     豁免——注入的 AMAGI_WEBUI_TOKEN 是壳与扩展间的共享密钥，/api/info
//     受 capability 保护（错误 token → 401/403 不可达），携正确 token 的
//     200 已证明服务归属；且 Windows 内嵌 pi 走 BootstrapShellAttach
//     （ConPTY 只起 shell，pi 命令经输入流注入），PTY pid 是 shell pid
//     而非 node pid，pid 等值在该架构下必然失配。token 为空
//     （legacy/独立终端未注入）时维持 pid 防线（防端口被其他进程复用）；
//   - 粘性复核：sessionId 演进（/resume 等）视为合法切换，由 adoptLocked
//     跟随更新。
func validateAdoption(snap *probeSnapshot, info *Info, candidatePort int, tokenProven bool) bool {
	if info == nil || info.SessionID == "" {
		return false
	}
	if !tokenProven && snap.pid > 0 && info.PID != snap.pid {
		return false
	}
	if candidatePort > 0 && info.Port != candidatePort {
		return false
	}
	return true
}

// adoptLocked 采纳一次成功探测：记录端口/sessionId/token 并置 available。
// 调用方必须持有 s.mu。
func (s *Service) adoptLocked(t *tracker, port int, info *Info, token string) {
	// 会话切换（/resume、/new、fork、reload）：pid 不变而 sessionId 演进，
	// 属同进程内的合法切换而非身份漂移，跟随更新。
	if old := t.piSessionID; old != "" && old != info.SessionID {
		s.log.Info("webui", "webui 会话切换",
			fmt.Sprintf("port=%d piSession=%s -> %s", port, old, info.SessionID))
	}
	t.piSessionID = info.SessionID
	t.port = port
	if token != "" {
		t.token = token
	}
	t.failStreak = 0
	if t.state != StateAvailable {
		s.log.Info("webui", "webui 探测 available",
			fmt.Sprintf("port=%d piSession=%s", port, info.SessionID))
	}
	t.state = StateAvailable
}

// markStillProbingLocked 处理"尚未 available"：窗口内保持 probing，
// 窗口耗尽落 unavailable（A-4：前端据此隐藏切换控件）。调用方必须持有 s.mu。
func (s *Service) markStillProbingLocked(t *tracker) {
	if s.now().Sub(t.registeredAt) > s.probingWindow {
		if t.state != StateUnavailable {
			s.log.Info("webui", "webui 探测窗口耗尽，判定 unavailable（未装插件或服务未起）")
		}
		t.state = StateUnavailable
		return
	}
	t.state = StateProbing
}

// statusOf 构造状态快照。available 且 token 已知时 URL 携带 v1.0.2 fragment
// （#/t=<token>，供 sandbox 页面凭 fragment 取 token 调用 API）。
func statusOf(t *tracker) Status {
	if t == nil {
		return Status{State: StateUnknown}
	}
	st := Status{State: t.state}
	if t.state == StateAvailable && t.port > 0 {
		st.Port = t.port
		if t.token != "" {
			st.URL = fmt.Sprintf("http://127.0.0.1:%d/#/t=%s", t.port, t.token)
		} else {
			st.URL = fmt.Sprintf("http://127.0.0.1:%d/", t.port)
		}
	}
	return st
}
