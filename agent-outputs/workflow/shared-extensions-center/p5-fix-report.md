# P5-Fix 报告：diting 审核修复批次

- 上游输入：`agent-outputs/workflow/shared-extensions-center/diting-review.md`（VERDICT: FIX，9 findings）；修复目标 `internal/sharedext/{deploy,toml_mcp,skills,mcp,aggregate}.go`、`app.go`、`frontend/src/`。
- 修复范围：Critical C1/C2/C3 与 Major M1/M2/M3 全部修复；Minor m1/m2/m3 修复，m4/m5 跳过（取舍见 §Minor）。
- 验证基线：`go vet ./...` ✓ RC=0；`go test ./... -count=1` ✓ 48 包全绿（含 `./internal/sharedext/ -race` ✓）；前端 `npx vitest run` ✓ 41 文件 376 用例全绿；`npm run build`（vue-tsc + vite）✓。
- 原取证复跑：diting overlay 取证测试按「预期损坏发生」断言编写，修复后 D1/D2/D3/D5/D7 在损坏断言处失败（= 损坏不再发生）、D6 日志显示 registry 已记录成功引擎且删除后无残留。完整输出归档：`diting-evidence/rerun-after-p5fix.log`（复跑命令见文末）。

## Critical

### C1 — writeMCPServer TOML 分支吞读错误（deploy.go）

- 修复：`writeMCPServer` TOML 分支 `data, _ := os.ReadFile` 改为显式处理——仅 `errors.Is(err, os.ErrNotExist)` 视为新建空文件，其余读错误（权限/文件锁/EISDIR）立即返回 `读取 %s: %w`，不写文件。
- 新增测试：`TestMCPTOMLReadErrorNoClobber`（mcp_test.go）——形态一：config.toml 用目录占位（跨平台触发非 ErrNotExist 读错误）→ 启用报「读取」错误且目标保持目录原样；形态二：diting D7 原取证 chmod 000 → 启用报错，恢复权限后原文件逐字节不变。
- 取证不复现自证：D7 复跑在 `预期原内容丢失（吞读错误后按空文件覆盖）` 断言处 FAIL——原文 `model = "gpt-5"...` 完整保留。

### C2 — toml_mcp.go 多行字符串不识别

- 修复：`parseTOMLDoc` 行扫描器增加多行字符串状态跟踪（`scanTOMLStringState`）：`"""`/`'''` 两种定界符；同行闭合（闭合后继续扫描同行剩余）与跨行至终结行；闭合处定界符 run 取末尾 3 个（run 前部 1-2 个属于内容，即 diting 提示的「终止符可少于开头」规则）；基本串内 `\X` 转义对整体跳过、字面串无转义；单行串与注释中的引号不影响状态。字符串内容行不作为表头/键值处理，但受管段内的字符串行仍计入段范围（整段替换语义）。文件存在未闭合多行字符串（本身即非法 TOML）→ 返回 `errTomlUnsupported` 拒绝编辑不动文件（未选保守方案，实现的是完整状态机）。
- 新增测试（toml_mcp_test.go）：`TestTOMLMultilineStringPhantomIgnored`（D2 场景：字符串内嵌 `[mcp_servers.x]` → Remove 幂等原样返回、Set 追加不产生重复段且定界符完整）；`TestTOMLMultilineStringInManagedSection`（D3 场景：受管段值多行字符串含 `[whatever]` → Set 整段替换，输出无孤立 `"""` 无幻影段，精确断言输出字节）；`TestTOMLMultilineLiteralAndSameLineClose`（`'''` 变体 + 同行闭合）；`TestTOMLUnterminatedMultilineRejected`（Set/Remove 均拒绝且不产出新内容）。
- 取证不复现自证：D2 复跑在 `预期字符串内部的段内容被误删` 断言处 FAIL；D3 复跑在 `预期输出含孤立多行字符串定界符（非法 TOML）` 断言处 FAIL，实际输出为干净的新段。
- 遗留（记录不修）：多行数组续行形态 `[x]` 的理论误判（P1 报告自认、diting 盲区⑤）不在本条范围——状态机只跟踪字符串；此类输入在受管段内出现时仍可能误判段边界，与修复前同级（无回归）。

