<!--
  TEST-ONLY harness 根组件：渲染 PG-05 页面 + 全局 Toast 宿主（与 App.vue 同构）。
-->
<template>
  <RemoteSettings />
  <Toast />
</template>

<script setup lang="ts">
import RemoteSettings from '../../src/views/settings/RemoteSettings.vue';
import Toast from '../../src/components/common/Toast.vue';
</script>
