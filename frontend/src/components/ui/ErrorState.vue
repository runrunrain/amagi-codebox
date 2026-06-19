<template>
  <div class="error-state">
    <div class="error-icon">!</div>
    <h3>{{ title }}</h3>
    <p v-if="message">{{ message }}</p>
    <button v-if="onRetry" class="retry-btn" @click="onRetry">重试</button>
  </div>
</template>

<script setup lang="ts">
interface Props {
  title?: string;
  message?: string;
  onRetry?: () => void;
}

withDefaults(defineProps<Props>(), {
  title: '加载失败',
  message: '',
});
</script>

<style scoped>
.error-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 44px 20px;
  gap: 12px;
  text-align: center;
}

.error-icon {
  width: 40px;
  height: 40px;
  border-radius: 50%;
  background: var(--danger);
  color: #fff;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 20px;
  font-weight: 600;
  margin-bottom: 8px;
}

.error-state h3 {
  font-size: 17px;
  font-weight: 600;
  color: var(--label);
  margin: 0;
}

.error-state p {
  font-size: 13px;
  color: var(--secondary);
  max-width: 400px;
  margin: 0;
}

.retry-btn {
  margin-top: 8px;
  padding: 7px 16px;
  font-size: 13px;
  font-weight: 500;
  font-family: inherit;
  background: var(--accent);
  color: #fff;
  border: none;
  border-radius: 8px;
  cursor: pointer;
  transition: background 0.12s;
}

.retry-btn:hover {
  background: var(--accentHover);
}
</style>
