package main

// app_drvfs_advisory.go — DrvFS advisory 的 App 只读查询面（P4-A1/C1）。
//
// 记录面在 internal/platform/drvfs_advisory.go（写入方：pty 启动路径、
// gitassist 形态拦截、paths 形态拦截）。本方法只做快照序列化：前端在
// GitPanel / PathPicker 失败处按需拉取，展示「该目录为 DrvFS 挂载」关联
// 提示；无轮询、无后台任务。

import (
	"encoding/json"

	"amagi-codebox/internal/platform"
)

// GetDrvFSAdvisories 返回进程内累积的 DrvFS advisory 快照（JSON 数组字符串，
// 旧在前；无记录时 "[]"）。只读：调用不影响内部状态。
func (a *App) GetDrvFSAdvisories() (string, error) {
	entries := platform.DrvFSAdvisories()
	if entries == nil {
		entries = []platform.DrvFSAdvisory{}
	}
	b, err := json.Marshal(entries)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
