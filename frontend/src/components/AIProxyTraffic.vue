<template>
  <div class="traffic-workspace">
    <div class="section-title">
      <div><h2>分流连接日志</h2><p class="hint">查看服务的实际出口，对照连接发生时的生效配置。</p></div>
      <div class="actions traffic-actions"><label class="live-toggle"><input v-model="live" type="checkbox" />自动刷新</label><AppButton :disabled="loading" @click="refresh"><AIProxyIcon name="refresh" />刷新</AppButton></div>
    </div>
    <form class="filters" @submit.prevent="applyFilters">
      <div class="search-row">
        <div class="search-field"><AIProxyIcon name="search" /><input v-model="draft.search" aria-label="搜索连接" placeholder="搜索提供商、域名或出口…" /></div>
        <AppButton type="button" :class="{ 'filter-active': showFilters || extraFilters.length }" :aria-expanded="showFilters" aria-controls="traffic-extra-filters" @click="showFilters = !showFilters"><AIProxyIcon name="filter" />更多筛选<span v-if="extraFilters.length" class="filter-count">{{ extraFilters.length }}</span></AppButton>
        <AppButton type="submit" :disabled="loading">查询</AppButton>
      </div>
      <div class="verdict-filters" role="group" aria-label="出口判断筛选">
        <button v-for="option in verdictOptions" :key="option.value" type="button" :class="[option.value, { selected: applied.verdict === option.value }]" :aria-pressed="applied.verdict === option.value" @click="setVerdict(option.value)"><span v-if="option.value" class="verdict-dot" />{{ option.label }}</button>
      </div>
      <div v-show="showFilters" id="traffic-extra-filters" class="extra-filters">
        <label class="field">提供商<input v-model="draft.provider" class="input" placeholder="完整名称，或点击汇总中的提供商" /></label>
        <label class="field">实际出口<input v-model="draft.exit" class="input" placeholder="完整名称，或点击汇总中的出口" /></label>
        <label class="field">开始时间（本地）<input v-model="draft.since" class="input" type="datetime-local" /></label>
        <label class="field">结束时间（本地）<input v-model="draft.until" class="input" type="datetime-local" /></label>
        <div class="filter-footer"><span>调整条件后，点击“查询”生效。</span><button type="button" class="text-button" @click="reset">清空筛选</button></div>
      </div>
      <p v-if="filterError" class="validation-error" role="alert">{{ filterError }}</p>
    </form>
    <div v-if="extraFilters.length || applied.search" class="active-filters"><span>已筛选</span><button v-for="filter in activeFilters" :key="filter.key" type="button" :aria-label="`移除筛选 ${filter.label}`" :title="applied[filter.key]" @click="clearFilter(filter.key)">{{ filter.label }}：{{ filter.value }}<AIProxyIcon name="close" /></button><button type="button" class="clear-all" @click="reset">清空</button></div>
    <div v-if="error || page?.error" class="notice traffic-error" role="alert"><AIProxyIcon name="alert" /><span>{{ error || page?.error }} · 记录可能不完整，请勿据此认定全部流量正常。</span></div>
    <div class="results-toolbar">
      <div class="results-count" aria-live="polite"><strong>{{ page ? page.total.toLocaleString() : '—' }}</strong><span>条连接</span><span v-if="loading" class="refresh-status">更新中…</span><span v-else-if="lastUpdated" class="refresh-status">{{ live && offset === 0 ? '自动更新' : '更新于 ' + lastUpdated }}</span></div>
      <div class="view-toggle" role="group" aria-label="日志显示方式"><button type="button" :aria-pressed="view === 'groups'" :class="{ selected: view === 'groups' }" @click="view = 'groups'">域名汇总</button><button type="button" :aria-pressed="view === 'records'" :class="{ selected: view === 'records' }" @click="view = 'records'">连接明细</button></div>
    </div>
    <div v-if="!page" class="traffic-empty" role="status"><AIProxyIcon :name="error ? 'alert' : 'clock'" /><strong>{{ error ? '暂时无法读取连接记录' : '正在读取连接记录…' }}</strong><p v-if="error">可点击右上角“刷新”重试。</p></div>
    <div v-else-if="!page.total" class="traffic-empty"><AIProxyIcon name="search" /><strong>{{ hasFilters ? '没有符合条件的连接' : '等待第一条连接' }}</strong><p>{{ hasFilters ? '试试其他关键词，或清空筛选条件。' : '启用分流并新建会话后，经过代理的连接会显示在这里。' }}</p><AppButton v-if="hasFilters" @click="reset">清空筛选</AppButton></div>
    <template v-else>
      <div v-if="view === 'groups'" class="table-frame">
        <div class="table-wrap" tabindex="0" role="region" aria-label="域名出口汇总" :aria-busy="loading">
          <table class="group-table"><caption class="sr-only">按提供商、域名与实际出口汇总，出口不符优先显示。</caption>
            <thead><tr><th>服务 / 域名</th><th>指定出口</th><th>实际出口</th><th>判断</th><th class="numeric">连接数</th></tr></thead>
            <tbody><tr v-for="(g, i) in page.groups" :key="i" :class="g.verdict"><td><button class="provider-link" @click="selectGroup(g.provider, '')">{{ g.provider || '未识别提供商' }}</button><code class="domain">{{ g.domain || '未知域名' }}</code></td><td><span class="expected-node" :title="g.expected">{{ exitName(g.expected) || '未能确定' }}</span></td><td><button class="exit-link" :disabled="!g.actual" :title="g.actual" :aria-label="g.actual || '尚无出口证据'" @click="selectGroup(g.provider, g.actual)">{{ exitName(g.actual) || '尚无出口证据' }}<AIProxyIcon v-if="g.actual" name="filter" /></button></td><td><span class="verdict-badge" :class="g.verdict"><AIProxyIcon :name="g.verdict === 'match' ? 'check' : g.verdict === 'mismatch' ? 'alert' : 'info'" />{{ verdictText(g.verdict) }}</span></td><td class="numeric">{{ g.count.toLocaleString() }}</td></tr></tbody>
          </table>
        </div>
        <div class="table-footer"><span>异常优先 · {{ page.groups.length }} 组域名出口</span><span>最多展示 500 组，可筛选缩小范围</span></div>
      </div>
      <div v-else class="table-frame">
        <div class="table-wrap" tabindex="0" role="region" aria-label="逐条连接明细" :aria-busy="loading">
          <table class="records-table"><caption class="sr-only">逐条连接明细，每页 100 条。</caption><thead><tr><th>时间 / 配置</th><th>服务 / 目标</th><th>入口 / 协议</th><th>指定 → 实际出口</th><th>判断</th></tr></thead>
            <tbody><tr v-for="r in page.records" :key="r.id" :class="r.verdict"><td class="time-cell">{{ new Date(r.time).toLocaleString() }}<small>配置 v{{ r.revision }}</small></td><td>{{ r.provider || '未识别提供商' }}<code class="domain">{{ r.destination }}</code></td><td>{{ r.inbound || '未知入口' }}<small>{{ r.network }}</small></td><td><span class="expected-node">{{ r.expected || '未能确定' }}</span><span class="actual-node"><AIProxyIcon name="arrow" />{{ r.actual || '尚无出口证据' }}</span></td><td><span class="verdict-badge" :class="r.verdict"><AIProxyIcon :name="r.verdict === 'match' ? 'check' : r.verdict === 'mismatch' ? 'alert' : 'info'" />{{ verdictText(r.verdict) }}</span></td></tr></tbody>
          </table>
        </div>
        <div class="pagination"><span>{{ displayedOffset + 1 }}–{{ Math.min(displayedOffset + 100, page.total) }} / {{ page.total }}<span v-if="offset > 0 && live" class="page-paused"> · 翻页时暂停刷新</span></span><div class="actions"><AppButton size="small" :disabled="loading || offset === 0" @click="changePage(-100)">上一页</AppButton><AppButton size="small" :disabled="loading || offset + 100 >= page.total" @click="changePage(100)">下一页</AppButton></div></div>
      </div>
    </template>
    <details class="help-disclosure"><summary><AIProxyIcon name="info" />记录范围与判断依据<AIProxyIcon name="chevron" /></summary><div class="help-body"><p>汇总优先显示节点名称，悬停可查看完整标识；连接明细保留完整出口记录。持续保存经过 CodeBox 代理的连接记录，不采样。这里统计连接次数，不是字节数或请求数；旧版本的运行日志不会回填。</p><p>“指定出口”来自连接发生时已生效的配置；“实际出口”来自内核的出口选择记录，不代表连接成功。无法确认绕过本代理的流量、上游之后的公网出口；缺少域名或出口证据时显示为“无法确认”。</p></div></details>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onActivated, onDeactivated, onUnmounted, ref, watch } from 'vue'
