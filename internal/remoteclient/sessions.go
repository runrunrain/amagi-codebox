// sessions.go 会话域 REST（蓝图 §7 绑定：RemoteClientListSessions/Launch/
// Stop/Restart/Delete）：
//
//	list/get/create/stop/restart/delete 直映射服务端 v1 会话路由；会话状态机
//	为服务端五态直映射，客户端不改名不聚合（§5）。create 成功后终端 attach、
//	输入/resize 走 ws.go + outbox.go，本文件只持短连接 REST。
package remoteclient

import (
	"context"
	"errors"

	"amagi-codebox/internal/remote/contract"
)

// SessionClient 是会话域 REST 客户端：包装一个已装载设备凭据的 Transport。
// 所有请求携带 device Cookie（auth）；未装载凭据时统一以 auth.unpaired 失败。
type SessionClient struct {
	t *Transport
}

// NewSessionClient 构建会话域客户端（Transport 由调用方装载凭据，通常经
// PairingService.CompletePairing 或登记簿+Keychain 恢复）。
func NewSessionClient(t *Transport) *SessionClient {
	return &SessionClient{t: t}
}

// ListSessions 列出宿主全部会话（空列表为 []，非 nil）。
func (c *SessionClient) ListSessions(ctx context.Context) (contract.SessionList, *ClientError) {
	var list contract.SessionList
	cerr := c.t.do(ctx, epSessionsList, requestOption{auth: true}, &list)
	return list, cerr
}

// GetSession 获取会话详情（staging/removed/未知统一 session.not_found）。
func (c *SessionClient) GetSession(ctx context.Context, sessionID contract.SessionID) (contract.SessionDetail, *ClientError) {
	var d contract.SessionDetail
	cerr := c.t.do(ctx, epSessionDetail, requestOption{sessionID: string(sessionID), auth: true}, &d)
	return d, cerr
}

// confirmBody 是 stop/restart/delete 的协议级意图载荷（confirm 必须字面 true）。
var confirmBody = contract.ConfirmActionRequest{Confirm: true}

// CreateSession 启动新会话：cliType 必须为五类冻结 CLI 之一（客户端侧先行
// 校验，本地失败综合为 bad_request），其余为可选安全启动参数；成功 201 返回
// SessionDetail。API key/provider URL/env 值永不跨此边界（宿主自行解析引用）。
func (c *SessionClient) CreateSession(ctx context.Context, req contract.CreateSessionRequest) (contract.SessionDetail, *ClientError) {
	if !knownCLIType(req.CLIType) {
		return contract.SessionDetail{}, localAPIError(0, contract.ErrorCodeBadRequest, contract.ErrorLayerSession,
			contract.ActionHintRetry, "invalid cliType", errors.New(string(req.CLIType)))
	}
	var d contract.SessionDetail
	cerr := c.t.do(ctx, epSessionCreate, requestOption{body: req, auth: true}, &d)
	return d, cerr
}

// StopSession 停止会话（需控制权；幂等收敛）；成功返回最新 SessionDetail。
func (c *SessionClient) StopSession(ctx context.Context, sessionID contract.SessionID) (contract.SessionDetail, *ClientError) {
	var d contract.SessionDetail
	cerr := c.t.do(ctx, epSessionStop, requestOption{sessionID: string(sessionID), body: confirmBody, auth: true}, &d)
	return d, cerr
}

// RestartSession 同 ID 重启会话（recipe 不变）；成功返回最新 SessionDetail。
func (c *SessionClient) RestartSession(ctx context.Context, sessionID contract.SessionID) (contract.SessionDetail, *ClientError) {
	var d contract.SessionDetail
	cerr := c.t.do(ctx, epSessionRestart, requestOption{sessionID: string(sessionID), body: confirmBody, auth: true}, &d)
	return d, cerr
}

// DeleteSession 移除会话（不可逆）；成功 204 无 body。
func (c *SessionClient) DeleteSession(ctx context.Context, sessionID contract.SessionID) *ClientError {
	return c.t.do(ctx, epSessionRemove, requestOption{sessionID: string(sessionID), body: confirmBody, auth: true}, nil)
}

// knownCLIType 报告 cliType 是否属于五类冻结 CLI（claudecode/opencode/codex/
// pi/omp）。客户端不扩展第五类之外的名字（契约封闭枚举）。
func knownCLIType(cli contract.CLIType) bool {
	for _, k := range contract.KnownCLITypes {
		if k == cli {
			return true
		}
	}
	return false
}
