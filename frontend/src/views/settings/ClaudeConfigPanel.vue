<template>
  <section class="claude-config" :aria-labelledby="headingId">
    <h3 class="config-heading">
      <button
        :id="headingId"
        type="button"
        class="config-toggle"
        :aria-expanded="expanded"
        :aria-controls="contentId"
        @click="expanded = !expanded"
      >
        <span class="config-title">Claude Code 配置检测</span>
        <span class="config-summary">
          <span class="config-meter" aria-hidden="true">
            <span :style="{ width: `${items.length ? (configured / items.length) * 100 : 0}%` }" />
          </span>
          <span><strong>{{ configured }}/{{ items.length }}</strong> 项已配置</span>
        </span>
        <span class="config-chevron" aria-hidden="true" />
      </button>
    </h3>

    <div v-show="expanded" :id="contentId">
      <div ref="filters" class="config-toolbar" role="group" aria-label="筛选配置项">
        <Segmented v-model="filter" :options="filterOptions" />
      </div>

      <p v-if="visibleItems.length === 0" class="config-empty" role="status">所有检测项均已配置</p>
      <ul v-else ref="list" class="config-list">
        <li v-for="item in visibleItems" :key="item.key" class="config-row">
          <div class="config-copy">
            <span class="config-name">{{ displayName(item.key) }}</span>
            <code v-if="displayName(item.key) !== item.key" class="config-key">{{ item.key }}</code>
            <p v-if="item.description" class="config-description">{{ item.description }}</p>
          </div>

          <div class="config-value">
            <span>{{ item.configured ? '当前值' : '建议值' }}</span>
            <code>{{ (item.configured ? item.currentValue : item.defaultValue) || '—' }}</code>
          </div>

          <div class="config-action">
            <span v-if="item.configured" class="config-complete">已配置</span>
            <AppButton
              v-else
              type="button"
              variant="outline"
              size="small"
              :data-config-key="item.key"
              :disabled="disabled && fixingKey !== item.key"
              :aria-disabled="fixingKey === item.key || undefined"
              :aria-label="`${fixingKey === item.key ? '正在应用' : '应用建议值'} · ${displayName(item.key)}`"
              @click="apply(item)"
            >
              {{ fixingKey === item.key ? '应用中…' : '应用' }}
            </AppButton>
          </div>
        </li>
      </ul>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, useId, watch } from 'vue'
import type { envcheck } from '../../../wailsjs/go/models'
import AppButton from '../../components/ui/AppButton.vue'
import Segmented from '../../components/ui/Segmented.vue'

const props = defineProps<{
  items: envcheck.ClaudeConfigItem[]
  disabled: boolean
  fixingKey: string | null
}>()

const emit = defineEmits<{ apply: [item: envcheck.ClaudeConfigItem] }>()

const id = useId()
const headingId = `${id}-heading`
const contentId = `${id}-items`
const expanded = ref(false)
const filter = ref('missing')
const filters = ref<HTMLElement | null>(null)
const list = ref<HTMLUListElement | null>(null)

const missing = computed(() => props.items.filter(item => !item.configured))
const configured = computed(() => props.items.length - missing.value.length)
const visibleItems = computed(() => filter.value === 'missing' ? missing.value : props.items)
const filterOptions = computed(() => [
  { value: 'missing', label: `未配置 ${missing.value.length}` },
  { value: 'all', label: `全部 ${props.items.length}` },
])

const labels = new Map([
  ['hasCompletedOnboarding', '首次引导'],
  ['API_TIMEOUT_MS', 'API 请求超时'],
  ['CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC', '非必要网络流量'],
  ['env.CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS', 'Agent Teams 协作'],
  ['env.CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS', '实验性 Beta 功能'],
  ['autoUpdatesChannel', '自动更新通道'],
  ['effortLevel', '推理深度'],
  ['env.ENABLE_LSP_TOOL', '语言服务器（LSP）'],
  ['permissions.allow', '直接允许的权限'],
  ['permissions.deny', '拒绝的权限'],
  ['permissions.ask', '需要询问的权限'],
  ['permissions.defaultMode', '默认权限模式'],
])

function displayName(key: string): string {
  return labels.get(key) ?? key
}

