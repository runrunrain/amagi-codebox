# diting full 审核：共用中心全阶段 diff（feat/shared-extensions-center vs master）

- 审核对象：分支 `feat/shared-extensions-center`（HEAD f61b39c6），基线 master，`git diff master...HEAD`（4 commit，74 文件 +10520/-211）。
- 方法：逐文件读 diff 与关键实现（internal/sharedext 全部源文件、app.go Shared Extensions 段、codexplugin/piplugin/ompplugin/opencodeplugin 改动、前端 api/store/composable/面板），对 7 项高风险关注面逐项构造失败假设并核实；Critical/Major 全部以取证脚本实证（`go test -overlay` 注入 `internal/sharedext` 运行，项目树零污染；脚本与输出归档于 `agent-outputs/workflow/shared-extensions-center/diting-evidence/`）。
- 复核通过项（有反例假设但未成立）：zip 解包防护（穿越/绝对路径/盘符/大小/数量/symlink/实际字节兜底均完备）；备份轮转真实（backupConfigFile + pruneBackups ≤3，测试覆盖）；MCP JSON 三格式解析与幂等删除；删除拦截双层（后端 plugin 来源不入 registry→Delete 报「不存在」；前端 canDelete=managed；聚合不可能把非 CodeBox 条目错标 managed）；pi 过滤与 pi 1.5.2 TUI 互认（本机真 pi 只读实测：四键 `!**` 与 `-skills/alpha/SKILL.md` 两形态 `pi list` 均正常解析）；pi settings.json 手术对非法 JSON 拒改（json.Valid + 原子写）；前端 MCP 导入部分成功信封透出、targets 保存失败回滚（computed 回读 store 即回滚）。

## Findings

### C1｜Critical｜internal/sharedext/deploy.go:257（writeMCPServer TOML 分支）— 吞读错误 → 整个 config.toml 被静默替换为仅含 MCP 段

- **触发条件**：`~/.codex/config.toml` 存在但 ReadFile 失败且非 ErrNotExist（权限错配/ACL/AV 文件锁/IO 错误；目录可写则 rename 可成功）。用户执行「导入 MCP → targets 勾选 codex → 启用」。
- **实际影响**：读错误被 `_` 丢弃，`data=nil` 按空文件处理 → `SetMCPServerSection(nil,...)` 生成仅含该段的新内容 → `writeFileAtomic` rename 覆盖 → 用户 config.toml 中 model/profiles 等全部内容丢失；且 `deployMCPLocked` 判定成功、**enable 返回 nil**，用户与 registry 均以为成功。TOML 目标无备份（BackedUp 仅 JSON 目标），不可恢复。
- **证据**（取证 D7，真实运行）：chmod 000 后启用 → `enable err: <nil>`；config.toml 由 `model = "gpt-5"\nprofile = "work"\n[profiles.work]...` 变为只剩 `[mcp_servers.foo]\ncommand = "npx"`。对照同函数 JSON 分支（deploy.go:267-274）与 `removeMCPServer` TOML 分支（deploy.go:296-302）均有正确的 ErrNotExist/其他错误分流——唯独此处遗漏。
- **修复方向**：`data, err := os.ReadFile(...)`；仅 `errors.Is(err, os.ErrNotExist)` 视为空文件，其余错误返回。

### C2｜Critical｜internal/sharedext/toml_mcp.go:64（parseTOMLDoc 表头识别）— 不理解多行字符串 → 误判段边界 → 写坏整个 config.toml

- **触发条件**：config.toml 任意位置存在多行字符串（`"""`/`'''`，如 instructions/notes 等自由字段），且 (a) 字符串内部含 `[` 开头行（典型：用户在配置里粘贴了 `[mcp_servers.xxx]` 示例/文档），或 (b) 受管段自身的值是多行字符串且内部含 `[xxx]` 形态行。随后任何 Set/Remove 触碰该文件。
- **实际影响**：(a) 字符串内部行被当作真实表头 → `RemoveMCPServerSection` 删掉字符串内部行（含闭合 `"""`）→ 字符串未闭合，文件非法；(b) 受管段范围被幻影表头截断 → 整段替换后残留孤立 `"""` 与幻影段，原键归属错乱。codex 解析 config.toml 失败，全部配置不可用；TOML 无备份，不可恢复。这是「不支持形态拒绝编辑不动文件」承诺（文件头注释与 P1 报告 §三.8）的结构性盲区——多行字符串不在拒绝清单（数组表头/裸表/点引号段名/根级点键）内。
- **证据**（取证 D2/D3）：
  - D2：输入 `[profile]\ntext = """\n[mcp_servers.x]\ncommand = "evil"\n"""\nkey = 1` → Remove("x") 输出 `[profile]\ntext = """`（闭合定界符与后续内容处理错乱，文件损坏）。
  - D3：输入 `[mcp_servers.x]\ncommand = "npx"\nnote = """\n[whatever]\n"""\nother = 1` → Set("x") 输出 `[mcp_servers.x]\ncommand = "npx"\nargs = ["a"]\n\n[whatever]\n"""\nother = 1`（`note = """` 行被当段内容删掉，残留孤立 `"""`，`other = 1` 归入幻影段 `[whatever]`）。
