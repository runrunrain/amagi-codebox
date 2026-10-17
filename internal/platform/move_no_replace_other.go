//go:build !darwin && !linux && !windows

package platform

import "errors"

// moveNoReplaceOS：其余平台无既定原子 no-replace 原语，显式不支持
// （fail closed；调用方不得借覆盖式移动凑功能）。
func moveNoReplaceOS(source, destination string) error {
	return ErrMoveNoReplaceUnsupported
}
