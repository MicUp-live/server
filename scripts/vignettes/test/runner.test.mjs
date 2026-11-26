import assert from 'node:assert/strict'
import { EventEmitter } from 'node:events'
import fs from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import test from 'node:test'
import { hostDeleteEventViaApi } from '../lib/micup.mjs'
import { hasFailingFindings, VignetteRunner } from '../lib/runner.mjs'
import { blockNonLocalRequests, cleanupTemporaryAccount } from '../vignettes/account-privacy.mjs'

test('high and medium findings fail a run, while informational findings do not', () => {
  assert.equal(hasFailingFindings([]), false)
  assert.equal(hasFailingFindings([{ severity: 'info' }]), false)
  assert.equal(hasFailingFindings([{ severity: 'medium' }]), true)
  assert.equal(hasFailingFindings([{ severity: 'high' }]), true)
})

test('report includes HTTP failures whose response body is still being read', async (t) => {
  const outDir = await fs.mkdtemp(path.join(os.tmpdir(), 'micup-vignette-test-'))
  t.after(async () => {
    await fs.unlink(path.join(outDir, 'findings.json'))
    await fs.unlink(path.join(outDir, 'storyboard.html'))
    await fs.rmdir(outDir)
  })
  const runner = new VignetteRunner({ baseUrl: 'http://localhost', outDir })
  const page = new EventEmitter()
  page.url = () => 'http://localhost/event'
  const actor = { id: 'singer', authenticated: false, page }
  runner._attachDiagnostics(actor)

  let releaseBody
  const body = new Promise((resolve) => { releaseBody = resolve })
  page.emit('response', {
    status: () => 503,
    url: () => 'http://localhost/api/v1/events',
    request: () => ({ method: () => 'GET' }),
    text: () => body,
  })
  const reportPromise = runner.writeReport()
  releaseBody('unavailable')
  const { report } = await reportPromise
  assert.equal(report.summary.high, 1)
  assert.equal(hasFailingFindings(report.findings), true)
  assert.match(report.findings[0].message, /503.*unavailable/)
})

test('response diagnostics use the actor’s current authentication state', async () => {
  const runner = new VignetteRunner({ baseUrl: 'http://localhost', outDir: '.' })
  const actor = { id: 'singer', authenticated: false, page: { url: () => 'http://localhost/' } }
  const response = {
    status: () => 401,
    url: () => 'http://localhost/api/v1/auth/me',
    request: () => ({ method: () => 'GET' }),
    text: async () => '',
  }
  await runner._inspectResponse(actor, response)
  assert.equal(runner.findings.length, 0)
  actor.authenticated = true
  await runner._inspectResponse(actor, response)
  assert.equal(runner.findings[0].severity, 'medium')
})

test('admission policy rejections are reported as expected informational findings', async () => {
  const runner = new VignetteRunner({ baseUrl: 'http://localhost', outDir: '.' })
  const actor = { id: 'singer', authenticated: true, page: { url: () => 'http://localhost/event' } }
  for (const [status, error] of [[409, 'max_active_limit'], [429, 'submission_cooldown']]) {
    await runner._inspectResponse(actor, {
      status: () => status,
      url: () => 'http://localhost/api/v1/events/event-1/requests',
      request: () => ({ method: () => 'POST' }),
      text: async () => JSON.stringify({ error }),
    })
  }
  assert.equal(runner.findings.length, 2)
  assert.ok(runner.findings.every(({ severity, category }) =>
    severity === 'info' && category === 'admission.expected_rejection'
  ))
  assert.equal(hasFailingFindings(runner.findings), false)
})

