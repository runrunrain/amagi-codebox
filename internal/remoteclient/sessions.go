// sessions.go 会话域 REST（蓝图 §7 绑定：RemoteClientListSessions/Launch/
// Stop/Restart/Delete）：
//
//	list/get/create/stop/restart/delete 直映射服务端 v1 会话路由；会话状态机
//	为服务端五态直映射，客户端不改名不聚合（§5）。create 成功后终端 attach、
//	输入/resize 走 ws.go + outbox.go，本文件只持短连接 REST。
package remoteclient
