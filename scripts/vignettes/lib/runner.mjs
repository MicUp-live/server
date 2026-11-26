import fs from 'node:fs'
import path from 'node:path'
import { chromium } from 'playwright-core'

const CHROME_CANDIDATES = [
  process.env.CHROME_PATH,
  '/usr/bin/google-chrome-stable',
  '/usr/bin/google-chrome',
  '/usr/bin/chromium',
  '/usr/bin/chromium-browser',
  '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
].filter(Boolean)

export function findChromeExecutable() {
  for (const candidate of CHROME_CANDIDATES) {
    if (fs.existsSync(candidate)) return candidate
  }
  throw new Error(
    'No Chrome/Chromium executable found. Set CHROME_PATH=/path/to/chrome or install google-chrome-stable.'
  )
}

export const VIEWPORTS = {
  host: {
    viewport: { width: 1440, height: 900 },
    deviceScaleFactor: 1,
    isMobile: false,
    hasTouch: false,
  },
  stage: {
    viewport: { width: 1280, height: 720 },
    deviceScaleFactor: 1,
    isMobile: false,
    hasTouch: false,
  },
  mobile: {
    viewport: { width: 390, height: 844 },
    deviceScaleFactor: 2,
    isMobile: true,
    hasTouch: true,
    userAgent:
      'Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36',
  },
}

export function hasFailingFindings(findings) {
  return findings.some(({ severity }) => severity === 'high' || severity === 'medium')
}

export class VignetteRunner {
  constructor({ baseUrl, outDir, headed = false, slowMo = 0, vignetteName = 'vignette' }) {
    this.baseUrl = baseUrl.replace(/\/+$/, '')
    this.outDir = outDir
    this.headed = headed
    this.slowMo = slowMo
    this.vignetteName = vignetteName
    this.browser = null
    this.actors = new Map()
    this.beats = []
    this.findings = []
    this.pendingResponses = new Set()
    this.startedAt = new Date().toISOString()
    this.beatCounter = 0
  }

  async init() {
    fs.mkdirSync(path.join(this.outDir, 'shots'), { recursive: true })
    const executablePath = findChromeExecutable()
    this.browser = await chromium.launch({
      executablePath,
      headless: !this.headed,
      slowMo: this.slowMo,
      args: ['--disable-dev-shm-usage'],
    })
  }

  async addActor(id, { label, role = 'mobile', authenticated = false, storageStatePath = null } = {}) {
    const preset = VIEWPORTS[role] || VIEWPORTS.mobile
    const contextOptions = {
      ...preset,
      baseURL: this.baseUrl,
      permissions: [],
    }
    if (storageStatePath && fs.existsSync(storageStatePath)) {
      contextOptions.storageState = storageStatePath
    }

    const context = await this.browser.newContext(contextOptions)
    const page = await context.newPage()
    page.setDefaultTimeout(20000)

    const actor = { id, label: label || id, role, authenticated, context, page }
    this.actors.set(id, actor)
    this._attachDiagnostics(actor)
    return actor
  }

  _attachDiagnostics(actor) {
    const { page, id } = actor

    page.on('pageerror', (err) => {
      const msg = err?.message || String(err)
      if (msg.includes('WebSocket') || msg.includes('vite')) return
      this.recordFinding({
        severity: 'high',
        category: 'pageerror',
        actor: id,
        message: msg,
        url: page.url(),
      })
    })

    page.on('console', (msg) => {
      if (msg.type() === 'error') {
        const text = msg.text()
        // Ignore expected unauthenticated session-restore 401 log if emitted by browser
        if (!actor.authenticated && text.includes('401')) return
        // Ignore Vite dev server HMR websocket notifications
        if (text.includes('[vite]') || text.includes('WebSocket') || text.includes('ws://')) return
        if (
          text.includes('Failed to load resource: the server responded with a status of 409') ||
          text.includes('Failed to load resource: the server responded with a status of 429') ||
          text.includes('Failed to load resource: the server responded with a status of 404')
        ) return
        if (actor.expectedOffline && text.includes('net::ERR_INTERNET_DISCONNECTED')) return
        if (
          text.includes('[MicUp API] POST') &&
          ((text.includes('-> 409') && text.includes('error: max_active_limit')) ||
            (text.includes('-> 429') && text.includes('error: submission_cooldown')))
        ) return
        if (
          text.includes('[MicUp API] GET') &&
          text.includes('/import-jobs/latest -> 404') &&
          text.includes('no import job')
        ) return
        this.recordFinding({
          severity: 'medium',
          category: 'console.error',
          actor: id,
          message: text,
          url: page.url(),
        })
      }
    })

    page.on('response', (res) => {
      const pending = this._inspectResponse(actor, res)
      this.pendingResponses.add(pending)
      pending.finally(() => this.pendingResponses.delete(pending))
    })
  }

