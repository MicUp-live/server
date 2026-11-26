import assert from 'node:assert/strict'
import {
  createShowViaUI,
  ensureHostLoggedIn,
  hostDeleteEventViaApi,
  singerCloseQueueModal,
  singerOpenEvent,
  singerSubmitWriteInSong,
} from '../lib/micup.mjs'

export const name = 'round-robin-and-admission'

async function publicQueue(stage, eventId) {
  const response = await stage.page.request.get(`/api/v1/events/${eventId}/queue`)
  assert.equal(response.status(), 200, 'Public queue is available')
  return (await response.json()).queue
}

export async function run(runner, config) {
  const host = await runner.addActor('host', {
    label: 'Host (KJ Desktop)',
    role: 'host',
    storageStatePath: config.hostStorageState,
  })
  const stage = await runner.addActor('stage', { label: 'Stage TV', role: 'stage' })
  const singers = {
    Avery: await runner.addActor('Avery', { role: 'mobile' }),
    Blake: await runner.addActor('Blake', { role: 'mobile' }),
    CooldownProbe: await runner.addActor('cooldown-probe', { label: 'Fresh cooldown probe', role: 'mobile' }),
  }
  const shows = []
  let songNumber = 0

  async function submit(singerName, singer) {
    await singerCloseQueueModal(singer)
    const result = await singerSubmitWriteInSong(singer, {
      songTitle: `Vignette ${shows.at(-1).joinCode} Song ${++songNumber}`,
      artistName: 'Vignette Round Robin',
      singerName,
    })
    assert.equal(result.status, 201, `${singerName} request is accepted`)
  }

  try {
    await ensureHostLoggedIn(host, { email: config.hostEmail, password: config.hostPassword })
    let show = await createShowViaUI(host, {
      venueName: 'Vignette Round Robin',
      songbookName: config.songbookName,
      rotationMode: 'round_robin',
      autoAccept: true,
      allowWriteIns: true,
    })
    shows.push(show)
    await Promise.all([
      stage.page.goto(`/stage/event/${show.eventId}`),
      ...Object.values(singers).map((singer) => singerOpenEvent(singer, show.eventId)),
    ])

    await submit('Avery', singers.Avery)
    await submit('Blake', singers.Blake)
    assert.deepEqual(
      (await publicQueue(stage, show.eventId)).map(({ singer_name, round }) => [singer_name, round]),
      [['Avery', 1], ['Blake', 1]],
      'First submissions from each singer occupy round one in submission order'
    )

    await submit('Avery', singers.Avery)
    await submit('Blake', singers.Blake)
    assert.deepEqual(
      (await publicQueue(stage, show.eventId)).map(({ singer_name, round }) => [singer_name, round]),
      [['Avery', 1], ['Blake', 1], ['Avery', 2], ['Blake', 2]],
      'Repeat submissions are assigned to the next round without displacing round one'
    )
    await runner.snap(
      'round-two-reserve',
      'Repeat singers are placed into a second round',
      'Public queue preserves round-one order and appends each repeat singer to round two.',
      ['host', 'stage', 'Avery', 'Blake']
    )

    show = await createShowViaUI(host, {
      venueName: 'Vignette Admission Limit',
      songbookName: config.songbookName,
      autoAccept: true,
      allowWriteIns: true,
      maxActivePerSinger: 1,
    })
    shows.push(show)
    await Promise.all([
      stage.page.goto(`/stage/event/${show.eventId}`),
      singerOpenEvent(singers.Avery, show.eventId),
    ])
    await submit('Avery', singers.Avery)
    await singerCloseQueueModal(singers.Avery)
    const limited = await singerSubmitWriteInSong(singers.Avery, {
      songTitle: `Vignette ${show.joinCode} Limited`,
      artistName: 'Vignette Admission',
      singerName: 'Avery',
    })
    assert.equal(limited.status, 409)
    assert.equal(limited.payload?.error, 'max_active_limit')
    assert.equal((await publicQueue(stage, show.eventId)).length, 1, 'Rejected limit request is not admitted')
    await runner.snap(
      'active-request-limit',
      'Singer active-request limit is enforced',
      'With a one-request limit, a second active request is rejected without admission.',
      ['host', 'Avery']
    )

    show = await createShowViaUI(host, {
      venueName: 'Vignette Admission Cooldown',
      songbookName: config.songbookName,
      autoAccept: true,
      allowWriteIns: true,
      submissionDelay: 30,
    })
    shows.push(show)
    await Promise.all([
      stage.page.goto(`/stage/event/${show.eventId}`),
      singerOpenEvent(singers.Avery, show.eventId),
    ])
    await submit('Avery', singers.Avery)
    await singerOpenEvent(singers.CooldownProbe, show.eventId)
    const cooledDown = await singerSubmitWriteInSong(singers.CooldownProbe, {
      songTitle: `Vignette ${show.joinCode} Cooldown`,
      artistName: 'Vignette Admission',
      singerName: 'Avery',
    })
    assert.equal(cooledDown.status, 429)
    assert.equal(cooledDown.payload?.error, 'submission_cooldown')
    assert.ok(cooledDown.payload?.seconds_remaining > 0)
    assert.equal((await publicQueue(stage, show.eventId)).length, 1, 'Rejected cooldown request is not admitted')
    await runner.snap(
      'submission-cooldown',
      'Singer submission cooldown is enforced',
      'A second immediate request receives the configured cooldown response and remaining time.',
      ['host', 'cooldown-probe']
    )
  } finally {
    if (!config.keepEvent) {
      for (const show of shows.reverse()) {
        await hostDeleteEventViaApi(host, show.eventId, Array.from(runner.actors.values()))
      }
    }
  }
}
