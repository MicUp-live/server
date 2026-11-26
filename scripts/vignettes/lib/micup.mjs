/**
 * Reusable MicUp.live UI actions for Playwright vignettes.
 * All actions drive the real browser UI (never bypassing UI flows except optional cleanup).
 */

export async function loginWithPassword(actor, { email, password, returnTo = '/' }) {
  const { page } = actor
  await page.goto(`/login?returnTo=${encodeURIComponent(returnTo)}`, { waitUntil: 'domcontentloaded' })

  // If already logged in (e.g., via storageState), /login immediately redirects away.
  try {
    await page.waitForSelector('input[type="email"]', { timeout: 4000 })
  } catch {
    if (!page.url().includes('/login')) {
      actor.authenticated = true
      return
    }
    throw new Error(`[${actor.id}] Login form did not render at ${page.url()}`)
  }

  await page.fill('input[type="email"]', email)
  await page.fill('input[type="password"]', password)
  await page.click('button[type="submit"]')

  await page.waitForFunction(() => !window.location.pathname.startsWith('/login'), null, {
    timeout: 15000,
  })
  actor.authenticated = true
}

export async function ensureHostLoggedIn(host, { email, password }) {
  await host.page.goto('/kj/queue', { waitUntil: 'domcontentloaded' })
  // Wait briefly for auth gate to either render queue or redirect to /login
  await host.page.waitForTimeout(1000)
  if (host.page.url().includes('/login')) {
    if (!email || !password) {
      throw new Error(
        'Host is not logged in. Set HOST_EMAIL & HOST_PASSWORD, or run `npm --prefix scripts/vignettes run auth`.'
      )
    }
    await loginWithPassword(host, { email, password, returnTo: '/kj/queue' })
    await host.page.goto('/kj/queue', { waitUntil: 'domcontentloaded' })
  }
  await host.page.waitForSelector('.kj-page-shell', { timeout: 15000 })
  host.authenticated = true
}

export async function createShowViaUI(host, {
  venueName = 'Vignette Lounge',
  showTitle,
  joinCode,
  songbookName = '',
  autoAccept = false,
  allowWriteIns = false,
  rotationMode,
  submissionDelay = 0,
  maxActivePerSinger = 0,
  kioskPin = '',
}) {
  const { page } = host
  const code = (joinCode || `VIG${Math.floor(1000 + Math.random() * 9000)}`).toUpperCase()
  const title = showTitle || `Vignette Test ${new Date().toISOString().slice(11, 16)}`

  // Click "+ New Show" in header or "Launch" CTA in empty state
  const newShowBtn = page.getByRole('button', { name: /New Show|Launch/i }).first()
  await newShowBtn.waitFor({ state: 'visible', timeout: 15000 })
  await newShowBtn.click()

  const dialog = page.locator('[aria-labelledby="create-show-title"]')
  await dialog.waitFor({ state: 'visible', timeout: 10000 })

  await dialog.locator('input[placeholder*="Irish Pub"]').fill(venueName)
  await dialog.locator('input[placeholder*="Tuesday Night Live"]').fill(title)
  await dialog.locator('input[placeholder*="OMALLEY"]').fill(code)

  if (songbookName) {
    const select = dialog.locator('select').first()
    const options = await select.locator('option').allTextContents()
    const match = options.find((opt) => opt.toLowerCase().includes(songbookName.toLowerCase()))
    if (match) {
      await select.selectOption({ label: match })
    }
  }

  if (rotationMode) {
    await dialog.locator(`input[name="create_rotation_mode"][value="${rotationMode}"]`).check()
  }

  await dialog.getByRole('checkbox', { name: /Auto-Accept Requests/i }).setChecked(autoAccept)
  await dialog
    .getByText('Singer Cooldown Delay', { exact: true })
    .locator('..')
    .locator('select')
    .selectOption(String(submissionDelay))
  await dialog
    .getByText(/Active Requests Limit per Singer/)
    .locator('..')
    .locator('select')
    .selectOption(String(maxActivePerSinger))

  // Ensure allow_write_ins matches desired mode if checkbox is present
  const writeInsBox = dialog.locator('#create-form-write-ins')
  if ((await writeInsBox.count()) > 0) {
    const checked = await writeInsBox.isChecked()
    if (checked !== allowWriteIns) {
      await writeInsBox.click()
    }
  }

  if (kioskPin) {
    await dialog.locator('#create-form-kiosk-pin').fill(kioskPin)
  }

  // Capture created event ID from API response
  const createRespPromise = page.waitForResponse(
    (res) => res.url().includes('/api/v1/events') && res.request().method() === 'POST',
    { timeout: 15000 }
  )

  await dialog.getByRole('button', { name: /Launch Show/i }).click()
  const resp = await createRespPromise
  if (!resp.ok()) {
    throw new Error(`Create show failed (${resp.status()}): ${await resp.text()}`)
  }
  const created = await resp.json()
  await dialog.waitFor({ state: 'hidden', timeout: 10000 })

  return {
    eventId: created.id,
    joinCode: created.join_code || code,
    title: created.name || title,
    songbookId: created.songbook_id,
  }
}

