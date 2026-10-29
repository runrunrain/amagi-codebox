# Pi 登录预设输出额度

## 实现
- 在 `frontend/src/components/provider/PiLoginProviderDetail.vue` 增加输出上限（tokens）、目录最大输出提示和「填入模型最大值」按钮；填写后须保存，编辑回填与列表显示完整。
- `PiLoginPreset.max_tokens` 持久化；留空/0 恢复默认，前端拒绝非法/小数/不安全整数，后端拒绝负值。
- 目录从本地 `models-store.json` / `models.json` 模型定义及现有前线契约抽取 `maxTokens`，不把 `modelOverrides.maxTokens` 当成目录最大值，不以 `contextWindow` 猜测；未知时禁用按钮。
- 保存与启动链写入 canonical Pi root 的 `providers.<provider>.modelOverrides.<model>.maxTokens`；删除最后一个预设、清零和切换模型会对账清理。保留其他模型、窗口与 compat 配置。
- 同供应商/模型预设共享窗口和输出覆写，配置同步对齐，界面明确提示。
- 输出额度只同步宿主本机 Pi，不分发至 WSL，UI 已明示。独立审核发现现有 WSL 正值补齐机制没有清理归属，故撤销了额外 WSL 扩展；三个 WSL 文件最终相对 HEAD 零 diff，未引入不可撤销的 WSL 输出覆写。
- Wails 绑定通过 `go run -tags bindings .` 官方生成。原有生成文件落后于 Go 导出面，保留生成器同步产生的其他既有接口/类型更新；没有新增对应业务实现。生成器本身产生的空行制表符可能被 `git diff --check` 作为尾空白报告。

## 验证
工作目录：`/Users/run.mao/workpace/amagi-codebox`。
- `go test ./... -count=1`：PASS，包含生产 NewApp 保存回调写入/切换模型清理/删除清理测试，全部 Go 包通过。
- `npm --prefix frontend test`：PASS，41 文件、404 测试。
- `npm --prefix frontend run build`：PASS，vue-tsc 与 Vite 均通过（绑定最终重新生成后重跑）。
- 浏览器：现有 `e2e/pi-login.html` + `TestPiLoginBrowserBridge`，隔离 HOME、真实 Go 配置服务、合成认证和模型目录。agent-browser 验证目录模型按钮填入 65536、保存后列表显示输出 65536、编辑回填 65536、手动改为 1024 后按钮恢复 65536、未知模型按钮禁用。桥接测试 PASS；浏览器和服务均已关闭。
- 截图：`output-limit.png`。

## 限制与交付
- 目录值是模型元数据，不是供应商实弹探测保证；实际输出仍受供应商、上下文余量和推理预算限制。
- 已有 Pi 会话需要重新加载模型或重启才能消费新模型元数据。
- 未打包/安装桌面应用，未修改真实用户配置，未提交 Git。
- 独立 quick review 的唯一 Major F01（WSL 输出覆写无法清理）通过撤销额外 WSL 分发、明确本机范围关闭，增量复核 PASS；两轮审核任务均已接受。原始报告见 `/Users/run.mao/.pi/agent/amagi/artifacts/5d0e5a28-f9cf-4bb9-9878-b76ccede022f/result.md`，增量报告见 `/Users/run.mao/.pi/agent/amagi/artifacts/60e0ca6f-70d3-4b34-856d-4dc10591eb49/result.md`。
- 审核修复后再次运行 `go test ./... -count=1 && npm --prefix frontend test && npm --prefix frontend run build`，exit=0；日志：`/Users/run.mao/.pi/agent/amagi/data/run-bg/bg-b9d5c4a3.log`。
