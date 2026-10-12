<template>
  <div class="config-content-group" role="group" :aria-label="label">
    <div class="ccg-head">
      <!-- SVG 仅取静态图标库，调用方传固定图标名；不渲染用户字段。 -->
      <span class="ccg-icon" aria-hidden="true" :style="{ color: getAccent(icon) }" v-html="getIcon(icon)" />
      <div class="ccg-heading"><slot name="header" /></div>
    </div>
    <div class="ccg-body"><slot /></div>
  </div>
</template>
<script setup lang="ts">
import { getIcon, getAccent } from './icons';
interface Props { icon: string; label: string }
defineProps<Props>();
</script>
<style scoped>
.config-content-group { min-width:0; border:1px solid var(--separator); border-radius:8px; background:var(--card); box-shadow:none; padding:12px; }
.ccg-head { display:flex; align-items:center; gap:8px; min-width:0; margin-bottom:8px; }
.ccg-icon { flex:0 0 24px; width:24px; height:24px; border-radius:6px; background:var(--control); display:flex; align-items:center; justify-content:center; }
.ccg-icon :deep(svg) { display:block; width:16px; height:16px; }
.ccg-heading { flex:1; min-width:0; }
.ccg-heading :deep(.field-label) { width:100%; min-width:0; flex-wrap:wrap; }
.ccg-body { min-width:0; display:flex; flex-direction:column; gap:6px; }
@container editor-detail (max-width:479px) { .config-content-group { padding:8px; } }
@supports not (container-type: inline-size) { .config-content-group { padding:8px; } }
</style>
