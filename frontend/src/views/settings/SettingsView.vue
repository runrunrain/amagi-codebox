<template>
  <section class="view-settings-page">
    <PageHead
      v-if="currentMeta"
      :title="currentMeta.title"
      :description="currentMeta.description"
    />
    <div class="settings-page">
      <!-- RC4-2：远程模式下读写宿主应用设置（legacy）；下方其余设置均为本机内容 -->
      <template v-if="rcStore.isRemoteMode">
        <RemoteHostSettingsCard @configure-token="tokenDialogOpen = true" />
      </template>      <GeneralSettings v-if="activeKey === 'general'" />
      <ShellSettings v-else-if="activeKey === 'shell'" />
      <TerminalSettings v-else-if="activeKey === 'terminal'" />
      <AppearanceSettings v-else-if="activeKey === 'appearance'" />
      <RemoteSettings v-else-if="activeKey === 'remote'" />
      <AgentProfileSettings v-else-if="activeKey === 'agent-profiles'" />
      <UpdateSettings v-else-if="activeKey === 'update'" />
      <AboutSettings v-else-if="activeKey === 'about'" />
    </div>

    <!-- RC4-2：legacy 访问令牌配置（远程模式） -->
    <LegacyTokenDialog
      v-model:open="tokenDialogOpen"
      :host-name="rcStore.currentHostName"
      :has-token="rcStore.currentHasLegacyToken"
      @changed="onTokenChanged"
    />
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useUIStore } from '../../stores/ui'
import { useRemoteClientStore } from '../../stores/remoteClient'
import PageHead from '../../components/ui/PageHead.vue'
import RemoteHostSettingsCard from '../../components/remote/RemoteHostSettingsCard.vue'
import LegacyTokenDialog from '../../components/remote/LegacyTokenDialog.vue'
import GeneralSettings from './GeneralSettings.vue'
import ShellSettings from './ShellSettings.vue'
import TerminalSettings from './TerminalSettings.vue'
import AppearanceSettings from './AppearanceSettings.vue'
import RemoteSettings from './RemoteSettings.vue'
import AgentProfileSettings from './AgentProfileSettings.vue'
import UpdateSettings from './UpdateSettings.vue'
import AboutSettings from './AboutSettings.vue'

const uiStore = useUIStore()
const rcStore = useRemoteClientStore()
const activeKey = computed(() => uiStore.activeSettingKey)

const tokenDialogOpen = ref(false)

/** 令牌配置变化后重拉宿主设置（卡片可能停留在 needs-token/401 态）。 */
function onTokenChanged() {
  if (rcStore.isRemoteMode) void rcStore.loadRemoteSettings()
}

const META: Record<string, { title: string; description: string }> = {
  general: { title: '常规设置', description: '配置应用启动默认项' },
  shell: { title: 'Shell', description: '自定义终端 Shell 路径' },
  terminal: { title: '终端设置', description: '终端渲染与滚动缓冲' },
  appearance: { title: '外观', description: '皮肤背景、调光与模糊' },
  remote: { title: '远程控制', description: 'HTTP/WebSocket 远程访问与移动端连接' },
  'agent-profiles': { title: 'Agent 配置档', description: 'CLI 模型配置多档保存与一键切换' },
  update: { title: '软件更新', description: '检查并安装新版本' },
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
