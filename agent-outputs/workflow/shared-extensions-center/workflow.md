# Workflow: 扩展管理「共用中心」+ 插件生命周期统一

- 任务链: feat/shared-extensions-center（自 master 切出）
- 自治级别: unattended（主上已确认全部决策点，授权每阶段 commit、最终合并 master 推送并打 tag 发版）
- 创建: 2026-10-14
- 状态: 执行中

## 目标与总验收

扩展管理页新增「共用」中心（公共 skill + 公共 MCP 管理），并统一插件生命周期（更新/卸载/启停/子项启停），插件来源 skill 在公共技能页标记且不可单独删除。

用户可观察验收：
1. 共用 tab：公共 skill 可从 zip/文件夹导入、删除、启停；MCP 可从 JSON 导入、删除、启停
2. 每个 skill/MCP 可按需勾选分发目标 CLI（复制+清单回收）
3. 插件来源 skill 在公共技能页有来源徽章、删除被禁用（前后端双拦截）、启停跳转插件详情
4. 插件可更新版本（Codex 补单插件更新；Pi 补启停；已装插件 skill 级启停补齐）
5. `go vet ./...`、`go test ./...`、前端 build 全绿；合并 master、推 tag 触发 GitHub Release

## 已确认决策（主上 2026-10-14）

| # | 决策 | 内容 |
|---|---|---|
| D1 | 分发方式 | 复制+部署清单（不用 symlink） |
| D2 | 默认目标 | 按需勾选（每条目 targets 字段，UI 勾选，不默认全选） |
| D3 | P4 范围 | 与 P1-P3 同轮完成 |
| D4 | 插件 skill 启停 | 公共技能页只展示+跳转插件详情，不在聚合页直接操作 |
| D5 | 发布 | 每大阶段 commit；完成后合并 master（仓库主分支实为 master）推送，打 tag v1.3.145 触发 release.yml |

## 实测落点（调查已验证，实现直接采用）

| CLI | skills 目录 | MCP 配置 | agentDir 解析 |
|---|---|---|---|
| claude | ~/.claude/skills/&lt;n&gt;/SKILL.md | ~/.claude.json 顶层 mcpServers（sjson 精准插入） | ~/.claude |
| codex | ~/.codex/skills/&lt;n&gt;/ | ~/.codex/config.toml [mcp_servers.&lt;n&gt;]（TOML 保真行级编辑，参考 internal/wslsupply/toml_edit.go 思路） | ~/.codex |
| opencode | ~/.config/opencode/skills/&lt;n&gt;/ | ~/.config/opencode/opencode.json "mcp" 键 | ~/.config/opencode |
| pi | &lt;agentDir&gt;/skills/&lt;n&gt;/ | &lt;agentDir&gt;/mcp.json mcpServers | $PI_CODING_AGENT_DIR → ~/.pi/agent |
| omp | &lt;agentDir&gt;/skills/&lt;n&gt;/ | &lt;agentDir&gt;/mcp.json mcpServers | $PI_CODING_AGENT_DIR → ~/.omp/agent |

## Phase 表

| Phase | 内容 | Agent | 依赖 | 交付/验收 | 状态 |
|---|---|---|---|---|---|
| P1 | internal/sharedext 包：skill 导入(zip安全/文件夹)/删除/启停/复制分发+清单回收；MCP JSON 多格式导入/删除/启停/5 CLI 写入移除；聚合器(手动+插件来源标记,带缓存)；精简 TOML 保真编辑器；全单测 | luban | - | 单测绿+vet 绿；commit `4c16b6ab` | done ✅ |
| P2 | app.go 接线+bind_list.go+文件/目录选择对话框；frontend api/sharedExt.ts+stores/sharedExt.ts；wails generate module | luban | P1 | 绑定可调；store 单测；commit | pending |
| P3 | 前端「共用」主 tab：CommonSkillsPanel(来源徽章/启停/删除禁用/目标勾选/导入)、CommonMCPPanel(JSON导入预览/启停/删除/目标)；跳转插件详情；Vitest | luban | P2 | Vitest 绿+npm build 绿；commit | pending |
| P4 | 生命周期补齐：Codex 单插件更新；Pi 包启停+skill 级过滤启停；OpenCode/OMP skill 级启停(按 CLI 实际能力)；更新/卸载后聚合刷新联动 | luban | P2 (可与P3并行度受限于共享前端文件→串行) | 单测绿；commit | pending |
| P5 | 集成验证(go vet/test、前端 build、e2e 语义核对)+diting full 审核阶段 diff+文档(docs/api.md, docs/user/plugins.md)+版本 bump 1.3.145 | Leader+diting | P1-P4 | 审核通过/修复闭环；commit | pending |
| P6 | 合并 master→推送→tag v1.3.145→GitHub Release 构建确认 | Leader | P5 | 远端 Release 发布 | pending |

## 跨边界链路清单

1. **前端共用页 → sharedext → 用户 CLI 配置文件**（最高风险：写 ~/.claude.json、config.toml 等）
   - 责任: P1 实现写入器+单测；P5 diting 审核阶段 diff 重点覆盖；P5 Leader 语义验收（真实入口级用例）
   - 失败路径: 写入前备份/错误回滚、TOML 不支持形态拒绝编辑不破坏原文件
2. **聚合 API → 五引擎插件服务**: P1 定义 PluginSkillsProvider 接口（sharedext 不 import 插件包）；P2 app.go 组装真实 providers；P4 更新/卸载后调 App 级刷新
3. **插件 skill 删除拦截**: 后端 DeleteSharedSkill 拒绝 plugin 来源（P1）+ 前端禁用按钮（P3）双层
4. **wailsjs 绑定再生成**: P2 运行 `wails generate module`，不手改

## 风险与修订日志

- 2026-10-14 创建。master 实为主分支（用户口中"main"），tag 线 v1.3.144 领先 wails.json 1.3.118，P5 统一 bump 至 1.3.145。P1 已派发（luban）。
