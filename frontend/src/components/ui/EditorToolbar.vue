<!--
  EditorToolbar - 配置编辑器统一工具栏（provider-center-layout-redesign §4.0.3）

  一行承载：[前置槽] [路径胶囊（点击复制）] [✓ 合法状态] [状态槽] ····· [生效标签 ⓘ] [操作槽]
  取代各编辑器自带的 h2 + subtitle + 工具栏 + 路径行 + 合法状态行（共 4–5 行 chrome）。
  复制逻辑留给各编辑器自己的 copyPath()（沿用 toast），本基件不依赖 useToast。
  窗口窄时 lead / trail 两组 flex-wrap 换行；路径只截断目录，文件名永不截断。
-->
<template>
  <div class="editor-toolbar">
    <div class="et-lead">
      <slot name="leading" />
      <button
        v-if="path"
        type="button"
        class="et-path"
        :title="path"
        :aria-label="`复制${pathLabel}路径：${path}`"
        @click="$emit('copy-path')"
      >
        <svg
          class="et-ic"
          viewBox="0 0 16 16"
          width="12"
          height="12"
          fill="none"
          stroke="currentColor"
          stroke-width="1.4"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <path d="M3.5 1.75h6l3 3v9.5h-9z" />
          <path d="M9.5 1.75v3h3" />
        </svg>
        <!-- dir + base 必须在同一个无 gap 的容器里：放在 .et-path（gap:6px）里会在路径中间多出一个「空格」 -->
        <span class="et-path-text"><span class="et-path-dir">{{ split.dir }}</span><span class="et-path-base">{{ split.base }}</span></span>
        <svg
          class="et-ic et-copy"
          viewBox="0 0 16 16"
          width="12"
          height="12"
          fill="none"
          stroke="currentColor"
          stroke-width="1.4"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <rect x="5.5" y="5.5" width="8" height="8" rx="1.5" />
          <path d="M5.5 10.5H4A1.5 1.5 0 0 1 2.5 9V4A1.5 1.5 0 0 1 4 2.5h5A1.5 1.5 0 0 1 10.5 4v1.5" />
        </svg>
      </button>
      <span v-if="validLabel" class="et-valid">✓ {{ validLabel }}</span>
      <slot name="status" />
    </div>
    <div class="et-trail">
      <span v-if="effect" class="et-effect">{{ effect }}</span>
      <InfoTip v-if="hasEffectTip" :label="effect || '生效时机'" :text="effectTip" />
      <slot name="actions" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import InfoTip from './InfoTip.vue';

interface Props {
  /** 配置文件完整路径；为空时不渲染胶囊 */
  path?: string;
  /** 路径对象名，用于胶囊 aria-label：复制{pathLabel}路径：… */
  pathLabel?: string;
  /** 合法状态文字（如「JSON 合法」），非空时显示 ✓ 前缀 */
  validLabel?: string;
  /** 生效标签（如「新会话生效」） */
  effect?: string;
  /** 生效标签旁的 InfoTip 补充说明 */
  effectTip?: string | string[];
}

const props = withDefaults(defineProps<Props>(), {
  path: '',
  pathLabel: '配置文件',
  validLabel: '',
  effect: '',
});

defineEmits<{
  'copy-path': [];
}>();

/** 按最后一个 '/' 或 '\\' 切分（兼容 Windows 路径），dir 含末尾分隔符 */
const split = computed(() => {
  const p = props.path;
  const i = Math.max(p.lastIndexOf('/'), p.lastIndexOf('\\'));
  return i < 0 ? { dir: '', base: p } : { dir: p.slice(0, i + 1), base: p.slice(i + 1) };
});

const hasEffectTip = computed(() => {
  const t = props.effectTip;
  return Array.isArray(t) ? t.length > 0 : !!t;
});
</script>

<style scoped>
.editor-toolbar {
  container: editor-toolbar / inline-size;
  min-width: 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  min-height: 32px;
}

.et-lead {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1 1 240px;
  min-width: 0;
}

.et-trail {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 0 0 auto;
}

.et-path {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 26px;
  max-width: min(100%, 440px);
  min-width: 0;
  padding: 0 8px;
  border: none;
  border-radius: var(--radius-sm);
  background: var(--control);
  color: var(--secondary);
  font-family: var(--mono);
  font-size: 11.5px;
  cursor: pointer;
}

.et-path:hover {
  background: var(--controlHover);
  color: var(--label);
}

.et-path:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 1px;
}

.et-ic {
  flex: 0 0 auto;
}

/* 目录可截断（省略号），文件名永不截断 */
.et-path-text {
  display: inline-flex;
  min-width: 0;
}

.et-path-dir {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--tertiary);
}

.et-path-base {
  flex: 0 0 auto;
  white-space: nowrap;
}

.et-copy {
  opacity: 0.55;
}

.et-path:hover .et-copy,
.et-path:focus-visible .et-copy {
  opacity: 1;
}

.et-valid {
  font-size: 12px;
  font-weight: 500;
  color: var(--success-strong);
  white-space: nowrap;
}

.et-effect {
  font-size: 11px;
  font-weight: 500;
  color: var(--secondary);
  background: var(--control);
  border-radius: 999px;
  padding: 1px 7px;
  white-space: nowrap;
}

/* 紧凑化插槽里的 Segmented（工具栏模式切换） */
.et-trail :deep(.segmented) {
  display: inline-flex;
  padding: 2px;
  border-radius: 8px;
}

.et-trail :deep(.seg) {
  flex: 0 0 auto;
  padding: 4px 12px;
  font-size: 12px;
  white-space: nowrap;
}
/* Queries affect children, never the query container itself. */
@container editor-toolbar (max-width: 639px) {
  .et-lead, .et-trail { flex:1 1 100%; min-width:0; flex-wrap:wrap; }
}
@container editor-toolbar (max-width: 399px) {
  .et-path { max-width:100%; }
  .et-trail :deep(.seg) { padding:4px 10px; }
  .et-trail :deep(.app-button) { flex-shrink:0; }
}
@supports not (container-type: inline-size) {
  .et-lead, .et-trail { flex:1 1 100%; min-width:0; flex-wrap:wrap; }
  .et-path { max-width:100%; }
  .et-trail :deep(.seg) { padding:4px 10px; }
}
</style>
