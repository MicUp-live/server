import assert from 'node:assert/strict'
import {
  createShowViaUI,
  ensureHostLoggedIn,
  hostDeleteEventViaApi,
  hostEndShowViaUI,
  singerOpenEvent,
  singerOpenQueueModal,
  singerSubmitWriteInSong,
} from '../lib/micup.mjs'

export const name = 'host-reorder-and-reconnection'

async function assertQueue(stage, eventId, expected, expectOnStage = false) {
  const response = await stage.page.request.get(`/api/v1/events/${eventId}/queue`)
  assert.equal(response.status(), 200, 'Public queue is available')
  const body = await response.json()
  assert.deepEqual(body.queue.map((request) => request.singer_name), expected)
  if (expectOnStage) assert.equal(body.queue[0]?.status, 'up_next', `${expected[0]} remains called to stage`)
  return body.queue
}

export async function run(runner, config) {
  const host = await runner.addActor('host', {
    label: 'Host (KJ Desktop)',
    role: 'host',
    storageStatePath: config.hostStorageState,
  })
  const stage = await runner.addActor('stage', { label: 'Stage TV', role: 'stage' })
  const singers = {}
  for (const name of ['Avery', 'Blake', 'Casey', 'Drew']) {
    singers[name] = await runner.addActor(name, { label: name, role: 'mobile' })
  }

  let show
  let songNumber = 0
  let singerOffline = false

  try {
    await ensureHostLoggedIn(host, { email: config.hostEmail, password: config.hostPassword })
    show = await createShowViaUI(host, {
      venueName: 'Vignette Host Reorder',
      songbookName: config.songbookName,
      rotationMode: 'fcfs',
      autoAccept: true,
      allowWriteIns: true,
    })
    await Promise.all([
      stage.page.goto(`/stage/event/${show.eventId}`),
      ...Object.values(singers).map((singer) => singerOpenEvent(singer, show.eventId)),
    ])

    for (const [singerName, singer] of Object.entries(singers)) {
      const result = await singerSubmitWriteInSong(singer, {
        songTitle: `Vignette ${show.joinCode} Song ${++songNumber}`,
        artistName: 'Vignette Host Reorder',
        singerName,
      })
      assert.equal(result.status, 201, `${singerName} request is accepted`)
    }

    await host.page.waitForFunction(
      () => document.querySelectorAll('.kj-request-card').length === 4,
      null,
      { timeout: 15000 }
    )
    const firstQueue = await assertQueue(stage, show.eventId, ['Avery', 'Blake', 'Casey', 'Drew'])
    const callResponse = host.page.waitForResponse(
      (response) => response.url().endsWith(`/api/v1/events/${show.eventId}/requests/${firstQueue[0].id}`) &&
        response.request().method() === 'PATCH'
    )
    const averyCard = host.page.locator('.kj-request-card').filter({ hasText: 'Avery' })
    await averyCard.getByRole('button', { name: /Call to Stage/i }).click()
    const called = await callResponse
    assert.equal(called.status(), 200, 'Host can call the first singer to stage')
    assert.equal((await called.json()).status, 'up_next')
    await assertQueue(stage, show.eventId, ['Avery', 'Blake', 'Casey', 'Drew'], true)
    await singerOpenQueueModal(singers.Blake)
    await singers.Blake.page.getByText(/#2 in line/).first().waitFor({ state: 'visible' })

    singerOffline = true
    singers.Blake.expectedOffline = true
    await singers.Blake.context.setOffline(true)
    const reorderResponse = host.page.waitForResponse(
      (response) => response.url().endsWith(`/api/v1/events/${show.eventId}/requests/reorder`) &&
        response.request().method() === 'POST'
    )
    const caseyCard = host.page.locator('.kj-request-card').filter({ hasText: 'Casey' })
    await caseyCard.getByRole('button', { name: 'Move request up' }).click()
    const reordered = await reorderResponse
    assert.equal(reordered.status(), 200, 'Host reorder is committed')

    const expected = ['Avery', 'Casey', 'Blake', 'Drew']
    await assertQueue(stage, show.eventId, expected, true)
    await runner.snap(
      'host-reorders-around-stage-lock',
      'Host moves Casey ahead while Avery stays on stage',
      'The host reorder changes the active queue without displacing the called performer.',
      ['host', 'stage']
    )

    await singers.Blake.context.setOffline(false)
    singerOffline = false
    singers.Blake.expectedOffline = false
    await singers.Blake.page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')))
    await singers.Blake.page.getByText(/#3 in line/).first().waitFor({ state: 'visible', timeout: 15000 })
    await runner.snap(
      'singer-reconnects-to-updated-queue',
      'Singer reconnects and receives the latest queue position',
      'After network recovery, visibility-triggered full sync updates Blake from #2 to #3.',
      ['host', 'stage', 'Blake']
    )

    await hostEndShowViaUI(host)
    await singers.Blake.page.getByText('Show Ended', { exact: true }).first()
      .waitFor({ state: 'visible', timeout: 15000 })
    await runner.snap(
      'show-ended-for-singer',
      'The singer sees the show close',
      'The singer view reflects the ended show before the host reopens it.',
      ['host', 'Blake']
    )

    await host.page.getByRole('button', { name: /Restart Show$/ }).click()
    const restartDialog = host.page.getByRole('dialog', { name: /Restart/ })
    await restartDialog.getByRole('checkbox', { name: /Start with a fresh queue/ }).uncheck()
    const restartResponse = host.page.waitForResponse(
      (response) => response.url().endsWith(`/api/v1/events/${show.eventId}/restart`) &&
        response.request().method() === 'POST'
    )
    await restartDialog.getByRole('button', { name: /Restart Show Now/ }).click()
    const restarted = await restartResponse
    assert.equal(restarted.status(), 200, 'Host can restart the ended show')
    assert.equal((await restarted.json()).status, 'active')
    await assertQueue(stage, show.eventId, expected, true)
    await singers.Blake.page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')))
    await singers.Blake.page.getByText(/#3 in line/).first().waitFor({ state: 'visible', timeout: 15000 })
    await runner.snap(
      'show-restarted-with-queue',
      'Host reopens the show with its queue intact',
      'After restart without clearing, the called singer and queue order remain and Blake sees the restored position.',
      ['host', 'stage', 'Blake']
    )
  } finally {
    if (singerOffline) await singers.Blake.context.setOffline(false)
    if (show && !config.keepEvent) {
      await hostDeleteEventViaApi(host, show.eventId, Array.from(runner.actors.values()))
    }
  }
}
