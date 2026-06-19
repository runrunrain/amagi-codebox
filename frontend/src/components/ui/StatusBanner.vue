<template>
  <div :class="classes">
    <span class="sb-text">{{ message }}</span>
    <button v-if="actionText" class="sb-btn" @click="$emit('action')">
      {{ actionText }}
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';

interface Props {
  type?: 'warning' | 'error';
  message: string;
  actionText?: string;
}

const props = withDefaults(defineProps<Props>(), {
  type: 'warning',
});

defineEmits<{
  action: [];
}>();

const classes = computed(() => ({
  'status-banner': true,
  [props.type]: true,
}));
</script>

<style scoped>
.status-banner {
  display: flex;
  align-items: center;
  gap: 12px;
  border-radius: 10px;
  padding: 10px 14px;
  font-size: 12px;
}

.status-banner.warning {
  background: rgba(255, 149, 0, 0.08);
  border: 1px solid rgba(255, 149, 0, 0.3);
  color: #9a6200;
}

.status-banner.error {
  background: rgba(255, 59, 48, 0.08);
  border: 1px solid rgba(255, 59, 48, 0.3);
  color: #a02620;
}

.sb-text {
  flex: 1;
  line-height: 1.5;
}

.sb-btn {
  font-size: 11px;
  color: var(--accent);
  background: none;
  border: none;
  cursor: pointer;
  text-decoration: underline;
  font-family: inherit;
  padding: 0;
}
</style>
