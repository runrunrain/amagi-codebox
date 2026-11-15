<!-- 契约 §4 #3：行式插件条目（左图标 → 名称+徽章+单行描述 → 行尾开关/自定义尾区）。
     整行可点击；switchVisible 才渲染 Switch（开关诚实原则）；无开关时可用 trailing 插槽放置行尾操作（如安装按钮）。
     可测试性契约：expose toggleWith(v)，内部即 Switch 的 update 处理器。 -->
<template>
  <div
    :class="['plugin-row', { active }]"
    role="button"
    tabindex="0"
    @click="emit('click')"
    @keydown.enter="emit('click')"
  >
    <PluginIcon :name="name" size="sm" />
    <div class="plugin-row-main">
      <div class="plugin-row-head">
        <span class="plugin-row-name">{{ name }}</span>
        <span v-if="badges.length" class="plugin-row-badges">
          <Badge
            v-for="(badge, index) in badges"
            :key="index"
            :type="badge.type"
            :text="badge.text"
            :color="badge.color"
            :variant="badge.variant"
          />
        </span>
      </div>
      <p v-if="description" class="plugin-row-desc">{{ description }}</p>
    </div>
    <div v-if="switchVisible" class="plugin-row-switch" @click.stop>
      <Switch
        :model-value="enabled"
        :disabled="switchDisabled"
        @update:model-value="toggleWith"
      />
    </div>
    <div v-else-if="$slots.trailing" class="plugin-row-switch" @click.stop>
      <slot name="trailing" />
    </div>
  </div>
</template>

<script setup lang="ts">
import PluginIcon from './PluginIcon.vue';
import Badge from '../../ui/Badge.vue';
import Switch from '../../ui/Switch.vue';
import type { PluginBadgeDescriptor } from '../pluginViewTypes';

interface Props {
  name: string;
  description?: string;
  badges?: PluginBadgeDescriptor[];
  enabled?: boolean;
  switchVisible?: boolean;
  switchDisabled?: boolean;
  active?: boolean;
}

withDefaults(defineProps<Props>(), {
  description: '',
  badges: () => [],
  enabled: false,
  switchVisible: false,
  switchDisabled: false,
  active: false,
});

const emit = defineEmits<{
  click: [];
  toggle: [value: boolean];
}>();

// Switch 的 update:modelValue 处理器：模板绑定与 expose 共用同一 emit 路径
function toggleWith(value: boolean) {
  emit('toggle', value);
}

defineExpose({ toggleWith });
</script>

<style scoped>
.plugin-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px 12px;
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: background 0.12s;
}

.plugin-row:hover,
.plugin-row.active {
  background: var(--control);
}

.plugin-row.active {
  box-shadow: inset 0 0 0 1px var(--separator);
}

.plugin-row-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.plugin-row-head {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.plugin-row-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--label);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.plugin-row-badges {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
  overflow: hidden;
  flex-shrink: 1;
}

.plugin-row-desc {
  margin: 0;
  font-size: 12px;
  color: var(--secondary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.plugin-row-switch {
  flex-shrink: 0;
  display: inline-flex;
}
</style>
