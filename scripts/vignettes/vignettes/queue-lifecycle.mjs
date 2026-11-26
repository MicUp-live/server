import assert from 'node:assert/strict'
import {
  createShowViaUI,
  ensureHostLoggedIn,
  hostActionOnSingerCard,
  hostBroadcastAnnouncement,
  hostDeclineSingerCard,
  hostDeleteEventViaApi,
  hostEndShowViaUI,
  hostSelectTab,
  hostTogglePause,
  loginWithPassword,
  singerCloseQueueModal,
  singerJoinShow,
  singerOpenQueueModal,
  singerSubmitSong,
  waitForPollingSync,
} from '../lib/micup.mjs'

export const name = 'queue-lifecycle'
export const description =
  'Full show lifecycle with Desktop Host, TV Stage Display, 1 Logged-in Mobile Singer, and 2 Guest Mobile Singers.'

export async function run(runner, config) {
  const {
    hostEmail,
    hostPassword,
    hostStorageState,
    singerAuthEmail,
    singerAuthPassword,
    singerAuthStorageState,
    songbookName = '',
    keepEvent = false,
  } = config

  const host = await runner.addActor('host', {
    label: 'Host (KJ Desktop)',
    role: 'host',
    authenticated: true,
    storageStatePath: hostStorageState,
  })

  const singerAuth = await runner.addActor('singer-auth', {
    label: 'Singer 1 (Logged-In Mobile)',
    role: 'mobile',
    authenticated: Boolean(singerAuthStorageState || (singerAuthEmail && singerAuthPassword)),
    storageStatePath: singerAuthStorageState,
  })

  const singerGuest1 = await runner.addActor('singer-guest-1', {
    label: 'Singer 2 (Guest Mobile)',
    role: 'mobile',
    authenticated: false,
  })

  const singerGuest2 = await runner.addActor('singer-guest-2', {
    label: 'Singer 3 (Guest Mobile)',
    role: 'mobile',
    authenticated: false,
  })

  const stage = await runner.addActor('stage', {
    label: 'Stage TV Display',
    role: 'stage',
    authenticated: false,
  })

  let createdEvent = null

  try {
    // 1. Authenticate Host & Logged-in Singer
    await ensureHostLoggedIn(host, { email: hostEmail, password: hostPassword })

    if (singerAuthEmail && singerAuthPassword) {
      await loginWithPassword(singerAuth, {
        email: singerAuthEmail,
        password: singerAuthPassword,
        returnTo: '/',
      })
    } else if (!singerAuthStorageState) {
      runner.recordFinding({
        severity: 'info',
        category: 'config.singer_auth_missing',
        actor: 'singer-auth',
        message:
          'No SINGER_AUTH_EMAIL/PASSWORD or storageState provided; Singer 1 is running unauthenticated for this run.',
      })
    }

    // 2. Host creates temporary show (manual staging approval to test Staging Inbox)
    createdEvent = await createShowViaUI(host, {
      venueName: 'Vignette Test Lounge',
      songbookName,
      autoAccept: false,
    })

    await runner.snap(
      'show-created',
      'Host launched temporary show',
      `Event ${createdEvent.eventId} (join code: ${createdEvent.joinCode}) created with manual staging approval.`,
      ['host']
    )

    // 3. Stage display & all 3 Mobile Singers join the show
    await Promise.all([
      stage.page.goto(`/stage/event/${encodeURIComponent(createdEvent.eventId)}`, {
        waitUntil: 'domcontentloaded',
      }),
      singerJoinShow(singerAuth, createdEvent.joinCode),
      singerJoinShow(singerGuest1, createdEvent.joinCode),
      singerJoinShow(singerGuest2, createdEvent.joinCode),
    ])

    await runner.snap(
      'singers-joined',
      'Mobile singers & Stage TV joined show',
      'All 3 mobile singers loaded the public catalog via /e/<code>; Stage TV opened /stage/event/<id>.',
      ['host', 'stage', 'singer-auth', 'singer-guest-1', 'singer-guest-2']
    )

    // 4. Singers submit songs (spaced slightly to stay well under 12/min per-IP limit)
    const s1 = await singerSubmitSong(singerAuth, {
      searchQuery: 'Bohemian',
      singerName: 'Alex Member',
      songIndex: 0,
      note: 'Logged-in singer request',
    })
    if (singerAuth.authenticated && !s1.prefilledName) {
      runner.recordFinding({
        severity: 'medium',
        category: 'ux.prefill',
        actor: 'singer-auth',
        message: 'Logged-in singer screen name was NOT prefilled in SongRequestModal.',
      })
    }
    if (!s1.ok) {
      runner.recordFinding({
        severity: 'high',
        category: 'flow.submit_failed',
        actor: 'singer-auth',
        message: `Singer 1 song submission failed with HTTP ${s1.status}`,
      })
    }

    await new Promise((r) => setTimeout(r, 1200))
    const s2 = await singerSubmitSong(singerGuest1, {
      searchQuery: 'Jolene',
      singerName: 'Jordan (Guest)',
      songIndex: 0,
      keyShift: '+1',
      useQuickNote: '1st time',
    })
    if (!s2.ok) {
      runner.recordFinding({
        severity: 'high',
        category: 'flow.submit_failed',
        actor: 'singer-guest-1',
        message: `Singer 2 song submission failed with HTTP ${s2.status}`,
      })
    }
    assert.equal(s2.payload?.key_change, 1, 'Singer-selected pitch is persisted in the request')

    await new Promise((r) => setTimeout(r, 1200))
    const s3 = await singerSubmitSong(singerGuest2, {
      searchQuery: 'Africa',
      singerName: 'Casey (Guest)',
      songIndex: 0,
      note: 'Testing decline flow',
    })
    if (!s3.ok) {
      runner.recordFinding({
        severity: 'high',
        category: 'flow.submit_failed',
        actor: 'singer-guest-2',
        message: `Singer 3 song submission failed with HTTP ${s3.status}`,
      })
    }

    // Wait for Host queue poll to pick up incoming requests
    await waitForPollingSync(6800)
    await hostSelectTab(host, 'staging')

    // Open queue modal on Singer 1 to inspect how pending status appears on mobile
    await singerOpenQueueModal(singerAuth).catch((err) => {
      runner.recordFinding({
        severity: 'medium',
        category: 'ux.queue_modal',
        actor: 'singer-auth',
        message: `Could not open Singer Queue modal: ${err.message}`,
      })
    })

    await runner.snap(
      'requests-in-staging',
      '3 requests arrived in Host Staging Inbox',
      'Host views Staging tab; Singer 1 has Queue modal open; Singers 2 & 3 view catalog header status.',
      ['host', 'singer-auth', 'singer-guest-1', 'singer-guest-2']
    )

    await singerCloseQueueModal(singerAuth)

    // 5. Host triages Staging: Accept Singer 1 & Singer 2, Decline Singer 3 with reason
    const s1Label = s1.prefilledName || 'Alex (Member)'
    await hostActionOnSingerCard(host, s1Label, 'Accept')
    await hostActionOnSingerCard(host, 'Jordan (Guest)', 'Accept')
    await hostDeclineSingerCard(host, 'Casey (Guest)', 'Already sung recently — pick another!')

    await hostSelectTab(host, 'active')
    await waitForPollingSync(6800)

    // Open queue modal on declined guest singer to see what feedback they get
    await singerOpenQueueModal(singerGuest2).catch(() => {})

    await runner.snap(
      'after-triage',
      'Host accepted 2 singers & declined 1 with reason',
      'Host views Active Queue; Singer 3 (declined) opens Queue modal to check decline visibility.',
      ['host', 'stage', 'singer-auth', 'singer-guest-1', 'singer-guest-2']
    )

    await singerCloseQueueModal(singerGuest2)

    // 6. Host calls Singer 1 to stage
    await hostActionOnSingerCard(host, s1Label, 'Call to Stage')
    await waitForPollingSync(6800)

    await runner.snap(
      'called-to-stage',
      'Singer 1 Called to Stage, Singer 2 On Deck',
      'Verify Singer 1 sees CALLED TO STAGE alert, Singer 2 sees On Deck / #2, and Stage TV updates.',
      ['host', 'stage', 'singer-auth', 'singer-guest-1']
    )

    // 7. Host broadcasts announcement
    await hostBroadcastAnnouncement(host, 'Last call for karaoke requests in 15 minutes!')
    await waitForPollingSync(6800)

    await runner.snap(
      'announcement-live',
      'Host broadcast live announcement',
      'Verify announcement banner renders cleanly without overflow on mobile and Stage TV.',
      ['host', 'stage', 'singer-auth', 'singer-guest-1']
    )

    // 8. Host pauses the show
    await hostTogglePause(host, 'pause')
    await waitForPollingSync(6800)

    await runner.snap(
      'show-paused',
      'Host paused incoming requests',
      'Verify mobile singers see paused state and request buttons are clearly disabled.',
      ['host', 'stage', 'singer-guest-1', 'singer-guest-2']
    )

    // 9. Host resumes, marks Singer 1 Done, calls Singer 2 to stage
    await hostTogglePause(host, 'resume')
    await hostActionOnSingerCard(host, s1Label, 'Done')
    await hostActionOnSingerCard(host, 'Jordan (Guest)', 'Call to Stage')
    await waitForPollingSync(6800)

    await runner.snap(
      'rotation-advanced',
      'Singer 1 completed, Singer 2 Called to Stage',
      'Verify rotation advanced cleanly on Host, Stage TV, and both singers.',
      ['host', 'stage', 'singer-auth', 'singer-guest-1']
    )

    // Check History tab on Host
    await hostSelectTab(host, 'history')
    await runner.snap(
      'host-history',
      'Host History tab (completed + declined)',
      'Verify completed and declined requests appear accurately in History.',
      ['host']
    )

    // 10. Host ends the show
    await hostEndShowViaUI(host)
    await waitForPollingSync(6800)

    await runner.snap(
      'show-ended',
      'Host ended the show',
      'Verify ended-show UX on Host, Stage TV, and mobile singers.',
      ['host', 'stage', 'singer-auth', 'singer-guest-1']
    )
  } finally {
    if (createdEvent?.eventId && !keepEvent) {
      console.log(`\nCleaning up temporary test event ${createdEvent.eventId}...`)
      await hostDeleteEventViaApi(host, createdEvent.eventId, Array.from(runner.actors.values()))
    }
  }
}
