import assert from 'node:assert/strict'
import {
  createShowViaUI,
  ensureHostLoggedIn,
  hostDeleteEventViaApi,
} from '../lib/micup.mjs'

export const name = 'kiosk-patron-session'

export async function run(runner, config) {
  const host = await runner.addActor('host', {
    label: 'Host (KJ Desktop)',
    role: 'host',
    storageStatePath: config.hostStorageState,
  })
  const kiosk = await runner.addActor('kiosk', { label: 'Venue Tablet Kiosk', role: 'host' })
  let show

  try {
    await ensureHostLoggedIn(host, { email: config.hostEmail, password: config.hostPassword })
    show = await createShowViaUI(host, {
      venueName: 'Vignette Kiosk',
      songbookName: config.songbookName,
      autoAccept: true,
      kioskPin: '2468',
    })

    await kiosk.page.goto(`/kiosk/${encodeURIComponent(show.joinCode)}`, { waitUntil: 'domcontentloaded' })
    const search = kiosk.page.getByPlaceholder('Search by Song Title or Artist...')
    await search.waitFor({ state: 'visible', timeout: 20000 })
    await search.fill('Bohemian Rhapsody')
    const song = kiosk.page.getByText('Bohemian Rhapsody', { exact: true })
    await song.waitFor({ state: 'visible', timeout: 15000 })
    await song.click()

    const request = kiosk.page.getByRole('dialog', { name: 'Request Song' })
    await request.waitFor({ state: 'visible' })
    await request.locator('#kiosk-singer-name').fill('Patron One')
    const submitResponse = kiosk.page.waitForResponse(
      (response) => response.url().includes('/requests') && response.request().method() === 'POST'
    )
    await request.getByRole('button', { name: /Send Request/i }).click()
    const submitted = await submitResponse
    assert.equal(submitted.status(), 201, 'Kiosk request is accepted by the API')
    const payload = await submitted.json()
    assert.equal(payload.singer_name, 'Patron One')
    assert.equal(payload.song_title, 'Bohemian Rhapsody')
    await kiosk.page.getByRole('heading', { name: "You're In Line, Patron One!" }).waitFor()
    await runner.snap(
      'patron-request-success',
      'Kiosk patron request shows success',
      'Patron searched the catalog, submitted a song, and saw the success splash.',
      ['host', 'kiosk']
    )

    await kiosk.page.getByRole('button', { name: /All Done! Next Singer/i }).click()
    await search.waitFor({ state: 'visible' })
    await kiosk.page.waitForFunction(() => document.querySelector('input[placeholder="Search by Song Title or Artist..."]')?.value === '')

    await search.fill('Bohemian Rhapsody')
    await song.waitFor({ state: 'visible', timeout: 15000 })
    await song.click()
    const secondRequest = kiosk.page.getByRole('dialog', { name: 'Request Song' })
    await secondRequest.locator('#kiosk-singer-name').fill('Patron Two')
    await kiosk.page.waitForFunction(
      () => !document.querySelector('[aria-labelledby="kiosk-request-title"]') ||
        document.querySelector('#kiosk-singer-name')?.value === 'Patron Two',
      null,
      { timeout: 5000 }
    )
    await kiosk.page.waitForFunction(
      () => document.querySelector('#kiosk-singer-name') === null &&
        document.querySelector('input[placeholder="Search by Song Title or Artist..."]')?.value === '',
      null,
      { timeout: 52000 }
    )
    await runner.snap(
      'idle-session-reset',
      'Kiosk resets after patron inactivity',
      'The 45-second idle timeout cleared the second patron search and request form.',
      ['kiosk']
    )

    await kiosk.page.locator('button[title="Host Exit Kiosk"]').click()
    const exitModal = kiosk.page.getByRole('dialog', { name: 'Exit Kiosk Mode' })
    await exitModal.waitFor({ state: 'visible' })
    await exitModal.locator('#kiosk-exit-pin').fill('2468')
    const pinResponse = kiosk.page.waitForResponse(
      (response) => response.url().includes('/kiosk/verify-pin') && response.request().method() === 'POST'
    )
    await exitModal.getByRole('button', { name: 'Unlock' }).click()
    const verified = await pinResponse
    assert.equal(verified.status(), 204, 'Configured host PIN exits kiosk mode')
    await kiosk.page.waitForURL((url) => url.pathname === '/')
    await runner.snap('pin-unlocked', 'Host PIN exits kiosk mode', 'The configured host PIN returned the tablet to navigation.', ['kiosk'])
  } finally {
    if (show && !config.keepEvent) {
      await hostDeleteEventViaApi(host, show.eventId, Array.from(runner.actors.values()))
    }
  }
}
