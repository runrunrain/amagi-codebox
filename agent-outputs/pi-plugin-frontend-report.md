# Pi 插件/包管理前端 — 实现报告

> 任务：为 Pi 插件/包管理实现前端，全面对标 OpenCode 插件前端链路。
> 依据：`agent-outputs/pi-plugin-backend-report.md` 第四节（API 签名与 TS 类型）、`agent-outputs/pi-compat-gap-analysis.md` 第④节阶段 A 前端部分。

## 一、变更摘要

| # | 文件 | 变更 |
|---|------|------|
| 1 | `frontend/wailsjs/go/piplugin/Service.js` + `Service.d.ts` | **新增（手写绑定）**。Wails 生成目录尚无 piplugin 绑定，按 `wailsjs/go/opencodeplugin/Service.js` 同构模式手写（`window['go']['piplugin']['Service'][...]`），文件头已注明需重新生成。 |
| 2 | `frontend/src/api/piPlugin.ts` | **新增**。封装 `RefreshPackages / GetPackageDetails / InstallPackage / UpdatePackage / RemovePackage`；类型 `PiPackage / PiResourceInfo / PiPackageDetail / PiPackagesData / PiCommandResult` 与后端报告第四节逐一对应。 |
| 3 | `frontend/src/stores/piPlugin.ts` | **新增**。Pinia store（`usePiPluginStore`），对标 `stores/opencodePlugin.ts`：5min 缓存 TTL、warnings、activeSource 选中态、details 缓存、install/update/remove 后强制刷新并重选；包身份归一化 `packageIdentity`（github:/file:// npm@version 剥离）。 |
| 4 | `frontend/src/components/extensions/PiPluginPanel.vue` | **新增**。对标 `OpenCodePluginPanel.vue`：包列表 + 详情分栏、安装对话框（npm/git/local 源输入）、更新（local 源禁用，显示"本地直载"）、移除确认框、详情含版本/描述/作者/仓库/安装路径/manifestDeclared 及 Extensions/Skills/Prompts/Themes 四类资源统计卡与 `type:name` 资源 chips；复用 AppButton/Badge/Dialog/ConfirmDialog/EmptyState/ErrorState/LoadingState 与相同样式 token；资源摘要 4 列（mobile 断点 2 列）。 |
| 5 | `frontend/src/views/ExtensionsView.vue` | engineOptions 增加 `{value:'pi',label:'Pi'}`；新增 `v-else-if="pluginEngine === 'pi'"` 分支挂载 `PiPluginPanel`（置于 codex `v-else` 之前）；import PiPluginPanel；PageHead 副标题补 "与 Pi"。 |
| 6 | `frontend/src/stores/plugin.ts` | `pluginEngine` 与 `setPluginEngine` 联合类型加 `'pi'`（2 处）。 |
| 7 | `frontend/src/api/index.ts` | Plugins 区加 `export * from './piPlugin';`。 |
| 8 | `frontend/src/views/UsageView.vue` | **核验结果**：全项目仅一处 app type 展示硬编码——客户端过滤 `<select>` 只含 claudecode/opencode/codex。已补 `<option value="pi">Pi</option>`，未改任何展示逻辑结构。usage store/api 的 appType 为透传字符串，无其他映射表。 |

## 二、验证结果

| 验证项 | 命令 | 结果 |
|--------|------|------|
| typecheck 门禁 | `vue-tsc --noEmit`（build 脚本首段） | ✅ 通过，0 error |
| 产物构建 | `npm --prefix frontend run build`（vite） | ✅ built in ~0.7s |
| 改动范围核对 | — | ✅ 未触碰任何后端 Go 文件；未动范围外组件/技术栈；未执行任何 git 操作 |

## 三、遗留事项（诚实披露）

1. **Wails 绑定需重新生成**：`wailsjs/go/piplugin/*` 为手写同构文件（Wails 运行时绑定来自 Go 端注册，与生成 JS 无关，故手写文件运行时可用），但应执行 `wails generate module`（或 `wails build`）让生成器接管并产出 `models.ts` 中的 piplugin 类型，替换手写 `Service.d.ts` 内的本地类型别名。
2. **浏览器/运行时交互验证未执行**：Wails 桌面应用需完整启动 Go 后端 + 前端才能做真实点击验证（且依赖第 1 条的运行时环境），本次仅完成构建 + typecheck 门禁（任务指定验收标准）。建议 diting 审核时在 `wails dev` 环境下做一轮 L2 交互冒烟：列表刷新 → 选中包查看详情/资源清单 → 安装/更新/移除对话框流转。
3. **文案**：周边组件以中文为主，PiPluginPanel 关键说明句为中英双语（随任务要求），按钮/状态文案与 OpenCode 面板中文风格保持一致。

## 四、建议下一步

diting 审核前端变更（重点核对 wailsjs 手写绑定与后端 `internal/piplugin` 方法名/返回 JSON 字段的一致性，及 ExtensionsView 分支顺序）。
