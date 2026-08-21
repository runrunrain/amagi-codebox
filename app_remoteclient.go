package main

// app_remoteclient.go — 远程客户端 App 绑定层（蓝图《桌面端互联-技术实现
// 方案》§7 绑定表，任务 RC1-5）：
//
// 把 internal/remoteclient 四域（transport/hosts/pairing/sessions）转发给
// 前端。方法风格与 app.go 既有绑定一致：入参白名单校验交给域层，本层只做
// 生命周期编排与错误包装；ClientError 经 %w 包装后其文本携带稳定错误码
// （transport.go 单点映射），前端据码做恢复决策。
//
// 连接模型（蓝图 §13：多主机并发连接后置）：至多一条已连接宿主；Connect
// 成功即顶替既有连接（旧连接的 Transport 丢弃，凭据仅内存态、随之失效）。
// 会话域方法一律作用于当前已连接宿主，未连接返回明确错误。
//
// 凭据恢复（RC1-go域实现.md §4 交接）：Transport 凭据仅内存持有，进程重启
// 后由 RemoteClientConnect 入口完成恢复——登记簿 DeviceID → Keychain 条目
// codebox-remoteclient/<DeviceID> 取 secret → SetCredential。
//
// 退出不强制断连：Shutdown 无 remoteclient 钩子（REST 短连接无泄漏句柄；
// WS 长连接属 conn.go 后续波次，接入时再补收尾）。

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/remoteclient"
)

// remoteHostsRegistryFile 是主机登记簿落盘文件名（configDir 旁路，不进
// models.json——蓝图 §5/§8、D-T04）。
const remoteHostsRegistryFile = "remote-hosts.json"

// errRemoteClientUnavailable 是远程客户端域未完成初始化（登记簿/凭据存储
// 构造失败）时所有绑定方法的统一前置错误。
var errRemoteClientUnavailable = errors.New("remote client is not initialized (host registry or credential store unavailable)")

// remoteClientConnection 是当前已连接宿主的句柄：Transport（持有内存态设备
// 凭据）+ 由它派生的会话域客户端。rcMu 保护替换/读取。
type remoteClientConnection struct {
	hostID    string
	transport *remoteclient.Transport
	sessions  *remoteclient.SessionClient
}

// initRemoteClientRegistry 构造主机登记簿（NewApp 调用，纯文件读，不碰
// Keychain——wails generate module 会构建并运行一个实例化 NewApp 的临时
// 进程，任何阻塞型系统调用都会把绑定生成挂死）。装载失败降级为同路径空
// 登记簿（下次写盘重建文件）+ 启动警告，不阻断启动。
func (a *App) initRemoteClientRegistry(configDir string) {
	registryPath := filepath.Join(configDir, remoteHostsRegistryFile)
	registry, err := remoteclient.LoadHostRegistry(registryPath)
	if err != nil {
		a.Log.Warn("remoteclient", "主机登记簿装载失败，降级为空登记簿", err.Error())
		a.addStartupWarning("远程主机登记簿读取失败，已降级为空列表；下一次主机变更会覆盖原文件，如需保留请先修复 " + registryPath)
		registry = remoteclient.NewHostRegistry(registryPath)
	}
	a.rcRegistry = registry
}

// initRemoteClientServices 构造凭据存储与配对服务（Startup 调用，在
// a.Secrets.Load() 之后；真实 GUI 进程 Keychain 可用，绑定生成进程不会
// 走到这里）。复用 App.Secrets 单实例（DPAPI/Keychain 保护的 secrets 存储），
// 与提供商密钥同库不同条目（codebox-remoteclient/<DeviceID>，D-T04）。任何
// 失败都不阻断启动：置 nil，相关绑定方法返回 errRemoteClientUnavailable。
func (a *App) initRemoteClientServices() {
	creds, err := remoteclient.NewSecretsCredentialStoreWithService(a.Secrets)
	if err != nil {
		a.Log.Warn("remoteclient", "设备凭据存储不可用，配对/连接功能降级", err.Error())
		a.addStartupWarning("远程客户端凭据存储不可用：配对与连接功能暂不可用")
		return
	}
	a.rcCreds = creds

	pairing, err := remoteclient.NewPairingService(a.rcRegistry, a.rcCreds)
	if err != nil {
		a.Log.Warn("remoteclient", "配对服务构建失败，配对/移除主机功能降级", err.Error())
		return
	}
	a.rcPairing = pairing
}

