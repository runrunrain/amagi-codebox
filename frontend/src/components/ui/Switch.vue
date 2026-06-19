<template>
  <button
    :class="classes"
    :disabled="disabled"
    role="switch"
    :aria-checked="modelValue"
    @click="toggle"
  >
    <span class="knob" />
  </button>
</template>

<script setup lang="ts">
import { computed } from 'vue';

interface Props {
  modelValue: boolean;
  disabled?: boolean;
}

const props = withDefaults(defineProps<Props>(), {
  disabled: false,
});

const emit = defineEmits<{
  'update:modelValue': [value: boolean];
}>();

const classes = computed(() => ({
  switch: true,
  off: !props.modelValue,
}));

function toggle() {
  if (!props.disabled) {
    emit('update:modelValue', !props.modelValue);
  }
}
</script>

<style scoped>
.switch {
  width: 44px;
  height: 26px;
  border-radius: 999px;
  background: var(--accent);
  position: relative;
  cursor: pointer;
  transition: background 0.15s;
  border: none;
  padding: 0;
}

.switch.off {
  background: #D2D2D7;
}

.switch:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.knob {
  position: absolute;
  top: 2px;
  left: calc(100% - 24px);
  width: 22px;
  height: 22px;
  border-radius: 50%;
  background: #fff;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.2);
  transition: left 0.15s;
}

.switch.off .knob {
  left: 2px;
}
</style>