  async _inspectResponse(actor, res) {
    const status = res.status()
    if (status < 400) return
    const url = res.url()
    // Guest singers check /auth/me on boot and expect 401
    if (!actor.authenticated && status === 401 && url.includes('/auth/me')) return

    let responseBody = ''
    try {
      responseBody = await res.text()
    } catch {
      // Response body unavailable
    }
    if (res.request().method() === 'DELETE' && new URL(url).pathname.endsWith('/auth/me')) {
      responseBody = ''
    }
    if (url.includes('/requests') && (status === 409 || status === 429)) {
      try {
        const error = JSON.parse(responseBody).error
        if (
          (status === 409 && error === 'max_active_limit') ||
          (status === 429 && error === 'submission_cooldown')
        ) {
          this.recordFinding({
            severity: 'info',
            category: 'admission.expected_rejection',
            actor: actor.id,
            message: `${res.request().method()} ${url} -> ${status} (${error})`,
            url: actor.page.url(),
          })
          return
        }
      } catch {
        // Non-policy responses remain unexpected failures.
      }
    }
    if (
      status === 404 &&
      res.request().method() === 'GET' &&
      url.includes('/import-jobs/latest') &&
      responseBody.trim() === 'no import job'
    ) {
      this.recordFinding({
        severity: 'info',
        category: 'import.no_prior_job',
        actor: actor.id,
        message: `GET ${url} -> 404 (no import job)`,
        url: actor.page.url(),
      })
      return
    }
    this.recordFinding({
      severity: status >= 500 ? 'high' : 'medium',
      category: `http.${status}`,
      actor: actor.id,
      message: `${res.request().method()} ${url} -> ${status}${responseBody ? ` (${responseBody.slice(0, 200)})` : ''}`,
      url: actor.page.url(),
    })
  }

  recordFinding(finding) {
    const entry = {
      at: new Date().toISOString(),
      beat: this.beatCounter,
      ...finding,
    }
    this.findings.push(entry)
    const icon = entry.severity === 'high' ? '!!' : entry.severity === 'medium' ? '!' : 'i'
    console.log(`  [FINDING ${icon}] (${entry.actor}/${entry.category}): ${entry.message}`)
  }

  async _inspectPageHealth(actor, beatTitle) {
    const { page, id } = actor
    try {
      const checks = await page.evaluate(() => {
        const doc = document.documentElement
        const overflowPx = Math.round(doc.scrollWidth - window.innerWidth)
        const brokenImages = Array.from(document.images)
          .filter((img) => img.complete && img.naturalWidth === 0)
          .map((img) => img.src)
        const errorBanners = Array.from(
          document.querySelectorAll('[role="alert"], .error-banner, .toast-error')
        )
          .map((el) => el.textContent?.trim())
          .filter(Boolean)
        return { overflowPx, brokenImages, errorBanners, title: document.title }
      })

      if (checks.overflowPx > 2) {
        this.recordFinding({
          severity: 'medium',
          category: 'layout.overflow',
          actor: id,
          message: `Horizontal overflow of ${checks.overflowPx}px at "${beatTitle}"`,
          url: page.url(),
        })
      }
      if (checks.brokenImages.length > 0) {
        this.recordFinding({
          severity: 'medium',
          category: 'media.broken_image',
          actor: id,
          message: `Broken image(s): ${checks.brokenImages.join(', ')}`,
          url: page.url(),
        })
      }
      for (const banner of checks.errorBanners) {
        this.recordFinding({
          severity: 'info',
          category: 'ui.alert',
          actor: id,
          message: `Visible alert/error banner at "${beatTitle}": ${banner}`,
          url: page.url(),
        })
      }
    } catch {
      // Ignore if page is navigating
    }
  }

