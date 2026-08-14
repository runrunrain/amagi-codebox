<script setup lang="ts">
/**
 * CliLauncher — PG-02 CLI 启动器（M2-B）
 * ---------------------------------------------------------------------------
 * 契约（Task Contract M2-B / design §5.2 index 4 / §5.4）：
 *   · 五类 frozen CLI 卡片（claudecode/opencode/codex/pi/omp），每类独立图标+文字；
 *   · available=false → 禁用并说明原因（宿主未就绪，不伪装可点）；
 *   · 提交中（launching）禁用防连点；
 *   · 启动失败分类由页面级面板呈现（AC-25），本组件只负责触发。
 * ---------------------------------------------------------------------------
 */
import { computed, reactive, ref } from 'vue';
import { CLI_METAS, type CliMeta } from '../../stores/lobby';
import type { CLIAvailability, CLIType, CreateSessionRequest, LaunchSettings } from '../../lib/contract';

const props = defineProps<{
  availability: CLIAvailability[];
  /** 当前正在启动的 CLI（防连点）；null = 空闲。 */
  launching: CLIType | null;
  settings?: LaunchSettings;
}>();

const emit = defineEmits<{
  launch: [request: CreateSessionRequest];
}>();

const selectedCLI = ref<CLIType | null>(null);
const draft = reactive({ workdir: '', providerRef: '', presetRef: '', modelRef: '', shellRef: '', useHeadroom: false });

const cliSettings = computed(() => props.settings?.clis.find((item) => item.cliType === selectedCLI.value));

function chooseCLI(cliType: CLIType): void {
  selectedCLI.value = cliType;
  const defaults = props.settings?.clis.find((item) => item.cliType === cliType)?.defaults;
  draft.workdir = props.settings?.workdirs[0]?.path ?? '';
  draft.providerRef = defaults?.providerRef ?? '';
  draft.presetRef = defaults?.presetRef ?? '';
  draft.modelRef = defaults?.presetRef ? '' : (defaults?.modelRef ?? '');
  draft.shellRef = defaults?.shellRef ?? '';
  draft.useHeadroom = defaults?.useHeadroom ?? false;
}

function applyPreset(): void {
  const selected = cliSettings.value?.presets.find((item) => item.ref === draft.presetRef);
  // A preset key is authoritative. Do not accidentally keep the model from a
  // previously selected/default recipe; the host resolves the preset model and
  // its parameters from the stable key.
  draft.modelRef = '';
  if (selected?.providerRef) draft.providerRef = selected.providerRef;
}

function applyProvider(): void {
  draft.modelRef = '';
}

function submit(): void {
  if (selectedCLI.value === null) return;
  const request: CreateSessionRequest = { cliType: selectedCLI.value };
  if (draft.workdir.trim()) request.workdir = draft.workdir.trim();
  if (draft.providerRef) request.providerRef = draft.providerRef;
  if (draft.presetRef) request.presetRef = draft.presetRef;
  if (draft.modelRef.trim()) request.modelRef = draft.modelRef.trim();
  if (draft.shellRef) request.shellRef = draft.shellRef;
  if (selectedCLI.value === 'claudecode') {
    request.useHeadroom = draft.useHeadroom;
  }
  emit('launch', request);
}

function isAvailable(cliType: CLIType): boolean {
  return props.availability.some((a) => a.cliType === cliType && a.available);
}

/** 每类 CLI 的独立图标（图形通道；不依赖颜色区分）。 */
const CLI_ICONS: Record<CLIType, string> = {
  // Claude Code：星号笔触
  claudecode: 'M12 3v18M5.6 7.5l12.8 9M18.4 7.5l-12.8 9',
  // OpenCode：尖括号
  opencode: 'M8 6l-6 6 6 6M16 6l6 6-6 6',
  // Codex：方块内光标
  codex: 'M4 4h16v16H4zM9 9h6M9 13h4',
  // Pi：希腊字母 π 笔触
  pi: 'M7 8h10M9 8c0 4-1 7-2 8M15 8c0 4 .5 6.5 2 8M5 8c0-2 1-3 3-3h8c2 0 3 1 3 3',
  // Oh My Pi：带直径线的圆环（O 风格，与 pi 的 π 笔触区分）
  omp: 'M12 8a4 4 0 1 0 0 8 4 4 0 1 0 0-8zM8.5 12h7',
};

function cliIcon(meta: CliMeta): string {
  return CLI_ICONS[meta.cliType];
}
</script>

