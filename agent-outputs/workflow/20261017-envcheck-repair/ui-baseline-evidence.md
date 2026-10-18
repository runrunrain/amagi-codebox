# 环境检测 UI 修复前证据

工具：agent-browser，隔离 session envcheck-repair-e826c0fb2831。
页面：e2e/envcheck-management.html，真实 EnvCheckSettings.vue + 页面内 Wails 替身。所有软件管理仅修改内存，未调用真实后端。

1. `?scenario=dependency` → 选择 Pi：状态 canInstall=false，顶层 solutions=[install_node]，issue.solutions=[]。页面无安装依赖按钮，普通「安装」仍可点击。截图 `/tmp/codebox-envcheck-dependency-before.png`。
2. `?scenario=fast` → 选择 Pi → 安装：操作在第一次快照前已经终态，卡片更新为已安装，但无成功结果通知（watch只观测running→null，未经历running就漏报）。
3. `?scenario=snapshot-error` → 选择 Pi → 安装：受理后的首次GetEnvCheckSnapshot拒绝一次。等待夹具operation.status=succeeded后，界面仍显示「未安装」且安装按钮可点；轮询没有启动，操作受理回执没有驱动状态。

复现命令使用 agent-browser open/find role button click/wait --fn/snapshot。修复后须在相同入口重新验证。