// rcContext 返回远程客户端调用使用的 ctx：优先 Wails 生命周期 ctx（应用
// 退出时随之取消），未注入（测试/早期调用）时退回 Background。
func (a *App) rcContext() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

// rcDropConnection 在持 rcMu 的调用方处丢弃指定宿主的连接视图（hostID 空
// 表示无条件丢弃）。Transport 无本地资源句柄，置 nil 即断开；凭据仅内存态
// 随之失效。
func (a *App) rcDropConnection(hostID string) {
	if a.rcConn == nil {
		return
	}
	if hostID != "" && a.rcConn.hostID != hostID {
		return
	}
	a.rcConn.transport.ClearCredential()
	a.rcConn = nil
}

// rcSessions 返回当前已连接宿主的会话域客户端；未连接时返回明确错误。
func (a *App) rcSessions() (*remoteclient.SessionClient, error) {
	a.rcMu.Lock()
	defer a.rcMu.Unlock()
	if a.rcConn == nil {
		return nil, errors.New("remote client: no host is connected; call RemoteClientConnect first")
	}
	return a.rcConn.sessions, nil
}

// ---------------------------------------------------------------------------
// 登记簿 CRUD（蓝图 §7：RemoteClientListHosts/AddHost/UpdateHost/RenameHost/
// RemoveHost）
// ---------------------------------------------------------------------------

// RemoteClientListHosts 返回主机登记簿全部条目（恒为非 nil 数组）。
func (a *App) RemoteClientListHosts() []remoteclient.HostEntry {
	if a.rcRegistry == nil {
		return []remoteclient.HostEntry{}
	}
	return a.rcRegistry.List()
}

// RemoteClientAddHost 新增一条未配对主机条目（Health=probing；hostPort 白
// 名单校验与规范化在域层完成）。
func (a *App) RemoteClientAddHost(displayName, hostPort string) (remoteclient.HostEntry, error) {
	if a.rcRegistry == nil {
		return remoteclient.HostEntry{}, errRemoteClientUnavailable
	}
	entry, err := a.rcRegistry.Add(displayName, hostPort)
	if err != nil {
		return remoteclient.HostEntry{}, fmt.Errorf("add remote host: %w", err)
	}
	a.Log.Info("remoteclient", "新增主机登记", fmt.Sprintf("id=%s hostPort=%s", entry.ID, entry.HostPort))
	return entry, nil
}

// RemoteClientUpdateHost 修改目标地址。域层会重置配对态（地址变更后旧凭据
// 不可信）：本层同步清理旧 DeviceID 的 Keychain 凭据（尽力而为，失败仅告
// 警——地址已不可信，不回滚），并丢弃该主机的既有连接。
func (a *App) RemoteClientUpdateHost(hostID, hostPort string) error {
	if a.rcRegistry == nil {
		return errRemoteClientUnavailable
	}
	prev, ok := a.rcRegistry.Get(hostID)
	if !ok {
		return fmt.Errorf("update remote host: host %q not found", hostID)
	}
	if err := a.rcRegistry.UpdateHostPort(hostID, hostPort); err != nil {
		return fmt.Errorf("update remote host: %w", err)
	}
	if prev.DeviceID != "" && a.rcCreds != nil {
		if err := a.rcCreds.Delete(remoteclient.CredentialEntryName(prev.DeviceID)); err != nil {
			a.Log.Warn("remoteclient", "地址变更后清理旧凭据失败（残留孤儿凭据，无害）",
				fmt.Sprintf("deviceID=%s err=%s", prev.DeviceID, err.Error()))
		}
	}
	a.rcMu.Lock()
	a.rcDropConnection(hostID)
	a.rcMu.Unlock()
	a.Log.Info("remoteclient", "主机地址已更新，配对态已重置", fmt.Sprintf("id=%s", hostID))
	return nil
}

