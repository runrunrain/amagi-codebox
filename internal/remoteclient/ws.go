// ws.go /ws/v1 客户端（蓝图 §3/§6 流程 3，决策 D-T05）：
//
// 长驻 goroutine 持有 WebSocket 连接，前端不直连 WS（Origin/凭据不暴露）。
// 传输库：复用 vendor 内 github.com/gorilla/websocket v1.5.3——服务端
// （internal/remote）用其 Upgrader，客户端用同库 client.go 的
// Dialer/DialContext；回环证明见 ws_dial_probe_test.go（T0-3/C-T02 结论）。
//
// 客户端→服务端帧：attach / input / resize / backfill / ping
// （contract.AttachFrame 等纯类型，编解码必须走 contract 的 Marshal/Decode
// 入口，禁止裸 json.Marshal，D-T02）。服务端→客户端事件：session.attached、
// output、backfill.*、session.state、restart.boundary、control.state、
// auth.revoked、error、input.ack（contract/ws.go）。
//
// 输入不直接写连接，一律经 outbox.go；输出经 conn.go 事件总线回流前端。
package remoteclient
