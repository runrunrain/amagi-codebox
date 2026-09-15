package main

// launch_executor.go — Production launchplan.Executor that prepares and applies
// typed Effects for all five CLIs. Prepare pre-allocates all fallible material;
// Apply performs side effects in canonical order. Abort does exact reverse
// compensation with CAS-validated config rollback.
//
// Design (composite-commit-addendum.md §4.4, §8.1):
//   - The root package implements the Executor port; the effect loop stays in
//     internal/remote (via gate.DoLaunchEffect).
//   - Each PreparedEffect captures its compensation capability during Prepare;
//     ArmOwnership makes Abort sufficient before the first syscall.
//   - process effect returns processcap.StartEvidence for exact registry binding.
//   - M-01: SharedStartSpec carries and verifies exact upstream URL + listen port.
//   - M-04: config mutation Apply records written digest; Abort only restores
//     when the current file still matches the written digest (CAS).
//   - C-01: PTY ready and bootstrap are real, exact-binding prepared effects.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"amagi-codebox/internal/headroom"
	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/processcap"
	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/session"
	"gopkg.in/yaml.v3"
)

// launchExecutorDeps holds the App service adapters the executor needs.
type launchExecutorDeps struct {
	pty           ptyStartPort
	headroom      headroomStartPort
	codexHeadroom headroomStartPort
	sharedCoord   *remote.SharedServiceCoordinator
	debts         *launchplan.CompensationDebtRegistry
	configMu      *sync.Mutex
	// webui 是 remote 创建/重启 embedded pi 会话的 Web 平面接线（remote-
	// webui-plane：对齐桌面 LaunchPiSession T-1.5）。nil = 未接线（测试），
	// pi 会话照常启动、只是不做 webui 注入与 tracker 注册。
	webui webuiLaunchPlane
}

// webuiLaunchPlane 是 remote 启动链与 internal/webui 的窄接口切片：
// PrepareEnv 分配 AMAGI_WEBUI_PORT / AMAGI_WEBUI_TOKEN 注入材料（失败返回
// 零值不阻断启动——扩展自选端口写注册表，探测侧走注册表回退发现）；
// RegisterSession 在启动 commit 后发布 per-session 探测 tracker。
type webuiLaunchPlane interface {
	PrepareEnv() (port int, token string)
	RegisterSession(sessionID string, pid, port int, token string)
}

type ptyStartPort interface {
	StartResolvedWithRunEvidence(sessionID string, spec platform.ResolvedLaunchSpec, runHandle any) (processcap.StartEvidence, error)
	WaitReadyForBinding(ctx context.Context, sessionID string, bindingID processcap.BindingID) error
	WriteRawForBinding(ctx context.Context, sessionID string, bindingID processcap.BindingID, data []byte) error
}

type headroomStartPort interface {
	IsRunning() bool
	GetStatus() headroom.HeadroomStatus
	SetPort(port int) error
	Start(backendURL string) error
	StartForOpenAI(backendURL string) error
	Stop() error
}

// appLaunchExecutor implements launchplan.Executor.
type appLaunchExecutor struct {
	deps launchExecutorDeps
}

func newAppLaunchExecutor(deps launchExecutorDeps) *appLaunchExecutor {
	return &appLaunchExecutor{deps: deps}
}

func (e *appLaunchExecutor) Prepare(_ context.Context, plan *launchplan.Plan, binding launchplan.ExecutionBinding) (launchplan.PreparedExecution, error) {
	if plan == nil {
		return nil, errors.New("launch executor: nil plan")
	}
	exec := &appPreparedExecution{
		plan:      plan,
		sessionID: binding.SessionID,
		runHandle: binding.RunHandle,
		effects:   make([]launchplan.PreparedEffect, len(plan.Effects)),
		applied:   make([]bool, len(plan.Effects)),
	}
	builder := &preparedEffectBuilder{
		deps:             e.deps,
		plan:             plan,
		sessionID:        binding.SessionID,
		runEpoch:         binding.RunEpoch,
		runHandle:        binding.RunHandle,
		sharedAdmissions: binding.SharedAdmissions,
	}
	for i, spec := range plan.Effects {
		effect, err := builder.build(spec)
		if err != nil {
			exec.Abort(context.Background())
			return nil, fmt.Errorf("launch executor: prepare effect %d: %w", i, err)
		}
		exec.effects[i] = effect
	}
	return exec, nil
}

