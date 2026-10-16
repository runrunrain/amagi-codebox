<template>
  <div class="wsl-target-placeholder">
    <span class="placeholder-icon">{{ excluded ? '⊗' : '◇' }}</span>
    <div>
      <strong>{{ cliLabel }}：{{ excluded ? 'WSL 目标已排除' : 'WSL 目标状态占位（未开放操作）' }}</strong>
      <p>{{ detail }}</p>
      <p v-if="!excluded" class="placeholder-sub">WSL 内手动安装的插件不受影响。</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { getWSLPluginTargetSupport, type WSLPluginTargetEntry } from '../../api/piPlugin';

const props = defineProps<{ cli: string }>();

const cliLabels: Record<string, string> = {
  claude: 'Claude Code',
  codex: 'Codex',
  opencode: 'OpenCode',
  omp: 'OMP',
};

const cliLabel = computed(() => cliLabels[props.cli] ?? props.cli);

// 支持契约以后端静态面（GetWSLPluginTargetSupport）为准；读取失败时回退到
// 与后端契约同义的本地声明（诚实占位，不放大故障）。
const fallbackEntry = computed<WSLPluginTargetEntry>(() =>
  props.cli === 'omp'
    ? { cli: 'omp', support: 'excluded', detail: 'omp 不纳入 WSL 供给（B7 裁定保持关闭）' }
    : {
        cli: props.cli,
        support: 'status-placeholder',
        detail: '该 CLI 的 WSL 侧原生操作契约未验证，不开放 WSL 目标操作入口',
      },
);

const entry = ref<WSLPluginTargetEntry | null>(null);

onMounted(async () => {
  try {
    const list = await getWSLPluginTargetSupport();
    entry.value = list.find(item => item.cli === props.cli) ?? null;
  } catch {
    entry.value = null;
  }
});

const resolved = computed(() => entry.value ?? fallbackEntry.value);
const excluded = computed(() => resolved.value.support === 'excluded');
const detail = computed(() => resolved.value.detail);
</script>

<style scoped>
.wsl-target-placeholder {
  border: 1px dashed var(--separator);
  border-radius: 12px;
  padding: 12px 16px;
  display: flex;
  align-items: flex-start;
  gap: 10px;
  color: var(--secondary);
}

.placeholder-icon {
  color: var(--tertiary);
}

.wsl-target-placeholder strong {
  font-size: 12px;
  color: var(--secondary);
}

.wsl-target-placeholder p {
  margin: 4px 0 0;
  font-size: 11px;
  color: var(--tertiary);
  line-height: 1.6;
}

.placeholder-sub {
  color: var(--tertiary);
}
</style>
