import test from 'node:test'
import assert from 'node:assert/strict'
import {
    normalizeShowStatus,
    getShowStatusInfo,
    getStatusTransitionToast,
} from './showStatus.js'

test('normalizeShowStatus only accepts canonical show statuses', () => {
    assert.equal(normalizeShowStatus('active'), 'active')
    assert.equal(normalizeShowStatus('paused'), 'paused')
    assert.equal(normalizeShowStatus('ended'), 'ended')
    assert.equal(normalizeShowStatus(''), null)
    assert.equal(normalizeShowStatus(undefined), null)
    assert.equal(normalizeShowStatus('unknown'), null)
})

test('getShowStatusInfo provides consistent paused, ended, active, and unset metadata', () => {
    const active = getShowStatusInfo({ status: 'active' })
    assert.equal(active.isActive, true)
    assert.equal(active.isPaused, false)
    assert.equal(active.isEnded, false)
    assert.equal(active.canRequest, true)
    assert.equal(active.dotGlow, true)
    assert.match(active.emptyQueueTitle, /mic is wide open/i)

    const paused = getShowStatusInfo({ status: 'paused' })
    assert.equal(paused.isActive, false)
    assert.equal(paused.isPaused, true)
    assert.equal(paused.isEnded, false)
    assert.equal(paused.canRequest, false)
    assert.equal(paused.dotGlow, false)
    assert.equal(paused.badgeLabel, 'Requests Paused')
    assert.match(paused.requestBlockedMessage, /paused/i)
    assert.match(paused.emptyQueueTitle, /paused/i)
    assert.match(paused.joinToast('Friday Night'), /paused/i)

    const ended = getShowStatusInfo({ status: 'ended' })
    assert.equal(ended.isActive, false)
    assert.equal(ended.isPaused, false)
    assert.equal(ended.isEnded, true)
    assert.equal(ended.canRequest, false)
    assert.equal(ended.dotGlow, false)
    assert.equal(ended.badgeLabel, 'Show Ended')
    assert.match(ended.requestBlockedMessage, /ended/i)
    assert.match(ended.emptyQueueTitle, /ended/i)
    assert.match(ended.queueFooterMessage, /ended/i)
    assert.match(ended.joinToast('Friday Night'), /ended/i)

    const unset = getShowStatusInfo(null)
    assert.equal(unset.status, null)
    assert.equal(unset.canRequest, false)
    assert.equal(unset.isPaused, false)
    assert.equal(unset.isEnded, false)
})

test('getStatusTransitionToast announces pause, end, resume, and restart transitions only on change', () => {
    assert.equal(getStatusTransitionToast('active', 'active'), null)
    assert.equal(getStatusTransitionToast(null, 'paused'), null)

    const paused = getStatusTransitionToast('active', 'paused')
    assert.equal(paused.type, 'info')
    assert.match(paused.message, /paused/i)

    const ended = getStatusTransitionToast('paused', 'ended')
    assert.equal(ended.type, 'info')
    assert.match(ended.message, /ended/i)

    const resumed = getStatusTransitionToast('paused', 'active')
    assert.equal(resumed.type, 'success')
    assert.match(resumed.message, /open again/i)

    const restarted = getStatusTransitionToast('ended', 'active')
    assert.equal(restarted.type, 'success')
    assert.match(restarted.message, /reopened/i)
})
