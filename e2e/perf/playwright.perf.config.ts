import { defineConfig } from '@playwright/test'
import * as path from 'node:path'

const round = process.env.M4B_ROUND ?? 'probe'
const artifactDir = process.env.M4B_ARTIFACT_DIR ?? path.resolve('test-results', 'm4-b-performance')

export default defineConfig({
  testDir: path.resolve(__dirname),
  testMatch: 'm4-b-performance.spec.ts',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 15 * 60_000,
  expect: { timeout: 10_000 },
  reporter: [
    ['list'],
    ['json', { outputFile: path.join(artifactDir, 'logs', `playwright-round-${round}.json`) }],
  ],
  outputDir: path.join(artifactDir, 'logs', `playwright-round-${round}`),
  use: {
    viewport: { width: 360, height: 800 },
    isMobile: true,
    hasTouch: true,
    trace: 'off',
    screenshot: 'off',
    video: 'off',
  },
})
