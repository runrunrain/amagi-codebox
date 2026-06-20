<template>
  <section class="view-settings-page">
    <PageHead
      v-if="currentMeta && activeKey !== 'rules'"
      :title="currentMeta.title"
      :description="currentMeta.description"
    />
    <div class="settings-page">
      <GeneralSettings v-if="activeKey === 'general'" />
      <ShellSettings v-else-if="activeKey === 'shell'" />
      <TerminalSettings v-else-if="activeKey === 'terminal'" />
      <RemoteSettings v-else-if="activeKey === 'remote'" />
      <UpdateSettings v-else-if="activeKey === 'update'" />
      <RulesView v-else-if="activeKey === 'rules'" />
      <AboutSettings v-else-if="activeKey === 'about'" />
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, defineAsyncComponent } from 'vue'
import { useUIStore } from '../../stores/ui'
import PageHead from '../../components/ui/PageHead.vue'
import GeneralSettings from './GeneralSettings.vue'
import ShellSettings from './ShellSettings.vue'
import TerminalSettings from './TerminalSettings.vue'
import RemoteSettings from './RemoteSettings.vue'
import UpdateSettings from './UpdateSettings.vue'
import AboutSettings from './AboutSettings.vue'

// 异步加载避免与 router 动态导入重复打包警告
const RulesView = defineAsyncComponent(() => import('../RulesView.vue'))

const uiStore = useUIStore()
const activeKey = computed(() => uiStore.activeSettingKey)

const META: Record<string, { title: string; description: string }> = {
  general: { title: '常规设置', description: '配置应用启动默认项' },
  shell: { title: 'Shell', description: '自定义终端 Shell 路径' },
  terminal: { title: '终端设置', description: '终端渲染与滚动缓冲' },
  remote: { title: '远程控制', description: 'HTTP/WebSocket 远程访问与移动端连接' },
  update: { title: '软件更新', description: '检查并安装新版本' },
  rules: { title: '注入规则', description: '管理 API 注入规则与代理状态' },
  about: { title: '关于', description: '应用信息' },
}

const currentMeta = computed(() => META[activeKey.value] || null)
</script>

<style scoped>
.view-settings-page {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 22px;
  padding: 32px 36px;
  overflow: auto;
}

.settings-page {
  display: flex;
  flex-direction: column;
  gap: 18px;
}
</style>
