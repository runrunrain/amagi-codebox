# Pi 兼容性修复报告 R3（r2 复审 Major/Minor 修复）

> 修复人：天城（Leader 直改；luban 两次 resume 均被中断，改由 Leader 实施）
> 依据：agent-outputs/pi-compat-review-r2.md 第 3 节

## 逐条映射

### Major-1：fork/clone 祖先文件删除后重复计费 → 已修复（语义改方案）
- 改动：`internal/appmeta/pi/parser.go`
- 方案：放弃 lineage-root dedup（依赖祖先文件持续可访问，本质脆弱），改为**内容指纹** dedup key = `pi:` + hash16(entryID, occurredAt, model, provider, input/output/cacheRead/cacheWrite)。复制 entry 逐字节相同 → 任何情况下同 key；同 8-hex ID 但内容不同的 entry → 不同 key。删除 resolveLineageRoot/walkLineageRoot/lineageRootCache/lineageMaxDepth 死代码与 sync 导入。
- 边界披露：entry 无 message/entry 级时间戳时 occurredAt 回退文件 mtime，复制件会不同 key——pi 总是写时间戳，属罕见残留风险，包文档已注明。
- 测试：重写 TestExtractUsageRecordsPiDedupCollisionSafe（同 ID 不同内容→不同 key；跨文件字节相同内容→同 key，覆盖祖先不可解析场景）；既有 fork fixture（祖先存在）仍过。

### Major-2：headers 保存链路被 scrub 丢弃 + secret 策略 → 已修复
- ① `internal/config/service.go` scrubProviderAPIKeys 现复制 Headers/AuthHeader（Anthropic/OpenAI 两格式）。
- ② SaveProvider 增加 warnSensitiveLiteralHeaders：Authorization/Proxy-Authorization/X-Api-Key/Api-Key 明文值打印双语警告引导 `$ENV:`（不强制拒绝，兼容既有配置）。
- ③ `internal/launcher/pi_config.go` WritePiAgentConfig：MkdirAll 后显式 Chmod(agentDir, 0700)、Rename 后显式 Chmod(models.json, 0600)，覆盖旧 0755 目录升级场景。
- 测试：TestSaveProviderRetainsHeadersAndAuthHeader（Save→reload→字段保留且 APIKey 仍被 scrub）；TestWritePiAgentConfigUpgradesLegacyPerms（0755→0700、0644→0600 升级）。

### Minor-1：semver pinned 判定 → 已修复
- 改动：`internal/piplugin/source.go` 手写严格 SemVer 校验（对齐 npm semver.valid：数字段禁前导零、prerelease/build 禁空标识符与非法字符、build 段允许前导零），替换原宽松正则。vendor 无现成 semver 库，未引新依赖。
- 测试：TestIsExactSemver 增补 11 个非法 exact-looking fixture（01.2.3、1.2.3-..、1.2.3+ 等）。

### Minor-2：models.ts 尾随空格 → 已修复
- sed 清除 13 处；`git diff --check` 干净。注意：`wails generate module` 重新生成会回潮。

## 验证
- `go build ./...` PASS；`go vet ./internal/...` 仅既有 macOS keychain deprecation 警告
- `go test -count=1 ./internal/...` 全 PASS（config/launcher/piplugin/appmeta/usage/remote 等）
- `git diff --check` PASS

## 未覆盖披露
- 真实 Wails 桌面冒烟（Extensions→Pi 交互）未做，环境受限
- 真实 pi 会话→usage 端到端（L3）未做
- Windows cmd.exe 注入回归为静态测试，未在真 Windows 跑

## R4 收尾（针对 review-r3 残余 1 Major + 3 Minor，Leader 直改）

| 问题 | 修复 | 文件 |
|---|---|---|
| Major：敏感 literal header 落 0644 主配置 + `$ENV:` 前缀误判 | 主配置写盘收紧为 0600（Save/saveLockedConfig 双路径 + tmp/rename 全链 Chmod）；`$ENV:` 判定改为与 launcher 同义的全串正则 | internal/config/service.go |
| Minor-1：hash16 拼接歧义 | pi parser hash16 改长度前缀编码 | internal/appmeta/pi/parser.go |
| Minor-2：残留 0644 tmp 权限 | WriteFile 后显式 Chmod(tmp, 0600) 再 Rename | internal/launcher/pi_config.go |
| Minor-3：semver 缺长度/MAX_SAFE_INTEGER 限制 | 补 256 字符上限 + 数字标识符 ≤ MAX_SAFE_INTEGER（含 prerelease） | internal/piplugin/source.go |

验证：`go build ./...` + `go test -count=1 ./internal/...` 全绿 + `git diff --check` 干净 + `npm --prefix frontend run build` 通过。新增 fixture：超长/超 MAX_SAFE_INTEGER 版本。本轮为低风险硬化项，按「低风险小变更 Leader 核对验证证据」未再派四轮复审。
