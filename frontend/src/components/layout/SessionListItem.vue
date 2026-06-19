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
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { tagColor as getTagColor, appTypeLabel as getAppTypeLabel } from '../../utils/format'

interface Props {
  session: {
    id: string
    appType: string
    model: string
    status: string
    workDir?: string
  }
  active?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  active: false,
})

const emit = defineEmits(['click'])

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
</style>
