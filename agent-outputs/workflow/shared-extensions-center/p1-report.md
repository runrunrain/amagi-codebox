# P1 报告：internal/sharedext 后端核心包

> 任务：公共 skill / 公共 MCP 管理 + 分发 + 聚合（纯后端 Go 包，无 Wails 绑定）。
> 上游输入：internal/plugin/reader.go（frontmatter/scanSkills 参考）、internal/wslsupply/toml_edit.go（TOML 保真思路，未 import）、internal/piconfig/service.go 与 internal/ompconfig/service.go（agentDir 复刻源）、CLAUDE.md。
> 产物：`internal/sharedext/`（16 文件，4043 行，其中 7 个源文件 + 9 个测试文件）。

## 一、导出 API 清单（P2 接线面）

### 类型（types.go）

| 类型 | 说明 |
|---|---|
| `Engine` | 引擎标识：`claude` / `codex` / `opencode` / `pi` / `omp` |
| `SharedSkill` | name/description/sourcePath/origin/enabled/targets/deployed/deployedAt/createdAt + `Missing`（仅 List 输出） |
| `SharedMCPServer` | name/config(map)/origin/enabled/targets/deployed/createdAt |
| `PluginSkill` | {Name, Description}（providers 输入） |
| `PluginSkillsProvider` | Engine/PluginID/PluginName/Skills/SubItemsDisabled（key 形如 `skill/<name>`） |
| `AggregatedSkill` | 聚合视图条目（origin=manual\|plugin、PluginEngine/PluginID/PluginName、Managed、Enabled） |

### Service 方法

```go
NewService(baseDir string, log *logging.Service) *Service   // baseDir = ~/.amagi-codebox；log 可 nil

// 通用
AllEngines() []Engine
ValidEngine(Engine) bool
SetEngineBaseDir(engine Engine, dir string)                  // 引擎基准根注入（测试/便携）；空串恢复默认

// skills（skills.go）
ListSharedSkills() ([]SharedSkill, error)                    // Missing 标记失联条目
ImportSkillFromZip(zipPath string) ([]SharedSkill, error)    // 多 skill；同名 skipped 聚合进 error 文本
ImportSkillFromDir(dirPath string) ([]SharedSkill, error)    // 自身含 SKILL.md=单 skill，否则扫描一层子目录
DeleteSharedSkill(name string) error                         // 先按 Deployed 回收，再删仓库目录与 registry
SetSharedSkillEnabled(name string, enabled bool) error       // true→分发全部 Targets；false→回收全部 Deployed；幂等
SetSharedSkillTargets(name string, targets []Engine) error   // diff 分发/回收；Enabled=false 只存不分发

// MCP（mcp.go）
ParseMCPJSON(text string) ([]SharedMCPServer, error)         // 纯解析预览；三格式；中文报错
ImportMCPServers(servers []SharedMCPServer) (imported []SharedMCPServer, skipped []string, err error)
ListSharedMCPServers() ([]SharedMCPServer, error)
DeleteSharedMCPServer(name string) error
SetSharedMCPServerEnabled(name string, enabled bool) error
SetSharedMCPServerTargets(name string, targets []Engine) error

// 聚合（aggregate.go）
GetAggregatedSkills(providers []PluginSkillsProvider, force bool) ([]AggregatedSkill, error)
                                                             // 30s TTL 缓存；force 旁路；registry 写操作失效

// TOML 保真编辑（toml_mcp.go，独立函数，可供 codex 配置面复用）
SetMCPServerSection(data []byte, name string, def map[string]any) ([]byte, error)
RemoveMCPServerSection(data []byte, name string) ([]byte, error)
```

## 二、存储与路径解析

- 仓库根 `<baseDir>/shared/`；skill 内容 `shared/skills/<name>/`；registry `shared/registry.json`（`{"skills":{...},"mcp":{...}}`，互斥锁 + 临时文件 rename 原子写）。
- 引擎基准根默认解析：claude=home、codex=home/.codex、opencode=home/.config/opencode、pi=$PI_CODING_AGENT_DIR→~/.pi/agent、omp=$PI_CODING_AGENT_DIR→~/.omp/agent（后两者逐行复刻 piconfig/ompconfig 的 agentDir()）。
- MCP 目标：claude→`~/.claude.json` 键 `mcpServers.<n>`（sjson SetBytes/DeleteBytes 原地编辑，绝不 unmarshal 大文件）；opencode→`opencode.json` 键 `mcp.<n>`（同上）；pi/omp→`<agentDir>/mcp.json` 键 `mcpServers.<n>`；codex→`config.toml` 段 `[mcp_servers.<n>]`（行级保真编辑）。
- claude.json 与 opencode.json 写前 `.bak-sharedext-<unixts>` 备份，按时间戳保留最近 3 份。

