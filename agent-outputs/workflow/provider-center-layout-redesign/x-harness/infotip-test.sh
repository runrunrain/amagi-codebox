#!/bin/bash
# X4 / X5 / X7：InfoTip 键盘、指针、裁切与翻转、皮肤下的不透明度。真实键盘 / 鼠标事件（agent-browser press / hover / click / mouse）。
# 用法：./infotip-test.sh   前置：vite dev 在 127.0.0.1:5199；退出码 = 失败断言数
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
AB="$HERE/ab.sh"
OUT="$HERE/../screenshots"
FAIL=0
ok()   { echo "  PASS  $1"; }
bad()  { echo "  FAIL  $1  ← $2"; FAIL=$((FAIL+1)); }
# 断言：JS 表达式（返回 boolean）
assert() { local name="$1" js="$2"; local r; r=$($AB eval "$js" 2>&1); if [ "$r" = "true" ]; then ok "$name"; else bad "$name" "got: $r"; fi; }

BTN_ZH='button[aria-label="说明：上下文压缩"]'
BTN_CON='button[aria-label="说明：并发限制"]'
# pop 状态读取：$1 = aria-label
state() { echo "(()=>{const b=document.querySelector('button[aria-label=\"$1\"]'); const p=document.getElementById(b.getAttribute('aria-describedby')); return {b,p,open:getComputedStyle(p).display!=='none',exp:b.getAttribute('aria-expanded')==='true',r:p.getBoundingClientRect(),br:b.getBoundingClientRect()}})()"; }

# 复位：点页面标题处（外点关闭任何固定浮层）+ 指针移到角落，保证用例相互隔离
reset() { $AB mouse move 700 70 >/dev/null; $AB mouse down >/dev/null; $AB mouse up >/dev/null; $AB mouse move 20 20 >/dev/null; sleep 0.25; }
# 等待元素出现（最多 3s）
waitfor() { for i in $(seq 1 30); do r=$($AB eval "!!document.querySelector('$1')"); [ "$r" = "true" ] && return 0; sleep 0.1; done; return 1; }

$AB set viewport 1280 800 >/dev/null
$AB open "http://127.0.0.1:5199/?s=full&n=it-$RANDOM#/provider" >/dev/null; sleep 1
$AB eval "__ab.seq('pi-agents')" >/dev/null; sleep 1.2

echo "== T1 初始：所有 InfoTip 关闭，describedby 目标存在且 role=tooltip"
assert "T1 所有按钮 aria-expanded=false 且浮层 display:none、role=tooltip" "[...document.querySelectorAll('button.infotip-btn')].every(b=>{const p=document.getElementById(b.getAttribute('aria-describedby'));return b.getAttribute('aria-expanded')==='false'&&p&&p.getAttribute('role')==='tooltip'&&getComputedStyle(p).display==='none'&&p.parentElement===document.body})"
assert "T1 按钮 aria-label 均以「说明：」开头" "[...document.querySelectorAll('button.infotip-btn')].every(b=>b.getAttribute('aria-label').startsWith('说明：'))"

echo "== T2 键盘：Tab 顺序、Enter 打开、卡片不折叠、Esc 关闭并回焦"
$AB eval "(()=>{const h=[...document.querySelectorAll('.ccc-header')].find(b=>b.textContent.includes('上下文压缩')); h.id='hdr-compaction'; return 1})()" >/dev/null
$AB focus "#hdr-compaction" >/dev/null; $AB press Tab >/dev/null
assert "T2 header 之后 Tab 落在 ⓘ 上" "document.activeElement.getAttribute('aria-label')==='说明：上下文压缩'"
assert "T2 仅聚焦不自动打开" "document.activeElement.getAttribute('aria-expanded')==='false'"
$AB press Enter >/dev/null; sleep 0.35
assert "T2 Enter 打开（aria-expanded=true、浮层可见）" "(()=>{const s=$(state '说明：上下文压缩'); return s.open&&s.exp})()"
assert "T2 Enter 不触发卡片折叠切换（header aria-expanded 仍为 true）" "document.getElementById('hdr-compaction').getAttribute('aria-expanded')==='true'"
assert "T2 浮层在视口内（含 8px 边距）" "(()=>{const s=$(state '说明：上下文压缩'); return s.r.left>=8-1&&s.r.right<=innerWidth-8+1&&s.r.top>=8-1&&s.r.bottom<=innerHeight-8+1})()"
assert "T2 浮层背景不透明且 z-index=2000" "(()=>{const s=$(state '说明：上下文压缩'); const cs=getComputedStyle(s.p); return cs.backgroundColor==='rgb(255, 255, 255)'&&cs.zIndex==='2000'&&cs.position==='fixed'})()"
$AB screenshot "$OUT/pi-agents-infotip-open-keyboard.png" >/dev/null
$AB press Escape >/dev/null; sleep 0.3
assert "T2 Esc 关闭并把焦点还给按钮" "(()=>{const s=$(state '说明：上下文压缩'); return !s.open&&document.activeElement===s.b})()"