// ---------------------------------------------------------------------------
// PreparedEffect builder
// ---------------------------------------------------------------------------

type preparedEffectBuilder struct {
	deps             launchExecutorDeps
	plan             *launchplan.Plan
	sessionID        string
	runEpoch         uint64
	runHandle        any
	sharedAdmissions map[launchplan.SharedServiceKind]any
	ptyProcess       *ptyStartEffect
}

func (b *preparedEffectBuilder) build(spec launchplan.EffectSpec) (launchplan.PreparedEffect, error) {
	switch spec.Kind {
	case launchplan.EffectHeadroomStart:
		if spec.Shared == nil {
			return nil, errors.New("headroom effect missing shared spec")
		}
		return b.buildHeadroomStart(spec.Shared), nil
	case launchplan.EffectConfigMutation:
		if spec.Config == nil {
			return nil, errors.New("config effect missing config spec")
		}
		return b.buildConfigMutation(spec.Config)
	case launchplan.EffectPTYStart:
		if spec.Process == nil {
			return nil, errors.New("process effect missing process spec")
		}
		effect, err := b.buildPTYStart(spec.Process)
		if err == nil {
			b.ptyProcess = effect
		}
		return effect, err
	case launchplan.EffectBootstrapWrite:
		if spec.Bootstrap == nil {
			return nil, errors.New("bootstrap effect missing spec")
		}
		if b.ptyProcess == nil {
			return nil, errors.New("bootstrap effect has no preceding PTY effect")
		}
		return &bootstrapWriteEffect{deps: b.deps, sessionID: b.sessionID, command: spec.Bootstrap.StartupCommand, process: b.ptyProcess}, nil
	default:
		return nil, fmt.Errorf("unsupported effect kind: %d", spec.Kind)
	}
}

func (b *preparedEffectBuilder) buildHeadroomStart(shared *launchplan.SharedStartSpec) launchplan.PreparedEffect {
	return &headroomStartEffect{
		deps: b.deps, service: shared.Service, upstreamURL: shared.UpstreamURL, listenPort: shared.ListenPort,
		admission: sharedAdmissionFromBinding(b.sharedAdmissions, shared.Service),
	}
}

func sharedAdmissionFromBinding(admissions map[launchplan.SharedServiceKind]any, kind launchplan.SharedServiceKind) *remote.SharedLaunchAdmission {
	if admissions == nil {
		return nil
	}
	admission, _ := admissions[kind].(*remote.SharedLaunchAdmission)
	return admission
}

func (b *preparedEffectBuilder) buildConfigMutation(cfg *launchplan.ConfigMutationSpec) (launchplan.PreparedEffect, error) {
	content, ok := b.plan.Secrets.Buffer(cfg.Candidate)
	if !ok {
		return nil, errors.New("config effect: candidate buffer not found")
	}
	owned := append([]byte(nil), content...)
	return &configMutationEffect{
		target: cfg.Target, content: owned, preimage: cfg.ExpectedPreimageDigest,
		owner: fmt.Sprintf("%s/%d/config/%d", b.sessionID, b.runEpoch, cfg.Target),
		debts: b.deps.debts, configMu: b.deps.configMu,
	}, nil
}

func (b *preparedEffectBuilder) buildPTYStart(proc *launchplan.ProcessStartSpec) (*ptyStartEffect, error) {
	if proc.Mode != launchplan.ModeEmbedded {
		return nil, errors.New("process effect: non-embedded mode not supported")
	}
	if proc.RequireRunHandle && b.runHandle == nil {
		return nil, errors.New("process effect: run handle required but not provided")
	}
	spec := proc.Resolved
	// remote-webui-plane（对齐桌面 LaunchPiSession T-1.5）：remote v1 创建/
	// 重启的 embedded pi 会话注入 webui 端口与 v1.0.2 capability token env，
	// 否则 /session/{id}/webui 恒为 unknown、Web 平面视图无从出现。仅 pi
	//（与桌面口径一致，omp 不注入）；分配失败不阻断启动——扩展自选端口写
	// 注册表，探测侧走注册表回退发现。
	var webuiPort int
	var webuiToken string
	webuiPlane := b.deps.webui != nil && spec.AppType == string(session.AppTypePi)
	if webuiPlane {
		webuiPort, webuiToken = b.deps.webui.PrepareEnv()
		entries := make([]string, 0, 2)
		if webuiPort > 0 {
			entries = append(entries, "AMAGI_WEBUI_PORT="+strconv.Itoa(webuiPort))
		}
		if webuiToken != "" {
			entries = append(entries, "AMAGI_WEBUI_TOKEN="+webuiToken)
		}
		if len(entries) > 0 {
			spec.Env.Variables = overrideWebUIEnv(spec.Env.Variables, entries)
		}
	}
	return &ptyStartEffect{
		deps:       b.deps,
		sessionID:  b.sessionID,
		spec:       spec,
		runHandle:  b.runHandle,
		webuiPlane: webuiPlane,
		webuiPort:  webuiPort,
		webuiToken: webuiToken,
	}, nil
}