function apply(item: envcheck.ClaudeConfigItem) {
  if (!props.disabled && !props.fixingKey) emit('apply', item)
}

// After a successful write the focused action can disappear from either filter.
watch(visibleItems, async (items) => {
  const active = document.activeElement
  if (!(active instanceof HTMLElement) || !list.value?.contains(active)) return
  const key = active.dataset.configKey
  if (!key || items.some(item => item.key === key && !item.configured)) return

  await nextTick()
  if (document.activeElement === document.body) {
    filters.value?.querySelector<HTMLButtonElement>('[aria-pressed="true"]')?.focus({ preventScroll: true })
  }
})
</script>

<style scoped>
.claude-config {
  container-type: inline-size;
  margin-top: 8px;
  min-width: 0;
  border: 1px solid var(--separator);
  border-radius: 12px;
  background: var(--card);
}

.config-heading {
  margin: 0;
  font: inherit;
}

.config-toggle {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
  padding: 12px 14px;
  border: 0;
  border-radius: 11px;
  background: color-mix(in srgb, var(--control) 48%, var(--card));
  color: var(--label);
  font: inherit;
  text-align: left;
  cursor: pointer;
}

.config-toggle[aria-expanded='true'] { border-radius: 11px 11px 0 0; }
.config-toggle:hover { background: var(--control); }
.config-toggle:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }
.config-title { flex: 1; min-width: 0; font-size: 13px; font-weight: 650; }

.config-summary {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  color: var(--secondary);
  font-size: 11px;
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}

.config-summary strong { color: var(--label); font-weight: 650; }
.config-meter { width: 52px; height: 4px; border-radius: 4px; overflow: hidden; background: var(--separator); }
.config-meter span { display: block; height: 100%; border-radius: inherit; background: var(--accent); }

.config-chevron {
  width: 7px;
  height: 7px;
  margin: -3px 2px 0 0;
  border-right: 1.5px solid var(--secondary);
  border-bottom: 1.5px solid var(--secondary);
  transform: rotate(45deg);
}

.config-toggle[aria-expanded='true'] .config-chevron { margin-top: 3px; transform: rotate(225deg); }

.config-toolbar { padding: 9px 14px; border-top: 1px solid var(--separator); }
.config-toolbar :deep(.segmented) { display: inline-flex; }
.config-toolbar :deep(.seg) { min-height: 28px; padding: 3px 11px; font-size: 12px; white-space: nowrap; }

.config-list { margin: 0; padding: 0; list-style: none; }

.config-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(90px, 24%) 72px;
  align-items: center;
  gap: 12px;
  padding: 11px 14px;
  border-top: 1px solid var(--separator);
}

.config-copy, .config-value { display: grid; gap: 3px; min-width: 0; overflow-wrap: anywhere; }
.config-name { font-size: 13px; font-weight: 600; color: var(--label); }
.config-key { color: var(--secondary); font: 11px var(--mono); }
.config-description { margin: 0; color: var(--secondary); font-size: 12px; line-height: 1.45; }
.config-value { color: var(--secondary); font-size: 11px; }
.config-value code { color: var(--label); font: 12px var(--mono); }

.config-action { justify-self: end; white-space: nowrap; }
.config-action button { min-width: 56px; border-radius: 7px; }
.config-action button[aria-disabled='true'] { opacity: .65; cursor: wait; }

.config-complete {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  color: var(--success-strong);
  font-size: 11px;
}

.config-complete::before { content: ''; width: 5px; height: 5px; border-radius: 50%; background: currentColor; }
.config-empty { margin: 0; padding: 18px 14px; border-top: 1px solid var(--separator); color: var(--secondary); font-size: 12px; }

@container (max-width: 480px) {
  .config-meter { display: none; }
  .config-row { grid-template-columns: minmax(0, 1fr) auto; gap: 8px 12px; }
  .config-copy { grid-column: 1 / -1; }
  .config-value { display: flex; flex-wrap: wrap; align-items: baseline; gap: 4px 8px; }
}

@container (max-width: 280px) {
  .config-toggle { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 5px 10px; }
  .config-summary { grid-column: 1 / -1; grid-row: 2; }
  .config-chevron { grid-column: 2; grid-row: 1; }
}
</style>
