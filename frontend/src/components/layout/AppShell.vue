<template>
  <div class="app">
    <Sidebar />
    <main class="main">
      <SettingsView v-if="uiStore.isInSettingsMode" :key="settingsLoadKey" />
      <slot v-else />
    </main>
  </div>
</template>

<script setup lang="ts">
import { defineAsyncComponent, defineComponent, h, ref } from 'vue'
import Sidebar from './Sidebar.vue'
import LoadingState from '../ui/LoadingState.vue'
import ErrorState from '../ui/ErrorState.vue'
import { useUIStore } from '../../stores/ui'

// 更换 key 会重新挂载异步组件并重新执行 loader（动态 import 可重试）。
const settingsLoadKey = ref(0)

// chunk 缺失/损坏或动态 import 被拒绝时呈现错误态 + 重试入口，避免设置
// 模式留下空白区域。
const SettingsLoadError = defineComponent({
  name: 'SettingsLoadError',
  setup() {
    return () =>
      h(ErrorState, {
        title: '设置加载失败',
        message: '设置界面资源加载失败，请重试。',
        onRetry: () => {
          settingsLoadKey.value++
        },
      })
  },
})

// 设置树（含 qrcode 的 RemoteSettings）体积较大且不走路由，
// 异步加载使其退出入口 chunk；delay 避免快速切换时 loading 闪烁。
const SettingsView = defineAsyncComponent({
  loader: () => import('../../views/settings/SettingsView.vue'),
  loadingComponent: LoadingState,
  errorComponent: SettingsLoadError,
  delay: 120,
})

const uiStore = useUIStore()
</script>

<style scoped>
.app {
  display: flex;
  height: 100vh;
  overflow: hidden;
}

.main {
  flex: 1;
  display: flex;
  flex-direction: column;
  background: var(--card);
  position: relative;
  min-width: 0;
  min-height: 0;
  overflow: auto;
}
</style>