// ---------------------------------------------------------------------------
// Headroom start effect (M-01: uses SharedStartSpec upstream/port)
// ---------------------------------------------------------------------------

type headroomStartEffect struct {
	deps        launchExecutorDeps
	service     launchplan.SharedServiceKind
	upstreamURL string
	listenPort  int
	startedByMe bool
	committed   bool
	admission   *remote.SharedLaunchAdmission
}

func (e *headroomStartEffect) Kind() launchplan.EffectKind { return launchplan.EffectHeadroomStart }
func (e *headroomStartEffect) ArmOwnership()               {}

func (e *headroomStartEffect) Apply(_ context.Context) (launchplan.EffectEvidence, error) {
	if e.listenPort <= 0 {
		return launchplan.EffectEvidence{}, errors.New("headroom listen port is invalid")
	}
	if e.upstreamURL == "" {
		return launchplan.EffectEvidence{}, errors.New("headroom upstream URL is empty")
	}
	var svc headroomStartPort
	var start func() error
	switch e.service {
	case launchplan.SharedClaudeHeadroom:
		svc = e.deps.headroom
		start = func() error { return svc.Start(e.upstreamURL) }
	case launchplan.SharedCodexHeadroom:
		svc = e.deps.codexHeadroom
		start = func() error { return svc.StartForOpenAI(e.upstreamURL) }
	default:
		return launchplan.EffectEvidence{}, fmt.Errorf("unknown headroom service: %d", e.service)
	}
	if svc == nil {
		return launchplan.EffectEvidence{}, errors.New("headroom service unavailable")
	}
	if svc.IsRunning() {
		status := svc.GetStatus()
		if !status.Running || status.Port != e.listenPort || status.BackendURL != e.upstreamURL {
			return launchplan.EffectEvidence{}, errors.New("running headroom configuration does not match launch plan")
		}
		return launchplan.EffectEvidence{}, nil
	}
	if err := svc.SetPort(e.listenPort); err != nil {
		return launchplan.EffectEvidence{}, fmt.Errorf("set headroom port: %w", err)
	}
	if err := start(); err != nil {
		return launchplan.EffectEvidence{}, fmt.Errorf("start headroom: %w", err)
	}
	e.startedByMe = true
	if e.deps.sharedCoord == nil || e.admission == nil {
		_ = svc.Stop()
		e.startedByMe = false
		return launchplan.EffectEvidence{}, errors.New("headroom start transaction is not coordinator-owned")
	}
	if err := e.deps.sharedCoord.MarkLaunchTransactionStarted(e.admission); err != nil {
		return launchplan.EffectEvidence{}, fmt.Errorf("record headroom start ownership: %w", err)
	}
	status := svc.GetStatus()
	if !status.Running || status.Port != e.listenPort || status.BackendURL != e.upstreamURL {
		if e.deps.sharedCoord.AuthorizeCompensatingStop(e.admission) {
			_ = svc.Stop()
		}
		e.startedByMe = false
		return launchplan.EffectEvidence{}, errors.New("started headroom configuration failed exact verification")
	}
	return launchplan.EffectEvidence{}, nil
}

func (e *headroomStartEffect) compensate(_ context.Context) bool {
	if !e.startedByMe || e.committed || e.deps.sharedCoord == nil || !e.deps.sharedCoord.AuthorizeCompensatingStop(e.admission) {
		return true
	}
	var err error
	switch e.service {
	case launchplan.SharedClaudeHeadroom:
		if e.deps.headroom != nil {
			err = e.deps.headroom.Stop()
		}
	case launchplan.SharedCodexHeadroom:
		if e.deps.codexHeadroom != nil {
			err = e.deps.codexHeadroom.Stop()
		}
	}
	return err == nil
}

