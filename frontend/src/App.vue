<script setup lang="ts">
import { onMounted } from 'vue'
import { usePlatformCapabilities } from './composables/usePlatformCapabilities'
import AppShell from './components/layout/AppShell.vue'
import Toast from './components/common/Toast.vue'
import StartupWarningBanner from './components/remote/StartupWarningBanner.vue'

const { ensure } = usePlatformCapabilities()

onMounted(() => {
  ensure()
})
</script>

<template>
  <AppShell>
    <router-view />
  </AppShell>
  <StartupWarningBanner />
  <Toast />
</template>

<style>
* {
  margin: 0;
  padding: 0;
  box-sizing: border-box;
}
</style>
