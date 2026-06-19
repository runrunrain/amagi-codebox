<template>
  <!--
    真正可用的下拉选择组件（苹果风）。
    两种模式：
      1. options + modelValue（v-model 受控，标准 select 行为）
      2. 无 options（兼容旧调用方：作为带 caret 的视觉容器，slot 内自定义触发器）
  -->
  <div v-if="options.length > 0" class="dropdown-root" :class="{ disabled }" ref="rootEl">
    <button
      type="button"
      class="dropdown-trigger"
      :disabled="disabled"
      @click="open = !open"
    >
      <span :class="['trigger-label', { placeholder: !hasSelection }]">{{ displayLabel }}</span>
      <span class="caret">▾</span>
    </button>
    <transition name="dropdown-fade">
      <ul v-if="open" class="dropdown-menu" role="listbox">
        <li
          v-for="opt in options"
          :key="opt.value"
          :class="['dropdown-item', { selected: opt.value === modelValue }]"
          role="option"
          :aria-selected="opt.value === modelValue"
          @click="choose(opt)"
        >
          <span class="item-label">{{ opt.label }}</span>
          <span v-if="opt.value === modelValue" class="item-check">✓</span>
        </li>
      </ul>
    </transition>
  </div>
  <!-- 兼容旧调用：作为视觉容器，slot 内容为触发器 -->
  <div
    v-else
    :class="{ dropdown: true, disabled }"
    @click="$emit('click', $event)"
  >
    <slot />
    <span v-if="showCaret" class="caret">▾</span>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, watch } from 'vue';

export interface DropdownOption {
  value: string;
  label: string;
  disabled?: boolean;
}

interface Props {
  /** 受控值（v-model）。为空时进入兼容模式 */
  modelValue?: string;
  /** 选项列表；非空时启用标准 select 模式 */
  options?: DropdownOption[];
  placeholder?: string;
  disabled?: boolean;
  /** 兼容旧调用：是否显示 caret */
  showCaret?: boolean;
}

const props = withDefaults(defineProps<Props>(), {
  modelValue: '',
  options: () => [],
  placeholder: '请选择',
  disabled: false,
  showCaret: true,
});

const emit = defineEmits<{
  'update:modelValue': [value: string];
  click: [event: MouseEvent];
}>();

const open = ref(false);
const rootEl = ref<HTMLElement | null>(null);

const hasSelection = computed(
  () => !!props.modelValue && props.options.some((o) => o.value === props.modelValue)
);

const displayLabel = computed(() => {
  const matched = props.options.find((o) => o.value === props.modelValue);
  return matched ? matched.label : props.placeholder;
});

function choose(opt: DropdownOption) {
  if (opt.disabled) return;
  emit('update:modelValue', opt.value);
  open.value = false;
}

function onDocClick(e: MouseEvent) {
  if (!rootEl.value) return;
  if (!rootEl.value.contains(e.target as Node)) {
    open.value = false;
  }
}

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') open.value = false;
}

onMounted(() => {
  document.addEventListener('mousedown', onDocClick);
  document.addEventListener('keydown', onKey);
});
onBeforeUnmount(() => {
  document.removeEventListener('mousedown', onDocClick);
  document.removeEventListener('keydown', onKey);
});

// 切换 disabled 时收起菜单
watch(
  () => props.disabled,
  (val) => {
    if (val) open.value = false;
  }
);
</script>

<style scoped>
/* ---- 标准 select 模式 ---- */
.dropdown-root {
  position: relative;
  display: inline-flex;
  flex-direction: column;
  font-size: 14px;
}

.dropdown-trigger {
  display: inline-flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  background: var(--control);
  border: 1px solid var(--separator);
  border-radius: 8px;
  padding: 7px 11px;
  cursor: pointer;
  color: var(--label);
  font-family: inherit;
  font-size: 13px;
  min-width: 140px;
  transition: background 0.12s, border-color 0.12s;
  text-align: left;
}

.dropdown-trigger:hover:not(:disabled) {
  background: var(--controlHover);
}

.dropdown-trigger:disabled,
.dropdown-root.disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.trigger-label {
  flex: 1;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.trigger-label.placeholder {
  color: var(--tertiary);
}

.caret {
  color: var(--tertiary);
  font-size: 10px;
  flex-shrink: 0;
}

.dropdown-menu {
  position: absolute;
  top: calc(100% + 4px);
  left: 0;
  right: 0;
  z-index: 50;
  list-style: none;
  margin: 0;
  padding: 4px;
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 9px;
  box-shadow: 0 6px 20px rgba(0, 0, 0, 0.14);
  max-height: 240px;
  overflow-y: auto;
}

.dropdown-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 7px 9px;
  border-radius: 6px;
  cursor: pointer;
  font-size: 13px;
  color: var(--label);
  transition: background 0.1s;
}

.dropdown-item:hover {
  background: var(--control);
}

.dropdown-item.selected {
  background: rgba(0, 122, 255, 0.08);
  color: var(--accent);
  font-weight: 500;
}

.item-label {
  flex: 1;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.item-check {
  color: var(--accent);
  font-size: 12px;
}

.dropdown-fade-enter-active,
.dropdown-fade-leave-active {
  transition: opacity 0.12s, transform 0.12s;
}

.dropdown-fade-enter-from,
.dropdown-fade-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}

/* ---- 兼容旧容器模式 ---- */
.dropdown {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  background: var(--control);
  border-radius: 7px;
  padding: 6px 10px;
  cursor: pointer;
  font-size: 14px;
  color: var(--label);
  transition: background 0.12s;
  user-select: none;
}

.dropdown:hover:not(.disabled) {
  background: var(--controlHover);
}

.dropdown.disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
