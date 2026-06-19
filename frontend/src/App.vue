<script setup lang="ts">
import { onMounted } from 'vue'
import { usePlatformCapabilities } from './composables/usePlatformCapabilities'
import AppShell from './components/layout/AppShell.vue'
import Toast from './components/common/Toast.vue'

const { ensure } = usePlatformCapabilities()

onMounted(() => {
  ensure()
})
</script>

<template>
  <AppShell>
    <router-view />
  </AppShell>
  <Toast />
</template>

<style>
* {
  margin: 0;
  padding: 0;
  box-sizing: border-box;
}
</style>
