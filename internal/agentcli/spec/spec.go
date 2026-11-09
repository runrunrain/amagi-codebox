// Package spec 是 agent-CLI 的"契约层"：命令表 schema、请求/响应包络、退出码、端点文件结构。
// 服务端（internal/agentcli）、客户端（internal/agentcli/client）、命令表内容（internal/agentcli/cmds）
// 与技能/文档生成（internal/agentcli/install）共享这一份定义，禁止在别处复制其字符串/常量。
package spec

import (
	"context"
	"fmt"
	"strings"
)

// ---- 端点发现 ----

// EndpointSchemaVersion 是 <home>/run/endpoint.json 的 schemaVersion。
const EndpointSchemaVersion = 1

// Endpoint 是应用启动 CLI 服务后发布到 <home>/run/endpoint.json 的内容（目录 0700、文件 0600、tmp+rename 原子写）。
// 严禁写入日志/回显。
type Endpoint struct {
	SchemaVersion int    `json:"schemaVersion"`
	URL           string `json:"url"` // 形如 http://127.0.0.1:53211
	PID           int    `json:"pid"`
	StartedAt     string `json:"startedAt"` // RFC3339
	AppVersion    string `json:"appVersion"`
	CLIToken      string `json:"cliToken"` // 32 字节 base64url，每次应用启动重新生成，轮换/关闭即失效
}

// ---- HTTP 协议 ----

const (
	HeaderToken = "X-Cli-Token"

	// PathPrefix 下：GET {PathPrefix}ping（探活，返回 PingData）；POST {PathPrefix}cmd/<dotted.name>（执行命令）。
	PathPrefix  = "/api/agent-cli/"
	PathPing    = PathPrefix + "ping"
	PathCommand = PathPrefix + "cmd/" // + Command.Name()
)

// Request 是 POST 命令体。
type Request struct {
	Args    map[string]any `json:"args,omitempty"`
	Confirm string         `json:"confirm,omitempty"` // 必须等于 Command.Confirm（若命令登记了确认值）
}

// Envelope 是所有响应与 --json 输出的统一包络。
type Envelope struct {
	OK    bool   `json:"ok"`
	Data  any    `json:"data,omitempty"`
	Error *Error `json:"error,omitempty"`
}

// PingData 是 ping 的 data。
type PingData struct {
	AppVersion string `json:"appVersion"`
	PID        int    `json:"pid"`
	TableHash  string `json:"tableHash"` // 服务端命令表哈希，客户端比对做版本偏差提示
}

// Error 是业务/协议错误。Code 是稳定的机器可读标识。
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// 稳定错误码。
const (
	ErrBadArgs              = "bad_args"              // 参数/用法错误 → 退出码 2，HTTP 400
	ErrUnknownCommand       = "unknown_command"       // HTTP 404 → 退出码 2
	ErrConfirmationRequired = "confirmation_required" // HTTP 409 → 退出码 4
	ErrUnauthorized         = "unauthorized"          // 令牌缺失/错误 HTTP 401 → 退出码 3
	ErrForbidden            = "forbidden"             // 非回环/带 Origin/Host 非法 HTTP 403 → 退出码 3
	ErrDisabled             = "cli_disabled"          // 命令行访问被关闭 HTTP 503 → 退出码 3
	ErrNotFound             = "not_found"             // 业务：资源不存在 → 退出码 1（HTTP 404）
	ErrConflict             = "conflict"              // 业务：状态冲突（如会话已停止）→ 退出码 1（HTTP 409，区别于确认）
	ErrNotReady             = "not_ready"             // 业务：依赖未就绪（如 control runtime）→ 退出码 1（HTTP 503）
	ErrInternal             = "internal"              // 其余 → 退出码 1（HTTP 500）
	ErrForbiddenCommand     = "forbidden_command"     // 命令被安全策略拒绝（如改禁改面的设置键）→ 退出码 2（HTTP 403）
)

// 退出码。
const (
	ExitOK          = 0
	ExitBusiness    = 1
	ExitUsage       = 2
	ExitUnreachable = 3
	ExitConfirm     = 4
)

// Errorf 构造业务错误。
func Errorf(code, format string, a ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, a...)}
}

// ---- 命令表 ----

// Risk 风险级。read/mutate 不要求确认；exec 与 destructive 必须登记 Confirm 值，CLI 以 --yes 注入。
type Risk string

const (
	RiskRead        Risk = "read"        // 只读
	RiskMutate      Risk = "mutate"      // 修改应用配置/状态，可逆
	RiskExec        Risk = "exec"        // 启动进程 / 向运行中会话发送输入 / 安装软件
	RiskDestructive Risk = "destructive" // 删除/清空，不可逆
)

// ParamType 参数类型。
type ParamType string

const (
	TypeString  ParamType = "string"
	TypeInt     ParamType = "int"
	TypeBool    ParamType = "bool"
	TypeStrings ParamType = "string[]" // 位置参数收尾位可全量收集；旗标可重复
	TypeJSON    ParamType = "json"     // 传 JSON 字面量（或 @file / - 表示 stdin，由客户端展开）
)

// Param 是命令参数。Name 为 camelCase；CLI 旗标是 kebab-case（buildId → --build-id）。
type Param struct {
	Name       string
	Type       ParamType
	Required   bool
	Positional bool // 位置参数；按 Params 中 Positional=true 的声明顺序排列
	Enum       []string
	Default    any // 仅用于帮助显示与客户端预填；服务端也需自行兜底
	Desc       string
	Stdin      bool // 允许 "-" 表示从 stdin 读取（用于密钥等不宜出现在 argv 的值）
	Secret     bool // 审计日志与错误回显必须脱敏该参数
}

// Command 是命令表条目。命令表是 CLI 解析/帮助/补全/服务端校验/文档/技能的唯一事实源。
type Command struct {
	Path     []string // 命令词，如 {"session","launch"}；Name() 为 "session.launch"
	Summary  string   // 一句话，中文
	Risk     Risk
	Confirm  string // Risk 为 exec/destructive 时必填，如 "LAUNCH"
	Params   []Param
	Examples []string // 形如 `codebox-agent session list --json`
	Notes    string   // 可选的补充说明（帮助与文档）
	Timeout  int      // 客户端 HTTP 超时秒数；0 用默认 120
}

func (c Command) Name() string { return strings.Join(c.Path, ".") }

// Args 是已解析的命令参数，供 Handler 读取。
type Args map[string]any

func (a Args) String(name string) string {
	if v, ok := a[name]; ok {
		switch t := v.(type) {
		case string:
			return t
		case fmt.Stringer:
			return t.String()
		case nil:
			return ""
		default:
			return fmt.Sprint(t)
		}
	}
	return ""
}

func (a Args) Bool(name string) bool {
	switch t := a[name].(type) {
	case bool:
		return t
	case string:
		return t == "true" || t == "1"
	}
	return false
}

func (a Args) Int(name string, def int) int {
	switch t := a[name].(type) {
	case float64: // JSON 数字
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	}
	return def
}

func (a Args) Strings(name string) []string {
	switch t := a[name].(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			out = append(out, fmt.Sprint(x))
		}
		return out
	case string:
		if t != "" {
			return []string{t}
		}
	}
	return nil
}

// Handler 执行一条命令。返回的 data 必须可 JSON 序列化；业务错误返回 *Error（其余 error 视为 internal）。
// ctx 随客户端断开取消。Handler 绝不能返回/记录明文 API Key。
type Handler func(ctx context.Context, args Args) (any, error)
