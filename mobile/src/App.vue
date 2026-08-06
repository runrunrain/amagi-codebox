<script setup lang="ts">
import AppLayout from './components/AppLayout.vue'
import { useVisualViewport } from './composables/useVisualViewport'

// M4-A：软键盘 VisualViewport 全局跟随（--vvh/--vv-offset CSS 变量，见 composable 注释）
useVisualViewport()
</script>

<template>
  <AppLayout />
</template>