  async snap(slug, title, notes = '', actorIds = null) {
    this.beatCounter += 1
    const num = String(this.beatCounter).padStart(2, '0')
    const targets = actorIds
      ? actorIds.map((id) => this.actors.get(id)).filter(Boolean)
      : Array.from(this.actors.values())

    console.log(`\n[Beat ${num}] ${title}`)
    if (notes) console.log(`  ${notes}`)

    const shots = []
    for (const actor of targets) {
      await this._inspectPageHealth(actor, title)
      const filename = `${num}-${slug}-${actor.id}.png`
      const relPath = path.join('shots', filename)
      const fullPath = path.join(this.outDir, relPath)
      try {
        await actor.page.screenshot({ path: fullPath, fullPage: false })
        shots.push({
          actorId: actor.id,
          label: actor.label,
          role: actor.role,
          authenticated: actor.authenticated,
          url: actor.page.url(),
          file: relPath,
        })
      } catch (err) {
        this.recordFinding({
          severity: 'high',
          category: 'screenshot.failed',
          actor: actor.id,
          message: `Screenshot failed at "${title}": ${err.message}`,
          url: actor.page.url(),
        })
      }
    }

    this.beats.push({
      index: this.beatCounter,
      slug,
      title,
      notes,
      at: new Date().toISOString(),
      shots,
    })
  }

  async writeReport() {
    while (this.pendingResponses.size) await Promise.all(this.pendingResponses)
    const report = {
      vignette: this.vignetteName,
      baseUrl: this.baseUrl,
      startedAt: this.startedAt,
      finishedAt: new Date().toISOString(),
      actors: Array.from(this.actors.values()).map(({ id, label, role, authenticated }) => ({
        id,
        label,
        role,
        authenticated,
      })),
      summary: {
        beats: this.beats.length,
        findingsTotal: this.findings.length,
        high: this.findings.filter((f) => f.severity === 'high').length,
        medium: this.findings.filter((f) => f.severity === 'medium').length,
        info: this.findings.filter((f) => f.severity === 'info').length,
      },
      findings: this.findings,
      beats: this.beats,
    }

    const jsonPath = path.join(this.outDir, 'findings.json')
    fs.writeFileSync(jsonPath, JSON.stringify(report, null, 2))

    const htmlPath = path.join(this.outDir, 'storyboard.html')
    fs.writeFileSync(htmlPath, renderStoryboardHtml(report))

    return { jsonPath, htmlPath, report }
  }

  async close() {
    if (this.browser) {
      await this.browser.close()
      this.browser = null
    }
  }
}

