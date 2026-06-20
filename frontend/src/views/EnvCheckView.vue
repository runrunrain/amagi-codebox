<template>
  <section class="view-envcheck">
    <PageHead title="环境检测" description="CLI 工具安装状态、版本与 PATH 校验" />
    <EnvCheckSettings />
  </section>
</template>

<script setup lang="ts">
import PageHead from '../components/ui/PageHead.vue'
import EnvCheckSettings from './settings/EnvCheckSettings.vue'
</script>

<style scoped>
.view-envcheck {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 18px;
  padding: 32px 36px;
  overflow: auto;
}
</style>