// RemoteClientRenameHost 修改显示名（本机可编辑字段）。
func (a *App) RemoteClientRenameHost(hostID, displayName string) error {
	if a.rcRegistry == nil {
		return errRemoteClientUnavailable
	}
	if err := a.rcRegistry.Rename(hostID, displayName); err != nil {
		return fmt.Errorf("rename remote host: %w", err)
	}
	return nil
}

// RemoteClientRemoveHost 移除条目并清理其 Keychain 凭据（PairingService.
// ForgetHost：凭据删除失败则整体失败，不留孤儿凭据）；若该主机当前已连接
// 则先丢弃本地连接视图。
func (a *App) RemoteClientRemoveHost(hostID string) error {
	if a.rcPairing == nil {
		return errRemoteClientUnavailable
	}
	a.rcMu.Lock()
	a.rcDropConnection(hostID)
	a.rcMu.Unlock()
	if err := a.rcPairing.ForgetHost(hostID); err != nil {
		return fmt.Errorf("remove remote host: %w", err)
	}
	a.Log.Info("remoteclient", "主机已移除（含凭据）", fmt.Sprintf("id=%s", hostID))
	return nil
}

// ---------------------------------------------------------------------------
// 探活与配对（蓝图 §6 流程 1）
// ---------------------------------------------------------------------------

// RemoteClientProbeHost 按 hostPort 探活（不要求先登记）：GET host/summary，
// 返回健康投影与（200 时的）宿主摘要。若该地址已登记且已配对，探活自动携
// 带凭据（可探测 auth.revoked），并把结论写回登记簿。
func (a *App) RemoteClientProbeHost(hostPort string) (remoteclient.ProbeResult, error) {
	hp, err := remoteclient.ValidateHostPort(hostPort)
	if err != nil {
		return remoteclient.ProbeResult{}, fmt.Errorf("probe remote host: %w", err)
	}
	entry := remoteclient.HostEntry{HostPort: hp}
	if a.rcRegistry != nil {
		for _, e := range a.rcRegistry.List() {
			if e.HostPort == hp {
				entry = e
				break
			}
		}
	}
	res, seen := remoteclient.ProbeHost(a.rcContext(), entry, a.rcCreds)
	if entry.ID != "" && a.rcRegistry != nil {
		if err := a.rcRegistry.SetHealth(entry.ID, res.State, seen); err != nil {
			a.Log.Warn("remoteclient", "探活结论写回登记簿失败", err.Error())
		}
	}
	return res, nil
}

// rcDeviceName 生成本设备在宿主侧的登记名：OS 主机名，取不到时固定回退值。
func (a *App) rcDeviceName() string {
	if hn, err := os.Hostname(); err == nil && strings.TrimSpace(hn) != "" {
		return strings.TrimSpace(hn)
	}
	return "codebox-desktop"
}

// RemoteClientCompletePairing 执行配对流：探活 → POST pairing/complete →
// 凭据验证 → Keychain + 登记簿回填（域层保证失败零残留）。设备登记名取
// OS 主机名。成功后前端通常立即 Connect 返回的 EntryID。
func (a *App) RemoteClientCompletePairing(hostPort, code string) (*remoteclient.PairingResult, error) {
	if a.rcPairing == nil {
		return nil, errRemoteClientUnavailable
	}
	res, cerr := a.rcPairing.CompletePairing(a.rcContext(), hostPort, code, a.rcDeviceName())
	if cerr != nil {
		return nil, fmt.Errorf("complete pairing: %w", cerr)
	}
	a.Log.Info("remoteclient", "配对完成",
		fmt.Sprintf("entry=%s deviceID=%s hostPort=%s", res.EntryID, res.DeviceID, res.HostPort))
	return res, nil
}

// RemoteClientConnectResult 是连接成功的返回投影：最新登记簿条目（含健康
// 投影）+ 已验证的宿主摘要。
type RemoteClientConnectResult struct {
	Host    remoteclient.HostEntry `json:"host"`
	Summary contract.HostSummary   `json:"summary"`
}

