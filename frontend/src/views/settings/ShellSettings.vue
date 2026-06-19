<template>
  <div class="set-card">
    <h2>自定义 Shell</h2>
    <p class="set-sub">添加额外的终端 Shell 可执行文件</p>

    <div class="setting-list">
      <div class="setting-row">
        <label>名称</label>
        <input
          class="text-input"
          v-model="newShellLabel"
          placeholder="如 Git Bash"
        />
      </div>

      <div class="setting-row">
        <label>Shell 路径</label>
        <div class="input-group">
          <input
            class="text-input flex-input"
            v-model="newShellPath"
            placeholder="/usr/local/bin/bash"
          />
          <AppButton variant="ghost" size="small" @click="browse">浏览</AppButton>
          <AppButton variant="primary" size="small" @click="addShell">添加</AppButton>
        </div>
      </div>
    </div>
  </div>

  <div class="set-card">
    <h2>已保存的 Shell</h2>
    <div class="setting-list">
      <div v-if="shellPaths.length === 0" class="empty-row">尚未添加自定义 Shell</div>
      <div v-for="entry in shellPaths" :key="entry.path" class="setting-row">
        <label>{{ entry.label || basename(entry.path) }}</label>
        <div class="row-value">
          <span class="mono">{{ entry.path }}</span>
          <AppButton variant="ghost" size="small" @click="removeShell(entry.path)">删除</AppButton>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { getShellPaths, addShellPath, removeShellPath } from '../../api/settings'
import { BrowseDirectory } from '../../../wailsjs/go/main/App'
import { settings } from '../../../wailsjs/go/models'
import { useToast } from '../../composables/useToast'
import AppButton from '../../components/ui/AppButton.vue'

type ShellEntry = settings.ShellEntry

const { showSuccess, showError } = useToast()

const shellPaths = ref<ShellEntry[]>([])
const newShellLabel = ref('')
const newShellPath = ref('')

function basename(p: string): string {
  const parts = p.replace(/\\/g, '/').split('/')
  return parts[parts.length - 1] || p
}

async function loadShellPaths() {
  try {
    shellPaths.value = await getShellPaths()
  } catch (err) {
    console.error('load shell paths:', err)
  }
}

async function browse() {
  try {
    const dir = await BrowseDirectory()
    if (dir) newShellPath.value = dir
  } catch (err) {
    console.error('browse dir:', err)
  }
}

async function addShell() {
  if (!newShellPath.value) {
    showError('请填写 Shell 路径')
    return
  }
  try {
    await addShellPath({
      path: newShellPath.value,
      label: newShellLabel.value || basename(newShellPath.value),
    } as any)
    await loadShellPaths()
    newShellLabel.value = ''
    newShellPath.value = ''
    showSuccess('Shell 路径已添加')
  } catch (err: any) {
    const msg = err?.toString?.() || String(err)
    if (msg.includes('already exists')) {
      showError('该路径已存在')
    } else {
      showError('添加失败: ' + msg)
    }
  }
}

async function removeShell(path: string) {
  try {
    await removeShellPath(path)
    await loadShellPaths()
    showSuccess('已删除')
  } catch (err: any) {
    showError('删除失败: ' + (err?.message || err))
  }
}

onMounted(() => {
  loadShellPaths()
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

.set-card h2 {
  font-size: 17px;
  font-weight: 600;
  color: var(--label);
  margin-bottom: 4px;
}

.set-sub {
  font-size: 12px;
  color: var(--tertiary);
  margin-bottom: 14px;
}

.setting-list {
  display: flex;
  flex-direction: column;
}

.setting-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 12px 0;
  border-top: 1px solid var(--separator);
}

.setting-row:first-child {
  border-top: none;
}

.setting-row label {
  font-size: 14px;
  color: var(--secondary);
  flex-shrink: 0;
}

.empty-row {
  padding: 12px 0;
  font-size: 13px;
  color: var(--tertiary);
}

.text-input {
  min-width: 200px;
  padding: 7px 12px;
  font-size: 13px;
  font-family: inherit;
  color: var(--label);
  background: var(--control);
  border: 1px solid var(--separator);
  border-radius: 8px;
}

.text-input:focus {
  outline: none;
  border-color: var(--accent);
}

.flex-input {
  flex: 1;
  min-width: 220px;
}

.input-group {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1;
  justify-content: flex-end;
}

.row-value {
  display: flex;
  align-items: center;
  gap: 12px;
}

.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
  color: var(--secondary);
  word-break: break-all;
}
</style>
