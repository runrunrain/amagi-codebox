<template>
  <!-- R6：环境变量面板自扩展中心迁入设置页（组件本体见 components/settings/EnvVarsPanel.vue）。
       不加 RemoteScopeBanner：SettingsView 在远程模式已有 RemoteHostSettingsCard 顶卡；
       远程模式下 wails 调用失败由面板自身 ErrorState 呈现，不额外造提示。 -->
  <div class="env-vars-settings">
    <EnvVarsPanel />
  </div>
</template>

<script setup lang="ts">
import EnvVarsPanel from '../../components/settings/EnvVarsPanel.vue';
</script>

<style scoped>
.env-vars-settings {
  display: flex;
  flex-direction: column;
  gap: 18px;
}
</style>
