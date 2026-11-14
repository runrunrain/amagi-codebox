<!-- 契约 §4 #2：确定性字母 + 色相图标（无图片/SVG 资源，纯文字）。
     算法以同文件导出的纯函数 pluginIconStyle 交付，组件消费同一函数，
     单测直接测纯函数（确定性），不依赖渲染。 -->
<script lang="ts">
/** pluginIconStyle 返回的确定性视觉参数 */
export interface PluginIconStyle {
  letter: string
  color: string
}

/** 固定五色 token 色板（契约 §4 #2 顺序不可变） */
const PALETTE = [
  'var(--accent)',
  'var(--success)',
  'var(--warning)',
  'var(--purple)',
  'var(--danger)',
] as const

/**
 * hash = Σ name.charCodeAt(i) * 31^i (mod 997)。
 * 31 的幂用模乘迭代（pow = pow*31 mod 997）逐位累积，与直接求幂再取模
 * 数学等价，避免长名下 31^i 超出 Number 精度。
 */
function pluginIconHash(name: string): number {
  let hash = 0
  let pow = 1
  for (let i = 0; i < name.length; i += 1) {
    hash = (hash + name.charCodeAt(i) * pow) % 997
    pow = (pow * 31) % 997
  }
  return hash
}

/** 确定性算法：同名恒同 letter/color；空名/空白名兜底「#」 */
export function pluginIconStyle(name: string): PluginIconStyle {
  const letter = name.trim()[0]?.toUpperCase() ?? '#'
  return { letter, color: PALETTE[pluginIconHash(name) % PALETTE.length] }
}
</script>

<script setup lang="ts">
import { computed } from 'vue';

interface Props {
  name: string;
  size?: 'sm' | 'md' | 'lg';
}

const props = withDefaults(defineProps<Props>(), {
  size: 'md',
});

const style = computed(() => pluginIconStyle(props.name));
</script>

<template>
  <span
    :class="['plugin-icon', `plugin-icon-${size}`]"
    :style="{ background: style.color }"
    aria-hidden="true"
  >{{ style.letter }}</span>
</template>

<style scoped>
.plugin-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-md);
  color: #fff;
  font-weight: 600;
  flex-shrink: 0;
  user-select: none;
  line-height: 1;
}

.plugin-icon-sm {
  width: 28px;
  height: 28px;
  font-size: 13px;
}

.plugin-icon-md {
  width: 36px;
  height: 36px;
  font-size: 16px;
}

.plugin-icon-lg {
  width: 56px;
  height: 56px;
  font-size: 24px;
  border-radius: 14px;
}
</style>
