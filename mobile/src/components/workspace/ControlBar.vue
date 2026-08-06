<script setup lang="ts">
/**
 * ControlBar — PG-03 控制权身份条（§6 ControlBar 组件契约）
 * ---------------------------------------------------------------------------
 * 身份投影（你/桌面端/其他设备/无人，图标+文字）+ 获取/释放切换；
 * 被收回/变化原因提示（control.state 事件 reason，如实呈现，可关闭）。
 * ---------------------------------------------------------------------------
 */
import { ref } from 'vue';
import type { ControlSnapshot } from '../../lib/contract';

/** E-06 控制提示（design §7 [R2/M-04]）：kind=lost（被收回/被接管）| conflict（acquire 409）。 */
export interface ControlNoticeView {
  kind: 'lost' | 'conflict';
  /** lost：变迁后的权威控制态；conflict：当前权威控制态（未被 409 改变）。 */
  controlState: 'you' | 'desktop' | 'other' | 'none';
  deviceName: string | null;
  text: string;
}

const props = defineProps<{
  control: ControlSnapshot;
  notice: ControlNoticeView | null;
  busy: boolean;
}>();

const emit = defineEmits<{
  acquire: [];
  release: [];
  'dismiss-notice': [];
}>();

const submitting = ref(false);

async function run(action: 'acquire' | 'release'): Promise<void> {
  if (submitting.value || props.busy) return;
  submitting.value = true;
  try {
    if (action === 'acquire') emit('acquire');
    else emit('release');
  } finally {
    // 实际请求由 store 执行；此处只防连点一拍。
    setTimeout(() => (submitting.value = false), 400);
  }
}
</script>

<template>
  <div class="control-bar" aria-label="控制权状态">
    <div class="control-row">
      <span class="control-identity" :class="`control-identity--${control.state}`">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M18 11V6a2 2 0 0 0-4 0v5M14 10V4a2 2 0 0 0-4 0v6M10 10.5V6a2 2 0 0 0-4 0v8M18 8a2 2 0 1 1 4 0v6a8 8 0 0 1-8 8h-2c-2.8 0-4.5-.86-5.99-2.34l-3.6-3.6a2 2 0 0 1 2.83-2.82L7 15" />
        </svg>
        <template v-if="control.state === 'you'">你正在控制</template>
        <template v-else-if="control.state === 'desktop'">桌面端控制中</template>
        <template v-else-if="control.state === 'other'">由 {{ control.deviceName }} 控制</template>
        <template v-else>无人控制</template>
      </span>
      <button
        v-if="control.state === 'none'"
        type="button"
        class="control-action"
        :disabled="submitting || busy"
        @click="run('acquire')"
      >
        获取控制权
      </button>
      <button
        v-else-if="control.state === 'you'"
        type="button"
        class="control-action control-action--release"
        :disabled="submitting || busy"
        @click="run('release')"
      >
        释放控制权
      </button>
      <span v-else class="control-observer-note">观察中</span>
    </div>
    <p
      v-if="notice"
      class="control-notice"
      :class="`control-notice--${notice.kind}`"
      role="status"
      data-testid="control-notice"
      data-e="e06"
      :data-kind="notice.kind"
      :data-control-state="notice.controlState"
    >
      {{ notice.text }}
      <button type="button" class="control-notice-dismiss" aria-label="关闭提示" @click="emit('dismiss-notice')">×</button>
    </p>
  </div>
</template>

<style scoped>
.control-bar {
  padding: 6px 12px;
  background: var(--VT-surface);
  border-top: 1px solid var(--VT-border);
}

/* M4-A safe-area + 横屏紧凑：横屏（刘海在左右）不贴边；矮视口压缩纵向节奏。 */
@media (orientation: landscape) {
  .control-bar {
    padding-left: calc(12px + env(safe-area-inset-left, 0px));
    padding-right: calc(12px + env(safe-area-inset-right, 0px));
  }
}

@media (orientation: landscape) and (max-height: 500px) {
  .control-bar {
    padding-top: 4px;
    padding-bottom: 4px;
  }
}

.control-row {
  display: flex;
  align-items: center;
  gap: 10px;
}

.control-identity {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  flex: 1;
  min-width: 0;
  font-size: 13px;
  font-weight: 600;
  color: var(--VT-text);
}

.control-identity--you svg {
  color: var(--VT-control);
}

.control-identity--desktop svg,
.control-identity--other svg {
  color: var(--VT-warning);
}

.control-identity--none svg {
  color: var(--VT-text-secondary);
}

.control-action {
  flex-shrink: 0;
  min-height: 44px;
  min-width: 44px;
  padding: 0 14px;
  border: none;
  border-radius: 8px;
  background: var(--VT-control);
  color: var(--VT-canvas);
  font-size: 13px;
  font-weight: 700;
  cursor: pointer;
}

.control-action--release {
  background: transparent;
  border: 1px solid var(--VT-border-strong);
  color: var(--VT-text);
}

.control-action:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.control-action:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
}

.control-observer-note {
  flex-shrink: 0;
  font-size: 12px;
  color: var(--VT-text-secondary);
}

.control-notice {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 6px 0 0;
  padding: 6px 10px;
  background: var(--VT-surface-raised);
  border-left: 4px solid var(--VT-warning);
  border-radius: 8px;
  font-size: 12px;
  color: var(--VT-text);
  line-height: 1.5;
}

.control-notice--conflict {
  border-left-color: var(--VT-danger);
}

.control-notice-dismiss {
  margin-left: auto;
  min-width: 44px;
  min-height: 44px;
  border: none;
  background: transparent;
  color: var(--VT-text-secondary);
  font-size: 16px;
  cursor: pointer;
}

.control-notice-dismiss:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
  border-radius: 8px;
}
</style>
