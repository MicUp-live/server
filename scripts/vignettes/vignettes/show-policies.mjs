import assert from 'node:assert/strict'
import {
  createShowViaUI,
  ensureHostLoggedIn,
  hostActionOnSingerCard,
  hostDeleteEventViaApi,
  singerCloseQueueModal,
  singerJoinShow,
  singerOpenQueueModal,
  singerSubmitWriteInSong,
} from '../lib/micup.mjs'

export const name = 'show-policies'

async function assertQueueOrder(host, stage, eventId, expectedSingers, { firstUpNext = false } = {}) {
  await host.page.waitForFunction((names) => {
    const cards = Array.from(document.querySelectorAll('.kj-request-card'))
    return cards.length === names.length && cards.every((card, idx) =>
      Array.from(card.querySelectorAll('span')).some((span) => span.textContent.trim() === names[idx])
    )
  }, expectedSingers, { timeout: 15000 })

  const response = await stage.page.request.get(`/api/v1/events/${eventId}/queue`)
  assert.equal(response.status(), 200, 'Public queue is available')
  const body = await response.json()
  assert.deepEqual(body.queue.map((row) => row.singer_name), expectedSingers, 'Public queue order matches expected')
  if (firstUpNext) {
    assert.equal(body.queue[0]?.status, 'up_next', `${expectedSingers[0]} is called to stage`)
  } else if (body.queue.length > 0) {
    assert.notEqual(body.queue[0]?.status, 'up_next', `${expectedSingers[0]} is not up_next`)
  }
  return body
}