// ---------------------------------------------------------------------------
// Config mutation effect (M-04: exact CAS rollback)
// ---------------------------------------------------------------------------

type configMutationEffect struct {
	mu            sync.Mutex
	target        launchplan.ConfigTarget
	content       []byte
	atomicWrite   func(string, []byte, os.FileMode, os.FileMode) (atomicConfigWriteResult, error)
	preimage      [32]byte
	owner         string
	debts         *launchplan.CompensationDebtRegistry
	path          string
	written       bool
	writtenDigest [32]byte
	prevExisted   bool
	prevMode      os.FileMode
	prevContent   []byte
	committed     bool
	configMu      *sync.Mutex
}

type atomicConfigWriteResult struct {
	Replaced bool
	Step     string
}

type configIOError struct {
	step string
	err  error
}

func (e *configIOError) Error() string { return "config " + e.step + ": " + e.err.Error() }
func (e *configIOError) Unwrap() error { return e.err }

func (e *configMutationEffect) Kind() launchplan.EffectKind { return launchplan.EffectConfigMutation }
func (e *configMutationEffect) ArmOwnership()               {}

func (e *configMutationEffect) Apply(ctx context.Context) (launchplan.EffectEvidence, error) {
	configLocked := false
	if e.configMu != nil {
		e.configMu.Lock()
		configLocked = true
	}
	defer func() {
		if configLocked {
			e.configMu.Unlock()
		}
	}()
	path, candidate, mode, dirMode, err := e.renderCandidate()
	if err != nil {
		return launchplan.EffectEvidence{}, err
	}
	e.path = path
	if err := e.recordPrevious(path); err != nil {
		return launchplan.EffectEvidence{}, err
	}
	actualPreimage := [32]byte{}
	if e.prevExisted {
		actualPreimage = sha256.Sum256(e.prevContent)
	}
	if actualPreimage != e.preimage {
		return launchplan.EffectEvidence{}, errors.New("config preimage mismatch: file changed since planning")
	}
	if e.target == launchplan.ConfigCodex {
		candidate, err = e.renderCodexCandidate(e.prevContent, path)
		if err != nil {
			return launchplan.EffectEvidence{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return launchplan.EffectEvidence{}, err
	}
	result, writeErr := e.writeAtomic(path, candidate, mode, dirMode)
	if result.Replaced {
		e.written = true
		e.writtenDigest = sha256.Sum256(candidate)
	}
	if configLocked {
		e.configMu.Unlock()
		configLocked = false
	}
	if writeErr != nil {
		if e.written {
			outcome := e.compensate(ctx)
			if outcome.Disposition != launchplan.CompensationConfirmed {
				return launchplan.EffectEvidence{}, errors.Join(writeErr, errors.New("config apply compensation unresolved"))
			}
		}
		return launchplan.EffectEvidence{}, writeErr
	}
	return launchplan.EffectEvidence{}, nil
}

func (e *configMutationEffect) renderCandidate() (path string, candidate []byte, mode, dirMode os.FileMode, err error) {
	switch e.target {
	case launchplan.ConfigPi:
		path = filepath.Join(defaultPiAgentDir(), "models.json")
		var cfg map[string]any
		if err = json.Unmarshal(e.content, &cfg); err != nil {
			return "", nil, 0, 0, fmt.Errorf("unmarshal pi config: %w", err)
		}
		candidate, err = json.MarshalIndent(cfg, "", "  ")
		candidate = append(candidate, '\n')
		return path, candidate, 0o600, 0o700, err
	case launchplan.ConfigOmp:
		path = filepath.Join(defaultOmpAgentDir(), "models.yml")
		var cfg map[string]any
		if err = json.Unmarshal(e.content, &cfg); err != nil {
			return "", nil, 0, 0, fmt.Errorf("unmarshal omp config: %w", err)
		}
		candidate, err = yaml.Marshal(cfg)
		return path, candidate, 0o600, 0o700, err
	case launchplan.ConfigCodex:
		codexHome, homeErr := platform.CodexHomeDir(os.Environ())
		if homeErr != nil {
			return "", nil, 0, 0, fmt.Errorf("get Codex home: %w", homeErr)
		}
		path = filepath.Join(codexHome, "config.toml")
		return path, nil, 0o644, 0o755, nil
	default:
		return "", nil, 0, 0, fmt.Errorf("unsupported config target: %d", e.target)
	}
}

func (e *configMutationEffect) renderCodexCandidate(previous []byte, path string) ([]byte, error) {
	parts := strings.SplitN(string(e.content), "\n", 2)
	model := strings.TrimSpace(parts[0])
	if model == "" {
		return nil, errors.New("codex config: empty model")
	}
	opts := codexConfigSyncOptions{Model: model, CleanupManagedConfig: true}
	if len(parts) > 1 && strings.TrimSpace(parts[1]) != "" {
		opts = codexConfigSyncOptions{
			Model: model, ModelProvider: codexModelProviderName,
			ProviderBaseURL: strings.TrimSpace(parts[1]), EnsureCustomProvider: true, ForceAPILogin: true,
		}
	}
	return renderCodexConfig(previous, path, opts)
}

func (e *configMutationEffect) recordPrevious(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		e.prevExisted = false
		e.prevMode = 0
		e.prevContent = nil
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat config preimage: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("config target is not a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config preimage: %w", err)
	}
	e.prevExisted = true
	e.prevMode = info.Mode().Perm()
	e.prevContent = data
	return nil
}

type atomicConfigFile interface {
	Name() string
	Chmod(os.FileMode) error
	Write([]byte) (int, error)
	Sync() error
	Close() error
}

type atomicConfigOperations interface {
	MkdirAll(string, os.FileMode) error
	Chmod(string, os.FileMode) error
	CreateTemp(string, string) (atomicConfigFile, error)
	Rename(string, string) error
	Remove(string) error
	SyncDirectory(string) error
}

type osAtomicConfigOperations struct{}

func (osAtomicConfigOperations) MkdirAll(path string, mode os.FileMode) error {
	return os.MkdirAll(path, mode)
}
func (osAtomicConfigOperations) Chmod(path string, mode os.FileMode) error {
	return os.Chmod(path, mode)
}
func (osAtomicConfigOperations) CreateTemp(dir, pattern string) (atomicConfigFile, error) {
	return os.CreateTemp(dir, pattern)
}
func (osAtomicConfigOperations) Rename(oldPath, newPath string) error {
	return os.Rename(oldPath, newPath)
}
func (osAtomicConfigOperations) Remove(path string) error { return os.Remove(path) }
func (osAtomicConfigOperations) SyncDirectory(path string) error {
	return platform.SyncConfigDirectory(path)
}

func writeAtomicConfig(path string, content []byte, mode, dirMode os.FileMode) (atomicConfigWriteResult, error) {
	return writeAtomicConfigWithOperations(path, content, mode, dirMode, osAtomicConfigOperations{})
}

func writeAtomicConfigWithOperations(path string, content []byte, mode, dirMode os.FileMode, ops atomicConfigOperations) (result atomicConfigWriteResult, retErr error) {
	dir := filepath.Dir(path)
	if err := ops.MkdirAll(dir, dirMode); err != nil {
		return result, &configIOError{step: "mkdir", err: err}
	}
	if err := ops.Chmod(dir, dirMode); err != nil {
		return result, &configIOError{step: "chmod-directory", err: err}
	}
	tmp, err := ops.CreateTemp(dir, "."+filepath.Base(path)+".amagi-*")
	if err != nil {
		return result, &configIOError{step: "create-temp", err: err}
	}
	tmpPath := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		_ = ops.Remove(tmpPath)
	}()
	if err := tmp.Chmod(mode); err != nil {
		return result, &configIOError{step: "chmod-temp", err: err}
	}
	if _, err := tmp.Write(content); err != nil {
		return result, &configIOError{step: "write-temp", err: err}
	}
	if err := tmp.Sync(); err != nil {
		return result, &configIOError{step: "sync-temp", err: err}
	}
	if err := tmp.Close(); err != nil {
		closed = true
		return result, &configIOError{step: "close-temp", err: err}
	}
	closed = true
	if err := ops.Rename(tmpPath, path); err != nil {
		return result, &configIOError{step: "rename", err: err}
	}
	result.Replaced = true
	result.Step = "rename"
	if err := ops.Chmod(path, mode); err != nil {
		return result, &configIOError{step: "chmod-target", err: err}
	}
	if err := ops.SyncDirectory(dir); err != nil {
		return result, &configIOError{step: "sync-directory", err: err}
	}
	return result, nil
}