- **修复方向**：parseTOMLDoc 增加多行字符串状态跟踪（进入 `"""`/`'''` 后跳过其内部所有行直到闭合）；或至少检测到受管作用域邻域存在未识别的 `"""`/`'''` 行时按 errTomlUnsupported 拒绝编辑。

### C3｜Critical｜internal/sharedext/toml_mcp.go:261,725（validateTOMLSectionName/serializeTOMLSection）— 段名未按 TOML bare key 规则校验 → 含空格/中文等名字写出非法 TOML

- **触发条件**：MCP server 名含 TOML bare key（`[A-Za-z0-9_-]`）之外的字——空格、中文、`=`、`@` 等。名字经 `validName`（skills.go:229，仅拒空与 `/\\\x00`）与 `validateTOMLSectionName`（仅拒 `." ' [ ] # \n \t`）双层放行；用户粘贴 `{"我的搜索": {...}}` 或 `{"my server": {...}}` 即命中。分发到 codex。
- **实际影响**：`emit("[%s]", key)` 输出 `[mcp_servers.my server]` / `[mcp_servers.搜索服务]`——TOML v1.0 语法错误，codex（Rust toml crate）解析失败，用户全部 codex 配置不可用。次生：读回时 `splitTOMLKeyPath` 吞空格（`my server`→`myserver`），冲突检测与二次写入永远匹配不上 → 重复段堆积。嵌套子表键（serializeTOMLSection 的 `[%s.%s]`，env 键名）同样未校验。
- **证据**（取证 D1）：`my server`/`搜索服务`/`a=b`/`a@b` 四名字全部原样写入段头；`[mcp_servers.my server]` 读回段名 `myserver`；二次写入产生两个 `[mcp_servers.my server]` 段。
- **修复方向**：段名/子表键序列化时非 bare key 一律 `quoteTOMLBasicString` 包裹（写入 `[mcp_servers."my server"]`，与读侧 splitTOMLKeyPath 的引号解析天然互通）；或 validateTOMLSectionName 收紧为 bare key 白名单并在导入/分发面提前拒绝。

### M1｜Major｜internal/sharedext/skills.go:376、mcp.go:207（SetSharedSkillEnabled/SetSharedMCPServerEnabled 及同名 targets 路径）— 部分失败不落 registry → 状态漂移，禁用/删除永久回收不掉已写入目标

- **触发条件**：多引擎分发中任一引擎失败（现实场景：codex config.toml 含数组表头被 C2 之外的拒绝路径拒绝、目标目录权限不足）。P1 报告 §三.5 声明「单引擎失败聚合返回，registry 只记实际 Deployed」，但实现是 `deploy*Locked` 返回 err 后直接 `return err`，`reg.Skills/MCP[name]=...` 与 `saveRegistryLocked` 均不执行。
- **实际影响**：已成功引擎磁盘已写（如 ~/.claude.json 的 mcpServers 条目已被 Claude Code 加载生效），registry 却 Deployed=[]、Enabled=false——「实际已部署但系统以为没有」。此后禁用/删除按 Deployed 空集回收 → 写入项永久残留在用户 CLI 配置中，且 UI 无任何线索。
- **证据**（取证 D5/D6）：
  - D6：targets=[claude,codex]、codex 含 `[[mcp_servers.x]]` → enable 返回「拒绝编辑」错误，但 `~/.claude.json` 已含 `"foo"`；registry `enabled=false deployed=[]`；随后 `DeleteSharedMCPServer("foo")` 成功返回，claude.json 中 `"foo"` 仍残留。
  - D5：skill 同构场景——claude 目标已有 SKILL.md，registry `enabled=false deployed=[]`，禁用后文件永久残留。
