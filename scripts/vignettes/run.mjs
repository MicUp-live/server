#!/usr/bin/env node
import fs from 'node:fs'
import path from 'node:path'
import readline from 'node:readline/promises'
import { fileURLToPath } from 'node:url'
import { chromium } from 'playwright-core'
import { bootstrapBotAccounts } from './lib/bootstrap.mjs'
import { VignetteRunner, findChromeExecutable, hasFailingFindings } from './lib/runner.mjs'
import * as accountPrivacy from './vignettes/account-privacy.mjs'
import * as favoritesAndEdgeCases from './vignettes/favorites-and-edge-cases.mjs'
import * as fairPriorityRotation from './vignettes/fair-priority-rotation.mjs'
import * as hostReorderAndReconnection from './vignettes/host-reorder-and-reconnection.mjs'
import * as kioskPatronSession from './vignettes/kiosk-patron-session.mjs'
import * as queueLifecycle from './vignettes/queue-lifecycle.mjs'
import * as roundRobinAndAdmission from './vignettes/round-robin-and-admission.mjs'
import * as songbookCatalogManagement from './vignettes/songbook-catalog-management.mjs'
import * as songbookImportManagement from './vignettes/songbook-import-management.mjs'

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const AUTH_DIR = process.env.MICUP_AUTH_DIR
  ? path.resolve(process.env.MICUP_AUTH_DIR)
  : path.join(__dirname, '.auth')
const OUTPUT_ROOT = path.join(__dirname, 'output')

const VIGNETTES = {
  [queueLifecycle.name]: queueLifecycle,
  [favoritesAndEdgeCases.name]: favoritesAndEdgeCases,
  [fairPriorityRotation.name]: fairPriorityRotation,
  [kioskPatronSession.name]: kioskPatronSession,
  [roundRobinAndAdmission.name]: roundRobinAndAdmission,
  [hostReorderAndReconnection.name]: hostReorderAndReconnection,
  [songbookCatalogManagement.name]: songbookCatalogManagement,
  [songbookImportManagement.name]: songbookImportManagement,
  [accountPrivacy.name]: accountPrivacy,
}

function parseArgs(argv) {
  const args = {
    baseUrl: process.env.MICUP_BASE_URL || 'http://localhost:5173',
    vignette: 'queue-lifecycle',
    headed: false,
    slowMo: 0,
    keepEvent: false,
    saveAuth: null,
    bootstrapBots: false,
    songbookName: process.env.MICUP_SONGBOOK_NAME || '',
  }

  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i]
    if (arg === '--base-url' && argv[i + 1]) args.baseUrl = argv[++i]
    else if (arg === '--vignette' && argv[i + 1]) args.vignette = argv[++i]
    else if (arg === '--headed') args.headed = true
    else if (arg === '--slow-mo' && argv[i + 1]) args.slowMo = Number(argv[++i]) || 0
    else if (arg === '--keep-event') args.keepEvent = true
    else if (arg === '--bootstrap-bots') args.bootstrapBots = true
    else if (arg === '--songbook' && argv[i + 1]) args.songbookName = argv[++i]
    else if (arg === '--save-auth') {
      const next = argv[i + 1]
      args.saveAuth = next && !next.startsWith('--') ? argv[++i] : 'host'
    } else if (arg === '--help' || arg === '-h') {
      printUsage()
      process.exit(0)
    }
  }
  return args
}

function printUsage() {
  console.log(`MicUp.live Playwright Vignette Runner

Usage:
  node scripts/vignettes/run.mjs [options]

Options:
  --base-url <url>      Target site (default: $MICUP_BASE_URL or http://localhost:5173)
  --vignette <name>     Vignette to run: ${Object.keys(VIGNETTES).join(', ')}
                        (default: queue-lifecycle)
  --songbook <name>     Substring match for songbook when creating a show
  --headed              Show browser windows live while running
  --slow-mo <ms>        Slow down Playwright actions (useful with --headed)
  --keep-event          Do not delete the temporary test event at the end
  --save-auth [role]    Open a headed browser to log in and save storageState
                        (role: "host" or "singer-auth"; saved to scripts/vignettes/.auth/)
  --bootstrap-bots      Register dedicated bot host + bot singer accounts (AWS SES
                        zero-bounce simulator addresses) and seed a sample songbook

Environment variables (alternative to --save-auth):
  MICUP_BASE_URL        Target URL (e.g. https://micup.live)
  HOST_EMAIL            Bot host email
  HOST_PASSWORD         Bot host password
  SINGER_AUTH_EMAIL     Optional logged-in test singer email
  SINGER_AUTH_PASSWORD  Optional logged-in test singer password
  CHROME_PATH           Optional override for Chrome/Chromium binary
`)
}

