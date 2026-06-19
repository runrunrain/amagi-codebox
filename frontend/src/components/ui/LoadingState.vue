<template>
  <div class="loading-state">
    <div class="spinner" />
    <p v-if="message" class="loading-message">{{ message }}</p>
  </div>
</template>

<script setup lang="ts">
interface Props {
  message?: string;
}

withDefaults(defineProps<Props>(), {
  message: '',
});
</script>

<style scoped>
.loading-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 44px 20px;
  gap: 16px;
  color: var(--tertiary);
}

.spinner {
  width: 32px;
  height: 32px;
  border: 2.5px solid var(--separator);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

.loading-message {
  font-size: 13px;
  color: var(--secondary);
  margin: 0;
}
</style>