### C3 — 段名 bare key 校验缺失

- 修复（写入侧 a）：新增 `isTOMLBareKey`/`tomlKeyString`；`SetMCPServerSection` 以 `mcp_servers."名字"` 引号形态生成段头（`quoteTOMLBasicString` 含转义，并补齐控制字符 `\uXXXX` 转义）；`serializeTOMLSection` 的键值行键、env 子表段头（`[mcp_servers."名字".env]`）与子表键同样经 `tomlKeyString` 渲染；bare key 名保持原样不引号。
- 修复（解析侧 b）：`splitTOMLKeyPath` 本就支持引号段（引号段解码后与裸段混合路径拆分），段头 `[mcp_servers."my server"]` 读回解码为 `mcp_servers.my server`，与写入侧 sectionRanges 查找键一致——读回、冲突检测（tomlSectionValue）、二次写入、删除全链路互通。
- 修复（导入侧 c）：新增 `validMCPName`（validName + 拒 `"` + 拒控制字符）；`ParseMCPJSON` 预览阶段丢弃非法名条目（全部非法 → 「未能识别」拒绝），`ImportMCPServers` 对非法名记 `skipped（名称非法）`。
- 校验收紧：`validateTOMLSectionName` 保持既有字符集拒绝（`.`/引号/括号/#/换行/Tab）并新增控制字符拒绝——含这些字符的名字仍拒绝（部分失败由 M1 保证可回收），中文/空格/`=`/`@` 等 bare key 外合法字符经引号包裹写入。
- 新增测试：`TestTOMLQuotedSectionNameRoundtrip`（toml_mcp_test.go：`搜索服务`/`my server`/`a=b`/`a@b` 四名字 Set → 引号段头 + env 子表引号路径 → 读回段名精确命中 → 二次 Set 无重复段 → Remove 干净；bare key 不引号；含 `"` 段名拒绝；中文 env 键引号包裹）；`TestParseMCPJSONInvalidNamesDropped`、`TestImportMCPServersInvalidNameSkipped`（mcp_test.go）。
- 取证不复现自证：D1 复跑在 `name="my server" 未生成预期段头` 断言处 FAIL——不再输出裸 `[mcp_servers.my server]` 非法形态（读回歧义与重复段的前提同时消除；新回归测试断言二次写入仅 1 段）。
- codex 可解析形态说明：本机无 codex CLI（diting 盲区②），合法性依据 TOML v1.0 表头/基本字符串语法（引号段头为规范形态）+ 本包解析器 roundtrip 断言。

## Major

### M1 — 分发部分失败不落 registry（skills.go / mcp.go）

- 修复：`SetSharedSkillEnabled`/`SetSharedMCPServerEnabled`：deploy 返回错误后仍先把 `skill/server` 实际状态（Deployed 已在 deploy 循环内逐成功引擎追加）写回 registry 并落盘，再返回聚合错误；Enabled 记录实际部署状态（`deployErr == nil || len(Deployed) > 0`，全部失败且从未部署才停用）。`SetShared*Targets` diff 路径：remove 失败不再提前 return，继续执行 add，最后统一落盘实际 Deployed 再返回聚合错误。undeploy 侧镜像修复：`undeploySkillEnginesLocked`/`undeployMCPLocked` 仅把实际回收/移除成功的引擎移出 Deployed，失败者保留（后续 Disable/Delete 重试回收，不再产生脱离回收依据的残留）。disable 部分失败同样先落盘（已回收者移出、失败者保留、Enabled=false）。
- 新增测试：`TestSkillPartialDeployRecordsSuccess`（skills_deploy_test.go：claude 成功 + codex 坏根 → enable 报错但 registry `enabled=true deployed=[claude]` → disable 把 claude 目标回收干净）；`TestMCPPartialDeployRecordsSuccess`（mcp_test.go：D6 同构场景，codex 数组表头拒绝 + claude 成功 → 报错但 registry 记录 → disable 回收 claude.json）。
- 取证不复现自证：D5 复跑在 `预期 registry 未记录` 断言处 FAIL（日志 `registry: enabled=true deployed=[claude]`）；D6 复跑日志 `registry: enabled=true deployed=[claude]`，且删除后不再出现「~/.claude.json 中 foo 残留」日志行（原取证该行存在）。

