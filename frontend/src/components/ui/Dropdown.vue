<template>
  <div :class="classes" @click="$emit('click', $event)">
    <slot />
    <span v-if="showCaret" class="caret">▾</span>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';

interface Props {
  disabled?: boolean;
  showCaret?: boolean;
}

const props = withDefaults(defineProps<Props>(), {
  disabled: false,
  showCaret: true,
});

defineEmits<{
  click: [event: MouseEvent];
}>();

const classes = computed(() => ({
  dropdown: true,
  disabled: props.disabled,
}));
</script>

<style scoped>
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

.caret {
  color: var(--tertiary);
  font-size: 10px;
}
</style>
