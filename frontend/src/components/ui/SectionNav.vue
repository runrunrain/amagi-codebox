<template>
  <nav ref="nav" class="section-nav" :aria-label="label">
    <div v-for="group in groups" :key="group.id" class="sn-group">
      <div v-if="group.label" :id="`${instanceId}-${group.id}`" class="sn-group-title">{{ group.label }}</div>
      <ul :aria-labelledby="group.label ? `${instanceId}-${group.id}` : undefined">
        <li v-for="item in group.items" :key="item.id">
          <button type="button" :data-nav-id="item.id" :class="['sn-item', { current: currentId === item.id, branch: branchId === item.id }]"
            :tabindex="!item.disabled && focusId === item.id ? 0 : -1" :disabled="item.disabled"
            :aria-label="item.ariaLabel || item.label" :aria-controls="controlsId"
            :aria-current="currentId === item.id ? 'page' : undefined"
            :aria-expanded="branchId ? branchId === item.id : undefined"
            :aria-describedby="item.meta || item.invalid ? descriptionId(item.id) : undefined"
            @focus="focusId = item.id" @keydown="onKey($event, item.id)" @click="activate(item)">
            <span class="sn-name">{{ item.label }}</span>
            <span v-if="item.meta || item.invalid" :id="descriptionId(item.id)" :class="['sn-meta', item.metaTone]">{{ item.meta }}{{ item.invalid ? ' · 需修正' : '' }}</span>
          </button>
          <ul v-if="item.children?.length" :class="['sn-children', { 'sn-scroll': item.scrollChildren }]">
            <li v-for="child in item.children" :key="child.id">
              <button type="button" :data-nav-id="child.id" :class="['sn-item sn-leaf', { current: currentId === child.id }]"
                :tabindex="!child.disabled && focusId === child.id ? 0 : -1" :disabled="child.disabled"
                :aria-label="child.ariaLabel || child.label" :aria-controls="controlsId"
                :aria-current="currentId === child.id ? 'page' : undefined"
                :aria-describedby="child.meta || child.invalid ? descriptionId(child.id) : undefined"
                @focus="focusId = child.id" @keydown="onKey($event, child.id)" @click="activate(child)">
                <span class="sn-name">{{ child.label }}</span>
                <span v-if="child.meta || child.invalid" :id="descriptionId(child.id)" :class="['sn-meta', child.metaTone]">{{ child.meta }}{{ child.invalid ? ' · 需修正' : '' }}</span>
              </button>
            </li>
          </ul>
        </li>
      </ul>
    </div>
    <p v-if="notice" class="sn-notice">{{ notice }}</p>
  </nav>
</template>
<script lang="ts">let navSeq = 0;</script>
<script setup lang="ts">
import { computed, ref, watch, nextTick } from 'vue';
import type { SectionNavGroup, SectionNavItem } from '../provider/configNavigation';
const props = defineProps<{ groups: SectionNavGroup[]; currentId: string; branchId?: string; label: string; controlsId: string; notice?: string }>();
const emit = defineEmits<{ select: [id: string] }>();
const instanceId = `section-nav-${++navSeq}`;
const descriptionId = (id: string) => `${instanceId}-${encodeURIComponent(id)}`;
const nav = ref<HTMLElement | null>(null);
const enabled = computed(() => props.groups.flatMap(g => g.items.flatMap(i => [i, ...(i.children || [])])).filter(i => !i.disabled));
const preferred = () => enabled.value.find(i => i.id === props.currentId)?.id || enabled.value.find(i => i.id === props.branchId)?.id || enabled.value[0]?.id || '';
const focusId = ref(preferred());
function focusButton(id: string) {
  const button = Array.from(nav.value?.querySelectorAll<HTMLButtonElement>('button[data-nav-id]') || []).find(b => b.dataset.navId === id);
  button?.focus(); button?.scrollIntoView({ block: 'nearest' });
}
function activate(item: SectionNavItem) { if (!item.disabled) emit('select', item.id); }
function onKey(event: KeyboardEvent, id: string) {
  const keys = ['ArrowUp', 'ArrowDown', 'Home', 'End'];
  if (!keys.includes(event.key)) return; // Enter / Space use native button activation.
  event.preventDefault();
  const items = enabled.value;
  const index = items.findIndex(i => i.id === id);
  const next = event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1 : Math.max(0, Math.min(items.length - 1, index + (event.key === 'ArrowDown' ? 1 : -1)));
  if (items[next]) { focusId.value = items[next].id; focusButton(focusId.value); }
}
watch(() => [props.currentId, enabled.value.map(i => i.id).join('\n')], () => {
  // Snapshot updates never steal focus; only a removed focused navigation item needs recovery.
  const hadFocus = nav.value?.contains(document.activeElement);
  const removed = !enabled.value.some(i => i.id === focusId.value);
  if (!hadFocus || removed) focusId.value = preferred();
  if (hadFocus && removed && focusId.value) void nextTick(() => focusButton(focusId.value));
});
</script>
<style scoped>
.section-nav { min-width:0; padding-right:8px; border-right:1px solid var(--separator); align-self:start; position:sticky; top:12px; max-height:max(240px, calc(100vh - 280px)); overflow-y:auto; }
ul { list-style:none; margin:0; padding:0; }
.sn-group-title { margin:8px 8px; font-size:11px; color:var(--secondary); }
.sn-item { display:flex; flex-direction:column; align-items:flex-start; gap:3px; width:100%; min-height:32px; padding:7px 8px; border:0; border-left:2px solid transparent; border-radius:4px; background:transparent; font:inherit; font-size:13px; font-weight:500; text-align:left; color:var(--secondary); cursor:pointer; }
.sn-item:hover:not(:disabled) { background:var(--controlHover); }
.sn-item.current { background:var(--control); border-left-color:var(--accent); color:var(--accent-strong); }
.sn-item.branch { color:var(--label); font-weight:600; }
.sn-item:focus-visible { outline:2px solid var(--accent); outline-offset:-1px; }
.sn-item:disabled { opacity:.55; cursor:default; }
.sn-name { max-width:100%; overflow-wrap:anywhere; }
.sn-leaf { font-weight:400; }
.sn-leaf .sn-name { display:-webkit-box; -webkit-line-clamp:2; -webkit-box-orient:vertical; overflow:hidden; }
.sn-children { margin-left:8px; }
.sn-scroll { max-height:256px; overflow-y:auto; }
.sn-meta, .sn-notice { font-size:11px; line-height:1.5; color:var(--tertiary); overflow-wrap:anywhere; }
.sn-meta.warning { color:var(--warning-strong); }
.sn-notice { margin:8px; }
@media (max-width:1023px) { .sn-item { font-size:12px; min-height:30px; } .sn-scroll { max-height:192px; } }
</style>