### M2 — deploySkillLocked 无条件 replaceDir 覆盖用户自建目录

- 修复：部署前 `os.Lstat(target)` 存在且该引擎不在该条目 Deployed 清单 → 返回冲突错误（聚合进 enable/targets 错误）：「目标已存在同名非 CodeBox 管理目录 %s，已跳过部署」，不删除不动；Deployed 含该引擎（CodeBox 上次部署）→ replaceDir 覆盖照旧（与 MCP 侧冲突检测语义不同——MCP 是覆盖+warn，本处按任务书采纳 diting 建议拒绝更安全）。
- 新增测试：`TestSkillDeployUserDirConflictRefused`（skills_deploy_test.go：预置用户自建同名目录 → 启用报错含「非 CodeBox 管理目录」且用户内容逐字节原样、冲突引擎不进 Deployed；对无冲突引擎正常部署后重复启用（Deployed 含该引擎）→ 覆盖为新内容成功）。

### M3 — skill 导入部分成功退化为失败（前端拿不到 imported）

- 修复：App 层新增 `ImportSharedSkillResult{Imported, Skipped}` 信封（仿 `ImportSharedMCPResult`）；`App.ImportSharedSkillFromZip/Dir` 经 `wrapSkillImport` 折叠服务层 `(imported, error)` 双返回值：error 为 nil → 信封；部分成功（imported 非空 + 「部分 skill 未导入: 」前缀错误）→ 跳过明细进 Skipped、不返回 error；全失败（imported 空）→ 原错误透传。`internal/sharedext` 原方法未动。前端 `api/sharedExt.ts` 导入函数改走 App 绑定并归一化空信封（Service 面的 `ImportSkillFromZip/Dir` 列入与 `ImportMCPServers` 同款的「禁止使用」陷阱注释）；store 返回信封（imported>0 才刷新清单，语义对齐 importMCPServers）；面板 `reportImportResult` 双 toast（成功 + skipped warn，与 MCP 导入对话框一致），部分成功路径照常 `loadAggregated` 刷新列表。`wails generate module` 再生成绑定（App.d.ts 返回类型、models.ts 新增 ImportSharedSkillResult；sharedext.PluginSkill/PluginSkillsProvider 类随之从生成物移除——前端无引用，Service.GetAggregatedSkills 签名因 m2 变化生成 `arg1:any`，前端不调用该绑定面）。
- 新增/适配测试：`TestAppImportSharedSkillEnvelope`（app_sharedext_test.go：全成功信封 / 部分成功 skipped 透出不报错 / 全失败 error / 落库实况）；前端 `api/sharedExt.test.ts`（App 信封路由 + 断言不调用丢弃部分的 Service 绑定 + null 归一化）、`stores/sharedExt.test.ts`（信封返回、imported=0 不刷新、失败写 error 抛出）、`useCommonSkillsPanel.test.ts`（成功/部分成功双 toast、部分成功仍刷新聚合、失败不刷新——新增 `zip 导入部分成功` 用例）。

## Minor 取舍