test('a songbook with no prior import reports the expected empty job lookup as informational', async () => {
  const runner = new VignetteRunner({ baseUrl: 'http://localhost', outDir: '.' })
  const page = new EventEmitter()
  page.url = () => 'http://localhost/kj/songbooks'
  const actor = { id: 'host', authenticated: true, page }
  runner._attachDiagnostics(actor)
  page.emit('console', {
    type: () => 'error',
    text: () => 'Failed to load resource: the server responded with a status of 404 (Not Found)',
  })
  assert.equal(runner.findings.length, 0)
  await runner._inspectResponse(actor, {
    status: () => 404,
    url: () => 'http://localhost/api/v1/songbooks/book-1/import-jobs/latest',
    request: () => ({ method: () => 'GET' }),
    text: async () => 'no import job\n',
  })
  assert.equal(runner.findings.length, 1)
  assert.equal(runner.findings[0].severity, 'info')
  assert.equal(runner.findings[0].category, 'import.no_prior_job')
  assert.equal(hasFailingFindings(runner.findings), false)
})

test('account deletion failure diagnostics never include recovery tokens', async () => {
  const runner = new VignetteRunner({ baseUrl: 'http://localhost', outDir: '.' })
  const actor = { id: 'host', authenticated: true, page: { url: () => 'http://localhost/' } }
  await runner._inspectResponse(actor, {
    status: () => 500,
    url: () => 'http://localhost/api/v1/auth/me',
    request: () => ({ method: () => 'DELETE' }),
    text: async () => JSON.stringify({ detail: 'failed', deletion_token: 'sensitive-token' }),
  })
  assert.equal(runner.findings[0].severity, 'high')
  assert.doesNotMatch(JSON.stringify(runner.findings), /sensitive-token/)
})

test('account privacy blocks nonlocal browser requests', async () => {
  let handler
  const blockedOrigins = new Set()
  await blockNonLocalRequests({ route: async (_pattern, fn) => { handler = fn } }, blockedOrigins)
  let action
  const route = (url) => ({
    request: () => ({ url: () => url }),
    continue: async () => { action = 'continued' },
    abort: async () => { action = 'aborted' },
  })
  await handler(route('http://127.0.0.1:8080/api/v1/auth/register'))
  assert.equal(action, 'continued')
  await handler(route('https://remote.example/api/v1/auth/register'))
  assert.equal(action, 'aborted')
  assert.deepEqual([...blockedOrigins], ['https://remote.example'])
})

test('temporary account cleanup retries with deletion recovery token, not refresh', async () => {
  const originalFetch = globalThis.fetch
  const calls = []
  globalThis.fetch = async (url, options) => {
    calls.push({ url, options })
    return { ok: true, status: 204 }
  }
  try {
    const result = await cleanupTemporaryAccount({ evaluate: (fn, arg) => fn(arg) }, 'recovery-token')
    assert.equal(result.status, 204)
    assert.equal(calls.length, 1)
    assert.equal(calls[0].url, '/api/v1/auth/me')
    assert.equal(calls[0].options.headers['X-Deletion-Token'], 'recovery-token')
    assert.equal(calls[0].options.headers.Authorization, undefined)
  } finally {
    globalThis.fetch = originalFetch
  }
})

test('intentional offline errors are suppressed only while the actor is offline', () => {
  const runner = new VignetteRunner({ baseUrl: 'http://localhost', outDir: '.' })
  const page = new EventEmitter()
  page.url = () => 'http://localhost/event'
  const actor = { id: 'singer', authenticated: false, expectedOffline: true, page }
  runner._attachDiagnostics(actor)
  const message = {
    type: () => 'error',
    text: () => 'Failed to load resource: net::ERR_INTERNET_DISCONNECTED',
  }
  page.emit('console', message)
  assert.equal(runner.findings.length, 0)
  actor.expectedOffline = false
  page.emit('console', message)
  assert.equal(runner.findings.length, 1)
  assert.equal(runner.findings[0].category, 'console.error')
})

test('temporary event cleanup surfaces failed deletes', async () => {
  const host = { page: { evaluate: async () => ({ ok: false, status: 500, body: 'unavailable' }) } }
  await assert.rejects(hostDeleteEventViaApi(host, 'event-1'), /event-1 \(500\): unavailable/)
})
