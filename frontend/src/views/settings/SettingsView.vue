<template>
  <section class="view-settings-page">
    <PageHead
      v-if="currentMeta"
      :title="currentMeta.title"
      :description="currentMeta.description"
    />
    <div class="settings-page">
      <GeneralSettings v-if="activeKey === 'general'" />
      <ShellSettings v-else-if="activeKey === 'shell'" />
      <TerminalSettings v-else-if="activeKey === 'terminal'" />
      <RemoteSettings v-else-if="activeKey === 'remote'" />
      <UpdateSettings v-else-if="activeKey === 'update'" />
      <EnvCheckSettings v-else-if="activeKey === 'envcheck'" />
      <AboutSettings v-else-if="activeKey === 'about'" />
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useUIStore } from '../../stores/ui'
import PageHead from '../../components/ui/PageHead.vue'
import GeneralSettings from './GeneralSettings.vue'
import ShellSettings from './ShellSettings.vue'
import TerminalSettings from './TerminalSettings.vue'
import RemoteSettings from './RemoteSettings.vue'
import UpdateSettings from './UpdateSettings.vue'
import EnvCheckSettings from './EnvCheckSettings.vue'
import AboutSettings from './AboutSettings.vue'

const uiStore = useUIStore()
const activeKey = computed(() => uiStore.activeSettingKey)

const META: Record<string, { title: string; description: string }> = {
  general: { title: '常规设置', description: '配置应用启动默认项' },
  shell: { title: 'Shell', description: '自定义终端 Shell 路径' },
  terminal: { title: '终端设置', description: '终端渲染与滚动缓冲' },
  remote: { title: '远程控制', description: 'HTTP/WebSocket 远程访问与移动端连接' },
  update: { title: '软件更新', description: '检查并安装新版本' },
  envcheck: { title: '环境检测', description: 'CLI 工具安装状态与修复' },
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