import { router } from '../../wailsjs/go/models'
import { GetAIProxyTraffic } from '../../wailsjs/go/main/App'
import AIProxyIcon from './AIProxyIcon.vue'
import AppButton from './ui/AppButton.vue'

const props = withDefaults(defineProps<{ active?: boolean }>(), { active: true })
const empty = () => ({ search: '', verdict: '', provider: '', exit: '', since: '', until: '' })
type Filters = ReturnType<typeof empty>
const draft = ref(empty()), applied = ref(empty()), offset = ref(0), displayedOffset = ref(0)
const page = ref<router.TrafficPage>(), loading = ref(false), error = ref(''), filterError = ref(''), live = ref(true)
const showFilters = ref(false), view = ref<'groups' | 'records'>('groups'), lastUpdated = ref('')
const verdictOptions = [{ value: '', label: '全部连接' }, { value: 'mismatch', label: '出口不符' }, { value: 'unknown', label: '无法确认' }, { value: 'match', label: '出口符合' }]
const filterLabels = { search: '关键词', provider: '提供商', exit: '实际出口', since: '开始', until: '结束' } as const
const activeFilters = computed(() => (Object.keys(filterLabels) as (keyof typeof filterLabels)[]).filter(key => applied.value[key]).map(key => ({ key, label: filterLabels[key], value: key === 'exit' ? exitName(applied.value[key]) : applied.value[key].replace('T', ' ') })))
const extraFilters = computed(() => activeFilters.value.filter(filter => filter.key !== 'search'))
// Preserve exact identifiers in filter queries and connection details; compact summaries show names.
const exitName = (value: string) => value.replace(/ \[[^\]]+\]$/, '') || value
const hasFilters = computed(() => Object.values(applied.value).some(Boolean))
const verdictText = (value: string) => ({ match: '出口符合', mismatch: '出口不符', unknown: '无法确认' }[value] || '无法确认')

