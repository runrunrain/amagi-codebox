<!-- 契约 §4 #10：详情页子项行（类型徽章 + 名称 + 单行描述 + 行尾开关）。
     toggleable=false 时不渲染 Switch（Codex 子项禁用是后端 no-op，一律只读）。
     可测试性契约：expose toggleWith(v)（即 Switch 的 update 处理器）。 -->
<template>
  <div class="sub-item-row">
    <Badge v-if="typeBadge" type="type" :text="typeBadge" :color="badgeColor" />
    <div class="sir-main">
      <span class="sir-name">{{ name }}</span>
      <span v-if="description" class="sir-desc">{{ description }}</span>
    </div>
    <Switch
      v-if="toggleable"
      :model-value="enabled"
      @update:model-value="toggleWith"
    />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import Badge from '../../ui/Badge.vue';
import Switch from '../../ui/Switch.vue';

interface Props {
  name: string;
  description?: string;
  /** 类型徽章文案（skill/agent/command/hook/mcp 及中文：技能/代理/命令/钩子/扩展/提示词/主题） */
  typeBadge?: string;
  enabled?: boolean;
  toggleable?: boolean;
}

const props = withDefaults(defineProps<Props>(), {
  description: '',
  typeBadge: '',
  enabled: false,
  toggleable: false,
});

const emit = defineEmits<{
  toggle: [value: boolean];
}>();

/** 徽章文案 → ui/Badge type-badge 色相（未知文案退 unknown 灰） */
const BADGE_COLORS: Record<string, string> = {
  skill: 'skill',
  agent: 'agent',
  command: 'command',
  hook: 'hook',
  mcp: 'mcp',
  技能: 'skill',
  代理: 'agent',
  命令: 'command',
  钩子: 'hook',
  扩展: 'capability',
  提示词: 'capability',
  主题: 'capability',
};

const badgeColor = computed(() => BADGE_COLORS[props.typeBadge] ?? 'unknown');

// Switch 的 update:modelValue 处理器：模板绑定与 expose 共用同一 emit 路径
function toggleWith(value: boolean) {
  emit('toggle', value);
}

defineExpose({ toggleWith });
</script>

<style scoped>
.sub-item-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 7px 10px;
  border-radius: var(--radius-md);
  min-height: 0;
}

.sub-item-row:hover {
  background: var(--control);
}

.sir-main {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: baseline;
  gap: 8px;
}

.sir-name {
  font-size: 13px;
  color: var(--label);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.sir-desc {
  font-size: 12px;
  color: var(--tertiary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  min-width: 0;
}
</style>