export async function run(runner, config) {
  const host = await runner.addActor('host', { label: 'KJ', role: 'host', storageStatePath: config.hostStorageState })
  const stage = await runner.addActor('stage', { label: 'Stage TV', role: 'stage' })
  const singerNames = ['Avery', 'Blake', 'Casey', 'Drew', 'Ellis']
  const singers = {}
  for (const singerName of singerNames) {
    singers[singerName] = await runner.addActor(singerName, { label: singerName, role: 'mobile' })
  }

  let show
  let songNumber = 0
  async function submit(singerName, titleOverride) {
    const singer = singers[singerName]
    await singerCloseQueueModal(singer)
    const result = await singerSubmitWriteInSong(singer, {
      songTitle: titleOverride || `Policy ${show.joinCode} Song ${++songNumber}`,
      artistName: 'Vignette Show Policies',
      singerName,
    })
    assert.equal(result.ok, true, `${singerName} submitted request (HTTP ${result.status})`)
    assert.equal(result.payload?.status, 'accepted', `${singerName} auto-accepted into rotation`)
    assert.ok(result.payload?.edit_token, `${singerName} received a domain-separated edit_token`)
  }

  try {
    await ensureHostLoggedIn(host, { email: config.hostEmail, password: config.hostPassword })
    show = await createShowViaUI(host, {
      venueName: 'Vignette Show Policies',
      songbookName: config.songbookName,
      rotationMode: 'fair_priority',
      autoAccept: true,
      allowWriteIns: true,
    })

    await Promise.all([
      stage.page.goto(`/stage/event/${show.eventId}`),
      singerJoinShow(singers.Avery, show.joinCode),
      ...Object.values(singers).slice(1).map((s) => s.page.goto(`/event/${show.eventId}`)),
    ])

    for (const singerName of singerNames) {
      await submit(singerName, singerName === 'Avery' ? 'Original Ballad' : undefined)
    }
    const initialQueue = await assertQueueOrder(host, stage, show.eventId, singerNames)
    assert.equal(initialQueue.allow_song_replacement, true, 'New shows default allow_song_replacement=true')

    // Beat 1: Avery swaps their song pre-call via the singer Queue modal while keeping #1 spot.
    await singerOpenQueueModal(singers.Avery)
    const swapBtn = singers.Avery.page.getByRole('button', { name: /Swap/i }).first()
    await swapBtn.waitFor({ state: 'visible', timeout: 15000 })
    await swapBtn.click()

    const replaceDialog = singers.Avery.page.locator('[aria-labelledby="replace-song-modal-title"]')
    await replaceDialog.waitFor({ state: 'visible', timeout: 10000 })
    await replaceDialog.locator('input[placeholder*="new song title"]').fill('Bohemian Rhapsody')
    await replaceDialog.locator('input[placeholder*="artist name"]').fill('Queen')

    const swapRespPromise = singers.Avery.page.waitForResponse((res) =>
      res.url().includes('/requests/') && res.url().endsWith('/song') && res.request().method() === 'PATCH'
    )
    await replaceDialog.getByRole('button', { name: /Save Song Change/i }).click()
    const swapResp = await swapRespPromise
    assert.equal(swapResp.status(), 200, `Pre-call song replacement succeeded (HTTP ${swapResp.status()})`)
    await replaceDialog.waitFor({ state: 'hidden', timeout: 10000 })

    // Confirm Avery is still #1 and the new title appears on Host and Singer views.
    await host.page.getByText('Bohemian Rhapsody').first().waitFor({ state: 'visible', timeout: 15000 })
    await singers.Avery.page.getByText('Bohemian Rhapsody').first().waitFor({ state: 'visible', timeout: 15000 })
    await assertQueueOrder(host, stage, show.eventId, singerNames)
    await runner.snap(
      'pre-call-song-swap',
      'Avery swapped song pre-call and kept #1 spot',
      'Bohemian Rhapsody replaced Original Ballad without changing rotation order.',
      ['host', 'stage', 'Avery']
    )

    // Beat 2: Host calls Avery to stage -> Swap button locks/disappears for Avery.
    await hostActionOnSingerCard(host, 'Avery', 'Call to Stage')
    await assertQueueOrder(host, stage, show.eventId, singerNames, { firstUpNext: true })
    await singers.Avery.page.getByText(/CALLED TO STAGE/i).first().waitFor({ state: 'visible', timeout: 15000 })
    await swapBtn.waitFor({ state: 'hidden', timeout: 10000 })
    await runner.snap(
      'called-to-stage-locks-swap',
      'Calling Avery to stage locks self-replacement',
      'Avery sees CALLED TO STAGE and can no longer self-swap the song.',
      ['host', 'stage', 'Avery']
    )

    // Beat 3: Host clicks Defer on absent Avery -> bumps down 3 spots (between Drew and Ellis),
    // returns status to accepted, and leaves Completed count at 0.
    const deferRespPromise = host.page.waitForResponse((res) =>
      res.url().includes('/requests/') && res.url().endsWith('/defer') && res.request().method() === 'POST'
    )
    await hostActionOnSingerCard(host, 'Avery', 'Defer')
    const deferResp = await deferRespPromise
    assert.equal(deferResp.status(), 200, `Host defer succeeded (HTTP ${deferResp.status()})`)

    const expectedAfterDefer = ['Blake', 'Casey', 'Drew', 'Avery', 'Ellis']
    const deferredQueue = await assertQueueOrder(host, stage, show.eventId, expectedAfterDefer, { firstUpNext: false })
    const averyEntry = deferredQueue.queue.find((row) => row.singer_name === 'Avery')
    assert.equal(averyEntry?.status, 'accepted', 'Deferred singer returned from up_next to accepted')
    assert.equal(averyEntry?.song_title, 'Bohemian Rhapsody', 'Deferred singer retained swapped song')

    // Confirm History tab has 0 completed/history items (defer did not mark completed).
    await host.page.getByRole('button', { name: /History \(0\)/i }).first().waitFor({ state: 'visible', timeout: 10000 })
    await singers.Avery.page.getByText(/#4 in line/).first().waitFor({ state: 'visible', timeout: 15000 })
    await swapBtn.waitFor({ state: 'visible', timeout: 10000 })
    await runner.snap(
      'absent-singer-deferred',
      'Absent Avery deferred down 3 spots without marking sung',
      'Queue order is now Blake, Casey, Drew, Avery, Ellis; History remains 0 and Avery is #4 in line.',
      ['host', 'stage', 'Avery']
    )

    // Beat 4: Host disables "Allow Pre-Call Song Changes" in Show Settings -> Swap button disappears for Avery.
    const settingsBtn = host.page
      .locator('.kj-qh-toolbar')
      .getByRole('button', { name: /Settings/i })
      .or(host.page.locator('button[title*="Queue rules" i]'))
      .first()
    await settingsBtn.waitFor({ state: 'visible', timeout: 10000 })
    await settingsBtn.click()
    const settingsDialog = host.page.locator('[aria-labelledby="settings-modal-title"]')
    await settingsDialog.waitFor({ state: 'visible', timeout: 10000 })
    const replacementCheckbox = settingsDialog.getByRole('checkbox', { name: /Allow Pre-Call Song Changes/i })
    assert.equal(await replacementCheckbox.isChecked(), true, 'Allow Pre-Call Song Changes starts checked')
    await replacementCheckbox.uncheck()

    const saveSettingsResp = host.page.waitForResponse((res) =>
      new URL(res.url()).pathname === `/api/v1/events/${show.eventId}` && res.request().method() === 'PATCH'
    )
    await settingsDialog.getByRole('button', { name: /Save/i }).click()
    const saved = await saveSettingsResp
    assert.equal(saved.ok(), true, `Host saved settings (HTTP ${saved.status()})`)
    await settingsDialog.waitFor({ state: 'hidden', timeout: 10000 })

    // Singer queue poll picks up allow_song_replacement=false and hides Swap.
    await swapBtn.waitFor({ state: 'hidden', timeout: 15000 })
    await runner.snap(
      'song-replacement-disabled',
      'Disabling song changes hides Swap for singers',
      'Host turned off Allow Pre-Call Song Changes; Swap button is hidden on Avery’s phone.',
      ['host', 'Avery']
    )
  } finally {
    if (show && !config.keepEvent) {
      await hostDeleteEventViaApi(host, show.eventId, Array.from(runner.actors.values()))
    }
  }
}
