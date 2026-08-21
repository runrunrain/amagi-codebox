// hosts.go 主机登记簿（蓝图 §3/§5/§8）：
//
// 本机持久化的已配对主机列表，JSON 原子写（tmp+rename），存放于 settings 目录
// 旁路（不进 models.json）；提供增删改查、健康探活（probe → host/summary）、
// 显示名管理。登记簿的写权威是本机；Health/LastSeen 为探活投影。凭据 secret
// 永不入簿（仅 Keychain，D-T04）。hostPort 输入校验：host:1-65535 白名单（§9）。
package remoteclient

import "time"

// HealthState 是登记簿条目的健康投影（客户端本地状态，非服务端事实）。
type HealthState string

const (
	HealthProbing     HealthState = "probing"
	HealthReachable   HealthState = "reachable"
	HealthUnreachable HealthState = "unreachable"
	HealthRevoked     HealthState = "revoked"
)

// HostEntry 是登记簿条目（蓝图 §5 领域模型）。ID/DisplayName/HostPort 为本机
// 可编辑字段；DeviceID 由配对流填入。
type HostEntry struct {
	ID          string      `json:"id"`
	DisplayName string      `json:"displayName"`
	HostPort    string      `json:"hostPort"`
	DeviceID    string      `json:"deviceId"`
	Health      HealthState `json:"health"`
	LastSeen    time.Time   `json:"lastSeen"`
}
