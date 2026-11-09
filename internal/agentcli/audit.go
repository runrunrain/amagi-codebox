package agentcli

// audit.go — agent-CLI 审计日志（DESIGN §3.4）：所有非 read 命令记一行
// JSONL 到 <home>/logs/agent-cli-audit.jsonl（0600）：时间、命令名、风险级、
// 结果码、耗时；只记参数名，绝不记参数值（防泄漏配置值/密钥）。
// 大小上限的轮转简化为超限截断：超过 10 MiB 时滚动到 .old（覆盖旧 .old）
// 再从空文件续写。写审计失败不阻断命令（仅记日志）。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"amagi-codebox/internal/agentcli/spec"
)

const (
	auditFileName   = "agent-cli-audit.jsonl"
	auditMaxBytes   = 10 << 20 // 超限滚动到 .old
	auditFilePerms  = 0o600
	auditDirPerms   = 0o700
	resultCodeOK    = "ok"
	auditRotateSuff = ".old"
)

// auditLog 是追加式 JSONL 审计器（进程内串行化）。
type auditLog struct {
	mu   sync.Mutex
	path string
	log  Logger
}

func newAuditLog(configDir string, log Logger) *auditLog {
	return &auditLog{
		path: filepath.Join(configDir, "logs", auditFileName),
		log:  log,
	}
}

// auditEntry 是审计行结构。ParamNames 只含参数名（排序稳定），不含值。
// CtxCancelled 标注请求取消相关的终态（评审 D-06）：Handler 在客户端超时/
// 断开后仍完成（结果如实返回但客户端可能已放弃等待）或因预检取消未执行。
type auditEntry struct {
	Time         string   `json:"time"` // RFC3339
	Command      string   `json:"command"`
	Risk         string   `json:"risk"`
	Code         string   `json:"code"` // "ok" 或 spec.Error.Code
	DurationMs   int64    `json:"durationMs"`
	ParamNames   []string `json:"paramNames"`
	CtxCancelled bool     `json:"ctxCancelled,omitempty"`
}

// record 追加一条审计（nil 接收者静默）。paramNames 由调用方传入（只含
// 参数名，排序稳定）；失败仅记日志，绝不影响命令结果。
func (a *auditLog) record(cmd spec.Command, paramNames []string, code string, duration time.Duration, ctxCancelled bool) {
	if a == nil {
		return
	}
	if code == "" {
		code = resultCodeOK
	}
	if paramNames == nil {
		paramNames = []string{}
	}
	b, err := json.Marshal(auditEntry{
		Time:         time.Now().Format(time.RFC3339),
		Command:      cmd.Name(),
		Risk:         string(cmd.Risk),
		Code:         code,
		DurationMs:   duration.Milliseconds(),
		ParamNames:   paramNames,
		CtxCancelled: ctxCancelled,
	})
	if err != nil {
		return
	}
	b = append(b, '\n')

	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.appendLocked(b); err != nil && a.log != nil {
		a.log.Warn(auditSource, "写 agent-CLI 审计失败", err.Error())
	}
}

func (a *auditLog) appendLocked(line []byte) error {
	dir := filepath.Dir(a.path)
	if err := os.MkdirAll(dir, auditDirPerms); err != nil {
		return err
	}
	// 超限滚动（截断式轮转，DESIGN 允许的简化）。
	if st, err := os.Stat(a.path); err == nil && st.Size() > auditMaxBytes {
		_ = os.Remove(a.path + auditRotateSuff)
		_ = os.Rename(a.path, a.path+auditRotateSuff)
	}
	f, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, auditFilePerms)
	if err != nil {
		return err
	}
	defer f.Close()
	// 已存在文件的权限兜底（防外部放宽）。
	if err := f.Chmod(auditFilePerms); err != nil {
		return err
	}
	_, err = f.Write(line)
	return err
}