let refreshPending = false, queryVersion = 0, suspended = false, mounted = false
let timer: ReturnType<typeof setInterval> | undefined
async function refresh() {
  if (!props.active || suspended || !mounted) return
  if (loading.value) { refreshPending = true; return }
  loading.value = true
  error.value = ''
  const version = queryVersion, requestedOffset = offset.value, q = { ...applied.value }
  try {
    const result = await GetAIProxyTraffic(new router.TrafficQuery({ ...q, offset: requestedOffset, since: q.since ? new Date(q.since).toISOString() : '', until: q.until ? new Date(q.until).toISOString() : '' }))
    if (version !== queryVersion || !mounted) return
    if (result.total > 0 && requestedOffset >= result.total) {
      offset.value = Math.floor((result.total - 1) / 100) * 100
      refreshPending = true
      return
    }
    page.value = result
    displayedOffset.value = requestedOffset
    lastUpdated.value = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  } catch (e) { if (version === queryVersion && mounted) error.value = String(e) }
  finally {
    loading.value = false
    if (refreshPending) { refreshPending = false; void refresh() }
  }
}
function applyFilters() {
  filterError.value = ''
  const { since, until } = draft.value
  if ((since && !Number.isFinite(Date.parse(since))) || (until && !Number.isFinite(Date.parse(until)))) { filterError.value = '请填写有效的日期与时间。'; return }
  if (since && until && Date.parse(since) > Date.parse(until)) { filterError.value = '开始时间不能晚于结束时间。'; return }
  applied.value = { ...draft.value, search: draft.value.search.trim(), provider: draft.value.provider.trim(), exit: draft.value.exit.trim() }
  offset.value = 0
  page.value = undefined
  queryVersion++
  void refresh()
}
function setVerdict(value: string) { draft.value.verdict = value; applyFilters() }
function reset() { draft.value = empty(); applyFilters() }
function clearFilter(key: keyof Filters) { draft.value[key] = ''; applyFilters() }
function selectGroup(provider: string, exit: string) { draft.value.provider = provider; draft.value.exit = exit; applyFilters() }
function changePage(delta: number) { offset.value = Math.max(0, offset.value + delta); queryVersion++; void refresh() }
function start() {
  if (timer || !props.active || suspended || !mounted) return
  void refresh()
  timer = setInterval(() => { if (live.value && offset.value === 0) void refresh() }, 3000)
}
function stop() { clearInterval(timer); timer = undefined; refreshPending = false; queryVersion++ }
watch(() => props.active, value => { if (value) start(); else stop() })
onMounted(() => { mounted = true; start() })
onActivated(() => { suspended = false; start() })
onDeactivated(() => { suspended = true; stop() })
onUnmounted(() => { mounted = false; stop() })
</script>

