package sharedmcp

// errors.go — 共用 MCP 领域的可分类错误（spec §2.3）。全部中文、可区分、
// 前端 toast 直接展示；风格对齐 internal/claudeaccounts 的 NewErr/ReasonOf
// 先例：结构体实现 error，Kind 供程序化分支（如 ErrForeignModified → UI
// danger 确认后 force 重试）。

import (
	"errors"
	"fmt"
)

// 错误种类（spec §2.3 契约清单）。
const (
	KindInvalidEntry    = "invalid-entry"    // 字段校验失败
	KindNameInvalid     = "name-invalid"     // 条目名字符集不兼容分发
	KindNotFound        = "not-found"        // 登记条目不存在
	KindExists          = "exists"           // Create 同名已存在
	KindRegistryCorrupt = "registry-corrupt" // 登记文件坏 JSON / 根非对象
	KindConflict        = "conflict"         // 目标存在手写同名条目
	KindUnsupported     = "unsupported"      // 形态该引擎无法表达（带 reason）
	KindForeignModified = "foreign-modified" // 目标条目被外部修改，移除需确认
	KindTargetCorrupt   = "target-corrupt"   // 目标引擎文件解析失败
	KindIO              = "io"               // 路径/权限/原子写失败
)

// Error 是本包的可分类错误。Msg 面向用户（中文），Kind 供前端/门面分支。
type Error struct {
	Kind string
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

// NewErr 构造带种类的错误（格式化文案）。
func NewErr(kind, format string, args ...any) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

// KindOf 返回错误的种类；非本包错误返回空串。
func KindOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return ""
}

// IsForeignModified 报告 err 是否为「目标条目被外部修改」（UI 需 danger
// 确认后以 force=true 重试的信号，spec §1.2/§3）。
func IsForeignModified(err error) bool { return KindOf(err) == KindForeignModified }
