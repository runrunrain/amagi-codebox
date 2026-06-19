<template>
  <button
    :class="classes"
    :disabled="disabled"
    @click="$emit('click', $event)"
  >
    <slot />
  </button>
</template>

<script setup lang="ts">
import { computed } from 'vue';

interface Props {
  active?: boolean;
  disabled?: boolean;
}

const props = withDefaults(defineProps<Props>(), {
  active: false,
  disabled: false,
});

defineEmits<{
  click: [event: MouseEvent];
}>();

const classes = computed(() => ({
  chip: true,
  active: props.active,
}));
</script>

<style scoped>
.chip {
  font-size: 12px;
  font-weight: 500;
  color: var(--secondary);
  background: var(--control);
  border-radius: 999px;
  padding: 5px 12px;
  cursor: pointer;
  transition: background 0.12s, color 0.12s;
  user-select: none;
  border: none;
  font-family: inherit;
}

.chip:hover:not(:disabled) {
  background: var(--controlHover);
}

.chip.active {
  background: var(--label);
  color: #fff;
}

.chip:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
