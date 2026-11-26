import assert from 'node:assert/strict'
import crypto from 'node:crypto'
import fs from 'node:fs/promises'
import { createShowViaUI, createSongbookViaUI } from '../lib/micup.mjs'

export const name = 'account-privacy'

export async function blockNonLocalRequests(context, blockedOrigins) {
  await context.route('**/*', async (route) => {
    const url = new URL(route.request().url())
    if (['http:', 'https:'].includes(url.protocol) &&
      !['localhost', '127.0.0.1', '[::1]'].includes(url.hostname)) {
      blockedOrigins.add(url.origin)
      await route.abort('blockedbyclient')
      return
    }
    await route.continue()
  })
}

export async function cleanupTemporaryAccount(page, recoveryToken) {
  return page.evaluate(async (deletionToken) => {
    const headers = { 'Content-Type': 'application/json' }
    if (deletionToken) {
      headers['X-Deletion-Token'] = deletionToken
    } else {
      const refresh = await fetch('/api/v1/auth/refresh', { method: 'POST', credentials: 'include' })
      if (!refresh.ok) return { ok: false, status: refresh.status }
      const { access_token: token } = await refresh.json()
      headers.Authorization = `Bearer ${token}`
    }
    const response = await fetch('/api/v1/auth/me', {
      method: 'DELETE',
      credentials: 'include',
      headers,
      body: JSON.stringify({ confirm: 'DELETE' }),
    })
    return { ok: response.ok, status: response.status }
  }, recoveryToken)
}

export async function run(runner) {
  const target = new URL(runner.baseUrl)
  if (target.protocol !== 'http:' || !['localhost', '127.0.0.1', '[::1]'].includes(target.hostname)) {
    throw new Error('account-privacy requires a dedicated local test environment')
  }
  const actor = await runner.addActor('account', { label: 'Temporary Host', role: 'host' })
  const { page } = actor
  const blockedOrigins = new Set()
  await blockNonLocalRequests(actor.context, blockedOrigins)
  const tag = crypto.randomBytes(8).toString('hex')
  const email = `vignette-privacy-${tag}@example.test`
  const password = `Vignette!${crypto.randomBytes(16).toString('base64url')}`
  const bookName = `Vignette Privacy ${tag}`
  let created = false
  let deleted = false
  let recoveryToken = ''
  const pendingDeletionResponses = []
  page.on('response', (response) => {
    if (response.request().method() !== 'DELETE' ||
      !new URL(response.url()).pathname.endsWith('/auth/me') || response.ok()) return
    pendingDeletionResponses.push((async () => {
      recoveryToken = (await response.allHeaders())['x-deletion-token'] || recoveryToken
      if (!recoveryToken && response.headers()['content-type']?.includes('application/json')) {
        recoveryToken = JSON.parse(await response.text()).deletion_token || ''
      }
    })())
  })

  try {
    await page.goto('/login?mode=register&isHost=1&returnTo=%2Fkj%2Fsongbooks')
    await page.locator('#display-name').fill('Vignette Privacy Host')
    await page.locator('#email').fill(email)
    await page.locator('#password').fill(password)
    const registration = page.waitForResponse(
      (response) => response.url().endsWith('/api/v1/auth/register') && response.request().method() === 'POST'
    )
    await page.getByRole('button', { name: 'Create Account with Password' }).click()
    const registered = await registration
    assert.equal(registered.status(), 201, 'Temporary host registration succeeds')
    created = true
    actor.authenticated = true
    await page.getByLabel('New songbook name').waitFor()
    const songbook = await createSongbookViaUI(actor, bookName)
    assert.ok(songbook.id)
    await page.goto('/kj/queue')
    const show = await createShowViaUI(actor, {
      venueName: `Vignette Privacy ${tag}`,
      songbookName: bookName,
    })
    assert.equal(show.songbookId, songbook.id)

    await page.locator('#btn-profile-avatar').click()
    await page.locator('#btn-profile-edit').click()
    const profile = page.locator('[aria-labelledby="edit-profile-modal-title"]')
    const [download] = await Promise.all([
      page.waitForEvent('download'),
      profile.locator('#btn-export-data').click(),
    ])
    const downloadPath = await download.path()
    assert.ok(downloadPath, 'Account data export downloaded')
    const contents = await fs.readFile(downloadPath, 'utf8')
    const data = JSON.parse(contents)
    assert.equal(data.user.email, email)
    assert.ok(data.songbooks.some((item) => item.songbook.id === songbook.id))
    assert.ok(data.events.some((event) => event.id === show.eventId))
    assert.ok(!contents.includes(password), 'Export does not contain the account password')
    assert.ok(!('password_hash' in data.user), 'Export does not expose a password hash')
    await runner.snap(
      'account-data-exported',
      'The temporary host downloads account data',
      'The JSON export contains the isolated account, songbook, and show without password material.',
      ['account']
    )

    await profile.locator('#btn-open-delete-account').click()
    const deletion = page.getByRole('dialog', { name: 'Delete Account' })
    const confirmButton = deletion.getByRole('button', { name: 'Delete My Account' })
    assert.equal(await confirmButton.isDisabled(), true)
    await deletion.getByPlaceholder('Type DELETE to confirm').fill('DELETE')
    await runner.snap(
      'account-deletion-confirmation',
      'The host explicitly confirms account deletion',
      'Deletion is disabled until the irreversible-action confirmation is entered.',
      ['account']
    )
    const deletedResponse = page.waitForResponse(
      (response) => response.url().endsWith('/api/v1/auth/me') && response.request().method() === 'DELETE'
    )
    await confirmButton.click()
    const response = await deletedResponse
    assert.ok(response.ok(), `Account deletion succeeds (${response.status()})`)
    deleted = true
    actor.authenticated = false
    await page.waitForURL((url) => url.pathname === '/', { timeout: 15000 })

    const login = await actor.context.request.post('/api/v1/auth/login', { data: { email, password } })
    assert.equal(login.status(), 401, 'Deleted account can no longer sign in')
    const publicShow = await actor.context.request.get(`/api/v1/events/${show.eventId}/public`)
    assert.equal(publicShow.status(), 404, 'The deleted account’s show is no longer available')
    await runner.snap(
      'account-deleted',
      'The temporary account is deleted and signed out',
      'The browser returns home and a fresh password login is denied.',
      ['account']
    )
  } finally {
    await Promise.all(pendingDeletionResponses)
    if (created && !deleted) {
      const cleanup = await cleanupTemporaryAccount(page, recoveryToken)
      if (!cleanup.ok) throw new Error(`Temporary account cleanup failed (${cleanup.status}); recover the test account ${email}`)
    }
    if (blockedOrigins.size) throw new Error(`account-privacy blocked non-local requests: ${[...blockedOrigins].join(', ')}`)
  }
}
