package main

// app_sharedmcp.go — 共用 MCP（~/.agents/.mcp.json 登记 + 四引擎分发）的
// App 接线与前端门面（共用 MCP spec §3）。机制本体在 internal/sharedmcp；
// 本文件只负责：
//   - 持有 Service（userHome=用户主目录 / configDir=状态库目录 / env 供
//     CODEX_HOME、PI_CODING_AGENT_DIR 解析，构造模式照抄
//     buildSharedSkillsService）；
//   - Delete 的登记-分发联动编排（spec §3：Applied 非空时先逐引擎
//     Undistribute——force 透传，任一失败整体失败且登记与已还原引擎保持
//     如实状态；全部成功才删登记 + 删 state.Entries[name]）；
//   - Wails 门面方法（List/Refresh/Create/Update/Delete/
//     SetSharedMcpEngineSelection 共 6 个，spec §3），返回值均为最新
//     Overview（前端免二次请求），全部不含 env/headers 明文日志。
//
// sharedmcp.Service 不进 bind_list.go：写面经 App 门面暴露（sharedskills
// 同模式），分类登记在 bind_manifest_test.go 的
// appClassifiedNonSessionMutations。

import (
	"fmt"
	"os"

	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/sharedmcp"
)

// SharedMcpOverview 是前端看到的共用 MCP 全量状态视图：sharedmcp.Overview
// 加门面层组装（当前无附加字段）。不内嵌 Overview 而平铺同名字段：
// JSON 形状与内嵌完全一致，但 wails v2.13 绑定生成器不递归内嵌结构体的
// 字段类型——内嵌时 sharedmcp.EntryOverview/EngineMeta 被引用却不生成
// class，vue-tsc 报 TS2694（SharedSkillView 内嵌可行只因其字段全是标量）。
// 平铺为直接字段后生成器照常递归，wailsjs/go/models.ts 的 sharedmcp
// 命名空间完整（EntryOverview/EngineMeta/EngineCell/SharedMcpEntry）。
type SharedMcpOverview struct {
	Entries        []sharedmcp.EntryOverview `json:"entries"`
	RegistryExists bool                      `json:"registryExists"`
	RegistryPath   string                    `json:"registryPath"`
	EnginesMeta    []sharedmcp.EngineMeta    `json:"enginesMeta"`
}

// constructSharedMcp 在 NewApp 构造共用 MCP 服务（与 constructSharedSkills
// 同时机）。纯构造不做文件 IO。
func (a *App) constructSharedMcp() {
	if a.SharedMcp != nil {
		return
	}
	a.SharedMcp = buildSharedMcpService(a)
}

// buildSharedMcpService 构造共用 MCP 服务：userHome=用户主目录（~/.agents
// 与各引擎默认路径所在），configDir=应用配置目录（shared-mcp-state.json
// 状态库），env=os.Environ()（CODEX_HOME/PI_CODING_AGENT_DIR 解析）。
// os.UserHomeDir 失败时回退 configDir（模式照抄 buildSharedSkillsService
// ——降级时 ~/.agents 不可达，List 以空态/错误如实呈现）。
func buildSharedMcpService(a *App) *sharedmcp.Service {
	home := defaultConfigDir()
	var log *logging.Service
	if a != nil {
		if a.configDir != "" {
			home = a.configDir
		}
		log = a.Log
	}
	userHome := home
	if resolved, err := os.UserHomeDir(); err == nil {
		userHome = resolved
	}
	return sharedmcp.New(userHome, home, os.Environ(), log)
}

// sharedMcpService 返回共用 MCP 服务。生产由 NewApp 构造；未构造的 App
// （测试夹具直接拼 struct）现场兜底构造，不写回字段（与 sharedSkillsService
// 同模式，避免与生产构造路径竞态）。
func (a *App) sharedMcpService() *sharedmcp.Service {
	if a != nil && a.SharedMcp != nil {
		return a.SharedMcp
	}
	return buildSharedMcpService(a)
}

// sharedMcpOverview 组装最新全量状态视图。
func (a *App) sharedMcpOverview() (*SharedMcpOverview, error) {
	ov, err := a.sharedMcpService().Overview()
	if err != nil {
		return nil, err
	}
	return &SharedMcpOverview{
		Entries:        ov.Entries,
		RegistryExists: ov.RegistryExists,
		RegistryPath:   ov.RegistryPath,
		EnginesMeta:    ov.EnginesMeta,
	}, nil
}

