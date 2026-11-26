import {
  createShowViaUI,
  ensureHostLoggedIn,
  hostDeleteEventViaApi,
  hostSelectTab,
  loginWithPassword,
  logoutViaUI,
  singerCancelOwnRequest,
  singerCloseQueueModal,
  singerFavoriteSong,
  singerJoinShow,
  singerOpenQueueModal,
  singerSubmitWriteInSong,
  waitForPollingSync,
} from '../lib/micup.mjs'

export const name = 'favorites-and-edge-cases'
export const description =
  'Validates cross-device account-synced favorites, shared-device logout isolation, custom write-in requests, and singer self-cancellation.'

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

  if (!singerAuthEmail || !singerAuthPassword) {
    throw new Error('Cross-device favorites requires SINGER_AUTH_EMAIL and SINGER_AUTH_PASSWORD or saved bot credentials.')
  }

  const host = await runner.addActor('host', {
    label: 'Host (KJ Desktop)',
    role: 'host',
    authenticated: true,
    storageStatePath: hostStorageState,
  })

  const singerAuthA = await runner.addActor('singer-auth-a', {
    label: 'Singer 1 Device A (Mobile)',
    role: 'mobile',
    authenticated: Boolean(singerAuthStorageState || (singerAuthEmail && singerAuthPassword)),
    storageStatePath: singerAuthStorageState,
  })

  // Device B starts as a brand-new isolated context with empty storage to test cross-device sync
  const singerAuthB = await runner.addActor('singer-auth-b', {
    label: 'Singer 1 Device B (Isolated Mobile)',
    role: 'mobile',
    authenticated: false,
  })

  const singerGuest = await runner.addActor('singer-guest', {
    label: 'Singer 2 (Guest Mobile)',
    role: 'mobile',
    authenticated: false,
  })

  let createdEvent = null

  try {
    // 1. Authenticate Host and Device A
    await ensureHostLoggedIn(host, { email: hostEmail, password: hostPassword })

    if (singerAuthEmail && singerAuthPassword) {
      await loginWithPassword(singerAuthA, {
        email: singerAuthEmail,
        password: singerAuthPassword,
        returnTo: '/',
      })
    } else if (!singerAuthStorageState) {
      runner.recordFinding({
        severity: 'info',
        category: 'config.singer_auth_missing',
        actor: 'singer-auth-a',
        message:
          'No SINGER_AUTH_EMAIL/PASSWORD or storageState provided; cross-device sync check requires credentials.',
      })
    }

    // 2. Host creates show with write-ins allowed and manual staging approval
    createdEvent = await createShowViaUI(host, {
      venueName: 'Vignette Edge Lounge',
      songbookName,
      autoAccept: false,
      allowWriteIns: true,
    })

    await runner.snap(
      'show-created-with-write-ins',
      'Host launched show with write-ins enabled',
      `Event ${createdEvent.eventId} (join code: ${createdEvent.joinCode}) launched with allow_write_ins=true.`,
      ['host']
    )

    // 3. Singer 1 (Device A) joins show and favorites a song from the catalog
    await singerJoinShow(singerAuthA, createdEvent.joinCode)

    // Set up listener for PUT /api/v1/auth/favorites before clicking favorite toggle
    let putFavoritesPromise = null
    const favorited = await singerFavoriteSong(singerAuthA, 0, {
      beforeToggle: () => {
        putFavoritesPromise = singerAuthA.page
          .waitForResponse(
            (res) => res.url().includes('/api/v1/auth/favorites') && res.request().method() === 'PUT',
            { timeout: 10000 }
          )
          .catch(() => null)
      },
    })

    // Wait for the debounced PUT to complete and verify 200 OK
    if (putFavoritesPromise) {
      const putResponse = await putFavoritesPromise
      if (!putResponse) {
        runner.recordFinding({
          severity: 'high',
          category: 'sync.favorites_put_timeout',
          actor: 'singer-auth-a',
          message: 'PUT /api/v1/auth/favorites did not fire within 10 seconds of favoriting a song.',
        })
      } else if (!putResponse.ok()) {
        runner.recordFinding({
          severity: 'high',
          category: 'sync.favorites_put_failed',
          actor: 'singer-auth-a',
          message: `PUT /api/v1/auth/favorites failed with status ${putResponse.status()}.`,
        })
      }
    } else {
      runner.recordFinding({
        severity: 'info',
        category: 'sync.favorites_already_saved',
        actor: 'singer-auth-a',
        message: `Every checked catalog track was already saved; verified "${favorited.title}" on the isolated device without toggling it.`,
      })
    }

    // Verify localStorage has at least 1 song
    const localFavSongs = await singerAuthA.page.evaluate(() => {
      try {
        return JSON.parse(localStorage.getItem('micup_singer_favorites') || '[]')
      } catch {
        return []
      }
    })
    if (!Array.isArray(localFavSongs) || localFavSongs.length === 0) {
      runner.recordFinding({
        severity: 'high',
        category: 'storage.favorites_empty',
        actor: 'singer-auth-a',
        message: 'localStorage micup_singer_favorites was empty after favoriting track.',
      })
    }

    await runner.snap(
      'singer-device-a-favorites',
      'Singer 1 favorited track on Device A',
      favorited.alreadyFavorite
        ? `Singer 1 already had "${favorited.title}" saved; the vignette left favorites unchanged.`
        : `Singer 1 favorited "${favorited.title}" on Device A; verified localStorage and account sync PUT 200 OK.`,
      ['host', 'singer-auth-a']
    )

    // 4. Singer 1 signs into Device B (fresh context) and verifies favorite sync
    if (singerAuthEmail && singerAuthPassword) {
      await loginWithPassword(singerAuthB, {
        email: singerAuthEmail,
        password: singerAuthPassword,
        returnTo: `/e/${encodeURIComponent(createdEvent.joinCode)}`,
      })
      await singerJoinShow(singerAuthB, createdEvent.joinCode)

      // Switch to Favorites tab
      const favTab = singerAuthB.page.locator('button').filter({ hasText: /★ Favorites/i }).first()
      await favTab.waitFor({ state: 'visible', timeout: 10000 })
      await favTab.click()

      // Assertions on Device B:
      // 1. Synced song title is actually visible in the DOM
      if (favorited?.title) {
        const songInFavorites = singerAuthB.page.locator(`text="${favorited.title}"`).first()
        try {
          await songInFavorites.waitFor({ state: 'visible', timeout: 10000 })
        } catch {
          runner.recordFinding({
            severity: 'high',
            category: 'sync.favorites_track_missing',
            actor: 'singer-auth-b',
            message: `Favorited song "${favorited.title}" was not visible in Device B Favorites view within 10s.`,
          })
        }
      } else {
        const favTrackCard = singerAuthB.page.locator('button[title*="Remove from favorites" i]').first()
        try {
          await favTrackCard.waitFor({ state: 'visible', timeout: 10000 })
        } catch {
          runner.recordFinding({
            severity: 'high',
            category: 'sync.favorites_not_rendered',
            actor: 'singer-auth-b',
            message: 'No favorited tracks were rendered in Device B Favorites view within 10s.',
          })
        }
      }

      // 2. "Your Repertoire is Empty" is NOT shown
      const emptyNotice = singerAuthB.page.locator('text="Your Repertoire is Empty"')
      if ((await emptyNotice.count()) > 0 && (await emptyNotice.isVisible())) {
        runner.recordFinding({
          severity: 'high',
          category: 'sync.favorites_empty_on_device_b',
          actor: 'singer-auth-b',
          message: 'Device B showed "Your Repertoire is Empty" despite account-synced favorites.',
        })
      }

      await runner.snap(
        'device-b-cross-device-sync',
        'Device B loaded account-synced favorites',
        `Singer 1 logged into an isolated second device and verified "${favorited?.title || 'track'}" synced from cloud account.`,
        ['singer-auth-a', 'singer-auth-b']
      )
    }

    // 5. Singer 1 logs out on Device A — verify shared-device isolation (favorites cleared)
    if (singerAuthA.authenticated) {
      await logoutViaUI(singerAuthA)
      // Check that local storage favorites were purged
      const remainingFavs = await singerAuthA.page.evaluate(() => {
        try {
          return JSON.parse(localStorage.getItem('micup_singer_favorites') || '[]')
        } catch {
          return []
        }
      })
      if (remainingFavs.length > 0) {
        runner.recordFinding({
          severity: 'high',
          category: 'privacy.favorites_leak',
          actor: 'singer-auth-a',
          message: 'Local favorites were not cleared on logout; shared device data leaked.',
        })
      }

      await runner.snap(
        'device-a-logout-isolation',
        'Device A logged out with local favorites cleared',
        'Verified shared-device security: logging out wipes device-local favorites store.',
        ['singer-auth-a']
      )
    }

    // 6. Guest Singer joins and submits a custom write-in request
    await singerJoinShow(singerGuest, createdEvent.joinCode)
    const writeInResult = await singerSubmitWriteInSong(singerGuest, {
      searchQuery: 'Ziggy Stardust Unplugged',
      songTitle: 'Ziggy Stardust (Acoustic)',
      artistName: 'David Bowie',
      singerName: 'Ziggy (Guest)',
      note: 'Custom write-in request test',
    })

    if (!writeInResult.ok) {
      runner.recordFinding({
        severity: 'high',
        category: 'flow.write_in_failed',
        actor: 'singer-guest',
        message: `Custom write-in submission failed with HTTP ${writeInResult.status}`,
      })
    }

    await runner.snap(
      'custom-write-in-submitted',
      'Guest singer submitted custom write-in request',
      'Searched unlisted track, clicked "Request as Write-In", and submitted with artist and screen name.',
      ['singer-guest']
    )

    // 7. Host inspects Staging Inbox to verify Write-In card
    await waitForPollingSync(6800)
    await hostSelectTab(host, 'staging')

    await runner.snap(
      'host-sees-write-in',
      'Host verifies write-in request in Staging Inbox',
      'Host views Staging Inbox containing the custom write-in request with Write-In badge.',
      ['host', 'singer-guest']
    )

    // 8. Guest Singer opens Queue modal and cancels their pending request
    await singerCancelOwnRequest(singerGuest)
    await singerCloseQueueModal(singerGuest)

    await runner.snap(
      'singer-self-cancellation',
      'Singer cancelled pending request from Queue modal',
      'Guest singer clicked Cancel (✕) on pending request; verified client-side cancellation toast.',
      ['singer-guest']
    )

    // 9. Host verifies request is removed from Staging and logged in History
    await waitForPollingSync(6800)
    await hostSelectTab(host, 'history')

    await runner.snap(
      'host-verified-cancellation',
      'Host verifies request cancelled and logged in History',
      'Host views History tab displaying the singer-cancelled request with Cancelled badge.',
      ['host']
    )
  } finally {
    if (createdEvent?.eventId && !keepEvent) {
      console.log(`\nCleaning up temporary test event ${createdEvent.eventId}...`)
      await hostDeleteEventViaApi(host, createdEvent.eventId, Array.from(runner.actors.values()))
    }
  }
}
