<template>
  <section class="view-extensions">
    <PageHead title="扩展管理" description="插件、工作区与环境变量管理"/>
    <EmptyState icon="◈" title="即将上线" description="扩展管理将在 P1 阶段实现完整的管理界面"/>
  </section>
</template>

<script setup lang="ts">
import PageHead from '../components/ui/PageHead.vue'
import EmptyState from '../components/ui/EmptyState.vue'
</script>

<style scoped>
.view-extensions {
  padding: 32px 36px;
  display: flex;
  flex-direction: column;
  gap: 22px;
}
</style>
