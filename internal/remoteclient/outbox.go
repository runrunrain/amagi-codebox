// outbox.go 输入发件箱（蓝图 §6 流程 3、§8）：
//
// 每会话一条 outbox。输入帧先分配 MessageID 再发送，等待 input.ack 跟踪
// 确认；断线时暂停出队，重连后由服务端 input ledger 的幂等语义保证重发窗口
// 安全。关闭顺序：outbox flush 取消 → WS close → REST idle（§8）。
package remoteclient

// messageIDPrefix 是输入帧幂等 ID 前缀（蓝图 §6：`msg-v1-` + 32 hex）。
const messageIDPrefix = "msg-v1-"
