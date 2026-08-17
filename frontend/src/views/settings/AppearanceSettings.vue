<template>
  <div class="set-card">
    <div class="card-head">
      <div>
        <h2>皮肤</h2>
        <p class="set-sub">选择本地图片作为应用全局背景，可调调光与模糊；终端区域保持不透明</p>
      </div>
      <AppButton variant="primary" :disabled="importing" @click="onPick">
        {{ importing ? '导入中...' : '选择图片' }}
      </AppButton>
    </div>

    <EmptyState
      v-if="skinStore.loaded && skinStore.skins.length === 0"
      title="还没有皮肤"
      description="点击右上角「选择图片」导入一张本地图片（png / jpeg / webp，≤20MB）"
    />

    <div v-else class="skin-grid">
      <div
        v-for="skin in skinStore.skins"
        :key="skin.id"
        class="skin-item"
        :class="{ current: skinStore.settings.enabled && skinStore.settings.imageId === skin.id }"
        role="button"
        tabindex="0"
        :aria-label="`应用皮肤 ${skin.fileName}`"
        @click="onApply(skin.id)"
        @keydown.enter="onApply(skin.id)"
        @keydown.space.prevent="onApply(skin.id)"
      >
        <img :src="skin.url" :alt="skin.fileName" loading="lazy" />
        <span v-if="skinStore.settings.enabled && skinStore.settings.imageId === skin.id" class="current-badge">使用中</span>
        <button
          class="skin-remove"
          type="button"
          aria-label="删除皮肤"
          title="删除"
          @click.stop="askRemove(skin.id)"
        >
          <svg viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round">
            <line x1="18" y1="6" x2="6" y2="18" /><line x1="6" y1="6" x2="18" y2="18" />
          </svg>
        </button>
      </div>
    </div>

    <div class="sliders" :class="{ disabled: !skinStore.active }">
      <div class="range-row">
        <span class="range-label">调光</span>
        <input
          type="range"
          min="0"
          max="100"
          step="1"
          :value="skinStore.settings.dim"
          :disabled="!skinStore.active"
          @input="onDimInput"
          @change="onDimCommit"
        />
        <span class="range-num">{{ skinStore.settings.dim }}%</span>
      </div>
      <div class="range-row">
        <span class="range-label">模糊</span>
        <input
          type="range"
          min="0"
          max="40"
          step="1"
          :value="skinStore.settings.blur"
          :disabled="!skinStore.active"
          @input="onBlurInput"
          @change="onBlurCommit"
        />
        <span class="range-num">{{ skinStore.settings.blur }}px</span>
      </div>
    </div>

    <div class="card-footer">
      <AppButton variant="ghost" :disabled="!skinStore.settings.enabled || skinStore.saving" @click="onReset">
        恢复默认
      </AppButton>
      <span class="footer-hint">皮肤保存于本地设置，重启后保持</span>
    </div>

    <ConfirmDialog
      v-model:open="confirmOpen"
      title="删除皮肤"
      message="确定删除这张皮肤图片吗？删除后无法恢复。"
      danger
      confirm-text="删除"
      @confirm="doRemove"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useSkinStore } from '../../stores/skin'
import { useToast } from '../../composables/useToast'
import AppButton from '../../components/ui/AppButton.vue'
import EmptyState from '../../components/ui/EmptyState.vue'
import ConfirmDialog from '../../components/ui/ConfirmDialog.vue'

const skinStore = useSkinStore()
const { showSuccess, showError } = useToast()

const importing = ref(false)
const confirmOpen = ref(false)
const pendingRemoveId = ref('')

function errMsg(err: unknown): string {
  return (err as any)?.message || String(err)
}

async function onPick() {
  importing.value = true
  try {
    const skin = await skinStore.importImage()
    if (!skin) return // 用户取消
    await skinStore.apply({ enabled: true, imageId: skin.id })
    showSuccess('皮肤已应用')
  } catch (err) {
    showError('导入失败: ' + errMsg(err))
  } finally {
    importing.value = false
  }
}