echo "== T3 Space 打开 / 再按 Space 关闭（pinned 切换）"
$AB press Space >/dev/null; sleep 0.3
assert "T3 Space 打开" "(()=>{const s=$(state '说明：上下文压缩'); return s.open})()"
$AB press Space >/dev/null; sleep 0.3
assert "T3 再按 Space 关闭" "(()=>{const s=$(state '说明：上下文压缩'); return !s.open})()"

echo "== T4 失焦：pinned 来源 Tab 离开即关闭"
$AB press Enter >/dev/null; sleep 0.3; $AB press Tab >/dev/null; sleep 0.3
assert "T4 Tab 离开后关闭" "(()=>{const s=$(state '说明：上下文压缩'); return !s.open})()"

echo "== T5 指针：hover 250ms 打开、离开 120ms 关闭、浮层上停留保持"
$AB mouse move 640 20 >/dev/null; sleep 0.2
$AB scrollintoview "$BTN_CON" >/dev/null; sleep 0.3
$AB hover "$BTN_CON" >/dev/null; sleep 0.12
assert "T5 hover 120ms 时尚未打开（延迟生效）" "(()=>{const s=$(state '说明：并发限制'); return !s.open})()"
sleep 0.4
assert "T5 hover 400ms 后已打开" "(()=>{const s=$(state '说明：并发限制'); return s.open&&!s.exp===false})()"
$AB mouse move 20 20 >/dev/null; sleep 0.4
assert "T5 指针离开 400ms 后已关闭" "(()=>{const s=$(state '说明：并发限制'); return !s.open})()"
echo "   — 指针从按钮移入浮层应保持打开"
$AB hover "$BTN_CON" >/dev/null; sleep 0.45
$AB eval "(()=>{const s=$(state '说明：并发限制'); window.__popCenter={x:Math.round(s.r.left+s.r.width/2),y:Math.round(s.r.top+s.r.height/2)}; return 1})()" >/dev/null
PX=$($AB eval "window.__popCenter.x"); PY=$($AB eval "window.__popCenter.y")
$AB mouse move "$PX" "$PY" >/dev/null; sleep 0.5
assert "T5 指针在浮层内停留 500ms 仍保持打开" "(()=>{const s=$(state '说明：并发限制'); return s.open})()"
$AB mouse move 20 20 >/dev/null; sleep 0.4

