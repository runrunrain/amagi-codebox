<template>
  <section class="view-provider">
    <PageHead title="Provider Center" description="统一管理服务提供商与各引擎预设"/>
    <EmptyState icon="☁" title="即将上线" description="Provider Center 将在 P1 阶段实现完整的管理界面"/>
  </section>
</template>

<script setup lang="ts">
import PageHead from '../components/ui/PageHead.vue'
import EmptyState from '../components/ui/EmptyState.vue'
</script>

<style scoped>
.view-provider {
  padding: 32px 36px;
  display: flex;
  flex-direction: column;
  gap: 22px;
}
</style>