## 三、设计取舍（关键决策记录）

1. **「手动导入同名 = 跳过」而非覆盖**：与插件市场更新语义（覆盖）区分；返回的 error 聚合列出跳过项，其余 skill 正常导入（部分成功不阻断）。
2. **zip 安全分层**：路径穿越/盘符/绝对路径/超限 → 拒绝整个导入（写前校验 + 解压实际字节兜底，防 header 谎报）；SKILL.md 超 1MB → 该 skill 跳过（内容不合格，非安全攻击）。symlink entry 跳过不报错。
3. **skill 收集 BFS「最浅层命中」**：目录含 SKILL.md 即收编且不再深入其子树（防嵌套 skill 重复导入）；zip 路径深度上限 4 层防御；ImportSkillFromDir 语义为「自身或一层子目录」。
4. **坏 frontmatter 回退目录名**（无闭合 `---`、非 map 行、name 空/含路径分隔符均回退），与 plugin 包 scanSkills 同款宽容。
5. **Deployed 是回收唯一依据**：回收只删「目标目录名==skill 名」的目录；单引擎失败 errors.Join 聚合返回，已成功引擎不回滚，registry 只记实际 Deployed。
6. **目录替换原子性**：copy 到临时目录 → 旧目录 rename 移走 → 新目录 rename 就位 → 删旧；任一 rename 失败回退「直接删除重制」并 warn。
7. **MCP 冲突覆盖策略**：目标已有同名 key 且内容与 registry 记录不一致（canonical JSON 比较，键序无关）→ 视为用户手改，仍覆盖（CodeBox 管理优先）+ warn 日志。
8. **TOML 编辑器只认段边界**：段内键值不解析（整段替换/删除不需要理解内容）；表头解析前剥离行尾注释；受管作用域不支持形态（数组表头、裸表、点引号段名、根级点键/整键赋值）→ 拒绝编辑不动文件。与 wslsupply 互不 import（该文件包私有且不动 WSL 回归面）。
9. **聚合缓存只缓存输出**：providers 由 P2 组装（本包不读插件系统），Service 缓存最终结果 30s TTL；所有 registry 写路径调 invalidateAggCacheLocked。
10. **enable 与 targets 解耦**：Enabled=true 且 Targets 空 → 只记状态不分发；先勾 targets 再启用与反序均收敛到同一 Deployed 实况。

## 四、修复过程中发现并解决的问题（附根因）

- **splitTOMLKeyPath 索引倒退死循环**：引号段名位于键路径中段时 `i = next`（next 是相对 s[i:] 的偏移）导致扫描位置倒退死循环。复刻自 wslsupply 的同款写法存在同一理论缺陷（该包测试未覆盖中段引号）；本包已改为 `i += next` 并加注释。未改动 wslsupply（越界）。
- **带行尾注释的表头误判**：`[profiles.default]  # 注释` 因未剥离注释被 parseTOMLHeader 判为 unterminated，且旧逻辑把解析失败行并入前段范围 → 替换段时整段后续内容被误删。修复：表头解析前 stripTOMLComment + 解析失败行不并入段范围。
- **tomlSectionValue 子表头后写入目标未切换**：`[mcp_servers.x.env]` 后的键仍写入顶层 map。修复：跟踪 cur 指针。

## 五、验证证据（真实运行，2026-10-24，Go 1.26.1 darwin/arm64）

```
$ go vet ./internal/sharedext/
(vet: OK, 无输出)

$ go test ./internal/sharedext/ -count=1 -race -timeout 180s
ok  	amagi-codebox/internal/sharedext	3.068s

$ go test ./internal/sharedext/ -count=1 -race -v | grep -cE "^--- PASS"
32        # 顶层测试全部 PASS（含子测试共 45 个用例）
failures: 0

$ go build ./...
build: OK (exit 0；仅 internal/secrets 既有 cgo deprecation warning，与本包无关)

$ golangci-lint run ./internal/sharedext/
0 issues.
```

### 测试 → 覆盖清单映射（任务书 12 项全做）

