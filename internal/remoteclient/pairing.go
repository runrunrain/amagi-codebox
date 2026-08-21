// pairing.go 配对流（蓝图 §6 流程 1）：
//
//	transport 探活 GET /api/remote/v1/host/summary（无鉴权）→ 用户在宿主端
//	打开配对窗获取一次性配对码 → POST pairing/complete → 服务端经 Set-Cookie
//	下发 device 凭据 → 拆出 DeviceID + secret：secret 存本机 Keychain 条目
//	`codebox-remoteclient/<DeviceID>`（D-T04，secret 不落盘、不入登记簿），
//	DeviceID 写入登记簿（hosts.go）。
//
// 实现排期：T0 骨架 → 配对里程碑填充；配对码一次性、hostPort 走白名单校验（§9）。
package remoteclient
