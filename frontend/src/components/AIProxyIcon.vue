<template>
  <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <path :d="paths[name]" />
  </svg>
</template>

<script setup lang="ts">
defineProps<{ name: keyof typeof paths }>()
const paths = {
  plus: 'M12 5v14M5 12h14',
  chevron: 'm9 5 7 7-7 7',
  arrow: 'M4 12h16m-6-6 6 6-6 6',
  close: 'm6 6 12 12M6 18 18 6',
  refresh: 'M20 10a8 8 0 0 0-14-4L3 9m0-6v6h6M4 14a8 8 0 0 0 14 4l3-3m0 6v-6h-6',
  search: 'M21 21l-5-5M18 10a8 8 0 1 1-16 0 8 8 0 0 1 16 0',
  filter: 'M4 7h9m4 0h3M4 17h3m4 0h9M13 4v6M7 14v6',
  globe: 'M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0M3 12h18M12 3a17 17 0 0 1 0 18 17 17 0 0 1 0-18',
  route: 'M5 6h8a5 5 0 0 1 0 10H8m3-3-3 3 3 3M5 6h.01M3 6a2 2 0 1 0 4 0 2 2 0 0 0-4 0',
  check: 'm5 12 4 4L19 6',
  alert: 'M12 8v5m0 3h.01M10.3 4.2 2.5 18a1.4 1.4 0 0 0 1.2 2h16.6a1.4 1.4 0 0 0 1.2-2L13.7 4.2a2 2 0 0 0-3.4 0',
  clock: 'M12 7v5l3 2M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0',
  info: 'M12 11v6m0-10h.01M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0',
} as const
</script>
