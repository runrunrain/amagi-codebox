<!-- 契约 §4 #5：列表工具栏（分组标题 17px/600 + 灰色计数徽章，右侧操作组）。
     loading=true 时根节点挂 is-loading 类（刷新态标记，断言依赖）。 -->
<template>
  <div
    :class="['list-toolbar', { 'is-loading': loading }]"
    :aria-busy="loading ? 'true' : undefined"
  >
    <div class="lt-title-wrap">
      <h3 class="lt-title">{{ title }}</h3>
      <span class="lt-count">{{ count }}</span>
    </div>
    <div class="lt-actions">
      <slot name="actions" />
    </div>
  </div>
</template>

<script setup lang="ts">
interface Props {
  title: string;
  count: number;
  loading?: boolean;
}

withDefaults(defineProps<Props>(), {
  loading: false,
});
</script>

<style scoped>
.list-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.lt-title-wrap {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.lt-title {
  font-size: 17px;
  font-weight: 600;
  color: var(--label);
  margin: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.lt-count {
  font-size: 11px;
  font-weight: 600;
  color: var(--secondary);
  background: var(--control);
  border-radius: 999px;
  padding: 2px 8px;
  line-height: 1.4;
  flex-shrink: 0;
}

.lt-actions {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}
</style>
