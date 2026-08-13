<script setup lang="ts">
import { onMounted } from 'vue'
import { usePlatformCapabilities } from './composables/usePlatformCapabilities'
import AppShell from './components/layout/AppShell.vue'
import Toast from './components/common/Toast.vue'
import StartupWarningBanner from './components/remote/StartupWarningBanner.vue'
import UpdateReminder from './components/common/UpdateReminder.vue'

const { ensure } = usePlatformCapabilities()

onMounted(() => {
  ensure()
})
</script>

<template>
  <AppShell>
    <router-view v-slot="{ Component, route }">
      <KeepAlive>
        <component
          :is="Component"
          v-if="route.meta.keepAlive"
          :key="String(route.name)"
        />
      </KeepAlive>
      <component
        :is="Component"
        v-if="!route.meta.keepAlive"
        :key="String(route.name)"
      />
    </router-view>
  </AppShell>
  <StartupWarningBanner />
  <UpdateReminder />
  <Toast />
</template>

<style>
* {
  margin: 0;
  padding: 0;
  box-sizing: border-box;
}
</style>
