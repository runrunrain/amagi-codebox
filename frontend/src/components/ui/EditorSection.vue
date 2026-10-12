<template>
  <section class="editor-section" :data-section="sectionId" role="region" :aria-labelledby="titleId">
    <header class="es-header">
      <h3 :id="titleId" class="es-title" tabindex="-1">{{ title }}</h3>
      <code v-if="code" class="es-code">{{ code }}</code>
      <span v-if="badge !== null && badge !== undefined" class="es-count">{{ badge }}</span>
      <span v-if="meta" :class="['es-meta', metaTone]">{{ meta }}</span>
      <InfoTip v-if="tip || tipItems?.length" :label="title" :text="tip" :items="tipItems" />
    </header>
    <div class="es-body"><slot /></div>
  </section>
</template>
<script lang="ts">let sectionSeq = 0;</script>
<script setup lang="ts">
import { computed, provide } from 'vue';
import InfoTip, { type InfoTipItem } from './InfoTip.vue';
import { EDITOR_SECTION_ACTIVE } from './sectionVisibility';
const props = withDefaults(defineProps<{ sectionId: string; title: string; active?: boolean; code?: string; badge?: number | null; meta?: string; metaTone?: 'neutral' | 'warning'; tip?: string | string[]; tipItems?: InfoTipItem[] }>(), { active: true, metaTone: 'neutral' });
const titleId = `editor-section-${++sectionSeq}`;
provide(EDITOR_SECTION_ACTIVE, computed(() => props.active));
</script>
<style scoped>
.editor-section, .es-body { min-width:0; }
.es-header { display:flex; align-items:center; flex-wrap:wrap; gap:6px; min-width:0; margin-bottom:12px; }
.es-title { margin:0; font-size:14px; font-weight:600; color:var(--label); overflow-wrap:anywhere; min-width:0; }
.es-title:focus-visible { outline:2px solid var(--accent); outline-offset:2px; }
.es-code { font-family:var(--mono); font-size:11px; color:var(--tertiary); overflow-wrap:anywhere; }
.es-count, .es-meta { font-size:11px; font-weight:500; color:var(--secondary); background:var(--control); border-radius:999px; padding:1px 7px; }
.es-meta.warning { color:var(--warning-strong); background:var(--warning-tint); }
</style>
