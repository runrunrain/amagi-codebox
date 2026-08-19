<script setup lang="ts">
import { onMounted, watch } from 'vue'
import { usePlatformCapabilities } from './composables/usePlatformCapabilities'
import { useSkinStore } from './stores/skin'
import { currentSkinImage, requestBake, cancelBake } from './utils/skinBake'
import AppShell from './components/layout/AppShell.vue'
import Toast from './components/common/Toast.vue'
import StartupWarningBanner from './components/remote/StartupWarningBanner.vue'
import UpdateReminder from './components/common/UpdateReminder.vue'

const { ensure } = usePlatformCapabilities()
const skinStore = useSkinStore()

// 皮肤视觉层（v1.3.38 性能修复）：模糊与调光经 skinBake 预烘焙进位图后
// 一次性注入 --skin-image——运行期零 filter、零全窗逐帧混合，修复 GPU
// 不可用 WebView 上终端打字/操作巨大延迟。opacity=内容面板不透明度，
// textBoost=前景文字加深，与 dim 解耦；烘焙中/不可用时回落原图直显。
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

    // 预烘焙：烘焙产物优先；烘焙中/blur=0/不支持时直接原图。
    // blurred=true 表示已烘进位图，skin.css 输出 filter:none 与透明 dim 层。
    const baked = currentSkinImage({ url: skin.url, blur, dim })
    root.style.setProperty('--skin-image', `url("${baked.url}")`)
    root.style.setProperty('--skin-dim', String(dim))
    root.style.setProperty('--skin-blur', `${blur}px`)
    root.style.setProperty('--skin-panel-alpha', String(opacity))
    if (baked.blurred) {
      root.dataset.skinBaked = '1' // 烘焙态：skin.css 零 filter + 透明蒙版
    } else {
      delete root.dataset.skinBaked
      // blur=0 快路径：回落态下也零 filter（免 blur(0px) 渲染面）。
      if (blur === 0) root.dataset.skinBlurZero = '1'
      else delete root.dataset.skinBlurZero
    }
    if (textBoost > 0) {
      root.style.setProperty('--skin-text-boost', String(textBoost))
    } else {
      root.style.removeProperty('--skin-text-boost')
    }
    root.dataset.skin = 'on'
    // 防抖重烘焙；完成后回调重同步（原子换图）。
    requestBake({ url: skin.url, blur, dim }, syncSkinDom)
  } else {
    cancelBake()
    root.style.removeProperty('--skin-image')
    root.style.removeProperty('--skin-dim')
    root.style.removeProperty('--skin-blur')
    root.style.removeProperty('--skin-panel-alpha')
    root.style.removeProperty('--skin-text-boost')
    delete root.dataset.skin
    delete root.dataset.skinBaked
    delete root.dataset.skinBlurZero
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