<template>
  <section class="cli-launcher" aria-label="启动新会话">
    <h2 class="launcher-title">启动新会话</h2>
    <div class="launcher-grid">
      <button
        v-for="meta in CLI_METAS"
        :key="meta.cliType"
        type="button"
        class="cli-card"
        :class="{ 'cli-card--disabled': !isAvailable(meta.cliType) }"
        :disabled="!isAvailable(meta.cliType) || launching !== null"
        :aria-disabled="!isAvailable(meta.cliType)"
        @click="chooseCLI(meta.cliType)"
      >
        <span class="cli-icon" aria-hidden="true">
          <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <path :d="cliIcon(meta)" />
          </svg>
        </span>
        <span class="cli-label">{{ meta.label }}</span>
        <span v-if="launching === meta.cliType" class="cli-state">启动中…</span>
        <span v-else-if="!isAvailable(meta.cliType)" class="cli-state cli-state--unavailable">
          宿主不可用：未安装或未配置
        </span>
        <span v-else class="cli-state">点击启动</span>
      </button>
    </div>
    <form v-if="selectedCLI" class="launch-settings" @submit.prevent="submit">
      <div class="settings-heading">
        <strong>{{ CLI_METAS.find((item) => item.cliType === selectedCLI)?.label }} 会话设置</strong>
        <button type="button" class="settings-close" aria-label="关闭会话设置" @click="selectedCLI = null">×</button>
      </div>
      <label>
        工作目录
        <input v-model="draft.workdir" list="remote-workdirs" placeholder="留空使用宿主默认目录" />
        <datalist id="remote-workdirs">
          <option v-for="item in settings?.workdirs ?? []" :key="item.path" :value="item.path">{{ item.label }}</option>
        </datalist>
      </label>
      <label v-if="(cliSettings?.providers.length ?? 0) > 0">
        服务提供商
        <select v-model="draft.providerRef" @change="applyProvider">
          <option value="">使用宿主默认</option>
          <option v-for="item in cliSettings?.providers ?? []" :key="item.ref" :value="item.ref">{{ item.label }}</option>
        </select>
      </label>
      <label v-if="(cliSettings?.presets.length ?? 0) > 0">
        终端预设
        <select v-model="draft.presetRef" @change="applyPreset">
          <option value="">使用宿主默认</option>
          <option v-for="item in cliSettings?.presets ?? []" :key="item.ref" :value="item.ref">{{ item.label }}</option>
        </select>
      </label>
      <label>
        模型（可选覆盖）
        <input v-model="draft.modelRef" placeholder="通常留空，由终端预设决定" />
      </label>
      <label>
        Shell
        <select v-model="draft.shellRef">
          <option value="">系统默认</option>
          <option v-for="item in settings?.shells ?? []" :key="item.path" :value="item.path">{{ item.label }}</option>
        </select>
      </label>
      <div v-if="selectedCLI === 'claudecode'" class="settings-checks">
        <label><input v-model="draft.useHeadroom" type="checkbox" /> 使用 Headroom</label>
      </div>
      <button class="launch-submit" type="submit" :disabled="launching !== null">
        {{ launching === selectedCLI ? '启动中…' : '启动会话' }}
      </button>
    </form>
  </section>
</template>

<style scoped>
.cli-launcher {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.launcher-title {
  margin: 0;
  font-size: 15px;
  font-weight: 700;
  color: var(--VT-text);
}

.launcher-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 10px;
}

.cli-card {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 6px;
  min-height: 44px;
  padding: 12px 14px;
  border: 1px solid var(--VT-border);
  border-radius: 10px;
  background: var(--VT-surface);
  color: var(--VT-text);
  cursor: pointer;
  text-align: left;
}

.cli-card:active:not(:disabled) {
  background: var(--VT-surface-raised);
}

.cli-card:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
}

.cli-icon {
  color: var(--VT-accent-strong);
}

.cli-label {
  font-size: 14px;
  font-weight: 700;
}

.cli-state {
  font-size: 12px;
  color: var(--VT-text-secondary);
  line-height: 1.4;
}

.cli-state--unavailable {
  color: var(--VT-text-disabled);
}

.cli-card--disabled {
  cursor: not-allowed;
}

.cli-card--disabled .cli-icon {
  color: var(--VT-text-disabled);
}

.cli-card--disabled .cli-label {
  color: var(--VT-text-disabled);
}

.launch-settings {
  display: grid;
  gap: 12px;
  padding: 14px;
  border: 1px solid var(--VT-border-strong);
  border-radius: 10px;
  background: var(--VT-surface);
}

.settings-heading { display: flex; align-items: center; justify-content: space-between; }
.settings-close { min-width: 36px; min-height: 36px; border: 0; background: transparent; color: var(--VT-text-secondary); font-size: 24px; }
.launch-settings label { display: grid; gap: 6px; color: var(--VT-text-secondary); font-size: 12px; }
.launch-settings input:not([type='checkbox']), .launch-settings select { min-height: 44px; padding: 9px 10px; border: 1px solid var(--VT-border); border-radius: 8px; background: var(--VT-canvas); color: var(--VT-text); font: inherit; }
.settings-checks { display: flex; flex-wrap: wrap; gap: 16px; }
.settings-checks label { display: flex; align-items: center; gap: 8px; }
.launch-submit { min-height: 44px; border: 0; border-radius: 8px; background: var(--VT-accent-strong); color: white; font-weight: 700; }
.launch-settings input:focus-visible, .launch-settings select:focus-visible, .launch-submit:focus-visible, .settings-close:focus-visible { outline: 2px solid var(--VT-accent); outline-offset: 2px; }

@media (hover: hover) {
  .cli-card:hover:not(:disabled) {
    background: var(--VT-surface-raised);
    border-color: var(--VT-border-strong);
  }
}

@media (min-width: 480px) {
  .launcher-grid {
    grid-template-columns: repeat(4, 1fr);
  }
}

@media (prefers-reduced-motion: reduce) {
  .cli-card {
    transition: none;
  }
}
</style>
