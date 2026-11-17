/**
 * main.ts — webui-claude SPA 入口（t7）。
 *
 * 纯浏览器态：不引 Wails/wailsjs，token 取 location.hash '#/t='（httpSource）。
 * 皮肤约定（design §7.2）：?skin=light query 叠加 webui-embedded-light 类
 * （与 pi 页面同款类名语义：浅底深字）；缺省深色嵌入。body 背景透明，
 * 内嵌态透出宿主皮肤层（App.vue 全局样式）。
 *
 * 皮肤类在 mount 前落根元素，避免首帧配色闪烁。
 */
import { createApp } from 'vue'
import App from './App.vue'
import './tokens.css'

const params = new URLSearchParams(window.location.search)
document.documentElement.classList.toggle('webui-embedded-light', params.get('skin') === 'light')
// 独立开发态（?mock=1）兜底底色：生产嵌入态由宿主皮肤层承托（body 透明
// 透皮），独立浏览器打开没有宿主层，近白文字会落在浏览器白画布上不可读
// （2026-11-16 浏览器实测）；mock 态补一块与目标肤同源的底色模拟宿主。
// 生产 iframe 不携带 mock 参数，永不命中。
document.documentElement.classList.toggle('mock-backdrop', params.get('mock') === '1')

createApp(App).mount('#app')
