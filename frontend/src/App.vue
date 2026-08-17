<script setup lang="ts">
import { onMounted, watch } from 'vue'
import { usePlatformCapabilities } from './composables/usePlatformCapabilities'
import { useSkinStore } from './stores/skin'
import AppShell from './components/layout/AppShell.vue'
import Toast from './components/common/Toast.vue'
import StartupWarningBanner from './components/remote/StartupWarningBanner.vue'
import UpdateReminder from './components/common/UpdateReminder.vue'

const { ensure } = usePlatformCapabilities()
const skinStore = useSkinStore()

// 皮肤视觉层：store → <html> 上的 CSS 变量与 data-skin 开关（skin.css 消费）。
// dim 保底 0.35，防止过亮背景吞掉前景可读性。
function syncSkinDom() {
  const root = document.documentElement
  const skin = skinStore.currentSkin
  if (skin) {
    const dim = Math.max(35, Math.min(100, Number(skinStore.settings.dim) || 0)) / 100
    const blur = Math.max(0, Math.min(40, Number(skinStore.settings.blur) || 0))
    root.style.setProperty('--skin-image', `url("${skin.url}")`)
    root.style.setProperty('--skin-dim', String(dim))
    root.style.setProperty('--skin-blur', `${blur}px`)
    root.dataset.skin = 'on'
  } else {
    root.style.removeProperty('--skin-image')
    root.style.removeProperty('--skin-dim')
    root.style.removeProperty('--skin-blur')
    delete root.dataset.skin
  }
}

onMounted(() => {
  ensure()
  skinStore.load()
})

watch(
  () => [skinStore.currentSkin, skinStore.settings.dim, skinStore.settings.blur],
  syncSkinDom,
  { immediate: true },
)
</script>

<template>
  <div v-if="skinStore.active" class="skin-layer" aria-hidden="true"></div>
  <div v-if="skinStore.active" class="skin-dim" aria-hidden="true"></div>
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