// RemoteClientConnect 连接已配对宿主。内含进程重启后的凭据恢复（RC1 §4 交
// 接）：登记簿 DeviceID → Keychain 条目 codebox-remoteclient/<DeviceID> 取
// secret → SetCredential 注入 Transport；随后以已鉴权 GET host/summary 验
// 证凭据，通过才建立连接（单连接模型：顶替既有连接）。失败路径把健康投影
// 写回登记簿（auth.revoked → revoked；网络族 → unreachable）。
func (a *App) RemoteClientConnect(hostID string) (RemoteClientConnectResult, error) {
	a.rcMu.Lock()
	defer a.rcMu.Unlock()
	if a.rcRegistry == nil || a.rcCreds == nil {
		return RemoteClientConnectResult{}, errRemoteClientUnavailable
	}
	entry, ok := a.rcRegistry.Get(hostID)
	if !ok {
		return RemoteClientConnectResult{}, fmt.Errorf("connect remote host: host %q not found", hostID)
	}
	if entry.DeviceID == "" {
		return RemoteClientConnectResult{}, fmt.Errorf("connect remote host: host %q is not paired; complete pairing first", hostID)
	}
	// 凭据恢复：secret 只在内存与 Keychain 间流转，不入日志/登记簿（§9）。
	secret, err := a.rcCreds.Get(remoteclient.CredentialEntryName(entry.DeviceID))
	if err != nil {
		return RemoteClientConnectResult{}, fmt.Errorf("connect remote host: load device credential: %w", err)
	}
	if secret == "" {
		return RemoteClientConnectResult{}, fmt.Errorf("connect remote host: device credential for %q is missing from the keychain; re-pair the host", entry.DeviceID)
	}
	t, err := remoteclient.NewTransport("http://" + entry.HostPort)
	if err != nil {
		return RemoteClientConnectResult{}, fmt.Errorf("connect remote host: %w", err)
	}
	if err := t.SetCredential(entry.DeviceID, secret); err != nil {
		return RemoteClientConnectResult{}, fmt.Errorf("connect remote host: %w", err)
	}
	summary, cerr := t.HostSummary(a.rcContext())
	if cerr != nil {
		health := remoteclient.HealthUnreachable
		if cerr.IsAuthRevoked() {
			health = remoteclient.HealthRevoked
		}
		_ = a.rcRegistry.SetHealth(hostID, health, time.Now())
		return RemoteClientConnectResult{}, fmt.Errorf("connect remote host: %w", cerr)
	}
	if a.rcConn != nil && a.rcConn.hostID != hostID {
		a.Log.Info("remoteclient", "顶替既有连接（单连接模型）", fmt.Sprintf("oldHost=%s", a.rcConn.hostID))
	}
	a.rcDropConnection("")
	a.rcConn = &remoteClientConnection{
		hostID:    hostID,
		transport: t,
		sessions:  remoteclient.NewSessionClient(t),
	}
	_ = a.rcRegistry.SetHealth(hostID, remoteclient.HealthReachable, time.Now())
	updated, _ := a.rcRegistry.Get(hostID)
	a.Log.Info("remoteclient", "已连接宿主", fmt.Sprintf("hostID=%s deviceID=%s", hostID, entry.DeviceID))
	return RemoteClientConnectResult{Host: updated, Summary: summary}, nil
}

// RemoteClientDisconnect 断开当前连接（仅丢弃本地连接视图与内存凭据；不动
// 登记簿/Keychain）。未连接或 hostID 不匹配时返回明确错误。
func (a *App) RemoteClientDisconnect(hostID string) error {
	a.rcMu.Lock()
	defer a.rcMu.Unlock()
	if a.rcConn == nil || a.rcConn.hostID != hostID {
		return fmt.Errorf("disconnect remote host: host %q is not connected", hostID)
	}
	a.rcDropConnection(hostID)
	a.Log.Info("remoteclient", "已断开宿主", fmt.Sprintf("hostID=%s", hostID))
	return nil
}

// ---------------------------------------------------------------------------
// 会话域（蓝图 §7：作用于当前已连接宿主；未连接返回明确错误）
// ---------------------------------------------------------------------------

// RemoteClientListRemoteSessions 列出宿主全部会话（空列表为 []，非 null）。
func (a *App) RemoteClientListRemoteSessions() (contract.SessionList, error) {
	c, err := a.rcSessions()
	if err != nil {
		return nil, err
	}
	list, cerr := c.ListSessions(a.rcContext())
	if cerr != nil {
		return nil, fmt.Errorf("list remote sessions: %w", cerr)
	}
	return list, nil
}

