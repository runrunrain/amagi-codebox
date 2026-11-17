/**
 * webui-claude Vite 配置（design §7.2/§7.3，t7）。
 *
 * 双部署形态硬约束（阻断 2-1，design §7.2）——同一份构建产物必须同时运行于：
 *   - 桌面：http://127.0.0.1:<port>/（回环根路径，internal/webui/service.go statusOf）
 *   - 远程：https://<remote>/webui/{sid}/（代理子路径，两种 URL 均以 / 结尾）
 *
 * 因此 base 固定 './'（相对资源引用）：产物 index.html 引用 ./assets/...，
 * 两种 base 下浏览器都解析到正确子路径。禁止默认 base:'/'——绝对 /assets/...
 * 在远程子路径形态 404 → HTML 200 但白屏，且 iframe load 事件照常触发、宿主
 * 与移动端均无从侦测（design §1 表 WebPlaneHost 行、R14）。
 *
 * 构建产物直接落 Go embed 目录（design §7.3）：outDir=../internal/claudewebui/dist，
 * emptyOutDir=true（目录在工程外，必须显式声明才会清空重建）。清空会连带删除
 * 入库占位 .gitkeep——restore-dist-gitkeep 插件在 closeBundle（产物写盘后）
 * 恢复之（design §13-R6：fresh checkout 编译依赖 dist 目录有入库文件）。
 * 构建脚本用 npm ci（fresh checkout 依赖入库的 package-lock.json，R11）。
 *
 * Vitest 配置内联在本文件（test 字段）：纯逻辑测试（basePath/doc/dist-base）
 * 无需 DOM，node 环境足够。
 */
import { writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import vue from '@vitejs/plugin-vue'
import { defineConfig, type Plugin } from 'vitest/config'

const distGitkeepPath = fileURLToPath(
  new URL('../internal/claudewebui/dist/.gitkeep', import.meta.url),
)

// closeBundle 在 emptyOutDir 清空与产物写入之后执行——每次 vite build 结束
// 都补回空 .gitkeep，否则本地构建后 git 恒显示该入库文件被删除（§13-R6）。
const restoreDistGitkeep = (): Plugin => ({
  name: 'restore-dist-gitkeep',
  closeBundle() {
    writeFileSync(distGitkeepPath, '')
  },
})

export default defineConfig({
  plugins: [vue(), restoreDistGitkeep()],
  // 双形态硬约束：相对资源引用（见文件头注释）。
  base: './',
  build: {
    outDir: '../internal/claudewebui/dist',
    emptyOutDir: true,
  },
  test: {
    environment: 'node',
    include: ['src/__tests__/**/*.test.ts'],
  },
})
