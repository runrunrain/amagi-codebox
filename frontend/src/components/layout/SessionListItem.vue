<template>
  <div class="sess-item" :class="{ active }" @click="handleClick">
    <!-- Status Dot -->
    <span class="sess-dot" :style="{ background: statusColor }"/>

    <!-- Session Info -->
    <div class="sess-info">
      <div class="sess-title" :title="fullTitle">{{ displayTitle }}</div>
      <div class="sess-meta">
        <!-- Tag Badge -->
        <span class="tag" :style="{ background: tagColorValue }">{{ appTypeLabelValue }}</span>
        <!-- Model + Status Text -->
        <span class="sess-sub">{{ session.model || '—' }} · {{ statusText }}</span>
      </div>
      <div v-if="summaryText" class="sess-summary" :title="summaryText">{{ summaryText }}</div>
    </div>

    <!-- Close Button (hover only) -->
    <button
      class="close-btn"
      @click.stop="handleClose"
      title="关闭会话"
    >
      <svg class="close-icon" viewBox="0 0 24 24" fill="none" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <line x1="18" y1="6" x2="6" y2="18"/>
        <line x1="6" y1="6" x2="18" y2="18"/>
      </svg>
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { tagColor as getTagColor, appTypeLabel as getAppTypeLabel, truncate } from '../../utils/format'

interface Props {
  session: {
    id: string
    appType: string
    model: string
    status: string
    workDir?: string
    title?: string
  }
  active?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  active: false,
})

const emit = defineEmits<{
  click: []
  close: [sessionId: string]
}>()

const statusColor = computed(() => {
  return props.session.status === 'running' ? 'var(--success)' : 'var(--tertiary)'
})

const statusText = computed(() => {
  return props.session.status === 'running' ? '运行中' : '已退出'
})

const tagColorValue = computed(() => {
  return getTagColor(props.session.appType)
})

const appTypeLabelValue = computed(() => {
  return getAppTypeLabel(props.session.appType)
})

// 会话标题（后端 tracker 从 Claude Code jsonl 首条 user message 提取，方案 P 动态跟踪）。
// 仅非空时渲染，保持已退出会话视觉稳定。
const summaryText = computed(() => {
  const raw = (props.session.title || '').trim()
  if (!raw) return ''
  return truncate(raw, 60)
})

const displayTitle = computed(() => {
  const dir = props.session.workDir || ''
  return dir ? dir.split(/[/\\]/).pop() || '新会话' : '新会话'
})

const fullTitle = computed(() => {
  return `#${props.session.id} ${displayTitle.value}`
})

function handleClick() {
  emit('click')
}

function handleClose() {
  emit('close', props.session.id)
}
</script>

<style scoped>
.sess-item {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 8px 9px;
  border-radius: 8px;
  cursor: pointer;
  transition: background .12s;
}

.sess-item:hover {
  background: var(--control);
}

.sess-item.active {
  background: var(--control);
}

.sess-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}

.sess-info {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.sess-title {
  font-size: 13px;
  font-weight: 500;
  color: var(--label);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.sess-item:not(.active) .sess-title {
  color: var(--secondary);
}

.sess-meta {
  display: flex;
  align-items: center;
  gap: 6px;
}

.tag {
  font-size: 10px;
  font-weight: 600;
  color: #fff;
  padding: 1px 5px;
  border-radius: 4px;
  line-height: 1.4;
}

.sess-sub {
  font-size: 11px;
  color: var(--tertiary);
}

.sess-summary {
  font-size: 11px;
  color: var(--tertiary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  opacity: 0.85;
}

.close-btn {
  width: 20px;
  height: 20px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: none;
  background: transparent;
  border-radius: 4px;
  cursor: pointer;
  opacity: 0;
  transition: opacity .15s, background .15s;
  flex-shrink: 0;
  padding: 0;
}

.sess-item:hover .close-btn {
  opacity: 1;
}

.close-btn:hover {
  background: rgba(255, 59, 48, 0.15);
}

.close-btn:hover .close-icon {
  stroke: #FF3B30;
}

.close-icon {
  width: 14px;
  height: 14px;
  stroke: var(--tertiary);
  transition: stroke .15s;
}
</style>