// RemoteClientGetRemoteSession 获取会话详情。
func (a *App) RemoteClientGetRemoteSession(sessionID string) (contract.SessionDetail, error) {
	c, err := a.rcSessions()
	if err != nil {
		return contract.SessionDetail{}, err
	}
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return contract.SessionDetail{}, errors.New("get remote session: sessionID is required")
	}
	d, cerr := c.GetSession(a.rcContext(), contract.SessionID(sid))
	if cerr != nil {
		return contract.SessionDetail{}, fmt.Errorf("get remote session: %w", cerr)
	}
	return d, nil
}

// RemoteClientLaunchRemoteSession 在宿主上启动新会话。可选参数空串表示不
// 指定（交宿主默认解析）；useHeadroom 仅在 true 时显式传递（false 与省略
// 语义等价，宿主缺省即 false）。cliType 五类白名单在域层先行校验（未知值
// 本地 bad_request，不出网）。
func (a *App) RemoteClientLaunchRemoteSession(cliType, workdir, providerRef, presetRef, modelRef, shellRef string, useHeadroom bool) (contract.SessionDetail, error) {
	c, err := a.rcSessions()
	if err != nil {
		return contract.SessionDetail{}, err
	}
	req := contract.CreateSessionRequest{CLIType: contract.CLIType(strings.TrimSpace(cliType))}
	if s := strings.TrimSpace(workdir); s != "" {
		req.Workdir = &s
	}
	if s := strings.TrimSpace(providerRef); s != "" {
		req.ProviderRef = &s
	}
	if s := strings.TrimSpace(presetRef); s != "" {
		req.PresetRef = &s
	}
	if s := strings.TrimSpace(modelRef); s != "" {
		req.ModelRef = &s
	}
	if s := strings.TrimSpace(shellRef); s != "" {
		req.ShellRef = &s
	}
	if useHeadroom {
		v := true
		req.UseHeadroom = &v
	}
	d, cerr := c.CreateSession(a.rcContext(), req)
	if cerr != nil {
		return contract.SessionDetail{}, fmt.Errorf("launch remote session: %w", cerr)
	}
	a.Log.Info("remoteclient", "远端会话已启动", fmt.Sprintf("session=%s cli=%s", d.ID, d.CLIType))
	return d, nil
}

// RemoteClientStopRemoteSession 停止会话（幂等收敛）。
func (a *App) RemoteClientStopRemoteSession(sessionID string) (contract.SessionDetail, error) {
	c, err := a.rcSessions()
	if err != nil {
		return contract.SessionDetail{}, err
	}
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return contract.SessionDetail{}, errors.New("stop remote session: sessionID is required")
	}
	d, cerr := c.StopSession(a.rcContext(), contract.SessionID(sid))
	if cerr != nil {
		return contract.SessionDetail{}, fmt.Errorf("stop remote session: %w", cerr)
	}
	return d, nil
}

// RemoteClientRestartRemoteSession 同 ID 重启会话（recipe 不变）。
func (a *App) RemoteClientRestartRemoteSession(sessionID string) (contract.SessionDetail, error) {
	c, err := a.rcSessions()
	if err != nil {
		return contract.SessionDetail{}, err
	}
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return contract.SessionDetail{}, errors.New("restart remote session: sessionID is required")
	}
	d, cerr := c.RestartSession(a.rcContext(), contract.SessionID(sid))
	if cerr != nil {
		return contract.SessionDetail{}, fmt.Errorf("restart remote session: %w", cerr)
	}
	return d, nil
}

// RemoteClientDeleteRemoteSession 移除会话（不可逆；204 无 body）。
func (a *App) RemoteClientDeleteRemoteSession(sessionID string) error {
	c, err := a.rcSessions()
	if err != nil {
		return err
	}
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return errors.New("delete remote session: sessionID is required")
	}
	if cerr := c.DeleteSession(a.rcContext(), contract.SessionID(sid)); cerr != nil {
		return fmt.Errorf("delete remote session: %w", cerr)
	}
	a.Log.Info("remoteclient", "远端会话已移除", fmt.Sprintf("session=%s", sid))
	return nil
}