async function onApply(id: string) {
  if (skinStore.settings.enabled && skinStore.settings.imageId === id) return
  try {
    await skinStore.apply({ enabled: true, imageId: id })
    showSuccess('皮肤已应用')
  } catch (err) {
    showError('应用失败: ' + errMsg(err))
  }
}

function askRemove(id: string) {
  pendingRemoveId.value = id
  confirmOpen.value = true
}

async function doRemove() {
  const id = pendingRemoveId.value
  confirmOpen.value = false
  try {
    await skinStore.remove(id)
    showSuccess('皮肤已删除')
  } catch (err) {
    // 被应用中的皮肤后端拒绝删除，原样提示
    showError(errMsg(err))
  }
}

function onDimInput(e: Event) {
  skinStore.preview({ dim: Number((e.target as HTMLInputElement).value) })
}

async function onDimCommit(e: Event) {
  try {
    await skinStore.apply({ dim: Number((e.target as HTMLInputElement).value) })
  } catch (err) {
    showError('保存失败: ' + errMsg(err))
  }
}

function onBlurInput(e: Event) {
  skinStore.preview({ blur: Number((e.target as HTMLInputElement).value) })
}

async function onBlurCommit(e: Event) {
  try {
    await skinStore.apply({ blur: Number((e.target as HTMLInputElement).value) })
  } catch (err) {
    showError('保存失败: ' + errMsg(err))
  }
}

async function onReset() {
  try {
    await skinStore.clear()
    showSuccess('已恢复默认外观')
  } catch (err) {
    showError('操作失败: ' + errMsg(err))
  }
}

onMounted(() => {
  if (!skinStore.loaded) skinStore.load()
})
</script>

<style scoped>
.set-card {
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 14px;
  padding: 20px 24px;
  box-shadow: var(--shadow);
}

.card-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 18px;
}

.set-card h2 {
  font-size: 17px;
  font-weight: 600;
  color: var(--label);
  margin-bottom: 4px;
}

.set-sub {
  font-size: 12px;
  color: var(--tertiary);
}

.skin-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
  gap: 12px;
}

.skin-item {
  position: relative;
  aspect-ratio: 16 / 10;
  border-radius: var(--radius-md);
  overflow: hidden;
  cursor: pointer;
  border: 2px solid var(--separator);
  transition: border-color 0.15s ease, transform 0.15s ease;
}

.skin-item:hover {
  transform: translateY(-1px);
  border-color: var(--tertiary);
}

.skin-item:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 2px;
}

.skin-item.current {
  border-color: var(--accent);
}

.skin-item img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}

.current-badge {
  position: absolute;
  left: 8px;
  bottom: 8px;
  font-size: 11px;
  line-height: 1;
  padding: 4px 8px;
  border-radius: 999px;
  color: #fff;
  background: var(--accent);
}

.skin-remove {
  position: absolute;
  top: 6px;
  right: 6px;
  width: 22px;
  height: 22px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: none;
  border-radius: 50%;
  color: #fff;
  background: rgba(0, 0, 0, 0.55);
  cursor: pointer;
  opacity: 0;
  transition: opacity 0.15s ease, background 0.15s ease;
}

.skin-item:hover .skin-remove,
.skin-remove:focus-visible {
  opacity: 1;
}

.skin-remove:hover {
  background: var(--danger);
}

.sliders {
  margin-top: 20px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.sliders.disabled {
  opacity: 0.5;
}

.range-row {
  display: flex;
  align-items: center;
  gap: 14px;
}

.range-label {
  font-size: 13px;
  color: var(--secondary);
  min-width: 32px;
}

.range-row input[type='range'] {
  flex: 1;
  max-width: 420px;
  accent-color: var(--accent);
  cursor: pointer;
}

.range-row input[type='range']:disabled {
  cursor: not-allowed;
}

.range-num {
  font-size: 13px;
  font-family: var(--mono);
  color: var(--label);
  min-width: 48px;
  text-align: right;
}

.card-footer {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: 18px;
}

.footer-hint {
  font-size: 11px;
  color: var(--tertiary);
}
</style>
