// control.go 控制域（蓝图 §6 流程 4 / §7 绑定 RemoteClientAcquireControl/
// ReleaseControl）：
//
//	acquire/release 直映射 v1 控制路由；200 → 刷新 ControlView 投影；
//	409（contract.ErrorCodeControlBusy）→ 「他人持有」提示，不重试。
//	attach 后依 control.state 事件渲染控制状态（§6）。
package remoteclient

import "amagi-codebox/internal/remote/contract"

// ControlView 是服务端 contract.ControlSnapshot 的客户端只读投影（蓝图 §5）。
// 写权威在服务端；本投影仅随 control.state 事件与 acquire/release 响应刷新，
// 客户端不得本地推导。
type ControlView struct {
	Snapshot contract.ControlSnapshot
}
