<script lang="ts" setup>
import { onMounted } from 'vue'
import AppLayout from './components/layout/AppLayout.vue'
import Toast from './components/common/Toast.vue'
import { useToast } from './composables/useToast'
import { GetStartupWarnings } from '../wailsjs/go/main/App'

const { showError } = useToast()

onMounted(async () => {
  try {
    const warnings = await GetStartupWarnings()
    for (const w of warnings) {
      showError(w, 8000)
    }
  } catch {
    // If the backend call fails, do not block app rendering.
  }
})
</script>

<template>
  <AppLayout />
  <Toast />
</template>

<style>
* {
  margin: 0;
  padding: 0;
  box-sizing: border-box;
}
</style>
