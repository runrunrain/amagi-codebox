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
  <article class="markdown-card" :class="{ 'markdown-card--streaming': streaming }">
    <div v-if="html" class="markdown-body" v-html="html"></div>
    <pre v-if="streaming && liveTail" class="markdown-live-tail">{{ liveTail }}</pre>
    <pre v-else-if="!html" class="markdown-raw">{{ markdown }}</pre>
    <div v-if="failed" class="markdown-error">Markdown 渲染失败，已显示原文。</div>
  </article>
</template>

<style scoped>
.markdown-card {
  border-radius: 14px;
  border: 1px solid rgba(88, 166, 255, 0.18);
  background: linear-gradient(180deg, rgba(22, 27, 34, 0.92), rgba(13, 17, 23, 0.96));
  padding: 12px 14px;
}

.markdown-card--streaming {
  border-color: rgba(88, 166, 255, 0.44);
}

.markdown-body :deep(a) {
  color: #79c0ff;
}

.markdown-body :deep(pre),
.markdown-raw,
.markdown-live-tail {
  overflow-x: auto;
  white-space: pre-wrap;
  word-break: break-word;
}

.markdown-live-tail {
  margin-top: 8px;
  color: #8b949e;
}

.markdown-error {
  margin-top: 8px;
  color: #ff7b72;
  font-size: 12px;
}
</style>
