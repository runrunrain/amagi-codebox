<template>
  <div class="masked-value">
    <span :class="valueClasses">{{ displayValue }}</span>
    <button
      v-if="toggleable"
      class="toggle-btn"
      @click="visible = !visible"
    >
      {{ visible ? '隐藏' : '显示' }}
    </button>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue';

interface Props {
  value: string;
  toggleable?: boolean;
  mono?: boolean;
}

const props = withDefaults(defineProps<Props>(), {
  toggleable: true,
  mono: true,
});

const visible = ref(false);

const displayValue = computed(() => {
  if (!props.value) return '';
  if (visible.value) return props.value;
  if (props.value.length <= 8) return '•'.repeat(props.value.length);
  return props.value.slice(0, 8) + '•'.repeat(Math.min(props.value.length - 8, 12));
});

const valueClasses = computed(() => ({
  'value': true,
  'mono': props.mono,
}));
</script>

<style scoped>
.masked-value {
  display: flex;
  align-items: center;
  gap: 8px;
}

.value {
  font-family: var(--mono);
  font-size: 13px;
  color: var(--secondary);
  letter-spacing: 0.5px;
}

.toggle-btn {
  background: none;
  border: none;
  cursor: pointer;
  font-size: 12px;
  color: var(--accent);
  padding: 4px 10px;
  font-family: inherit;
}

.toggle-btn:hover {
  text-decoration: underline;
}
</style>