- **修复方向**：deploy/undeploy 循环完成后无论是否有 err 都先落盘 skill/server 实际状态（Deployed 追加发生在成功引擎上，已具备信息），再返回聚合错误；targets diff 路径（skills.go:415-431、mcp.go:247-263）同理。

### M2｜Major｜internal/sharedext/deploy.go:25,95（deploySkillLocked→replaceDir）— 部署覆盖目标同名目录无检测无警告 → 用户自建同名 skill 静默丢失

- **触发条件**：用户在 `~/.claude/skills/<name>`（或 pi/omp/codex/opencode 对应 skills 目录）自建了与共享 skill 同名的目录（热门名字如 git/pdf/docx 容易撞）。首次启用分发时 `replaceDir` 无条件替换：旧目录 rename 移走 → 新目录就位 → `_ = os.RemoveAll(oldDir)`。
- **实际影响**：用户自建 skill 内容被静默删除，无 warn、无备份、UI 无提示；之后禁用/删除共享 skill 时回收会把替换版也删掉——同名目录下用户内容永久丢失。对照 MCP 分发有「同名且内容不一致 → 覆盖但 warn」检测（deploy.go:258-268），skill 目录完全没有等价防护，任务书关注面 3 点名的问题。
- **证据**：静态路径（deploy.go:95-140 replaceDirLogged 无任何目标预检；测试 skills_deploy_test.go 的幂等覆盖用例也确认重复部署直接替换）；Deployed 记录仅约束回收，不约束首次覆盖。
- **修复方向**：deploySkillLocked 对「目标已存在且不在 Deployed 清单」的引擎记 warn 日志 + 前端确认提示（或至少 warn）；可选做一次性 `.bak` 目录改名而非直接 RemoveAll。

### M3｜Major｜frontend/src/api/sharedExt.ts:66、stores/sharedExt.ts importSkillFromZip/FromDir、useCommonSkillsPanel.ts handleImportZip — skill 导入部分成功退化为「导入失败」且列表不刷新

- **触发条件**：zip/文件夹导入含多个 skill 且任一同名跳过（后端返回 (imported, error) 双非空——设计为「部分成功不阻断」）。
- **实际影响**：Wails v2 对 (value, error) 绑定在 error 非 nil 时 promise reject，imported 不可达（api/sharedExt.ts:66 注释自认「Wails 绑定下 error 时返回列表不可达」）；store 的 catch 路径不调 loadSkills，面板 catch 只 toast「导入失败: 部分 skill 未导入: xxx（同名已存在，跳过）」且不 loadAggregated。用户看到失败 + 列表无变化 → 认为整个导入失败，实际部分 skill 已入库（重进页面才可见）。与 MCP 侧信封方案（App.ImportSharedMCPServers 折叠 imported/skipped，UI 双 toast）不一致——MCP 面做对了，skill 面漏做。
- **证据**：代码路径（App 层 skill 导入无信封：app.go:6860-6875 直接透传双返回值）；P1 报告 §六.6 明确要求「前端应同时展示两者」未达成。
- **修复方向**：照抄 MCP 信封——App.ImportSharedSkill* 返回 `{imported, skipped}` 结构体，error 仅留给整体失败；store 部分成功路径也刷新列表。

### m1｜Minor｜internal/sharedext/deploy.go:267-274（writeMCPServer JSON 分支）— 非法 JSON 的 ~/.claude.json 被写成纯垃圾字节

- 触发：用户手改坏 ~/.claude.json（非法 JSON）后启用 MCP。sjson.SetBytes 对非法输入输出 `,"mcpServers":{...}}`（连起始 `{` 都丢失，取证 D4），writeFileAtomic 写回。前置条件是文件已坏 + 写前备份保留原内容（BackedUp=true 兜住恢复路径），故降为 Minor。修复：写入前 `json.Valid(data)` 防护（对照 piplugin mutatePackageEntry 已有同款防护），非法即拒绝。

### m2｜Minor｜app.go:6933-6938 + internal/sharedext/aggregate.go — 聚合 30s TTL 缓存无实际收益，providers 组装每次全量执行