| 项 | 处置 | 说明 |
|---|---|---|
| m1 非法 JSON 写垃圾字节 | **已修** | `writeMCPServer`/`removeMCPServer` JSON 分支在读后 `json.Valid(data)` 校验（文件不存在 → "{}" 天然合法），非法即拒绝写入/编辑，原文件字节不动。测试：`TestMCPInvalidJSONTargetRefused`。范围说明：diting 原定位是 ~/.claude.json（MB 级），json.Valid 为 O(n) 无分配扫描，大文件代价可忽略，故对全部 JSON 目标生效而非仅 pi/omp 小文件。 |
| m2 聚合 TTL 无收益 | **已修** | `GetAggregatedSkills` 改收惰性构建器 `func() []PluginSkillsProvider`，仅在缓存未命中时调用；`App.GetAllSharedSkillsAggregated` 传 `a.buildSharedSkillProviders` 方法值——缓存命中不再执行 providers 组装（codex CLI fork + 五引擎扫描）。测试：`TestAggregatedSkillsCache` 扩展（命中 0 次构建器 / force 1 次）+ `providersOf/countableProviders` 辅助。 |
| m3 固定 tmp 名竞态 | **已修** | `writeFileAtomic` 改 `os.CreateTemp(dir, base+".tmp-sharedext-*")` 随机后缀（写后 Chmod 到目标权限），defer 清理临时文件；rename 失败回退直接覆盖语义保留。（任务书 m3 表述「部署临时目录」——replaceDir 的部署临时目录本就是 `MkdirTemp` 随机名，实际固定名问题在 writeFileAtomic 临时文件，已修。） |
| m4 UpdatePlugin 文案边界 | **跳过** | codexplugin/service.go 的错误分类需触及 InstallPlugin 后置步骤语义（installed_plugins 状态机），超出本批「共用中心」修复面；维持现状，建议单独立项。 |
| m5 zip 根级 SKILL.md 随机名 | **跳过** | `collectSkillDirs` 命中解包临时目录时回退目录名 `sharedext-unzip-<rand>`——改名需要稳定派生规则（如取 frontmatter description 哈希或要求打包规范），收益低；维持现状。 |

## 验证与自检

- 命令（cwd=仓库根）：`go vet ./...` RC=0；`go test ./... -count=1` 48 包全 ok；`go test ./internal/sharedext/ -count=1 -race` ok；`cd frontend && npx vitest run` 41 文件 376 用例全过；`npm run build` 绿。
- diting 取证复跑（项目树零污染，overlay 注入）：`cp agent-outputs/workflow/shared-extensions-center/diting-evidence/evidence_test.go.txt /tmp/diting_evidence_test.go && go test -overlay agent-outputs/workflow/shared-extensions-center/diting-evidence/overlay.json ./internal/sharedext/ -run TestDiting -v` → D1/D2/D3/D5/D7 在「预期损坏」断言处 FAIL（损坏不再发生）、D4 仍 PASS（其记录的是 sjson 库固有行为，即 m1 的动机，防护在本包写入面）、D6 PASS 且日志证明 registry 已记录、删除后无残留、D8 PASS（JSON 转义键 roundtrip 无回归）。完整输出：`diting-evidence/rerun-after-p5fix.log`。
- 行为变更说明（可观察面）：① skill/MCP 启用部分失败现在返回错误的同时 registry 记录成功引擎（UI 刷新后可见 Deployed/Enabled 与磁盘一致）；② 用户自建同名 skill 目录上的启用从「静默覆盖」变为报错跳过；③ MCP 名含 `"`/控制字符在预览即被丢弃；④ 前端 skill 导入部分成功显示双 toast 并刷新列表（不再误报失败）。既有测试全部通过，无其他契约变化。
- git 边界：本次改动 = `internal/sharedext/`（5 源文件 + 4 测试文件）、`app.go`、`app_sharedext_test.go`（App 绑定面测试，M3 信封签名必须同步；根目录 App-binding 测试为仓库惯例位置）、`frontend/src/`（api/store/panel + 3 测试）、`frontend/wailsjs/`（wails generate module 生成）、本报告 + 取证复跑日志（diting-evidence/，untracked 证据目录）。工作区中 `workflow.md`/`docs/api.md`/`docs/user/plugins.md`/`frontend/package.json`/`wails.json` 为本任务开始前的既有未提交改动，未触碰。
- 未覆盖/风险：① codex CLI 对新引号段头的实际解析未实测（本机无独立 codex CLI，依据 TOML v1.0 规范 + 包内 roundtrip）；② Windows 文件锁/ACL 场景未覆盖（本机 darwin/arm64，C1 测试用 EISDIR 目录占位保证跨平台触发面）；③ 多行数组续行理论误判（见 C2 遗留）维持修复前状态。
- 回滚方式：`git checkout -- internal/sharedext/ app.go app_sharedext_test.go frontend/src/ frontend/wailsjs/`（本批全部改动集中于此；报告文件直接删除）。