func (e *configMutationEffect) writeAtomic(path string, content []byte, mode, dirMode os.FileMode) (atomicConfigWriteResult, error) {
	if e.atomicWrite != nil {
		return e.atomicWrite(path, content, mode, dirMode)
	}
	return writeAtomicConfig(path, content, mode, dirMode)
}

func (e *configMutationEffect) compensate(ctx context.Context) launchplan.CompensationOutcome {
	outcome := e.compensateAttempt(ctx)
	if e.debts != nil {
		e.debts.Record(outcome, e.compensateAttempt)
	}
	return outcome
}

func (e *configMutationEffect) compensateAttempt(ctx context.Context) launchplan.CompensationOutcome {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.configMu != nil {
		e.configMu.Lock()
		defer e.configMu.Unlock()
	}
	outcome := launchplan.CompensationOutcome{Owner: e.owner, Effect: launchplan.EffectConfigMutation, Step: "config-restore"}
	if e.committed || !e.written {
		outcome.Disposition = launchplan.CompensationConfirmed
		return outcome
	}
	if err := ctx.Err(); err != nil {
		outcome.Disposition = launchplan.CompensationUnavailable
		outcome.Message = err.Error()
		return outcome
	}
	current, err := os.ReadFile(e.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && !e.prevExisted {
			if syncErr := platform.SyncConfigDirectory(filepath.Dir(e.path)); syncErr != nil {
				outcome.Disposition = launchplan.CompensationUnavailable
				outcome.Message = syncErr.Error()
				return outcome
			}
			e.written = false
			outcome.Disposition = launchplan.CompensationConfirmed
			return outcome
		}
		outcome.Disposition = launchplan.CompensationIndeterminate
		outcome.Message = err.Error()
		return outcome
	}
	currentDigest := sha256.Sum256(current)
	preimageAlreadyRestored := e.prevExisted && currentDigest == sha256.Sum256(e.prevContent)
	if currentDigest != e.writtenDigest && !preimageAlreadyRestored {
		outcome.Disposition = launchplan.CompensationIndeterminate
		outcome.Message = "config CAS mismatch; external content retained"
		return outcome
	}
	if !e.prevExisted {
		if err := os.Remove(e.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			outcome.Disposition = launchplan.CompensationUnavailable
			outcome.Message = err.Error()
			return outcome
		}
		if err := platform.SyncConfigDirectory(filepath.Dir(e.path)); err != nil {
			outcome.Disposition = launchplan.CompensationUnavailable
			outcome.Message = err.Error()
			return outcome
		}
	} else {
		result, err := e.writeAtomic(e.path, e.prevContent, e.prevMode.Perm(), e.configDirMode())
		if err != nil {
			outcome.Disposition = launchplan.CompensationUnavailable
			if result.Replaced {
				outcome.Disposition = launchplan.CompensationIndeterminate
			}
			outcome.Message = err.Error()
			return outcome
		}
	}
	e.written = false
	zeroBytes(e.prevContent)
	e.prevContent = nil
	outcome.Disposition = launchplan.CompensationConfirmed
	return outcome
}

