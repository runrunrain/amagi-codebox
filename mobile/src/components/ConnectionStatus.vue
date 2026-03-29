<script setup lang="ts">
import { useConnection } from '../stores/connection'

const { isConnected } = useConnection()
</script>

<template>
  <div class="connection-status">
    <div class="indicator" :class="{ 'indicator--connected': isConnected }"></div>
  </div>
</template>

<style scoped>
.connection-status {
  display: flex;
  align-items: center;
  padding: 8px;
}

.indicator {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  background: #f85149;
  box-shadow: 0 0 4px rgba(248, 81, 73, 0.5);
}

.indicator--connected {
  background: #3fb950;
  box-shadow: 0 0 4px rgba(63, 185, 80, 0.5);
}
</style>
