import { defineConfig } from 'vitest/config'

// 桌面前端单测基建：集中式 frontend/src/__tests__/ 镜像 src 目录结构（与 mobile 对齐）。
// environment 'node'：纯逻辑/纯 TS 单测，不引 jsdom，避免新依赖面；
// 未来出现组件级测试需求时再评估 DOM 环境与 vue 插件接入。
export default defineConfig({
  test: {
    environment: 'node',
    include: ['src/__tests__/**/*.test.ts'],
  },
})
