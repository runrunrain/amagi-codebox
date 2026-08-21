// Package remoteclient 实现桌面端「远程客户端」能力：作为 v1 契约的消费端，
// 连接另一台运行 amagi-codebox 的宿主桌面端（internal/remote 服务端），提供
// 配对、主机登记簿、会话域/控制域 REST、/ws/v1 终端流与连接生命周期管理。
// 前端经 App 转发层新增绑定消费本包（蓝图《桌面端互联-技术实现方案》§3）。
//
// 依赖方向（蓝图 §3，硬约束）：本包只依赖 internal/remote/contract（纯契约
// 类型）、internal/secrets（Keychain 凭据）与 internal/logging；禁止反向
// import internal/remote（服务端实现）任何符号。
//
// 写权威（蓝图 §5）：一切远端事实以服务端事件/响应为准，客户端仅持投影与
// 本机登记簿（登记簿权威 = 本机）。
//
// 错误契约（蓝图 §7）：服务端 12 个稳定错误码（contract.KnownErrorCodes）
// 透传给上层；REST/网络到客户端错误的映射集中在本文件一处。
package remoteclient

import (
	"fmt"

	"amagi-codebox/internal/remote/contract"
)

// ClientError 是本包所有 REST/WS 调用的统一错误包装：HTTP 状态码（网络层
// 失败时为 0）+ 解码后的服务端 v1 错误体（无契约体时为 nil）+ 底层错误。
// 上层（App 转发层/前端）依据 Code 而非 Message 做恢复决策。
type ClientError struct {
	StatusCode int                // HTTP 状态码；WS/纯网络失败为 0
	API        *contract.APIError // 解码成功的服务端 v1 错误体；可为 nil
	Err        error              // 底层错误（网络/解码失败）
}

// Error 实现 error；不含凭据与原始终端内容（蓝图 §9）。
func (e *ClientError) Error() string {
	if e.API != nil {
		return fmt.Sprintf("remoteclient: %s (layer=%s, http=%d)", e.API.Code, e.API.Layer, e.StatusCode)
	}
	return fmt.Sprintf("remoteclient: %v (http=%d)", e.Err, e.StatusCode)
}

// Unwrap 暴露底层错误供 errors.Is/As 链路判断。
func (e *ClientError) Unwrap() error { return e.Err }

// Code 返回稳定错误码；无契约体（网络失败/非契约响应）时返回空串，上层按
// net.unreachable / service.down 语义兜底归类。
func (e *ClientError) Code() contract.ErrorCode {
	if e.API != nil {
		return e.API.Code
	}
	return ""
}

// IsAuthRevoked 报告是否为 auth.revoked：连接层见此码（或 WS Close 1008）
// 必须立即进入 revoked 态并停止重连——fail-closed（蓝图 §6 流程 2）。
func (e *ClientError) IsAuthRevoked() bool {
	return e.Code() == contract.ErrorCodeAuthRevoked
}

// IsRateLimited 报告是否为 rate.limited：重连退避加倍（蓝图 §6 流程 2）。
func (e *ClientError) IsRateLimited() bool {
	return e.Code() == contract.ErrorCodeRateLimited
}

// 保证 *ClientError 满足 error 接口（骨架期编译锚点）。
var _ error = (*ClientError)(nil)

// Transport 承载 REST 基座状态：base URL、device Cookie（Go 侧保管并随请求
// 注入，不进前端事件，蓝图 §9）、X-Request-ID 生成。骨架期仅占位类型，
// 请求发送/错误解码在配对里程碑（RC1）填充。
type Transport struct {
	BaseURL string // 例：http://host:port（Tailscale 明文 HTTP，TLS 后置）
}
