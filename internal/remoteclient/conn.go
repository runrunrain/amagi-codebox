// conn.go 连接生命周期（蓝图 §3/§5/§6 流程 2）：
//
// 每主机一条连接 goroutine。状态机见 ConnState；重连退避 1s→2s→…→30s 封顶；
// auth.revoked（或 WS Close 1008）→ 立即置 revoked 态并停止重连（fail-closed，
// 对应 hosts.go 的 HealthRevoked）；rate.limited → 退避加倍。连接状态与远端
// 事件经事件总线回流前端（wails EventsEmit：rc:conn-state、rc:session-state、
// rc:terminal-output、rc:control-state、rc:revoked 等，蓝图 §7）。
package remoteclient

// ConnState 是客户端连接状态机的本地投影（蓝图 §5）。远端事实仍以服务端
// 事件为准；本状态机只描述客户端侧连接视图。
type ConnState string

const (
	ConnDisconnected ConnState = "disconnected"
	ConnConnecting   ConnState = "connecting"
	ConnAttached     ConnState = "attached"
	ConnReadonly     ConnState = "readonly"
	ConnDegraded     ConnState = "degraded"
)
