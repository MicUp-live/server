import { test } from 'node:test'
import assert from 'node:assert'
import {
    requestNotificationPermission,
    isNotificationGranted,
    areStageAlertsEnabled,
    setStageAlertsEnabled,
    sendSingerNotification,
    requestScreenWakeLock,
    releaseScreenWakeLock,
    registerServiceWorker,
} from './notifications.js'

test('notifications utility runs safely when browser APIs are undefined', async () => {
    assert.strictEqual(isNotificationGranted(), false)
    assert.strictEqual(areStageAlertsEnabled(), false)
    const perm = await requestNotificationPermission()
    assert.strictEqual(perm, 'unsupported')
    const notif = await sendSingerNotification('Test')
    assert.strictEqual(notif, null)
    const sw = await registerServiceWorker()
    assert.strictEqual(sw, null)
    const lock = await requestScreenWakeLock()
    assert.strictEqual(lock, null)
    assert.doesNotThrow(() => releaseScreenWakeLock())
    assert.doesNotThrow(() => setStageAlertsEnabled(false))
})
