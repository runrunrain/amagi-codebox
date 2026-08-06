#!/usr/bin/env node
import { createHash } from 'node:crypto'
import { execFileSync } from 'node:child_process'
import { gzipSync } from 'node:zlib'
import { promises as fs } from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { chromium } from '@playwright/test'

const outputFile = process.argv[2]
const phase = process.argv[3] ?? 'snapshot'
if (!outputFile) {
  console.error('usage: node e2e/perf/capture-environment.mjs <output.json> [phase]')
  process.exit(2)
}

function command(file, args = []) {
  try {
    return execFileSync(file, args, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim()
  } catch (error) {
    return `unavailable: ${error instanceof Error ? error.message : String(error)}`
  }
}

async function walk(dir) {
  const entries = await fs.readdir(dir, { withFileTypes: true })
  const files = []
  for (const entry of entries) {
    const full = path.join(dir, entry.name)
    if (entry.isDirectory()) files.push(...(await walk(full)))
    else if (entry.isFile()) files.push(full)
  }
  return files
}

const distRoot = path.resolve('mobile', 'dist')
const distFiles = []
for (const file of await walk(distRoot)) {
  const bytes = await fs.readFile(file)
  distFiles.push({
    path: path.relative(distRoot, file),
    bytes: bytes.length,
    gzipBytes: gzipSync(bytes, { level: 9 }).length,
    sha256: createHash('sha256').update(bytes).digest('hex'),
  })
}
distFiles.sort((a, b) => a.path.localeCompare(b.path))
const distManifest = distFiles.map(({ path: file, sha256 }) => `${file} ${sha256}`).join('\n')

const browser = await chromium.launch({ headless: true })
const browserVersion = browser.version()
await browser.close()

const processRows = command('ps', ['-axo', 'pcpu=,pmem=,comm='])
  .split('\n')
  .map((line) => {
    const match = line.trim().match(/^(\d+(?:\.\d+)?)\s+(\d+(?:\.\d+)?)\s+(.+)$/)
    if (!match) return null
    return { cpuPercent: Number(match[1]), memoryPercent: Number(match[2]), command: path.basename(match[3]) }
  })
  .filter(Boolean)
  .sort((a, b) => b.cpuPercent - a.cpuPercent)
  .slice(0, 12)

const packageLock = await fs.readFile(path.resolve('package-lock.json'))
const packageJson = JSON.parse(await fs.readFile(path.resolve('package.json'), 'utf8'))
const mobilePackageJson = JSON.parse(await fs.readFile(path.resolve('mobile', 'package.json'), 'utf8'))

const snapshot = {
  schemaVersion: 1,
  capturedAt: new Date().toISOString(),
  phase,
  source: {
    head: command('git', ['rev-parse', 'HEAD']),
    expectedHead: '1935f7f',
    worktreeDirty: command('git', ['status', '--porcelain']).length > 0,
    packageLockSha256: createHash('sha256').update(packageLock).digest('hex'),
    rootVersion: packageJson.version,
    mobileVersion: mobilePackageJson.version,
  },
  host: {
    os: `${os.type()} ${os.release()}`,
    macOSProductVersion: command('sw_vers', ['-productVersion']),
    architecture: os.arch(),
    cpuModel: os.cpus()[0]?.model ?? 'unknown',
    logicalCpuCount: os.cpus().length,
    totalMemoryBytes: os.totalmem(),
    loadAverage1m5m15m: os.loadavg(),
    uptimeSeconds: os.uptime(),
    power: command('pmset', ['-g', 'batt']),
    topProcessesByCpu: processRows,
  },
  toolchain: {
    node: process.version,
    npm: command('npm', ['--version']),
    go: command('go', ['version']),
    goEnvironment: command('go', ['env', 'GOOS', 'GOARCH', 'CGO_ENABLED']).split('\n'),
    playwright: packageJson.devDependencies?.['@playwright/test'] ?? 'unknown',
    chromium: browserVersion,
  },
  measurementContract: {
    productionAssetSource: 'mobile/dist served by the real Go e2e harness',
    workers: 1,
    retries: 0,
    viewportCssPx: { width: 360, height: 800 },
    mobileEmulation: { isMobile: true, hasTouch: true },
    cacheRule: 'Every core/history sample starts with a fresh BrowserContext; paired-open within a core sample is a warm-cache supplemental lane.',
    warmupRule: 'One discarded full probe round plus one discarded core probe before measured rounds.',
    statistics: 'R-7 linear-interpolation p50/p95 over successful samples; failures are retained and invalidate the round.',
    longTaskThresholdMs: 50,
    firstFrameRule: 'DOM operability conditions followed by two requestAnimationFrame callbacks.',
    relay: {
      scope: 'WebSocket application frames only; HTTP and WS handshake remain loopback/unshaped.',
      oneWayBaseLatencyMs: 80,
      oneWayUniformJitterMs: 30,
      deterministicSeed: '0x4b0000 + round*1000 + sample',
      disconnect: 'Browser Network offline plus relay close code 1011; browser Network online supplies production R0.',
    },
  },
  representativeness: {
    hostCount: 1,
    physicalDeviceCount: 0,
    network: '127.0.0.1 loopback plus deterministic WS-frame delay/jitter',
    missingFromOriginalC007: ['second host', 'physical iOS Safari', 'physical Android Chrome', 'same-LAN Wi-Fi path', 'real TCP loss/reordering'],
    routing: 'Treat as reproducible M4-B surrogate evidence; M4-C must provide the physical-device/network matrix.',
  },
  productionDist: {
    manifestSha256: createHash('sha256').update(distManifest).digest('hex'),
    fileCount: distFiles.length,
    totalBytes: distFiles.reduce((sum, file) => sum + file.bytes, 0),
    totalGzipBytes: distFiles.reduce((sum, file) => sum + file.gzipBytes, 0),
    files: distFiles,
  },
}

await fs.mkdir(path.dirname(path.resolve(outputFile)), { recursive: true })
await fs.writeFile(outputFile, `${JSON.stringify(snapshot, null, 2)}\n`, 'utf8')
console.log(`wrote ${outputFile}`)
