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
// dim 保底 0.35，防止过亮背景吞掉前景可读性；opacity=内容面板不透明度
// （0..100，与 dim 解耦），注入 --skin-panel-alpha 供 skin.css 面板 token 混合；
// textBoost=前景文字加深（0..100，0=不增强保持现状），注入 --skin-text-boost 供
// skin.css 前景 token 向 black 混合，0 档直接移除变量让 color-mix 声明失效回退原色。
function syncSkinDom() {
  const root = document.documentElement
  const skin = skinStore.currentSkin
  if (skin) {
    const dim = Math.max(35, Math.min(100, Number(skinStore.settings.dim) || 0)) / 100
    const blur = Math.max(0, Math.min(40, Number(skinStore.settings.blur) || 0))
    const rawOpacity = Number(skinStore.settings.opacity)
    const opacity =
      Math.max(0, Math.min(100, Number.isFinite(rawOpacity) ? rawOpacity : 70)) / 100
    const rawBoost = Number(skinStore.settings.textBoost)
    const textBoost =
      Math.max(0, Math.min(100, Number.isFinite(rawBoost) ? rawBoost : 0)) / 100
    root.style.setProperty('--skin-image', `url("${skin.url}")`)
    root.style.setProperty('--skin-dim', String(dim))
    root.style.setProperty('--skin-blur', `${blur}px`)
    root.style.setProperty('--skin-panel-alpha', String(opacity))
    if (textBoost > 0) {
      root.style.setProperty('--skin-text-boost', String(textBoost))
    } else {
      root.style.removeProperty('--skin-text-boost')
    }
    root.dataset.skin = 'on'
  } else {
    root.style.removeProperty('--skin-image')
    root.style.removeProperty('--skin-dim')
    root.style.removeProperty('--skin-blur')
    root.style.removeProperty('--skin-panel-alpha')
    root.style.removeProperty('--skin-text-boost')
    delete root.dataset.skin
  }
}

onMounted(() => {
  ensure()
  skinStore.load()
})

watch(
  () => [
    skinStore.currentSkin,
    skinStore.settings.dim,
    skinStore.settings.blur,
    skinStore.settings.opacity,
    skinStore.settings.textBoost,
  ],
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