- `GetAllSharedSkillsAggregated` 在调用 `GetAggregatedSkills`（缓存判定在其内部）之前无条件 `buildSharedSkillProviders()`——含 codex CLI fork（ListPlugins）与五引擎目录扫描。缓存命中也只省 registry 读+合并，重活照跑；P4 的 OnChange/InvalidateSharedExtCache 机制因此近乎冗余（providers 本就每次新鲜，插件变更总能反映——正确性无损，纯性能问题）。每次打开共用 tab 都触发一轮 CLI fork + 全盘扫描。修复：把 providers 组装移入缓存未命中分支（组装函数注入 GetAggregatedSkills 或在 App 层先探缓存）。

### m3｜Minor｜杂项（各自独立、低影响）

- skills.go ImportSkillFromZip：`zip -j` 形态（根级 SKILL.md，解包后临时目录直接含 SKILL.md）→ collectSkillDirs 命中临时目录本身 → 无 frontmatter name 时 skill 名 = `sharedext-unzip-<随机>`，不可重现且丑陋。
- codexplugin/service.go UpdatePlugin ④：InstallPlugin 的「已安装但后置步骤失败」（setPluginEnabled/verifyPluginInstall 失败）返回 err 时，被误报为「重装失败（旧版本已卸载，请重新安装）」——插件实际已装上，文案引导用户重复安装。建议区分错误类别。
- deploy.go writeFileAtomic 临时文件固定名 `<path>.tmp-sharedext`：进程内被 s.mu 串行化无碍，跨进程（用户手动编辑器/其他工具同时写）存在理论竞态窗口；随机后缀可消除。

## 覆盖、盲区与验证可信度

- **覆盖**：任务书 7 项关注面全部逐项核查（见开头复核通过项 + findings 映射）；internal/sharedext 9 源文件逐行读；app.go Shared Extensions 段（6827-7280）逐行读；codex/pi/omp/opencode 四引擎 diff 逐行读；前端 api/store/composable/面板与 8 个新测试文件读；wailsjs 生成物抽查（与绑定面一致）。
- **测试复核**：`go test ./internal/sharedext/ -count=1 -race` ok；四插件包 + root app（Shared 相关）全 ok；`go vet`（本分支改动面 + root）RC=0；前端 `npx vitest run` 41 文件 375 用例全过。P1/P4 报告声称的验证基本可信；P4 报告提及的 TestConcurrentRegistryAccess 偶发失败已在本分支修复（前置导入注释），本次 -race 多轮未见复现。
- **盲区**：① omp marketplace `installed_plugins.json` 字段形态无实样（P4 自认，三候选容错+未命中为空，风险限于聚合漏列非损坏）；② codex CLI 对非法 TOML 的具体报错行为未实测（本机无独立 codex CLI；C2/C3 的非法性判定依据 TOML v1.0 规范 bare key/表头语法，证据充分）；③ Windows（ConPTY/文件锁/ACL）场景未覆盖，本机 darwin/arm64；④ 全仓 `go test ./...`（48 包）未整跑（跑了个别包+race+root 相关+前端全量），非本分支改动面的既有失败不在此结论内；⑤ C2 的多行数组续行 `[x]` 形态误判（P1 报告自认的理论缺陷）未单独取证，与 C2 同根因（行级解析不理解跨行结构），修复方向覆盖。
- **取证可复现**：`diting-evidence/evidence_test.go.txt` + `overlay.json`；复跑方式：`go test -overlay <repo>/agent-outputs/workflow/shared-extensions-center/diting-evidence/overlay.json ./internal/sharedext/ -run TestDiting -v`（overlay 指向的 /tmp 源文件需先按 .txt 副本还原）。注意取证 D3 的断言表达式写错（数错 `"""` 个数）显示 FAIL，但其 t.Logf 输出即为损坏证据，结论不受影响。

## 结论

VERDICT: FIX

必须修复的最小集合：**C1**（deploy.go:257 吞读错误——一行分流修复）、**C2**（toml_mcp.go 多行字符串盲区——parseTOMLDoc 状态跟踪或拒绝编辑）、**C3**（TOML 段名引号包裹或白名单校验）。三者同属「分发 MCP 到 codex 即可能损坏用户 config.toml」的写入安全面，且 C1 静默无感知。强烈建议同批修复 **M1**（部分失败 registry 漂移，与 C2 的拒绝路径叠加时高概率出现）与 **M2/M3**。
