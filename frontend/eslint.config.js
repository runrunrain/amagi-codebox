// ESLint flat config — Phase 3 S7 lint 门禁
// 分层策略：
//  - error：语法级/正确性问题（eslint recommended + tseslint recommended 中非噪音规则 + vue essential）
//  - warn：存量巨量风格问题（如 no-explicit-any），允许存量存在，仅提示不阻塞
//  - off：与 vue-tsc/vite 构建链路重复或对本项目无价值的规则
// build 链路（vue-tsc --noEmit && vite build）不依赖本配置，lint 独立成 npm run lint。
import js from '@eslint/js'
import tseslint from 'typescript-eslint'
import pluginVue from 'eslint-plugin-vue'

export default tseslint.config(
  {
    ignores: [
      'dist/**',
      'node_modules/**',
      'wailsjs/**', // Wails 自动生成绑定，禁止手改也不 lint
      'src_legacy_backup/**', // 历史备份，已脱离维护
      'test-results/**',
      'playwright-report/**',
      'coverage/**',
    ],
  },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  ...pluginVue.configs['flat/essential'],
  {
    // .vue 的 <script> 块走 TS parser（vue-eslint-parser 已在 essential 中接管模板）
    files: ['**/*.vue'],
    languageOptions: {
      parserOptions: {
        parser: tseslint.parser,
        extraFileExtensions: ['.vue'],
      },
    },
    rules: {
      // vue-eslint-parser 下 no-undef 对 <script setup> 顶层误报，类型检查由 vue-tsc 兜底
      'no-undef': 'off',
    },
  },
  {
    files: ['**/*.{ts,tsx,mts,cts,vue}'],
    rules: {
      // ---------- 存量分层：warn（存量约 247 处 any，先提示不阻塞） ----------
      '@typescript-eslint/no-explicit-any': 'warn',
      // ---------- 存量分层：off（设计决策，见下） ----------
      // 本项目 UI 套件有意使用单词组件名（Toast/Sidebar/Badge/Dialog 等），不作为页面路由组件
      'vue/multi-word-component-names': 'off',
      '@typescript-eslint/no-unused-vars': [
        'error',
        { argsIgnorePattern: '^_', varsIgnorePattern: '^_', caughtErrorsIgnorePattern: '^_' },
      ],
    },
  },
  {
    // ANSI 转义序列剥离必须匹配控制字符 \x1b/\x07，属刻意为之
    files: ['src/utils/sessionDetailText.ts'],
    rules: {
      'no-control-regex': 'off',
    },
  },
  {
    // 环境声明文件：DefineComponent<{}, {}, any> 是 vue 声明的经典写法，交由 vue-tsc 管控
    files: ['**/*.d.ts'],
    rules: {
      '@typescript-eslint/no-empty-object-type': 'off',
    },
  },
  {
    // Node 侧脚本/配置文件允许 require 风格与 console
    files: ['*.config.{js,mjs,ts}', 'scripts/**/*.mjs', 'e2e/**/*.{ts,mjs}', 'harness/**/*.{ts,mjs}', 'playwright.*.config.ts'],
    languageOptions: {
      globals: {
        process: 'readonly',
        console: 'readonly',
        __dirname: 'readonly',
        __filename: 'readonly',
        Buffer: 'readonly',
        setTimeout: 'readonly',
        clearTimeout: 'readonly',
        setInterval: 'readonly',
        clearInterval: 'readonly',
        URL: 'readonly',
        URLSearchParams: 'readonly',
        fetch: 'readonly',
        AbortController: 'readonly',
        AbortSignal: 'readonly',
        // playwright page.evaluate 回调在浏览器上下文执行（harness/remote/run.playwright.mjs）
        window: 'readonly',
        document: 'readonly',
        navigator: 'readonly',
        localStorage: 'readonly',
        location: 'readonly',
        getComputedStyle: 'readonly',
        requestAnimationFrame: 'readonly',
        MutationObserver: 'readonly',
        performance: 'readonly',
        globalThis: 'readonly',
      },
    },
  },
)
