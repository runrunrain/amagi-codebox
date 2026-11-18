<template>
  <aside v-if="error || count > 0" class="legacy-notice" role="status">
    <template v-if="error">{{ error }}</template>
    <template v-else>
      旧版仓库还有 {{ count }} 条{{ kind === 'skills' ? '技能' : ' MCP' }}登记，保留在 <code>{{ summary?.path }}</code>。
      本页不再读取旧版清单，也不会自动迁移或回收已分发的配置。
    </template>
  </aside>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { getLegacySharedExtensionsSummary } from '../../api/legacySharedExtensions';
import type { LegacySharedExtensionsSummary } from '../../api/legacySharedExtensions';

const props = defineProps<{ kind: 'skills' | 'mcp' }>();
const summary = ref<LegacySharedExtensionsSummary>();
const error = ref('');
const count = computed(() => summary.value?.[props.kind] ?? 0);

onMounted(async () => {
  try {
    summary.value = await getLegacySharedExtensionsSummary();
  } catch {
    error.value = '无法确认旧版共用扩展登记；旧版数据未修改。';
  }
});
</script>

<style scoped>
.legacy-notice {
  padding: 8px 12px;
  border: 1px solid var(--separator);
  border-radius: 8px;
  color: var(--secondary);
  font-size: 12px;
  overflow-wrap: anywhere;
}
</style>
