// control.go 控制域（蓝图 §6 流程 4 / §7 绑定 RemoteClientAcquireControl/
// ReleaseControl）：
//
//	acquire/release 直映射 v1 控制路由（空 body POST）；200 → 刷新 ControlView
//	投影；409（contract.ErrorCodeControlBusy）→ 「他人持有」，不重试；403
//	（control.forbidden）→ 未 attach（无 live lease）或非当前 controller。
//	attach 后依 control.state 事件渲染控制状态（§6）；写权威在服务端，本域
//	只持快照投影，不在客户端本地推导控制权。
package remoteclient

import (
	"context"
	"net/http"

	"amagi-codebox/internal/remote/contract"
)

// ControlClient 是控制域 REST 客户端：包装一个已装载设备凭据的 Transport。
// 所有请求携带 device Cookie（auth）；未装载凭据时统一以 auth.unpaired 失败。
type ControlClient struct {
	t *Transport
}

// NewControlClient 构建控制域客户端（Transport 由调用方装载凭据，通常与
// SessionClient 共用同一连接视图）。
func NewControlClient(t *Transport) *ControlClient {
	return &ControlClient{t: t}
}

// AcquireControl 获取会话控制权（空 body POST）：none→you；同 device 幂等；
// 他人/桌面占用 → 409 control.busy；未 attach（无 live lease）→ 403
// control.forbidden。成功返回服务端权威 ControlSnapshot（契约条件联合校验
// 后才透出；畸形成功体如实映射 service.down，不伪装成功）。
func (c *ControlClient) AcquireControl(ctx context.Context, sessionID contract.SessionID) (contract.ControlSnapshot, *ClientError) {
	var snap contract.ControlSnapshot
	cerr := c.t.do(ctx, epControlAcquire, requestOption{sessionID: string(sessionID), auth: true}, &snap)
	if cerr != nil {
		return contract.ControlSnapshot{}, cerr
	}
	if err := contract.ValidateControlSnapshot(snap); err != nil {
		return contract.ControlSnapshot{}, serviceDownError(http.StatusOK, err)
	}
	return snap, nil
}

// ReleaseControl 释放控制权（仅当前 controller；空 body POST）：you→none；
// 非 holder → 403 control.forbidden。成功返回释放后的服务端快照（期望
// state=none；仍以服务端返回为准，不本地推导）。
func (c *ControlClient) ReleaseControl(ctx context.Context, sessionID contract.SessionID) (contract.ControlSnapshot, *ClientError) {
	var snap contract.ControlSnapshot
	cerr := c.t.do(ctx, epControlRelease, requestOption{sessionID: string(sessionID), auth: true}, &snap)
	if cerr != nil {
		return contract.ControlSnapshot{}, cerr
	}
	if err := contract.ValidateControlSnapshot(snap); err != nil {
		return contract.ControlSnapshot{}, serviceDownError(http.StatusOK, err)
	}
	return snap, nil
}

// ControlView 是服务端 contract.ControlSnapshot 的客户端只读投影（蓝图 §5）。
// 写权威在服务端；本投影仅随 acquire/release 响应与 control.state 事件刷新，
// 客户端不得本地推导。嵌入（匿名）使 JSON 与契约快照同形（state/deviceName
// 顶层字段），与 mobile 端 acquireControl 返回形状一致。
type ControlView struct {
	contract.ControlSnapshot
}

// NewControlView 由服务端快照构造投影。
func NewControlView(s contract.ControlSnapshot) ControlView {
	return ControlView{ControlSnapshot: s}
}

// You 报告控制权是否在本设备（state=you）。
func (v ControlView) You() bool { return v.State == contract.ControlStateYou }

// HeldByOther 报告控制权是否被其它设备持有（state=other；此时
// OtherDeviceName 有值，供「他人持有」提示）。
func (v ControlView) HeldByOther() bool { return v.State == contract.ControlStateOther }

// OtherDeviceName 返回 state=other 时的持有设备名；其余状态空串。
func (v ControlView) OtherDeviceName() string {
	if v.State == contract.ControlStateOther && v.DeviceName != nil {
		return *v.DeviceName
	}
	return ""
}
