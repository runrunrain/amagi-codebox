<template>
  <div class="plane-root">
    <header class="plane-header">
      <span class="plane-title" :title="doc.title">{{ headerTitle }}</span>
      <span v-if="doc.truncatedHead" class="plane-note">已省略较早消息</span>
      <span v-if="reconnecting" class="plane-note" role="status">连接中断，重连中…</span>
    </header>

    <!-- 页内错误态（design §7.2-3，白屏防线的另一半）：token 缺失/非法或
         /api/info 失败时给出可读错误与重试，不依赖宿主 iframe load 侦测。 -->
    <div v-if="phase === 'error'" class="plane-error" role="alert">
      <p class="plane-error-title">{{ errorTitle }}</p>
      <p class="plane-error-detail">{{ errorDetail }}</p>
      <button type="button" class="plane-btn" @click="retry">重试</button>
    </div>

    <Stream v-else :doc="doc" :ended="ended" />

    <!-- 输入面（t8）：错误相位不渲染；.stream-scroll 为 flex:1，本组件自然落底。 -->
    <Composer v-if="phase !== 'error'" :token="planeToken" :busy="doc.busy" :ended="ended" />

    <!-- aria-live=polite 仅终态播报（design §7.2：busy 起落不刷屏，
         只在回复完成/会话结束时播报一次）。 -->
    <div class="sr-only" aria-live="polite">{{ announcement }}</div>
  </div>
</template>

<script setup lang="ts">
/**
 * App.vue — webui-claude 根组件（t7）。
 *
 * 接线：位置/凭证解析（?mock=1、?skin=light、#/t= token）→ 数据源选择
 * （MockDataSource / HttpDataSource）→ 回调落到 StreamDoc 与 UI 相位。
 * 输入面（Composer，t8：提交/中断/草稿/宿主桥）在非错误相位渲染，
 * token 经 planeToken 下发；mock 模式为占位值（提交仅会命中 401 行内提示）。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { browserTransport, extractPlaneToken, HttpDataSource } from './data/httpSource'
import { MockDataSource } from './data/mockSource'
import type { DataSource, SourceCallbacks } from './data/source'
import { createStreamDoc } from './state/doc'
import Stream from './views/Stream.vue'
import Composer from './views/Composer.vue'

const doc = createStreamDoc()
const phase = ref<'connecting' | 'live' | 'error'>('connecting')
const errorKind = ref<'no-token' | 'info' | 'history'>('info')
const ended = ref(false)
const reconnecting = ref(false)
const announcement = ref('')
/** 平面 token（Composer 的 API Bearer 用）；mock 模式为占位值。 */
const planeToken = ref('')

let source: DataSource | null = null
let mockMode = false

const headerTitle = computed(() => doc.title || 'Claude 会话')

const errorTitle = computed(() => {
  switch (errorKind.value) {
    case 'no-token':
      return '缺少访问凭证'
    case 'info':
      return '无法连接会话平面服务'
    case 'history':
      return '无法加载会话历史'
  }
})

const errorDetail = computed(() => {
  switch (errorKind.value) {
    case 'no-token':
      return 'URL 未携带访问凭证（#/t=…）。请从 CodeBox 重新打开 Web 平面。'
    case 'info':
      return '会话平面服务不可达或已停止。可稍后重试，或切回终端平面。'
    case 'history':
      return '会话历史加载失败。可重试，或切回终端平面。'
  }
})

function buildCallbacks(): SourceCallbacks {
  return {
    onHello: () => {
      phase.value = 'live'
      reconnecting.value = false
    },
    onTranscript: (ev) => {
      phase.value = 'live'
      if (ev.snapshot) {
        doc.applySnapshot(ev.items, { title: ev.title, truncatedHead: ev.truncatedHead })
      } else {
        doc.applyRecords(ev.items)
      }
    },
    onBusy: (value) => doc.setBusy(value),
    onEnded: () => {
      ended.value = true
    },
    onReconnect: () => {
      reconnecting.value = true
    },
    onFatal: (kind) => {
      phase.value = 'error'
      errorKind.value = kind
    },
  }
}