export async function singerJoinShow(singer, joinCode) {
  const { page } = singer
  await page.goto(`/e/${encodeURIComponent(joinCode)}`, { waitUntil: 'domcontentloaded' })
  // Wait for catalog search box to appear (means event & songbook resolved)
  await page.locator('input[placeholder*="anthem" i], input[placeholder*="Filter" i]').first().waitFor({
    state: 'visible',
    timeout: 20000,
  })
  // Give the catalog worker a moment to finish indexing
  await page.waitForTimeout(800)
}

export async function singerOpenEvent(singer, eventId) {
  const { page } = singer
  await page.goto(`/event/${encodeURIComponent(eventId)}`, { waitUntil: 'domcontentloaded' })
  await page.locator('input[placeholder*="anthem" i], input[placeholder*="Filter" i]').first().waitFor({
    state: 'visible',
    timeout: 20000,
  })
  await page.waitForTimeout(800)
}

export async function singerSubmitSong(singer, {
  searchQuery = '',
  singerName,
  keyShift = null,
  note = '',
  useQuickNote = '',
  songIndex = 0,
}) {
  const { page } = singer
  const searchInput = page.locator('input[placeholder*="anthem" i]').first()
  if (searchQuery) {
    await searchInput.fill(searchQuery)
    await page.waitForTimeout(500)
  }

  // Click a song request button in the catalog list
  const requestButtons = page.locator('button.btn-primary.btn-sm')
  await requestButtons.first().waitFor({ state: 'visible', timeout: 15000 })
  const count = await requestButtons.count()
  const idx = Math.min(songIndex, Math.max(0, count - 1))
  await requestButtons.nth(idx).click()

  const modal = page.locator('[aria-labelledby="song-request-title"]')
  await modal.waitFor({ state: 'visible', timeout: 10000 })
  // Allow SongRequestModal's isOpen effect to finish prefill before typing
  await page.waitForTimeout(250)

  const nameInput = modal.locator('input[placeholder*="Freddie" i]')
  const prefilledName = await nameInput.inputValue()
  if (singerName && (!prefilledName || !singer.authenticated)) {
    await nameInput.fill(singerName)
  }

  if (keyShift !== null && keyShift !== undefined && keyShift !== '') {
    const raw = String(keyShift).trim()
    let keyPattern
    if (raw === '0' || raw.toLowerCase() === 'standard') {
      keyPattern = /standard/i
    } else if (/^[+-]?\d+/.test(raw)) {
      const isNegative = raw.startsWith('-')
      const digits = raw.replace(/^[+-]/, '')
      const prefix = isNegative ? '-' : '\\+?'
      keyPattern = new RegExp(`^\\s*${prefix}${digits}`, 'i')
    } else {
      const escaped = raw.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
      keyPattern = new RegExp(`^\\s*${escaped}`, 'i')
    }
    await modal.getByRole('button', { name: keyPattern }).first().click()
  }

  if (useQuickNote) {
    const quickBtn = modal.getByRole('button', { name: new RegExp(useQuickNote, 'i') })
    if ((await quickBtn.count()) > 0) {
      await quickBtn.first().click()
    }
  }

  if (note) {
    const notesInput = modal.locator('input[placeholder*="Duet" i], textarea[placeholder*="Duet" i]').first()
    if ((await notesInput.count()) > 0) {
      await notesInput.fill(note)
    }
  }

  const submitRespPromise = page
    .waitForResponse(
      (res) => res.url().includes('/requests') && res.request().method() === 'POST',
      { timeout: 15000 }
    )
    .then((response) => ({ response }), (error) => ({ error }))

  await modal.locator('button[type="submit"]').click()
  const responseResult = await submitRespPromise
  if (responseResult.error) throw responseResult.error
  const resp = responseResult.response
  const ok = resp.ok()
  let payload = null
  try {
    payload = await resp.json()
  } catch {}

  if (ok) {
    await modal.waitFor({ state: 'hidden', timeout: 10000 }).catch(() => {})
  }

  return { ok, status: resp.status(), payload, prefilledName }
}

