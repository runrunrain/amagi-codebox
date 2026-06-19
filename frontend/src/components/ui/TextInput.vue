<template>
  <div :class="containerClasses">
    <slot name="prefix" />
    <input
      v-model="inputValue"
      :type="type"
      :placeholder="placeholder"
      :disabled="disabled"
      :readonly="readonly"
      class="text-input-field"
      @focus="focused = true"
      @blur="focused = false"
    >
    <slot name="suffix" />
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue';

interface Props {
  modelValue: string;
  type?: 'text' | 'password' | 'email' | 'number';
  placeholder?: string;
  disabled?: boolean;
  readonly?: boolean;
  mono?: boolean;
}

const props = withDefaults(defineProps<Props>(), {
  type: 'text',
  placeholder: '',
  disabled: false,
  readonly: false,
  mono: false,
});

const emit = defineEmits<{
  'update:modelValue': [value: string];
}>();

const focused = ref(false);

const inputValue = computed({
  get: () => props.modelValue,
  set: (value) => emit('update:modelValue', value),
});

const containerClasses = computed(() => ({
  'text-input': true,
  mono: props.mono,
  focused: focused.value,
}));
</script>

<style scoped>
.text-input {
  display: flex;
  align-items: center;
  gap: 8px;
  background: var(--control);
  border-radius: 8px;
  padding: 6px 10px;
  transition: box-shadow 0.12s;
}

.text-input.focused {
  box-shadow: 0 0 0 2px rgba(0, 122, 255, 0.2);
}

.text-input-field {
  flex: 1;
  border: none;
  background: transparent;
  font-size: 14px;
  color: var(--label);
  font-family: inherit;
  min-width: 0;
  outline: none;
}

.text-input-field::placeholder {
  color: var(--tertiary);
}

.text-input.field:disabled {
  opacity: 0.5;
}

.text-input.mono .text-input-field {
  font-family: var(--mono);
}
</style>
