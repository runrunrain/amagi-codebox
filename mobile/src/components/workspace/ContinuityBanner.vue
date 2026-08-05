<script setup lang="ts">
/**
 * ContinuityBanner — E-07 断线→重连→恢复 显著交互条（design §7 [R2/M-04]）
 * ---------------------------------------------------------------------------
 * 权威依据：M3 连续性设计 §7——E-07=断线→重连→恢复；页面次序固定
 * StatusBar → ControlBar(E-06) → NoticeStack/ContinuityBanner(E-07)
 * → Timeline(E-08 marker) → Composer；ContinuityBanner 在 ControlBar 之后，
 * 不取代 ControlBar。
 *   · reconnecting：warning——保留时间线、禁用 Composer（store canWrite 已保证），
 *     显示尝试序号与下次重试倒计时（退避单次上限 ≤5s）；
 *   · restored：success 至少 3s（store timer 自动消退，可手动关闭）；
 *     同拍有缺口时文案「已恢复，部分历史不可用」（E-07 transport + E-08 history，
 *     不重编号），并提供「跳到缺口」原位导航（GapMarker 始终保留）；
 *   · terminal fatal / P1 lastError 压制由 NoticeStack 优先级（store.primaryNotice）
 *     在页面层裁决——本组件只在被渲染时呈现当前 episode，不自行判断优先级。
 * 无障碍：role=status + aria-live=polite（不抢焦点）；色彩之外有图标+文字双通道；
 * reduced-motion 下无任何动画。
 */
import type { RecoveryEpisode } from '../../stores/workspace';

defineProps<{
  episode: RecoveryEpisode;
}>();

const emit = defineEmits<{
  'jump-gap': [];
  dismiss: [];
}>();
</script>

<template>
  <div
    class="continuity-banner"
    :class="`continuity-banner--${episode.state}`"
    role="status"
    aria-live="polite"
    data-testid="continuity-banner"
    :data-state="episode.state"
    :data-generation="episode.generation"
  >
    <!-- 断线恢复中（退避倒计时） -->
    <template v-if="episode.state === 'reconnecting'">
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M21 12a9 9 0 1 1-3-6.7" /><polyline points="21 3 21 9 15 9" />
      </svg>
      <div class="banner-body">
        <strong>连接中断，正在恢复（第 {{ episode.attempt }} 次尝试）</strong>
        <span class="banner-detail">
          <template v-if="episode.nextDelayMs !== null">
            {{ Math.round(episode.nextDelayMs / 100) / 10 }}s 后重试 · 自动恢复，输出与草稿均保留
          </template>
          <template v-else>正在重新连接…</template>
        </span>
      </div>
    </template>

    <!-- 已恢复（≥3s 自动消退；含缺口时诚实标注并给原位导航） -->
    <template v-else>
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <polyline points="20 6 9 17 4 12" />
      </svg>
      <div class="banner-body">
        <strong>{{ episode.withGap ? '已恢复，部分历史不可用' : '已恢复' }}</strong>
        <span v-if="episode.withGap" class="banner-detail">
          {{ episode.gapFillable ? '缺口处以标记原位呈现，可从标记处尝试补齐' : '不可恢复的缺口以标记原位保留，已从最新继续' }}
        </span>
      </div>
      <button
        v-if="episode.withGap"
        type="button"
        class="banner-action"
        @click="emit('jump-gap')"
      >
        跳到缺口
      </button>
      <button
        type="button"
        class="banner-dismiss"
        aria-label="关闭恢复提示"
        @click="emit('dismiss')"
      >
        ×
      </button>
    </template>
  </div>
</template>

<style scoped>
.continuity-banner {
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 6px 12px;
  padding: 10px 12px;
  border-radius: 10px;
  font-size: 13px;
  line-height: 1.5;
  background: var(--VT-surface);
  color: var(--VT-text);
}

.continuity-banner--reconnecting {
  border: 1px solid var(--VT-warning);
  border-left: 4px solid var(--VT-warning);
}

.continuity-banner--reconnecting > svg {
  color: var(--VT-warning);
  flex-shrink: 0;
}

.continuity-banner--restored {
  border: 1px solid var(--VT-success);
  border-left: 4px solid var(--VT-success);
}

.continuity-banner--restored > svg {
  color: var(--VT-success);
  flex-shrink: 0;
}

.banner-body {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

/* 中文标题窄视口折行：仅在标点/空格处折（keep-all），避免词语拦腰断行。 */
.banner-body strong {
  word-break: keep-all;
}

.banner-detail {
  color: var(--VT-text-secondary);
  font-size: 12px;
}

.banner-action {
  flex-shrink: 0;
  min-height: 44px;
  min-width: 44px;
  padding: 0 12px;
  border: 1px solid var(--VT-border-strong);
  border-radius: 8px;
  background: transparent;
  color: var(--VT-accent-strong);
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
}

.banner-action:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
}

.banner-dismiss {
  flex-shrink: 0;
  min-width: 44px;
  min-height: 44px;
  border: none;
  background: transparent;
  color: var(--VT-text-secondary);
  font-size: 16px;
  cursor: pointer;
}

.banner-dismiss:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
  border-radius: 8px;
}

/* 动效克制（checklist 8）：本组件无任何动画；reduced-motion 无需额外覆写，
   但保留显式守卫以防后续维护引入过渡。 */
@media (prefers-reduced-motion: reduce) {
  .continuity-banner {
    transition: none;
  }
}
</style>
