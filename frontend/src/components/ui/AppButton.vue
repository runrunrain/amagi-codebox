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
  variant?: 'primary' | 'ghost' | 'icon' | 'danger' | 'outline';
  size?: 'small' | 'medium' | 'large';
  disabled?: boolean;
}

const props = withDefaults(defineProps<Props>(), {
  variant: 'ghost',
  size: 'medium',
  disabled: false,
});

defineEmits<{
  click: [event: MouseEvent];
}>();

const classes = computed(() => {
  const base = 'btn';
  const variants: Record<string, string> = {
    primary: 'btn-primary',
    ghost: 'btn-ghost',
    icon: 'icon-btn',
    danger: 'btn-ghost danger',
    outline: 'btn-outline',
  };
  const sizes: Record<string, string> = {
    small: 'btn-sm',
    medium: '',
    large: 'btn-lg',
  };
  return [base, variants[props.variant], sizes[props.size]].filter(Boolean).join(' ');
});
</script>

<style scoped>
.btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  border: none;
  border-radius: 10px;
  cursor: pointer;
  font-size: 13px;
  font-weight: 500;
  padding: 9px 16px;
  transition: background 0.15s, box-shadow 0.15s;
  font-family: inherit;
}

.btn-primary {
  background: var(--accent);
  color: #fff;
}

.btn-primary:hover:not(:disabled) {
  background: var(--accentHover);
}

.btn-ghost {
  background: var(--control);
  color: var(--secondary);
}

.btn-ghost:hover:not(:disabled) {
  background: var(--controlHover);
}

.btn-ghost.danger {
  color: #FF3B30;
}

/* 描边按钮（契约 §3.3：透明底 + 1px separator 描边，hover 浮 control 底色） */
.btn-outline {
  background: transparent;
  border: 1px solid var(--separator);
  color: var(--label);
}

.btn-outline:hover:not(:disabled) {
  background: var(--control);
}

.icon-btn {
  width: 26px;
  height: 26px;
  padding: 0;
  background: transparent;
}

.icon-btn:hover:not(:disabled) {
  background: var(--control);
}

/* Icon buttons keep zero padding at every size; size modifiers (.btn-sm/.btn-lg)
   only apply to text buttons. Without this override, .btn-sm's `padding: 6px 12px`
   (declared later, equal specificity) wins and squeezes the 26px border-box
   content area to ~2px, collapsing the slotted SVG icon to an invisible sliver. */
.btn.icon-btn {
  padding: 0;
}

.btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.btn-sm {
  padding: 6px 12px;
  font-size: 12px;
}

.btn-lg {
  padding: 12px 20px;
  font-size: 14px;
}
</style>
