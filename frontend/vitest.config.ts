import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'

// 桌面前端单测基建：集中式 frontend/src/__tests__/ 镜像 src 目录结构（与 mobile 对齐）。
// environment 'node'：纯逻辑/纯 TS 单测，不引 jsdom，避免新依赖面。
// plugins: @vitejs/plugin-vue（既有 devDependency）——Phase 1 修复批次 F08 起
// 支持渲染 .vue 组件的真实模板（SSR renderToString，仍无 DOM 环境依赖）。
export default defineConfig({
  plugins: [vue()],
  test: {
    environment: 'node',
    include: ['src/__tests__/**/*.test.ts'],
  },
})
