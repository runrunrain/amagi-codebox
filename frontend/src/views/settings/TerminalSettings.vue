<template>
  <div class="set-card">
    <h2>滚动缓冲</h2>
    <p class="set-sub">终端输出保留的最大行数，影响内存占用（1000 ~ 10,000,000）</p>

    <div class="range-row">
      <input
        type="range"
        min="1000"
        max="10000000"
        step="1000"
        :value="scrollback"
        @input="onRangeInput"
      />
      <span class="range-num">{{ Number(scrollback).toLocaleString() }}</span>
    </div>

    <div class="card-footer">
      <AppButton variant="primary" :disabled="saving" @click="saveTerminal">
        {{ saving ? '保存中...' : '保存终端设置' }}
      </AppButton>
      <span class="footer-hint">重新打开终端后生效</span>
    </div>
  </div>

  <div class="set-card">
    <h2>Claude Code Web 视图</h2>
    <p class="set-sub">
      为内嵌 Claude Code 会话提供网页视图平面；WSL 与外部终端会话不适用；运行中切换不影响已启动会话。
    </p>

    <div class="setting-row">
      <label>启用 Claude Code Web 视图</label>
      <div class="inline-toggle">
        <span class="state-text">{{ claudeWebUILoading ? '读取中…' : (claudeWebUIEnabled ? '已开启' : '已关闭') }}</span>
        <Switch
          :model-value="claudeWebUIEnabled"
          :disabled="claudeWebUILoading || claudeWebUIToggling"
          @update:model-value="onToggleClaudeWebUI"
        />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { getTerminalSettings, setTerminalSettings, getClaudeWebUIEnabled, setClaudeWebUIEnabled } from '../../api/settings'
import { useToast } from '../../composables/useToast'
import AppButton from '../../components/ui/AppButton.vue'
import Switch from '../../components/ui/Switch.vue'

const { showSuccess, showError } = useToast()

const scrollback = ref<number>(100000)
const saving = ref(false)

// Claude Code Web 视图开关：该仓无 settings Pinia store，视图直调 api
//（与 GeneralSettings/AgentCliSettings 同款接线）。
const claudeWebUIEnabled = ref(false)
const claudeWebUILoading = ref(true)
const claudeWebUIToggling = ref(false)

async function loadClaudeWebUI() {
  try {
    claudeWebUIEnabled.value = await getClaudeWebUIEnabled()
  } catch (err: any) {
    console.error('load claude webui settings:', err)
    showError('读取 Claude Code Web 视图设置失败: ' + (err?.message || err))
  } finally {
    claudeWebUILoading.value = false
  }
}

async function onToggleClaudeWebUI(value: boolean) {
  claudeWebUIToggling.value = true
  try {
    await setClaudeWebUIEnabled(value)
    claudeWebUIEnabled.value = value
    showSuccess(value ? 'Claude Code Web 视图已开启（新启动会话生效）' : 'Claude Code Web 视图已关闭')
  } catch (err: any) {
    showError('保存失败: ' + (err?.message || err))
  } finally {
    claudeWebUIToggling.value = false
  }
}

function onRangeInput(e: Event) {
  scrollback.value = Number((e.target as HTMLInputElement).value)
}

async function loadTerminal() {
  try {
    const t = await getTerminalSettings()
    scrollback.value = t.scrollback || 100000
  } catch (err) {
    console.error('load terminal settings:', err)
  }
}

async function saveTerminal() {
  saving.value = true
  try {
    const val = Math.max(1000, Math.min(10000000, scrollback.value || 100000))
    await setTerminalSettings({ scrollback: val } as any)
    scrollback.value = val
    showSuccess('终端设置已保存（重新打开终端后生效）')
  } catch (err: any) {
    showError('保存失败: ' + (err?.message || err))
  } finally {
    saving.value = false
  }
}

onMounted(() => {
  loadTerminal()
  loadClaudeWebUI()
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
  margin-bottom: 18px;
}

.range-row {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 6px 0;
}

.range-row input[type='range'] {
  flex: 1;
  max-width: 420px;
  accent-color: var(--accent);
  cursor: pointer;
}

.range-num {
  font-size: 13px;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  color: var(--label);
  min-width: 110px;
  text-align: right;
}

.card-footer {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: 16px;
}

/* Claude Code Web 视图开关行（同 AgentCliSettings 的 setting-row 接线） */
.setting-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 12px 0;
  border-top: 1px solid var(--separator);
}

.setting-row label {
  font-size: 14px;
  color: var(--secondary);
}

.inline-toggle {
  display: flex;
  align-items: center;
  gap: 12px;
}

.state-text {
  font-size: 12px;
  color: var(--tertiary);
}

.footer-hint {
  font-size: 11px;
  color: var(--tertiary);
}
</style>
