package platform

import (
	"sync"
	"time"
)

// drvfs_advisory.go — DrvFS advisory 记录面（C1/E4：告警从 pty 启动路径的
// log-only 单点扩到配套面）。
//
// 背景：DrvFS（/mnt/<drive> 9P 挂载）上 stat 密集型操作有 10~200 倍 I/O 惩
// 罚、小文件写最痛（p4-evidence §8-1/8-2 定量），且 Windows 侧工具与 WSL 内
// 工具对同一工作区看到的路径形态不同。此前唯一感知点是 pty 启动路径的一条
// log.Warn（C8），GitAssist/PathPicker 等配套面失败时用户收不到任何「这是
// DrvFS/路径形态问题」的解释。
//
// 设计：进程内环形记录（无轮询/后台任务），三个入口按需写入——
//   - pty（启动路径，原有的 log.Warn 语义不变，advisory 是追加的记录面）；
//   - gitassist（workDir Linux 形态前置拦截时）；
//   - paths（目录选择器收到 Linux 形态 root 时）。
// 前端经 App.GetDrvFSAdvisories 只读查询（按需拉取），在 GitPanel/PathPicker
// 失败处展示关联提示。仅作提示，不阻断任何操作。

// DrvFSAdvisory 是一条 DrvFS 相关感知记录。
type DrvFSAdvisory struct {
	// Source 写入方标识："pty" / "gitassist" / "paths"。
	Source string `json:"source"`
	// WorkDir 触发感知的工作目录（保持调用方原始形态：盘符或 Linux 形态）。
	WorkDir string `json:"workDir"`
	// MountPath 对应的 WSL 挂载点（/mnt/<drive>/...）；非 DrvFS 形态为空。
	MountPath string `json:"mountPath"`
	// Detail 面向用户的一句话说明。
	Detail string `json:"detail"`
	// At 写入时刻（Unix 秒）。
	At int64 `json:"at"`
}

// drvfsAdvisoryMaxEntries 是记录上限（环形截断：超出后丢最旧）。
const drvfsAdvisoryMaxEntries = 50

var (
	drvfsAdvisoryMu sync.Mutex
	drvfsAdvisories []DrvFSAdvisory
)

// RecordDrvFSAdvisory 追加一条 advisory。同一 (Source, WorkDir) 只保留最新
// 一条（更新原位），避免重复调用无限增长。At 未填时取当前时刻。
func RecordDrvFSAdvisory(a DrvFSAdvisory) {
	if a.At == 0 {
		a.At = time.Now().Unix()
	}
	drvfsAdvisoryMu.Lock()
	defer drvfsAdvisoryMu.Unlock()
	for i := range drvfsAdvisories {
		if drvfsAdvisories[i].Source == a.Source && drvfsAdvisories[i].WorkDir == a.WorkDir {
			drvfsAdvisories[i] = a
			return
		}
	}
	drvfsAdvisories = append(drvfsAdvisories, a)
	if overflow := len(drvfsAdvisories) - drvfsAdvisoryMaxEntries; overflow > 0 {
		drvfsAdvisories = append([]DrvFSAdvisory(nil), drvfsAdvisories[overflow:]...)
	}
}

// DrvFSAdvisories 返回当前记录快照（副本，旧在前）。无记录时返回 nil。
// 只读查询：调用方不得修改返回切片以影响内部状态。
func DrvFSAdvisories() []DrvFSAdvisory {
	drvfsAdvisoryMu.Lock()
	defer drvfsAdvisoryMu.Unlock()
	if len(drvfsAdvisories) == 0 {
		return nil
	}
	out := make([]DrvFSAdvisory, len(drvfsAdvisories))
	copy(out, drvfsAdvisories)
	return out
}
