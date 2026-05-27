<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { renderMarkdownToHtml } from '../../utils/renderMarkdown'

const props = defineProps<{
  markdown: string
  streaming?: boolean
}>()

const html = ref('')
const failed = ref(false)

async function render() {
  try {
    html.value = await renderMarkdownToHtml(props.markdown)
    failed.value = false
  } catch {
    html.value = ''
    failed.value = true
  }
}

onMounted(render)
watch(() => props.markdown, render)
</script>

<template>
  <article class="markdown-card" :class="{ 'markdown-card--streaming': streaming }">
    <div v-if="html" class="markdown-body" v-html="html"></div>
    <pre v-else class="markdown-raw">{{ markdown }}</pre>
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
.markdown-raw {
  overflow-x: auto;
  white-space: pre-wrap;
  word-break: break-word;
}

.markdown-error {
  margin-top: 8px;
  color: #ff7b72;
  font-size: 12px;
}
</style>