func (e *configMutationEffect) configDirMode() os.FileMode {
	if e.target == launchplan.ConfigCodex {
		return 0o755
	}
	return 0o700
}

func (e *configMutationEffect) hasPartialOwnership() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.written
}

func (e *configMutationEffect) markCommitted() {
	e.mu.Lock()
	e.committed = true
	e.mu.Unlock()
}

func (e *configMutationEffect) disposeSensitive() {
	e.mu.Lock()
	defer e.mu.Unlock()
	zeroBytes(e.content)
	e.content = nil
	// An unresolved debt retains its private retry preimage capability;
	// committed or confirmed effects no longer need that sensitive copy.
	if e.committed || !e.written {
		zeroBytes(e.prevContent)
		e.prevContent = nil
	}
}

// ---------------------------------------------------------------------------
// PTY start effect
// ---------------------------------------------------------------------------

type ptyStartEffect struct {
	deps      launchExecutorDeps
	sessionID string
	spec      platform.ResolvedLaunchSpec
	runHandle any
	start     processcap.StartEvidence
	applied   bool
	committed bool
	// remote-webui-plane：embedded pi 会话的 webui 注入材料（Prepare 阶段
	// 分配，随 spec.Env 注入进程；commit 后据此 RegisterSession）。
	// webuiPlane=false 表示非 pi 会话或未接线。
	webuiPlane bool
	webuiPort  int
	webuiToken string
}

