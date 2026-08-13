<template>
  <Transition name="update-reminder">
    <section
      v-if="visible && updateInfo"
      class="update-reminder"
      role="alert"
      aria-live="polite"
    >
      <div class="update-icon" aria-hidden="true">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M12 3v12" />
          <path d="m7 10 5 5 5-5" />
          <path d="M5 21h14" />
        </svg>
      </div>
      <div class="update-copy">
        <strong>发现新版本 v{{ updateInfo.latestVersion }}</strong>
        <span>当前版本 v{{ updateInfo.currentVersion }}</span>
      </div>
      <AppButton variant="primary" size="small" @click="openUpdateSettings">
        查看更新
      </AppButton>
      <button class="dismiss" type="button" aria-label="关闭本次更新提醒" @click="visible = false">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
          <path d="m6 6 12 12" />
          <path d="m18 6-12 12" />
        </svg>
      </button>
    </section>
  </Transition>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { checkForUpdate } from '../../api/updater'
import { useUIStore } from '../../stores/ui'
import AppButton from '../ui/AppButton.vue'
import { updater } from '../../../wailsjs/go/models'

type UpdateInfo = updater.UpdateInfo

const uiStore = useUIStore()
const visible = ref(false)
const updateInfo = ref<UpdateInfo | null>(null)

onMounted(async () => {
  try {
    const info = await checkForUpdate()
    if (info?.hasUpdate) {
      updateInfo.value = info
      visible.value = true
    }
  } catch (error) {
    // 启动检查不打扰用户；手动检查仍会在设置页展示完整错误。
    console.warn('[UpdateReminder] Background update check failed:', error)
  }
})

function openUpdateSettings() {
  uiStore.setActiveSettingKey('update')
  uiStore.enterSettingsMode()
  visible.value = false
}
</script>

<style scoped>
.update-reminder {
  position: fixed;
  top: 18px;
  right: 18px;
  z-index: 9998;
  display: flex;
  align-items: center;
  gap: 12px;
  width: min(430px, calc(100vw - 36px));
  padding: 13px 12px 13px 14px;
  border: 1px solid color-mix(in srgb, var(--accent) 35%, var(--separator));
  border-radius: 13px;
  background: color-mix(in srgb, var(--card) 96%, transparent);
  box-shadow: var(--shadow);
  backdrop-filter: blur(16px);
}

.update-icon {
  display: grid;
  place-items: center;
  width: 34px;
  height: 34px;
  flex: 0 0 auto;
  border-radius: 9px;
  color: var(--accent);
  background: color-mix(in srgb, var(--accent) 12%, transparent);
}

.update-icon svg,
.dismiss svg {
  width: 18px;
  height: 18px;
}

.update-copy {
  display: flex;
  flex: 1;
  min-width: 0;
  flex-direction: column;
  gap: 2px;
}

.update-copy strong {
  overflow: hidden;
  color: var(--label);
  font-size: 13px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.update-copy span {
  color: var(--secondary);
  font-size: 11px;
}

.dismiss {
  display: grid;
  place-items: center;
  width: 28px;
  height: 28px;
  flex: 0 0 auto;
  padding: 0;
  border: 0;
  border-radius: 7px;
  color: var(--secondary);
  background: transparent;
  cursor: pointer;
}

.dismiss:hover {
  color: var(--label);
  background: color-mix(in srgb, var(--accent) 8%, transparent);
}

.update-reminder-enter-active,
.update-reminder-leave-active {
  transition: opacity 180ms ease, transform 180ms ease;
}

.update-reminder-enter-from,
.update-reminder-leave-to {
  opacity: 0;
  transform: translateY(-8px);
}
</style>