function startSource(): void {
  const token = extractPlaneToken(window.location.hash)
  mockMode = new URLSearchParams(window.location.search).get('mock') === '1'
  if (!mockMode && !token) {
    phase.value = 'error'
    errorKind.value = 'no-token'
    return
  }
  const callbacks = buildCallbacks()
  const transport = browserTransport()
  if (mockMode) {
    // 开发态剧本回放：无后端依赖（design §7.2 ?mock=1）。Composer 占位
    // token 只会命中真实端点的 401，行内错误提示即预期开发态表现。
    planeToken.value = 'mock'
    source = new MockDataSource({ transport, callbacks })
  } else {
    planeToken.value = token as string
    source = new HttpDataSource({
      transport,
      loc: window.location,
      token: token as string,
      callbacks,
    })
  }
  source.start()
}

function retry(): void {
  source?.stop()
  source = null
  ended.value = false
  reconnecting.value = false
  phase.value = 'connecting'
  startSource()
}

onMounted(() => {
  startSource()
})

onBeforeUnmount(() => {
  source?.stop()
  source = null
})

// aria-live 终态播报：busy 下落沿 → 「回复完成」；ended → 「会话已结束」。
watch(
  () => doc.busy,
  (value, old) => {
    if (old && !value) announcement.value = '回复完成'
  },
)
watch(ended, (value) => {
  if (value) announcement.value = '会话已结束'
})
</script>

<style>
/* 全局（非 scoped）：body 透明 + 语义配色映射（tokens.css 1:1 快照之上的
   双肤组合层——深色嵌入缺省，?skin=light → .webui-embedded-light 浅色；
   零手写色值，全部引用 token）。 */
html,
body {
  margin: 0;
  padding: 0;
  height: 100%;
}

/* 透明 body：内嵌态透出宿主皮肤层（与 pi 页面同约定，design §7.2）。 */
body {
  background: transparent;
  color: var(--termText);
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', 'PingFang SC',
    'Microsoft YaHei', sans-serif;
  font-size: 13px;
  line-height: 1.6;
}

:root {
  --plane-text: var(--termText);
  --plane-text-dim: var(--termGray);
  --plane-accent: var(--termBlue);
  --plane-success: var(--termGreen);
  --plane-danger: var(--danger);
  --plane-card: var(--termBg);
  --plane-border: var(--termGray);
  --plane-user-card: var(--termBg);
}

.webui-embedded-light {
  --plane-text: var(--label);
  --plane-text-dim: var(--secondary);
  --plane-accent: var(--accent);
  --plane-success: var(--success-strong);
  --plane-danger: var(--danger-strong);
  --plane-card: var(--card);
  --plane-border: var(--separator);
  --plane-user-card: var(--control);
}

.plane-root {
  display: flex;
  flex-direction: column;
  height: 100vh;
  color: var(--plane-text);
}

.plane-header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 12px;
  border-bottom: 1px solid var(--plane-border);
  flex: none;
}

.plane-title {
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  min-width: 0;
}

.plane-note {
  color: var(--plane-text-dim);
  font-size: 12px;
  white-space: nowrap;
}

.plane-error {
  margin: 48px auto;
  max-width: 420px;
  padding: 20px 24px;
  text-align: center;
  background: var(--plane-card);
  border: 1px solid var(--plane-border);
  border-radius: var(--radius-card);
}

.plane-error-title {
  font-weight: 600;
  margin: 0 0 8px;
}

.plane-error-detail {
  color: var(--plane-text-dim);
  margin: 0 0 16px;
  white-space: pre-line;
}

.plane-btn {
  padding: 6px 18px;
  font-size: 13px;
  color: var(--plane-text);
  background: var(--plane-card);
  border: 1px solid var(--plane-border);
  border-radius: var(--radius-sm);
  cursor: pointer;
}

.plane-btn:hover {
  border-color: var(--plane-accent);
  color: var(--plane-accent);
}

.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
  white-space: nowrap;
  border: 0;
}
</style>