export async function singerOpenQueueModal(singer) {
  const { page } = singer
  const modal = page.locator('[aria-labelledby="singer-queue-title"]')
  if ((await modal.count()) > 0 && (await modal.isVisible())) {
    return
  }
  // Queue button in SingerHeader shows either "Queue (N)", "You: #N in line", or "CALLED TO STAGE!"
  const btn = page
    .locator('button')
    .filter({ hasText: /Queue|in line|CALLED TO STAGE|Up Next/i })
    .first()
  await btn.waitFor({ state: 'visible', timeout: 10000 })
  await btn.click()
  await modal.waitFor({ state: 'visible', timeout: 10000 })
}

export async function singerCloseQueueModal(singer) {
  const { page } = singer
  const modal = page.locator('[aria-labelledby="singer-queue-title"]')
  if ((await modal.count()) > 0 && (await modal.isVisible())) {
    await modal.getByRole('button', { name: /^Close$/i }).first().click()
    await modal.waitFor({ state: 'hidden', timeout: 5000 }).catch(() => {})
  }
}

export async function hostSelectTab(host, tabName) {
  const { page } = host
  const patterns = {
    staging: /Staging/i,
    active: /Active Queue|Queue \(/i,
    history: /History/i,
  }
  const btn = page.locator('.kj-queue-tab').filter({ hasText: patterns[tabName] || new RegExp(tabName, 'i') }).first()
  await btn.waitFor({ state: 'visible', timeout: 10000 })
  await btn.click()
  await page.waitForTimeout(300)
}

export async function hostActionOnSingerCard(host, singerName, actionLabel) {
  const { page } = host
  // Find the request card containing the singer's name
  const card = page.locator('.kj-request-card, .card').filter({ hasText: singerName }).first()
  await card.waitFor({ state: 'visible', timeout: 15000 })
  const btn = card.getByRole('button', { name: new RegExp(actionLabel, 'i') }).first()
  await btn.click()
  await page.waitForTimeout(400)
}

export async function hostDeclineSingerCard(host, singerName, reason = 'Song not available right now') {
  const { page } = host
  await hostActionOnSingerCard(host, singerName, 'Decline')
  const modal = page.locator('[aria-labelledby="decline-modal-title"], .modal-overlay, [role="dialog"]').last()
  await modal.waitFor({ state: 'visible', timeout: 10000 })
  const textarea = modal.locator('textarea, input[type="text"]').first()
  if ((await textarea.count()) > 0) {
    await textarea.fill(reason)
  }
  await modal.getByRole('button', { name: /Decline Request|Confirm|Decline/i }).last().click()
  await page.waitForTimeout(500)
}

export async function hostBroadcastAnnouncement(host, message) {
  const { page } = host
  const openBtn = page
    .locator('.kj-qh-toolbar')
    .getByRole('button', { name: /Shout Out/i })
    .or(page.locator('button[title*="Announcement" i], button[title*="Shout-Out" i]'))
    .first()
  await openBtn.waitFor({ state: 'visible', timeout: 10000 })
  await openBtn.click()

  const modal = page.locator('[aria-labelledby="announcement-title"], .modal-overlay, [role="dialog"]').last()
  await modal.waitFor({ state: 'visible', timeout: 10000 })

  const textarea = modal.locator('textarea').first()
  await textarea.waitFor({ state: 'visible', timeout: 5000 })
  await textarea.fill(message)

  const patchRespPromise = page.waitForResponse(
    (res) => res.url().includes('/api/v1/events') && res.request().method() === 'PATCH',
    { timeout: 15000 }
  )

  await modal.getByRole('button', { name: /Broadcast/i }).click()

  const resp = await patchRespPromise
  if (!resp.ok()) {
    throw new Error(`Broadcast announcement failed (${resp.status()}): ${await resp.text()}`)
  }

  await modal.waitFor({ state: 'hidden', timeout: 10000 }).catch(() => {})
  await page.waitForTimeout(400)
}

export async function hostTogglePause(host, action = 'pause') {
  const { page } = host
  const pattern = action === 'pause' ? /^⏸️?\s*Pause$/i : /^▶️?\s*Resume$/i
  await page.getByRole('button', { name: pattern }).first().click()
  await page.waitForTimeout(600)
}

export async function hostEndShowViaUI(host) {
  const { page } = host
  const settingsBtn = page
    .locator('.kj-qh-toolbar')
    .getByRole('button', { name: /Settings/i })
    .or(page.locator('button[title*="Queue rules" i]'))
    .or(page.getByRole('button', { name: /^⚙️?\s*Settings$/i }))
    .first()
  await settingsBtn.waitFor({ state: 'visible', timeout: 10000 })
  await settingsBtn.click()

  const modal = page.locator('[aria-labelledby="settings-modal-title"], .modal-overlay, [role="dialog"]').last()
  await modal.waitFor({ state: 'visible', timeout: 10000 })

  const endBtn = modal.getByRole('button', { name: /End Show/i }).first()
  await endBtn.scrollIntoViewIfNeeded()

  // Handle native window.confirm() dialog
  page.once('dialog', async (dialog) => {
    await dialog.accept()
  })

  const patchRespPromise = page.waitForResponse(
    (res) => res.url().includes('/api/v1/events') && res.request().method() === 'PATCH',
    { timeout: 15000 }
  )

  await endBtn.click()

  const resp = await patchRespPromise
  if (!resp.ok()) {
    throw new Error(`End show failed (${resp.status()}): ${await resp.text()}`)
  }

  await modal.waitFor({ state: 'hidden', timeout: 10000 }).catch(() => {})
  await page.waitForTimeout(400)
}

export async function hostDeleteEventViaApi(host, eventId, actors = []) {
  if (!eventId) return
  await Promise.all(
    actors
      .filter((actor) => !actor.page.isClosed() && !actor.page.url().startsWith('about:blank'))
      .map((actor) =>
        actor.page.evaluate(() => {
          if (window.location.pathname === '/') return
          window.history.pushState({}, '', '/')
          window.dispatchEvent(new PopStateEvent('popstate'))
        })
      )
  )
  const result = await host.page.evaluate(async (id) => {
    const refreshRes = await fetch('/api/v1/auth/refresh', {
      method: 'POST',
      credentials: 'include',
    })
    const refreshData = refreshRes.ok ? await refreshRes.json() : {}
    const token = refreshData.access_token || ''
    const response = await fetch(`/api/v1/events/${encodeURIComponent(id)}`, {
      method: 'DELETE',
      credentials: 'include',
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    })
    return { ok: response.ok, status: response.status, body: await response.text() }
  }, eventId)
  if (!result.ok) {
    throw new Error(`Failed to delete temporary event ${eventId} (${result.status}): ${result.body}`)
  }
}

export async function createSongbookViaUI(host, name) {
  const { page } = host
  await page.goto('/kj/songbooks', { waitUntil: 'domcontentloaded' })
  await page.getByLabel('New songbook name').fill(name)
  const responsePromise = page.waitForResponse(
    (response) => response.url().endsWith('/api/v1/songbooks') &&
      response.request().method() === 'POST'
  )
  await page.getByRole('button', { name: 'Create' }).click()
  const response = await responsePromise
  if (!response.ok()) {
    throw new Error(`Create songbook failed (${response.status()}): ${await response.text()}`)
  }
  const songbook = await response.json()
  await page.locator('.kj-songbook-card').filter({ hasText: name }).waitFor()
  return songbook
}

export async function hostDeleteSongbookViaApi(host, songbookId) {
  if (!songbookId) return
  const result = await host.page.evaluate(async (id) => {
    const refresh = await fetch('/api/v1/auth/refresh', {
      method: 'POST',
      credentials: 'include',
    })
    if (!refresh.ok) {
      return { ok: false, status: refresh.status, body: await refresh.text() }
    }
    const { access_token: token } = await refresh.json()
    const response = await fetch(`/api/v1/songbooks/${encodeURIComponent(id)}`, {
      method: 'DELETE',
      credentials: 'include',
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    })
    return { ok: response.ok, status: response.status, body: await response.text() }
  }, songbookId)
  if (!result.ok) {
    throw new Error(`Failed to delete temporary songbook ${songbookId} (${result.status}): ${result.body}`)
  }
}

export async function waitForPollingSync(ms = 6800) {
  // LiveQueue polls every 3.5s, PublicBook polls every 6s
  await new Promise((r) => setTimeout(r, ms))
}

export async function logoutViaUI(actor) {
  const { page } = actor
  const avatar = page.locator('#btn-profile-avatar')
  await avatar.waitFor({ state: 'visible', timeout: 10000 })
  await avatar.click()
  const logoutBtn = page.locator('#btn-profile-logout')
  await logoutBtn.waitFor({ state: 'visible', timeout: 5000 })
  await logoutBtn.click()
  actor.authenticated = false
  await page.waitForTimeout(600)
}

export async function singerFavoriteSong(singer, songIndex = 0, { beforeToggle } = {}) {
  const { page } = singer
  const identifySong = async (starButton) => {
    const card = starButton.locator('xpath=..')
    const titleEl = card.locator('.song-catalog-title')
    const artistEl = card.locator('.song-catalog-artist')
    const aria = (await starButton.getAttribute('aria-label')) || ''
    return {
      title: (await titleEl.count()) > 0
        ? (await titleEl.innerText()).trim()
        : aria.replace(/^Favorite(d)?:?\s*/i, '').trim(),
      artist: (await artistEl.count()) > 0 ? (await artistEl.innerText()).trim() : '',
    }
  }

  const search = page.locator('input[placeholder*="anthem" i]').first()
  let savedFallback = null
  for (const query of ['', ...'abcdefghijklmnopqrstuvwxyz']) {
    if (query && (await search.count()) > 0) {
      await search.fill(query)
      await page.waitForTimeout(250)
    }

    const unsaved = page.locator('button[title="Save track to favorites"]')
    if (await unsaved.count()) {
      const idx = Math.min(songIndex, (await unsaved.count()) - 1)
      const starButton = unsaved.nth(idx)
      const song = await identifySong(starButton)
      if (!song.title) {
        throw new Error('Cannot identify the selected song to verify favorites synchronization.')
      }
      await beforeToggle?.()
      await starButton.click()
      await page.getByRole('button', { name: `Favorited: ${song.title}`, exact: true }).first()
        .waitFor({ state: 'visible', timeout: 10000 })
      return { ...song, alreadyFavorite: false }
    }

    const saved = page.locator('button[title="Favorited track (tap to remove)"]')
    if (!savedFallback && (await saved.count()) > 0) {
      savedFallback = await identifySong(saved.first())
    }
  }

  if (savedFallback?.title) return { ...savedFallback, alreadyFavorite: true }
  throw new Error('No catalog tracks were available to verify favorites synchronization.')
}

export async function singerSubmitWriteInSong(singer, {
  searchQuery,
  songTitle,
  artistName = '',
  singerName,
  note = '',
}) {
  const { page } = singer
  const searchInput = page.locator('input[placeholder*="anthem" i], input[placeholder*="Filter" i]').first()
  await searchInput.fill(searchQuery || songTitle)
  await page.waitForTimeout(600)

  // Click "Request ... as a Write-In" button
  const writeInBtn = page.getByRole('button', { name: /Request .* as a Write-In/i }).first()
  await writeInBtn.waitFor({ state: 'visible', timeout: 10000 })
  await writeInBtn.click()

  const modal = page.locator('[aria-labelledby="song-request-title"]')
  await modal.waitFor({ state: 'visible', timeout: 10000 })
  await page.waitForTimeout(250)

  // Title input
  const titleInput = modal.locator('#write-in-song-title, input[placeholder*="Pink Pony" i]').first()
  if (songTitle && (await titleInput.count()) > 0) {
    await titleInput.fill(songTitle)
  }

  // Artist input
  const artistInput = modal.locator('#write-in-song-artist, input[placeholder*="Chappell" i]').first()
  if (artistName && (await artistInput.count()) > 0) {
    await artistInput.fill(artistName)
  }

  // Singer Screen Name
  const nameInput = modal.locator('input[placeholder*="Freddie" i]')
  const prefilledName = await nameInput.inputValue()
  if (singerName && (!prefilledName || !singer.authenticated)) {
    await nameInput.fill(singerName)
  }

  if (note) {
    const notesInput = modal.locator('input[placeholder*="Duet" i], textarea[placeholder*="Duet" i]').first()
    if ((await notesInput.count()) > 0) {
      await notesInput.fill(note)
    }
  }

  const submitRespPromise = page
    .waitForResponse(
      (res) => res.url().includes('/requests') && res.request().method() === 'POST',
      { timeout: 15000 }
    )
    .then((response) => ({ response }), (error) => ({ error }))

  await modal.locator('button[type="submit"]').click()
  const responseResult = await submitRespPromise
  if (responseResult.error) throw responseResult.error
  const resp = responseResult.response
  const ok = resp.ok()
  let payload = null
  try {
    payload = await resp.json()
  } catch {}

  if (ok) {
    await modal.waitFor({ state: 'hidden', timeout: 10000 }).catch(() => {})
  }

  return { ok, status: resp.status(), payload, prefilledName }
}

export async function singerCancelOwnRequest(singer) {
  const { page } = singer
  await singerOpenQueueModal(singer)

  const cancelBtn = page.locator('button[title*="Cancel request" i]').first()
  await cancelBtn.waitFor({ state: 'visible', timeout: 10000 })

  const deleteRespPromise = page.waitForResponse(
    (res) => res.url().includes('/requests/') && res.request().method() === 'DELETE',
    { timeout: 15000 }
  )

  await cancelBtn.click()
  const resp = await deleteRespPromise
  if (!resp.ok()) {
    throw new Error(`Cancel request failed (${resp.status()}): ${await resp.text()}`)
  }

  await page.waitForTimeout(600)
}