async function runSaveAuth(baseUrl, role) {
  fs.mkdirSync(AUTH_DIR, { recursive: true })
  const outPath = path.join(AUTH_DIR, `${role}.json`)
  const browser = await chromium.launch({
    executablePath: findChromeExecutable(),
    headless: false,
  })
  const context = await browser.newContext({ baseURL: baseUrl })
  const page = await context.newPage()
  const startPath = role === 'host' ? '/login?returnTo=/kj/queue' : '/login?returnTo=/'

  console.log(`Opening ${baseUrl}${startPath} — sign in as "${role}" in the browser window.`)
  await page.goto(startPath)

  const rl = readline.createInterface({ input: process.stdin, output: process.stdout })
  await rl.question('Press ENTER here once signed in to save storageState... ')
  rl.close()

  await context.storageState({ path: outPath })
  await browser.close()
  console.log(`Saved auth state to ${outPath}`)
}

async function main() {
  const args = parseArgs(process.argv.slice(2))

  if (args.saveAuth) {
    await runSaveAuth(args.baseUrl, args.saveAuth)
    return
  }

  if (args.bootstrapBots) {
    await bootstrapBotAccounts({ baseUrl: args.baseUrl, authDir: AUTH_DIR })
    return
  }

  // Also load saved bot credentials if present (fallback when cookies expire)
  const credsPath = path.join(AUTH_DIR, 'credentials.json')
  let savedCreds = null
  if (fs.existsSync(credsPath)) {
    try {
      savedCreds = JSON.parse(fs.readFileSync(credsPath, 'utf8'))
    } catch {}
  }

  const vignette = VIGNETTES[args.vignette]
  if (!vignette) {
    console.error(`Unknown vignette "${args.vignette}". Available: ${Object.keys(VIGNETTES).join(', ')}`)
    process.exit(1)
  }

  const stamp = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19)
  const outDir = path.join(OUTPUT_ROOT, `${stamp}-${args.vignette}`)

  const hostStatePath = path.join(AUTH_DIR, 'host.json')
  const singerStatePath = path.join(AUTH_DIR, 'singer-auth.json')

  const runner = new VignetteRunner({
    baseUrl: args.baseUrl,
    outDir,
    headed: args.headed,
    slowMo: args.slowMo,
    vignetteName: args.vignette,
  })

  await runner.init()
  let runError = null
  let report

  try {
    console.log(`Running vignette "${args.vignette}" against ${args.baseUrl}`)
    console.log(`Output directory: ${outDir}`)
    await vignette.run(runner, {
      hostEmail: process.env.HOST_EMAIL || savedCreds?.host?.email || '',
      hostPassword: process.env.HOST_PASSWORD || savedCreds?.host?.password || '',
      hostStorageState: fs.existsSync(hostStatePath) ? hostStatePath : null,
      singerAuthEmail: process.env.SINGER_AUTH_EMAIL || savedCreds?.singerAuth?.email || '',
      singerAuthPassword: process.env.SINGER_AUTH_PASSWORD || savedCreds?.singerAuth?.password || '',
      singerAuthStorageState: fs.existsSync(singerStatePath) ? singerStatePath : null,
      songbookName: args.songbookName,
      keepEvent: args.keepEvent,
    })
  } catch (err) {
    runError = err
    runner.recordFinding({
      severity: 'high',
      category: 'vignette.aborted',
      actor: 'runner',
      message: err?.stack || err?.message || String(err),
    })
    await runner.snap('aborted', 'Vignette aborted — state at failure', err?.message || String(err))
  } finally {
    const { jsonPath, htmlPath, report: result } = await runner.writeReport()
    report = result
    await runner.close()

    console.log('\n=== Vignette Run Complete ===')
    console.log(`Beats captured : ${report.summary.beats}`)
    console.log(
      `Findings       : ${report.summary.findingsTotal} (high: ${report.summary.high}, medium: ${report.summary.medium}, info: ${report.summary.info})`
    )
    console.log(`Storyboard HTML: ${htmlPath}`)
    console.log(`Findings JSON  : ${jsonPath}`)
  }

  if (runError || hasFailingFindings(report.findings)) {
    process.exit(1)
  }
}

main().catch((err) => {
  console.error(err)
  process.exit(1)
})
