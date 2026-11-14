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

createApp(App).mount('#app')