func (e *ptyStartEffect) Kind() launchplan.EffectKind { return launchplan.EffectPTYStart }
func (e *ptyStartEffect) ArmOwnership()               {}

func (e *ptyStartEffect) Apply(ctx context.Context) (launchplan.EffectEvidence, error) {
	if e.deps.pty == nil {
		return launchplan.EffectEvidence{}, errors.New("PTY service unavailable")
	}
	start, err := e.deps.pty.StartResolvedWithRunEvidence(e.sessionID, e.spec, e.runHandle)
	if err != nil {
		return launchplan.EffectEvidence{}, fmt.Errorf("start pty: %w", err)
	}
	e.start = start
	e.applied = true
	if err := start.Validate(processcap.BackendPTY); err != nil {
		closeEvidence := start.Binding.CloseExact(context.Background())
		if closeEvidence.Confirmed() {
			e.applied = false
			return launchplan.EffectEvidence{}, fmt.Errorf("validate PTY start evidence: %w", err)
		}
		return launchplan.EffectEvidence{}, errors.Join(fmt.Errorf("validate PTY start evidence: %w", err), errors.New("exact PTY close is indeterminate"))
	}
	if err := e.deps.pty.WaitReadyForBinding(ctx, e.sessionID, start.Binding.BindingID()); err != nil {
		closeEvidence := start.Binding.CloseExact(context.Background())
		if closeEvidence.Confirmed() {
			e.applied = false
			return launchplan.EffectEvidence{}, fmt.Errorf("wait PTY ready: %w", err)
		}
		return launchplan.EffectEvidence{}, errors.Join(fmt.Errorf("wait PTY ready: %w", err), errors.New("exact PTY close is indeterminate"))
	}
	return launchplan.EffectEvidence{Process: &start}, nil
}

