<!--
  FieldLabel - 字段标签行：显示名 + 等宽配置键 code + 可选 InfoTip + 可选生效标签
  （provider-center-layout-redesign §4.0.2）

  取代「Leader 服务器（mcp.leader，主进程加载名单）」这类括号套括号的写法：
  键名走 code（P5），解释走 InfoTip（P6），生效时机走右侧标签（L3）。
  注意：不能用 <label> 包住 InfoTip——没有 for 的 label 会把内部第一个按钮当作
  被标注控件，点击标签文字会误开浮层，所以只有传了 htmlFor 才用 <label>。
-->
<template>
  <div class="field-label">
    <label v-if="htmlFor" :for="htmlFor" class="fl-text">{{ text }}</label>
    <span v-else class="fl-text">{{ text }}</span>
    <code v-if="code" class="fl-code">{{ code }}</code>
    <InfoTip v-if="hasTip" :label="text" :text="tip" :items="items" />
    <span v-if="effect" class="fl-effect">{{ effect }}</span>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import InfoTip, { type InfoTipItem } from './InfoTip.vue';

interface Props {
  /** 显示名（中文） */
  text: string;
  /** 配置键，等宽小字 */
  code?: string;
  /** InfoTip 段落 */
  tip?: string | string[];
  /** InfoTip 术语对照 */
  items?: InfoTipItem[];
  /** 生效标签（如「重启 pi 生效」），靠右 */
  effect?: string;
  /** 关联控件 id；传入时文字部分渲染为 <label for> */
  htmlFor?: string;
}

const props = defineProps<Props>();

const hasTip = computed(() => {
  const t = props.tip;
  const hasText = Array.isArray(t) ? t.length > 0 : !!t;
  return hasText || !!(props.items && props.items.length);
});
</script>

<style scoped>
.field-label {
  display: flex;
  align-items: center;
  gap: 6px;
  min-height: 20px;
  min-width: 0;
  flex-wrap: wrap;
}

.fl-text {
  font-size: 12px;
  font-weight: 500;
  color: var(--secondary);
}

.fl-code {
  overflow-wrap: anywhere;
  min-width: 0;
  font-family: var(--mono);
  font-size: 10.5px;
  color: var(--tertiary);
}

.fl-effect {
  margin-left: auto;
  font-size: 11px;
  font-weight: 500;
  color: var(--secondary);
  background: var(--control);
  border-radius: 999px;
  padding: 1px 7px;
  white-space: nowrap;
}
</style>