| # | 任务书覆盖项 | 测试 |
|---|---|---|
| 1 | zip 路径穿越（../、绝对路径、盘符）拒绝且未写文件；symlink 跳过 | skills_import_test.go: TestImportSkillZipPathTraversalRejected / TestImportSkillZipSymlinkEntrySkipped |
| 2 | zip 超 size/文件数上限拒绝 | TestImportSkillZipOversizeFileRejected / TestImportSkillZipTooManyFilesRejected |
| 3 | 坏 frontmatter → name 回退目录名 | TestImportSkillBadFrontmatterFallsBackToDirName（4 子用例） |
| 4 | 多 skill zip / 一层子目录导入；同名 skipped | TestImportSkillZipMultipleAndConflict / TestImportSkillFromDirSubdirectories |
| 5 | 启用分发 2 引擎、禁用回收、幂等覆盖 | skills_deploy_test.go: TestSkillEnableDeployAndDisableRecycle / TestSkillEnableIdempotentOverwrite |
| 6 | targets diff 加/减/disabled 不分发 | TestSkillTargetsDiff / TestSkillTargetsInvalidEngine |
| 7 | 删除清理 + Missing 标记 | TestSkillDeleteCleansEverything / TestSkillMissingFlag |
| 8 | MCP 三格式解析 + 报错 + 同名 skipped | mcp_test.go: TestParseMCPJSONFormats(5 子) / TestParseMCPJSONUnrecognized / TestImportMCPServersConflictSkipped |
| 9 | MCP 写入/移除全引擎 + 备份轮转 ≤3 | TestMCPServerDeployAndRemoveAllEngines / TestMCPClaudeBackupRotation / TestMCPTargetsDiffAndDisabled |
| 10 | TOML set/更新/删除/保真/CRLF/env/数组表拒绝 | toml_mcp_test.go: TestTOMLSetOnEmpty…TestTOMLValueSerialization（9 组，含逐字节断言） |
| 11 | 聚合合并/标记/禁用/TTL/force/写失效 | aggregate_test.go: TestAggregatedSkillsMergeAndMarkers / ManualPriority / Cache |
| 12 | 并发竞态（-race） | concurrency_test.go: TestConcurrentRegistryAccess（16 goroutine 混合读写） |

## 六、给 P2 的接线注意事项

1. **构造**：`NewService(baseDir, log)`——baseDir 传 `~/.amagi-codebox`（config 服务同款根），log 传 logging.Service（可 nil）。建议在 app.go 持有单例；本包单锁串行化全部公开方法，无需外部加锁。
2. **providers 组装**（GetAggregatedSkills）：P2 需从五个插件引擎服务收集 skill 清单 + SubItemsDisabled（key=`skill/<name>`，与 plugin 包 PluginSubItemState 的用户级启停记录对齐）后传入；30s TTL 已内置，前端刷新按钮传 force=true。
3. **ParseMCPJSON 是纯函数**（预览不落库）：前端「粘贴 JSON 预览 → 用户勾选 → ImportMCPServers」两步交互的支柱；Import 返回的 skipped 列表要透出给 UI。
4. **SetEngineBaseDir 可用于便携模式**（P2 若有 portable roots 需求）；传空恢复默认 home 解析。
5. **备份文件命名** `.bak-sharedext-<unixts>`（同秒多次写共用一个备份名，语义为同一切片）；写入前备份失败不阻断写入（warn 日志）。
6. **错误语义**：Import 部分成功时返回非 nil error + 已成功列表（前端应同时展示两者）；启停/删除失败时 registry 不落半程状态。
7. **Missing 条目**：ListSharedSkills 对仓库目录失联的条目返回 Missing=true，P2 前端应提供「清理失效条目」入口（调 DeleteSharedSkill）。
8. **codex TOML 目标**：config.toml 含不支持形态（数组表等）时写入/回收会报错并保持文件不动——P2 需把该错误透传给用户提示手查，不要静默吞掉。

## 七、遗留 / 风险

- 无未完成项。已知边界（设计内）：TOML 段内多行数组的续行若恰好形如 `[x]`（嵌套数组元素跨行），理论上会被误判为段边界（wslsupply 同款理论缺陷，MCP def 场景实际不会出现：我方序列化只产单行字符串数组）；极端罕见，未做启发式消歧。
- pi 与 omp 的 agentDir 同读 `$PI_CODING_AGENT_DIR`（忠实复刻两个源包的现状，两包语义即如此）；若上游未来分家，本包 engineBase 需同步。
- 报告回滚方式：删除 `internal/sharedext/` 目录与本报告文件即可，无其他包引用本包（P1 零接线）。
