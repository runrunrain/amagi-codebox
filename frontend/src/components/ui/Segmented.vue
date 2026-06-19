<template>
  <div :class="containerClasses">
    <button
      v-for="(option, index) in options"
      :key="option.value"
      :class="optionClasses(option.value)"
      :disabled="disabled"
      @click="select(option.value)"
    >
      {{ option.label }}
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';

interface Option {
  value: string;
  label: string;
  disabled?: boolean;
}

interface Props {
  modelValue: string;
  options: Option[];
  variant?: 'pill' | 'underline';
  disabled?: boolean;
}

const props = withDefaults(defineProps<Props>(), {
  variant: 'pill',
  disabled: false,
});

const emit = defineEmits<{
  'update:modelValue': [value: string];
}>();

const containerClasses = computed(() => {
  if (props.variant === 'underline') {
    return 'segmented underline-tabs';
  }
  return 'segmented';
});

function optionClasses(value: string) {
  const isActive = value === props.modelValue;
  if (props.variant === 'underline') {
    return ['seg', 'underline-seg', { active: isActive }];
  }
  return ['seg', { active: isActive }];
}

function select(value: string) {
  emit('update:modelValue', value);
}
</script>

<style scoped>
.segmented {
  display: flex;
  gap: 2px;
  background: var(--control);
  border-radius: 9px;
  padding: 3px;
}

.seg {
  flex: 1;
  padding: 7px 0;
  border: none;
  background: transparent;
  border-radius: 7px;
  cursor: pointer;
  font-size: 13px;
  color: var(--secondary);
  font-family: inherit;
  transition: all 0.12s;
}

.seg:hover:not(:disabled) {
  background: rgba(0, 0, 0, 0.05);
}

.seg.active {
  background: var(--card);
  color: var(--label);
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.08);
}

.seg:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

/* Underline variant */
.underline-tabs {
  background: transparent;
  border-radius: 0;
  padding: 0;
  border-bottom: 1px solid var(--separator);
  gap: 24px;
}

.underline-seg {
  flex: 0 0 auto;
  background: transparent;
  border-radius: 0;
  padding: 9px 2px;
  border-bottom: 2px solid transparent;
  margin-bottom: -1px;
  box-shadow: none;
}

.underline-seg:hover:not(:disabled) {
  background: transparent;
  color: var(--label);
}

.underline-seg.active {
  background: transparent;
  color: var(--label);
  border-bottom-color: var(--accent);
  font-weight: 600;
  box-shadow: none;
}
</style>