function escapeHtml(str) {
  return String(str ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

function renderStoryboardHtml(report) {
  const findingsRows = report.findings.length
    ? report.findings
        .map(
          (f) => `<tr class="sev-${escapeHtml(f.severity)}">
            <td><span class="pill pill-${escapeHtml(f.severity)}">${escapeHtml(f.severity)}</span></td>
            <td>#${f.beat}</td>
            <td><code>${escapeHtml(f.actor)}</code></td>
            <td><code>${escapeHtml(f.category)}</code></td>
            <td>${escapeHtml(f.message)}</td>
          </tr>`
        )
        .join('\n')
    : `<tr><td colspan="5" class="muted">No automated errors or layout overflows detected.</td></tr>`

  const beatsHtml = report.beats
    .map((beat) => {
      const cards = beat.shots
        .map(
          (shot) => `<figure class="shot shot-${escapeHtml(shot.role)}">
            <figcaption>
              <strong>${escapeHtml(shot.label)}</strong>
              <span class="meta">${escapeHtml(shot.role)} · ${shot.authenticated ? 'logged-in' : 'guest'}</span>
              <div class="url">${escapeHtml(shot.url)}</div>
            </figcaption>
            <a href="${escapeHtml(shot.file)}" target="_blank">
              <img src="${escapeHtml(shot.file)}" alt="${escapeHtml(beat.title)} - ${escapeHtml(shot.label)}" loading="lazy" />
            </a>
          </figure>`
        )
        .join('\n')

      return `<section class="beat" id="beat-${beat.index}">
        <div class="beat-header">
          <h2>Beat ${String(beat.index).padStart(2, '0')}: ${escapeHtml(beat.title)}</h2>
          ${beat.notes ? `<p class="notes">${escapeHtml(beat.notes)}</p>` : ''}
        </div>
        <div class="shot-grid">${cards}</div>
      </section>`
    })
    .join('\n')

  return `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>MicUp Vignette: ${escapeHtml(report.vignette)}</title>
  <style>
    :root { color-scheme: dark; --bg: #0f1117; --card: #171a24; --border: #2a2f3f; --text: #e8ecf4; --muted: #9aa4b8; }
    * { box-sizing: border-box; }
    body { margin: 0; padding: 24px; font-family: system-ui, -apple-system, sans-serif; background: var(--bg); color: var(--text); }
    header { margin-bottom: 24px; border-bottom: 1px solid var(--border); padding-bottom: 16px; }
    h1 { margin: 0 0 8px; font-size: 1.5rem; }
    .meta-bar { color: var(--muted); font-size: 0.9rem; display: flex; flex-wrap: wrap; gap: 16px; }
    .summary { display: flex; gap: 12px; margin: 16px 0 24px; flex-wrap: wrap; }
    .stat { background: var(--card); border: 1px solid var(--border); border-radius: 8px; padding: 10px 16px; }
    .stat strong { font-size: 1.25rem; display: block; }
    table { width: 100%; border-collapse: collapse; background: var(--card); border: 1px solid var(--border); border-radius: 8px; overflow: hidden; margin-bottom: 32px; font-size: 0.88rem; }
    th, td { text-align: left; padding: 8px 12px; border-bottom: 1px solid var(--border); vertical-align: top; }
    th { background: #1e2230; color: var(--muted); font-weight: 600; }
    .pill { display: inline-block; padding: 2px 8px; border-radius: 999px; font-size: 0.75rem; font-weight: 700; text-transform: uppercase; }
    .pill-high { background: rgba(239,68,68,.2); color: #f87171; }
    .pill-medium { background: rgba(245,158,11,.2); color: #fbbf24; }
    .pill-info { background: rgba(99,102,241,.2); color: #a5b4fc; }
    .beat { margin-bottom: 36px; border: 1px solid var(--border); border-radius: 12px; padding: 16px; background: var(--card); }
    .beat-header h2 { margin: 0 0 6px; font-size: 1.15rem; }
    .notes { margin: 0 0 14px; color: var(--muted); font-size: 0.92rem; }
    .shot-grid { display: flex; flex-wrap: wrap; gap: 16px; align-items: flex-start; }
    .shot { margin: 0; background: #0b0d12; border: 1px solid var(--border); border-radius: 8px; overflow: hidden; }
    .shot-host, .shot-stage { flex: 1 1 540px; max-width: 680px; }
    .shot-mobile { flex: 0 1 250px; max-width: 260px; }
    .shot figcaption { padding: 8px 10px; border-bottom: 1px solid var(--border); font-size: 0.82rem; }
    .shot .meta { color: var(--muted); margin-left: 6px; font-size: 0.75rem; }
    .shot .url { color: var(--muted); font-size: 0.7rem; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; margin-top: 2px; }
    .shot img { display: block; width: 100%; height: auto; }
    .muted { color: var(--muted); }
  </style>
</head>
<body>
  <header>
    <h1>MicUp.live Vignette Storyboard: ${escapeHtml(report.vignette)}</h1>
    <div class="meta-bar">
      <span>Target: <strong>${escapeHtml(report.baseUrl)}</strong></span>
      <span>Started: ${escapeHtml(report.startedAt)}</span>
      <span>Finished: ${escapeHtml(report.finishedAt)}</span>
    </div>
  </header>

  <div class="summary">
    <div class="stat"><strong>${report.summary.beats}</strong>Beats</div>
    <div class="stat"><strong>${report.summary.high}</strong>High Findings</div>
    <div class="stat"><strong>${report.summary.medium}</strong>Medium Findings</div>
    <div class="stat"><strong>${report.summary.info}</strong>Info Notes</div>
  </div>

  <h2>Automated Findings &amp; Pain Points</h2>
  <table>
    <thead><tr><th>Severity</th><th>Beat</th><th>Actor</th><th>Category</th><th>Detail</th></tr></thead>
    <tbody>${findingsRows}</tbody>
  </table>

  <h2>Beat-by-Beat Storyboard</h2>
  ${beatsHtml}
</body>
</html>`
}