<style scoped src="../styles/ai-proxy-controls.css"></style>
<style scoped>
.traffic-workspace { display: flex; flex-direction: column; gap: 20px; }
.live-toggle { display: flex; align-items: center; gap: 7px; color: var(--secondary); font-size: 12px; white-space: nowrap; cursor: pointer; }
.live-toggle input { width: 14px; height: 14px; margin: 0; accent-color: var(--accent); }
.filters { border: 1px solid var(--proxy-border); border-radius: 12px; padding: 16px; background: var(--card); }
.search-row { display: flex; align-items: center; gap: 10px; }
.search-field { flex: 1; min-width: 0; display: flex; align-items: center; gap: 9px; padding: 0 12px; border: 1px solid var(--proxy-border); border-radius: 8px; background: var(--card); color: var(--secondary); }
.search-field:focus-within { border-color: var(--accent); box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent) 10%, transparent); }
.search-field input { width: 100%; min-width: 0; padding: 10px 0; font: inherit; font-size: 12px; border: 0; outline: none; background: transparent; color: var(--label); }
.search-field input::placeholder { color: var(--secondary); }
.search-field svg { width: 16px; height: 16px; }
.search-row .btn { white-space: nowrap; min-height: 37px; font-size: 12px; }
.filter-active { color: var(--accent-strong); }
.filter-count { padding: 1px 5px; border-radius: 4px; background: var(--card); font-size: 10px; }
.verdict-filters { display: flex; gap: 8px; margin-top: 14px; flex-wrap: wrap; }
.verdict-filters button { display: flex; align-items: center; gap: 6px; padding: 6px 10px; border: 1px solid transparent; border-radius: 6px; font: inherit; font-size: 12px; color: var(--secondary); background: none; cursor: pointer; transition: background .15s, color .15s; }
.verdict-filters button:hover { background: var(--control); }
.verdict-filters button.selected { background: var(--control); color: var(--label); font-weight: 500; }
.verdict-filters button.selected.mismatch { background: color-mix(in srgb, var(--danger) 7%, var(--card)); color: var(--danger-strong); border-color: color-mix(in srgb, var(--danger) 15%, transparent); }
.verdict-dot { width: 5px; height: 5px; border-radius: 50%; background: var(--success-strong); }
.mismatch .verdict-dot { background: var(--danger-strong); }
.unknown .verdict-dot { background: var(--warning-strong); }
.extra-filters { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px 20px; padding-top: 18px; margin-top: 16px; border-top: 1px solid var(--proxy-border); }
.filter-footer { grid-column: 1 / -1; display: flex; align-items: center; justify-content: space-between; font-size: 11px; color: var(--secondary); }
.validation-error { color: var(--danger-strong); font-size: 12px; margin: 12px 0 0; }
.active-filters { display: flex; gap: 7px; flex-wrap: wrap; align-items: center; font-size: 11px; color: var(--secondary); margin-top: -5px; }
.active-filters button { display: flex; align-items: center; gap: 5px; border: 0; border-radius: 5px; padding: 5px 8px; background: var(--control); color: var(--secondary); font: inherit; cursor: pointer; overflow-wrap: anywhere; max-width: 100%; text-align: left; }
.active-filters button svg { width: 12px; height: 12px; }
.active-filters button:hover { color: var(--label); background: var(--controlHover); }
.active-filters .clear-all { background: none; color: var(--accent-strong); }
.traffic-error { background: color-mix(in srgb, var(--danger) 6%, var(--card)); color: var(--danger-strong); overflow-wrap: anywhere; }
.results-toolbar { display: flex; align-items: center; justify-content: space-between; gap: 16px; }
.results-count { display: flex; align-items: baseline; gap: 7px; font-size: 12px; color: var(--secondary); }
.results-count strong { font-size: 21px; font-weight: 600; letter-spacing: -.5px; font-variant-numeric: tabular-nums; }
.refresh-status { font-size: 10px; margin-left: 8px; }
.view-toggle { display: flex; gap: 2px; padding: 3px; background: var(--control); border-radius: 8px; }
.view-toggle button { padding: 6px 12px; border: 0; border-radius: 6px; font: inherit; font-size: 12px; background: transparent; color: var(--secondary); cursor: pointer; white-space: nowrap; }
.view-toggle button.selected { background: var(--card); box-shadow: 0 1px 3px rgba(0, 0, 0, .08); color: var(--label); }
.table-frame { border: 1px solid var(--proxy-border); border-radius: 12px; overflow: hidden; margin-top: -6px; background: var(--card); }
.table-wrap { overflow: auto; }
table { width: 100%; border-collapse: collapse; text-align: left; table-layout: fixed; font-size: 12px; line-height: 1.6; min-width: 670px; }
th { padding: 12px 16px; background: color-mix(in srgb, var(--control) 55%, var(--card)); color: var(--secondary); font-size: 11px; font-weight: 500; }
td { padding: 17px 16px; border-top: 1px solid var(--proxy-border); vertical-align: middle; overflow-wrap: anywhere; }
.group-table th:first-child { width: 28%; }
.group-table th:nth-child(2), .group-table th:nth-child(3) { width: 24%; }
.group-table th:nth-child(4) { width: 130px; }
.group-table th:last-child { width: 78px; }
.records-table th:first-child { width: 155px; }
.records-table th:nth-child(2) { width: 25%; }
.records-table th:nth-child(3) { width: 110px; }
.records-table th:last-child { width: 126px; }
.numeric { text-align: right; font-variant-numeric: tabular-nums; }
tr.mismatch { background: color-mix(in srgb, var(--danger) 4%, var(--card)); }
tr.mismatch td:first-child { box-shadow: inset 3px 0 var(--danger-strong); }
tbody tr:hover { background: color-mix(in srgb, var(--control) 45%, var(--card)); }
tbody tr.mismatch:hover { background: color-mix(in srgb, var(--danger) 7%, var(--card)); }
.domain { display: block; font: 11px/1.7 var(--mono); color: var(--secondary); margin-top: 5px; }
.provider-link, .exit-link { padding: 0; border: 0; background: none; font: inherit; font-weight: 500; color: var(--label); text-align: left; cursor: pointer; overflow-wrap: anywhere; }
.provider-link:hover, .exit-link:hover:not(:disabled) { color: var(--accent-strong); text-decoration: underline; text-underline-offset: 3px; }
.exit-link { display: inline-flex; align-items: center; gap: 6px; }
.exit-link svg { width: 12px; height: 12px; opacity: 0; }
.exit-link:hover svg, .exit-link:focus-visible svg { opacity: 1; }
.exit-link:disabled { color: var(--secondary); cursor: default; }
.expected-node { color: var(--secondary); }
.mismatch .exit-link, .mismatch .actual-node { color: var(--danger-strong); }
.verdict-badge { display: inline-flex; align-items: center; gap: 4px; padding: 3px 7px; border-radius: 5px; font-size: 11px; white-space: nowrap; color: var(--warning-strong); background: color-mix(in srgb, var(--warning) 8%, var(--card)); }
.verdict-badge svg { width: 13px; height: 13px; }
.verdict-badge.match { color: var(--success-strong); background: color-mix(in srgb, var(--success) 7%, var(--card)); }
.verdict-badge.mismatch { color: var(--danger-strong); background: color-mix(in srgb, var(--danger) 8%, var(--card)); }
.actual-node { display: flex; align-items: center; gap: 5px; margin-top: 5px; }
.actual-node svg { width: 13px; height: 13px; }
small { display: block; color: var(--secondary); font-size: 10px; margin-top: 5px; }
.table-footer, .pagination { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 12px 16px; border-top: 1px solid var(--proxy-border); font-size: 10px; color: var(--secondary); }
.pagination { font-size: 12px; }
.page-paused { font-size: 10px; }
.traffic-empty { display: flex; flex-direction: column; align-items: center; gap: 12px; padding: 48px 20px; border: 1px dashed var(--proxy-border); border-radius: 12px; text-align: center; }
.traffic-empty > svg { width: 28px; height: 28px; margin-bottom: 4px; color: var(--secondary); }
.traffic-empty p { font-size: 12px; color: var(--secondary); margin: 0; }
.help-disclosure { padding-top: 4px; }
.help-disclosure > summary > svg:last-child { margin-left: 0; }
.sr-only { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }
@media (max-width: 1000px) { .traffic-actions { gap: 8px; } .refresh-status { display: none; } }
@media (max-width: 760px) { .traffic-workspace > .section-title { align-items: flex-start; flex-wrap: wrap; gap: 14px; } .traffic-actions { margin-left: auto; } .filters { padding: 12px; } .search-row { flex-wrap: wrap; } .search-field { flex-basis: 100%; } .search-row .btn:last-child { margin-left: auto; } .extra-filters { grid-template-columns: minmax(0, 1fr); } .verdict-filters { gap: 3px; } .verdict-filters button { padding: 6px 8px; font-size: 11px; } .table-footer { flex-wrap: wrap; } .page-paused { display: block; } .results-count strong { font-size: 18px; } .view-toggle button { padding: 6px 9px; } }
</style>