echo "== T6 点击固定、浮层内按下不关闭、外点关闭、滚轮滚动关闭"
reset
$AB scrollintoview "$BTN_CON" >/dev/null; sleep 0.3
$AB click "$BTN_CON" >/dev/null; sleep 0.3; $AB mouse move 20 20 >/dev/null; sleep 0.5
assert "T6 点击后离开指针仍保持打开（pinned）" "(()=>{const s=$(state '说明：并发限制'); return s.open&&s.exp})()"
$AB screenshot "$OUT/pi-agents-infotip-pinned-collapsed-card.png" >/dev/null
CENTER=$($AB eval "(()=>{const s=$(state '说明：并发限制'); return Math.round(s.r.left+s.r.width/2)+','+Math.round(s.r.top+s.r.height/2)})()" | tr -d '"')
$AB mouse move "${CENTER%,*}" "${CENTER#*,}" >/dev/null; $AB mouse down >/dev/null; $AB mouse up >/dev/null; sleep 0.3
assert "T6 在浮层内按下指针不会关闭（按钮虽失焦，仍可选中文字）" "(()=>{const s=$(state '说明：并发限制'); return s.open})()"
$AB mouse move 700 140 >/dev/null; $AB mouse down >/dev/null; $AB mouse up >/dev/null; sleep 0.3
assert "T6 外部 pointerdown 关闭" "(()=>{const s=$(state '说明：并发限制'); return !s.open})()"
reset
$AB click "$BTN_CON" >/dev/null; sleep 0.3
assert "T6 再次点击重新打开" "(()=>{const s=$(state '说明：并发限制'); return s.open})()"
$AB mouse move 700 140 >/dev/null; sleep 0.2
$AB mouse wheel -120 >/dev/null; sleep 0.6
assert "T6 滚轮滚动页面后关闭（避免浮层与锚点错位）" "(()=>{const s=$(state '说明：并发限制'); return !s.open})()"
reset

echo "== T7 折叠卡片头 / 卡片底部：浮层完整显示（不被 overflow:hidden 裁切）、视口底部自动翻转"
reset
$AB scrollintoview "$BTN_CON" >/dev/null; sleep 0.3
$AB click "$BTN_CON" >/dev/null; sleep 0.35
assert "T7 折叠卡片头的浮层完整落在视口内" "(()=>{const s=$(state '说明：并发限制'); return s.open&&s.r.left>=7&&s.r.right<=innerWidth-7&&s.r.top>=7&&s.r.bottom<=innerHeight-7})()"
assert "T7 浮层不是卡片的后代（Teleport 到 body，不受 .config-cat-card overflow:hidden 影响）" "(()=>{const s=$(state '说明：并发限制'); return !s.b.closest('.config-cat-card').contains(s.p)})()"
$AB eval "(()=>{const s=$(state '说明：并发限制'); return JSON.stringify({anchorTop:Math.round(s.br.top),anchorBottom:Math.round(s.br.bottom),popTop:Math.round(s.r.top),popBottom:Math.round(s.r.bottom),vh:innerHeight, placedAbove:s.r.bottom<=s.br.top})})()"
$AB screenshot "$OUT/pi-agents-infotip-flip-collapsed-card.png" >/dev/null
$AB press Escape >/dev/null; sleep 0.2; reset

echo "== T8 卡片内字段级 ⓘ（展开 MCP 路由 → Leader 服务器 ⓘ，三态对照）"
$AB eval "(()=>{const h=[...document.querySelectorAll('.ccc-header')].find(b=>b.textContent.includes('MCP 路由')); if(h.getAttribute('aria-expanded')==='false') h.click(); return 1})()" >/dev/null
LBL='说明：Leader 服务器'
waitfor "button[aria-label=\"$LBL\"]" || bad "T8 等待 MCP 卡片展开" "按钮未出现"
$AB scrollintoview "button[aria-label=\"$LBL\"]" >/dev/null; sleep 0.3
$AB focus "button[aria-label=\"$LBL\"]" >/dev/null; $AB press Enter >/dev/null; sleep 0.35
assert "T8 字段级 ⓘ 打开，含 <dl> 三态对照（3 个 dt）" "(()=>{const s=$(state '$LBL'); return s.open&&s.p.querySelectorAll('dt').length===3&&s.p.querySelectorAll('p').length>=2})()"
$AB screenshot "$OUT/pi-agents-infotip-field-items.png" >/dev/null
$AB press Escape >/dev/null; sleep 0.2

echo "== T9 InfoTip 浮层不污染 role=alert（e2e 契约：卡片内 getByRole('alert') 只来自 compactionError）"
assert "T9 页面上 role=alert 节点数为 0（无错误态）" "document.querySelectorAll('[role=alert]').length===0"

echo "== 汇总：失败断言数 = $FAIL"
exit $FAIL
