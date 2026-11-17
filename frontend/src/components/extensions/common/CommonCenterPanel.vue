<template>
  <div class="common-center-panel">
    <!-- 子段：公共技能 | 公共 MCP -->
    <div class="cc-sub-tabs">
      <Segmented v-model="subTab" :options="subTabOptions" variant="underline" />
    </div>

    <CommonSkillsPanel v-if="subTab === 'skills'" />
    <CommonMCPPanel v-else />
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue';
import Segmented from '../../ui/Segmented.vue';
import CommonSkillsPanel from './CommonSkillsPanel.vue';
import CommonMCPPanel from './CommonMCPPanel.vue';

// 子段切换为纯 UI 态（两个面板各自管理数据加载）
const subTab = ref<'skills' | 'mcp'>('skills');
const subTabOptions = [
  { value: 'skills', label: '公共技能' },
  { value: 'mcp', label: '公共 MCP' },
];
</script>

<style scoped>
.common-center-panel {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.cc-sub-tabs {
  display: flex;
  gap: 24px;
  border-bottom: 1px solid var(--separator);
}
</style>
