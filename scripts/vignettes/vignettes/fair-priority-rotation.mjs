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

export const name = 'fair-priority-rotation'

async function assertQueue(host, stage, eventId, expected, onStage = false) {
  await host.page.waitForFunction((names) => {
    const cards = Array.from(document.querySelectorAll('.kj-request-card'))
    return cards.length === names.length && cards.every((card, index) =>
      Array.from(card.querySelectorAll('span')).some((span) => span.textContent.trim() === names[index])
    )
  }, expected, { timeout: 15000 })

  const response = await stage.page.request.get(`/api/v1/events/${eventId}/queue`)
  assert.equal(response.status(), 200, 'Public queue is available')
  const body = await response.json()
  assert.equal(body.rotation_mode, 'fair_priority')
  assert.equal(body.auto_accept, true)
  assert.deepEqual(body.queue.map((row) => row.singer_name), expected)
  assert.equal(Object.hasOwn(body, 'singer_weights'), false, 'Weights are private to the host')
  if (onStage) assert.equal(body.queue[0].status, 'up_next', `${expected[0]} is called to stage`)

  if (expected.length >= 3) {
    const visibleOnStage = expected.slice(0, 7)
    await stage.page.waitForFunction((names) => {
      const stageName = document.querySelector('.stage-grid h1')?.textContent.trim()
      const deckLabel = Array.from(document.querySelectorAll('.stage-grid div')).find((el) =>
        el.textContent.trim() === 'ON DECK (UP NEXT)'
      )
      const rotation = Array.from(document.querySelectorAll('.stage-up-next-card div'))
        .map((el) => el.textContent.trim())
        .filter((text) => names.includes(text))
      return stageName === names[0] && deckLabel?.nextElementSibling?.textContent.trim() === names[1] &&
        rotation.join('|') === names.slice(2).join('|')
    }, visibleOnStage, { timeout: 15000 })
  }
}

export async function run(runner, config) {
  const host = await runner.addActor('host', { label: 'KJ', role: 'host', storageStatePath: config.hostStorageState })
  const stage = await runner.addActor('stage', { label: 'Stage TV', role: 'stage' })
  const singers = {}
  for (const singerName of ['Avery', 'Blake', 'Casey', 'Drew', 'Ellis', 'Frankie', 'Harper', 'Gale']) {
    singers[singerName] = await runner.addActor(singerName, { label: singerName, role: 'mobile' })
  }

  let show
  let songNumber = 0
  async function submit(singerName) {
    const singer = singers[singerName]
    await singerCloseQueueModal(singer)
    const result = await singerSubmitWriteInSong(singer, {
      songTitle: `Vignette ${show.joinCode} Song ${++songNumber}`,
      artistName: 'Vignette Fair Rotation',
      singerName,
    })
    assert.equal(result.ok, true, `${singerName} submitted a request (HTTP ${result.status})`)
    assert.equal(result.payload?.status, 'accepted', `${singerName}'s request bypassed staging`)
  }

  try {
    await ensureHostLoggedIn(host, { email: config.hostEmail, password: config.hostPassword })
    show = await createShowViaUI(host, {
      venueName: 'Vignette Fair Rotation',
      songbookName: config.songbookName,
      rotationMode: 'fair_priority',
      autoAccept: true,
      allowWriteIns: true,
    })
    await Promise.all([
      stage.page.goto(`/stage/event/${show.eventId}`),
      singerJoinShow(singers.Avery, show.joinCode),
      // Direct event links give the other isolated browsers the same show without using the shared join quota.
      ...Object.values(singers).slice(1).map((singer) => singer.page.goto(`/event/${show.eventId}`)),
    ])

    for (const singerName of ['Avery', 'Blake']) {
      await submit(singerName)
      await hostActionOnSingerCard(host, singerName, 'Call to Stage')
      await assertQueue(host, stage, show.eventId, [singerName], true)
      await hostActionOnSingerCard(host, singerName, 'Done')
      await assertQueue(host, stage, show.eventId, [])
    }
    await runner.snap('first-turns-completed', 'Avery sang before Blake', 'Both first performances completed.', ['host', 'stage'])

    const protectedSingers = ['Casey', 'Drew', 'Ellis']
    for (const singerName of protectedSingers) await submit(singerName)
    await hostActionOnSingerCard(host, 'Casey', 'Call to Stage')
    await submit('Blake')
    await submit('Avery')
    await assertQueue(host, stage, show.eventId, [...protectedSingers, 'Avery', 'Blake'], true)
    await runner.snap('least-recently-sung', 'Avery moves ahead of Blake', 'Avery sang less recently and returns ahead of Blake, who requested first.', ['host', 'stage'])

    await submit('Frankie')
    await assertQueue(host, stage, show.eventId, [...protectedSingers, 'Frankie', 'Avery', 'Blake'], true)
    await singerOpenQueueModal(singers.Frankie)
    await singers.Frankie.page.getByText(/#4 in line/).first().waitFor({ state: 'visible', timeout: 15000 })
    await runner.snap('newcomer-placement', 'Frankie joins fourth', 'The newcomer goes ahead of repeat singers after the protected positions.', ['host', 'stage', 'Frankie'])

    await submit('Harper')
    await assertQueue(host, stage, show.eventId, [...protectedSingers, 'Frankie', 'Harper', 'Avery', 'Blake'], true)
    await runner.snap('newcomer-tie', 'Harper joins behind Frankie', 'Newcomers with equal priority keep their submission order.', ['host', 'stage'])

    await host.page.getByRole('button', { name: /^Weights$/ }).click()
    const weights = host.page.getByRole('dialog', { name: 'Private Singer Weights' })
    await weights.getByRole('button', { name: 'Increase new singer weight' }).click()
    await weights.getByText('+4', { exact: true }).waitFor({ state: 'visible' })
    const newSinger = weights.getByLabel('Add or pre-set a singer')
    await newSinger.fill('Gale')
    await newSinger.press('Enter')
    await weights.getByText('Gale', { exact: true }).waitFor({ state: 'visible' })
    const saveResponse = host.page.waitForResponse((response) =>
      new URL(response.url()).pathname === `/api/v1/events/${show.eventId}` &&
      response.request().method() === 'PATCH'
    )
    await weights.getByRole('button', { name: 'Save Weights' }).click()
    const saved = await saveResponse
    assert.deepEqual(saved.request().postDataJSON().singer_weights, { gale: 4 }, 'Host submitted the new weight')
    assert.equal(saved.ok(), true, `Host saved the weight (HTTP ${saved.status()})`)
    assert.deepEqual((await saved.json()).singer_weights, { gale: 4 }, 'Host response confirms the new weight')
    await weights.waitFor({ state: 'hidden' })

    await submit('Gale')
    await assertQueue(host, stage, show.eventId, [...protectedSingers, 'Gale', 'Frankie', 'Harper', 'Avery', 'Blake'], true)
    await singers.Frankie.page.getByText(/#5 in line/).first().waitFor({ state: 'visible', timeout: 15000 })
    await runner.snap('private-weight-placement', 'Gale moves ahead of Frankie', 'The host weight affects placement outside the protected positions and stays absent from the public queue.', ['host', 'stage', 'Frankie'])
  } finally {
    if (show && !config.keepEvent) {
      await hostDeleteEventViaApi(host, show.eventId, Array.from(runner.actors.values()))
    }
  }
}