func (e *ptyStartEffect) compensate(ctx context.Context) bool {
	if !e.applied || e.committed {
		return true
	}
	if e.start.Binding == nil {
		return false
	}
	closeEvidence := e.start.Binding.CloseExact(ctx)
	if closeEvidence.Confirmed() {
		e.applied = false
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Bootstrap write effect (C-01: exact ready/live PTY write before publication)
// ---------------------------------------------------------------------------

type bootstrapWriteEffect struct {
	deps      launchExecutorDeps
	sessionID string
	command   string
	process   *ptyStartEffect
	applied   bool
	committed bool
}

func (e *bootstrapWriteEffect) Kind() launchplan.EffectKind { return launchplan.EffectBootstrapWrite }
func (e *bootstrapWriteEffect) ArmOwnership()               {}
func (e *bootstrapWriteEffect) Apply(ctx context.Context) (launchplan.EffectEvidence, error) {
	if e.command == "" {
		e.applied = true
		return launchplan.EffectEvidence{}, nil
	}
	if e.deps.pty == nil || e.process == nil || !e.process.applied || e.process.start.Binding == nil {
		return launchplan.EffectEvidence{}, errors.New("bootstrap has no live PTY binding")
	}
	binding := e.process.start.Binding
	bindingID := binding.BindingID()
	writeDone := make(chan error, 1)
	payload := []byte(e.command + "\r\n")
	go func() {
		writeDone <- e.deps.pty.WriteRawForBinding(ctx, e.sessionID, bindingID, payload)
	}()
	select {
	case err := <-writeDone:
		if err != nil {
			return launchplan.EffectEvidence{}, fmt.Errorf("write PTY bootstrap: %w", err)
		}
		e.applied = true
		return launchplan.EffectEvidence{}, nil
	case <-ctx.Done():
		closeEvidence := binding.CloseExact(context.Background())
		e.process.applied = false
		if !closeEvidence.Confirmed() {
			return launchplan.EffectEvidence{}, errors.Join(ctx.Err(), errors.New("bootstrap timeout exact close is indeterminate"))
		}
		return launchplan.EffectEvidence{}, fmt.Errorf("PTY bootstrap timed out: %w", ctx.Err())
	}
}

// ---------------------------------------------------------------------------
// PreparedExecution
// ---------------------------------------------------------------------------

type appPreparedExecution struct {
	plan         *launchplan.Plan
	sessionID    string
	runHandle    any
	effects      []launchplan.PreparedEffect
	applied      []bool
	processStart processcap.StartEvidence
	hasProcess   bool
	committed    bool
}

func (e *appPreparedExecution) Count() int                             { return len(e.effects) }
func (e *appPreparedExecution) Effect(i int) launchplan.PreparedEffect { return e.effects[i] }

func (e *appPreparedExecution) RecordApplied(i int, evidence launchplan.EffectEvidence) {
	if i < 0 || i >= len(e.applied) {
		return
	}
	e.applied[i] = true
	if evidence.Process != nil {
		e.processStart = *evidence.Process
		e.hasProcess = true
	}
}

func (e *appPreparedExecution) ProcessEvidence() (processcap.StartEvidence, bool) {
	return e.processStart, e.hasProcess
}

func (e *appPreparedExecution) Abort(ctx context.Context) launchplan.CompensationReport {
	report := launchplan.CompensationReport{}
	for i := len(e.effects) - 1; i >= 0; i-- {
		if !e.applied[i] && !effectHasPartialOwnership(e.effects[i]) {
			continue
		}
		report.Attempted++
		switch eff := e.effects[i].(type) {
		case *headroomStartEffect:
			if !eff.compensate(ctx) {
				report.Failed++
			}
		case *configMutationEffect:
			outcome := eff.compensate(ctx)
			report.Outcomes = append(report.Outcomes, outcome)
			if outcome.Disposition != launchplan.CompensationConfirmed {
				report.Failed++
			}
		case *ptyStartEffect:
			if !eff.compensate(ctx) {
				report.Failed++
			}
		case *bootstrapWriteEffect:
			// no compensation needed
		default:
			report.Failed++
		}
	}
	return report
}

func effectHasPartialOwnership(effect launchplan.PreparedEffect) bool {
	switch typed := effect.(type) {
	case *headroomStartEffect:
		return typed.startedByMe
	case *configMutationEffect:
		return typed.hasPartialOwnership()
	case *ptyStartEffect:
		return typed.applied
	default:
		return false
	}
}

// commitWebUIPlane 在启动 commit 后发布 webui tracker（对齐桌面
// LaunchPiSession：PTY 启动成功后 RegisterSession，供 /session/{id}/webui
// 探测与 /webui/{sid} 反向代理消费）。Abort 路径不会调用本方法，无残留
// tracker；注入失败（port=0/token=""）仍注册——走注册表回退发现。
func (e *ptyStartEffect) commitWebUIPlane() {
	if !e.webuiPlane || e.deps.webui == nil || e.start.PID <= 0 {
		return
	}
	e.deps.webui.RegisterSession(e.sessionID, e.start.PID, e.webuiPort, e.webuiToken)
}

// overrideWebUIEnv 以覆盖语义写入 AMAGI_WEBUI_* env 条目：宿主进程自身若
// 携带残留的 AMAGI_WEBUI_*（如 CodeBox 从 pi 会话内启动时继承），merge 后
// 直接 append 会产生重复键，改为先移除旧值再追加（与桌面 envOverrides 的
// 覆盖语义一致）。
func overrideWebUIEnv(vars []string, entries []string) []string {
	out := make([]string, 0, len(vars)+len(entries))
	for _, v := range vars {
		if strings.HasPrefix(v, "AMAGI_WEBUI_") {
			continue
		}
		out = append(out, v)
	}
	return append(out, entries...)
}

func (e *appPreparedExecution) MarkCommitted() {
	e.committed = true
	for _, eff := range e.effects {
		switch typed := eff.(type) {
		case *headroomStartEffect:
			typed.committed = true
		case *configMutationEffect:
			typed.markCommitted()
		case *ptyStartEffect:
			typed.committed = true
			typed.commitWebUIPlane()
		case *bootstrapWriteEffect:
			typed.committed = true
		}
	}
}

func (e *appPreparedExecution) DisposeSecrets() {
	if e.plan != nil && e.plan.Secrets != nil {
		e.plan.Secrets.Dispose()
	}
	for _, eff := range e.effects {
		switch typed := eff.(type) {
		case *ptyStartEffect:
			if typed.spec.Env.Variables != nil {
				for i := range typed.spec.Env.Variables {
					typed.spec.Env.Variables[i] = ""
				}
			}
		case *configMutationEffect:
			typed.disposeSensitive()
		}
	}
	e.runHandle = nil
}

func zeroBytes(data []byte) {
	for i := range data {
		data[i] = 0
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// Type assertion for the Executor interface.
var _ launchplan.Executor = (*appLaunchExecutor)(nil)
