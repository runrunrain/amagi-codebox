<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { collectMarkdownForRender } from '../../composables/useMarkdownStreamCollector'
import { renderMarkdownToHtml } from '../../utils/renderMarkdown'

const props = defineProps<{
  markdown: string
  streaming?: boolean
}>()

const html = ref('')
const failed = ref(false)
const committedMarkdown = ref('')
const liveTail = ref('')

async function render(markdown = props.markdown) {
  try {
    html.value = await renderMarkdownToHtml(markdown)
    failed.value = false
  } catch {
    html.value = ''
    failed.value = true
  }
}

onMounted(async () => {
  committedMarkdown.value = props.markdown
  liveTail.value = ''
  await render(committedMarkdown.value)
})

watch(() => props.markdown, async (next) => {
  if (!props.streaming) {
    committedMarkdown.value = next
    liveTail.value = ''
    await render(next)
    return
  }

  const result = collectMarkdownForRender(committedMarkdown.value, next)
  liveTail.value = result.liveTail
  if (result.shouldRender) {
    committedMarkdown.value = result.committedSource
    await render(result.committedSource)
  }
})
</script>

<template>
  <article class="markdown-card markdown-flow" :class="{ 'markdown-card--streaming': streaming }">
    <div v-if="html" class="markdown-body" v-html="html"></div>
    <pre v-if="streaming && liveTail" class="markdown-live-tail">{{ liveTail }}</pre>
    <pre v-else-if="!html" class="markdown-raw">{{ markdown }}</pre>
    <div v-if="failed" class="markdown-error">Markdown 渲染失败，已显示原文。</div>
  </article>
</template>

<style scoped>
.markdown-card {
  max-width: 100%;
  min-width: 0;
  color: var(--session-text-soft, #dedee4);
  font-size: 15px;
  line-height: 1.68;
  overflow-wrap: anywhere;
}

.markdown-card--streaming {
  position: relative;
}

.markdown-body :deep(a) {
  color: var(--session-accent, #7aa2ff);
  overflow-wrap: anywhere;
  word-break: break-word;
}

.markdown-body :deep(p),
.markdown-body :deep(ul),
.markdown-body :deep(ol),
.markdown-body :deep(blockquote),
.markdown-body :deep(table),
.markdown-body :deep(pre) {
  margin: 0 0 12px;
}

.markdown-body :deep(p:last-child),
.markdown-body :deep(ul:last-child),
.markdown-body :deep(ol:last-child),
.markdown-body :deep(blockquote:last-child),
.markdown-body :deep(table:last-child),
.markdown-body :deep(pre:last-child) {
  margin-bottom: 0;
}

.markdown-body :deep(ul),
.markdown-body :deep(ol) {
  padding-left: 20px;
}

.markdown-body :deep(li) {
  margin: 5px 0;
}

.markdown-body :deep(h1),
.markdown-body :deep(h2),
.markdown-body :deep(h3),
.markdown-body :deep(h4) {
  margin: 16px 0 8px;
  color: var(--session-text, #f4f4f5);
  line-height: 1.24;
  letter-spacing: -0.02em;
}

.markdown-body :deep(h1) { font-size: 1.32em; }
.markdown-body :deep(h2) { font-size: 1.2em; }
.markdown-body :deep(h3) { font-size: 1.1em; }

.markdown-body :deep(code) {
  max-width: 100%;
  border-radius: 6px;
  padding: 0.1em 0.35em;
  background: color-mix(in srgb, var(--session-surface-subtle, #121217) 82%, var(--session-text, #f4f4f5) 10%);
  color: var(--session-text, #f4f4f5);
  font-family: "Cascadia Code", "JetBrains Mono", "SFMono-Regular", Consolas, monospace;
  font-size: 0.88em;
  overflow-wrap: anywhere;
}

.markdown-body :deep(pre),
.markdown-raw,
.markdown-live-tail {
  max-width: 100%;
  overflow-x: auto;
  white-space: pre;
  word-break: normal;
  -webkit-overflow-scrolling: touch;
  border: 1px solid var(--session-border-weak, rgba(255, 255, 255, 0.08));
  border-radius: 10px;
  background: color-mix(in srgb, var(--session-surface-subtle, #121217) 92%, #000 8%);
  color: var(--session-text-muted, #a1a1aa);
  font-family: "Cascadia Code", "JetBrains Mono", "SFMono-Regular", Consolas, monospace;
  font-size: 12.5px;
  line-height: 1.55;
  padding: 10px;
  touch-action: pan-x;
}

.markdown-body :deep(pre code) {
  display: block;
  min-width: max-content;
  padding: 0;
  background: transparent;
  color: inherit;
  white-space: pre;
  overflow-wrap: normal;
}

.markdown-body :deep(table) {
  display: block;
  max-width: 100%;
  overflow-x: auto;
  border-collapse: collapse;
  -webkit-overflow-scrolling: touch;
  touch-action: pan-x;
}

.markdown-body :deep(th),
.markdown-body :deep(td) {
  min-width: 96px;
  padding: 6px 8px;
  border: 1px solid var(--session-border-weak, rgba(255, 255, 255, 0.08));
  text-align: left;
  vertical-align: top;
}

.markdown-body :deep(blockquote) {
  padding-left: 12px;
  border-left: 2px solid var(--session-border, rgba(255, 255, 255, 0.12));
  color: var(--session-text-muted, #a1a1aa);
}

.markdown-live-tail {
  margin-top: 8px;
  color: var(--session-text-faint, #71717a);
  border-style: dashed;
}

.markdown-card--streaming::after {
  content: "";
  display: inline-block;
  width: 7px;
  height: 1.1em;
  margin-left: 2px;
  vertical-align: -0.18em;
  border-radius: 2px;
  background: var(--session-accent, #7aa2ff);
  animation: markdown-caret 1.05s steps(2, start) infinite;
}

.markdown-error {
  margin-top: 8px;
  color: var(--session-danger, #f87171);
  font-size: 12px;
}

@keyframes markdown-caret {
  0%, 45% { opacity: 1; }
  46%, 100% { opacity: 0; }
}

@media (prefers-reduced-motion: reduce) {
  .markdown-card--streaming::after {
    animation: none;
  }
}
</style>