// ---- Wails 门面（spec §3，共 6 个） ----

// ListSharedMcp 返回登记条目 × 各引擎的分发状态矩阵（含
// drift/conflict/unsupported 实测）与引擎元数据（含 omp excluded 行）。
func (a *App) ListSharedMcp() (*SharedMcpOverview, error) {
	return a.sharedMcpOverview()
}

// RefreshSharedMcp 显式刷新：服务层无缓存，语义等同 ListSharedMcp
// （sharedskills.Refresh 先例——为契约点名的 Refresh 显式提供）。
func (a *App) RefreshSharedMcp() (*SharedMcpOverview, error) {
	return a.sharedMcpOverview()
}

// CreateSharedMcpEntry 在 ~/.agents/.mcp.json 新增条目（0600 原子写；同名
// 已存在后端拒绝），返回最新 Overview。新条目默认全部引擎未勾选，用户在
// 矩阵显式勾选才分发（spec 裁定三）。
func (a *App) CreateSharedMcpEntry(entry sharedmcp.SharedMcpEntry) (*SharedMcpOverview, error) {
	a.sharedMcpWrites.Lock()
	defer a.sharedMcpWrites.Unlock()
	if err := a.sharedMcpService().Create(entry); err != nil {
		return nil, err
	}
	return a.sharedMcpOverview()
}

// UpdateSharedMcpEntry 更新登记条目内容（name 是标识不可改；条目不存在
// 后端拒绝）。只改登记文件，不自动重分发——已分发引擎随内容变化如实转
// drift，由用户在矩阵逐引擎 resync（spec §3）。
func (a *App) UpdateSharedMcpEntry(name string, entry sharedmcp.SharedMcpEntry) (*SharedMcpOverview, error) {
	a.sharedMcpWrites.Lock()
	defer a.sharedMcpWrites.Unlock()
	if err := a.sharedMcpService().Update(name, entry); err != nil {
		return nil, err
	}
	return a.sharedMcpOverview()
}

// DeleteSharedMcpEntry 删除登记条目并还原已分发引擎（spec §3 编排）：
// Applied 非空时先逐引擎 Undistribute（force 透传——外部手改的目标条目
// 需 force 二次确认）；任一失败 → 整体失败返回，登记与已还原引擎保持
// 如实状态；全部成功才删登记 + 删 state.Entries[name]。
func (a *App) DeleteSharedMcpEntry(name string, force bool) (*SharedMcpOverview, error) {
	a.sharedMcpWrites.Lock()
	defer a.sharedMcpWrites.Unlock()
	svc := a.sharedMcpService()
	applied := svc.AppliedEngines(name)
	if len(applied) > 0 {
		done := 0
		for _, engine := range applied {
			if err := svc.Undistribute(name, engine, force); err != nil {
				return nil, fmt.Errorf("删除已中止，登记条目「%s」保留：%v（此前已成功还原 %d 个引擎保持已还原状态，未处理的引擎不再改动）",
					name, err, done)
			}
			done++
		}
	}
	if err := svc.Delete(name); err != nil {
		return nil, err
	}
	if err := svc.ForgetStateEntry(name); err != nil {
		return nil, err
	}
	return a.sharedMcpOverview()
}

// SetSharedMcpEngineSelection 设置「条目 × 引擎」一格的勾选（spec §3）：
// selected=true → Distribute（幂等 resync——drift 格的「重新分发」即调本
// 方法；目标被外部改过时同样返回 ErrForeignModified，force 确认后才覆盖）；
// false → Undistribute（ErrForeignModified 时 UI 弹 danger 确认后以
// force=true 重试）。引擎间互不牵连（逐引擎独立成败）。
func (a *App) SetSharedMcpEngineSelection(name, engine string, selected, force bool) (*SharedMcpOverview, error) {
	a.sharedMcpWrites.Lock()
	defer a.sharedMcpWrites.Unlock()
	if err := a.sharedMcpService().SetSelected(name, engine, selected, force); err != nil {
		return nil, err
	}
	return a.sharedMcpOverview()
}
