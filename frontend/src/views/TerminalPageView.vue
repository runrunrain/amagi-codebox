<template>
  <section class="view-terminal">
    <PageHead title="终端" description=""/>
    <EmptyState icon="▉" title="即将上线" description="终端页面将在 P1 阶段实现，包含 xterm.js 集成与真实 PTY 交互"/>
  </section>
</template>

<script setup lang="ts">
import PageHead from '../components/ui/PageHead.vue'
import EmptyState from '../components/ui/EmptyState.vue'
</script>

<style scoped>
.view-terminal {
  padding: 32px 36px;
  display: flex;
  flex-direction: column;
  gap: 22px;
}
</style>
