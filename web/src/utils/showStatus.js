/**
 * Shared presentation and intake-policy helpers for show lifecycle status
 * (`active`, `paused`, `ended`) across singer and kiosk surfaces.
 */

export function normalizeShowStatus(status) {
    if (status === 'active' || status === 'paused' || status === 'ended') {
        return status
    }
    return null
}

export function getShowStatusInfo(eventOrStatus) {
    const rawStatus = typeof eventOrStatus === 'string'
        ? eventOrStatus
        : eventOrStatus?.status
    const status = normalizeShowStatus(rawStatus)

    if (status === 'paused') {
        return {
            status: 'paused',
            isActive: false,
            isPaused: true,
            isEnded: false,
            canRequest: false,
            badgeLabel: 'Requests Paused',
            dotColor: 'var(--color-chorus)',
            dotGlow: false,
            bannerIcon: '⏸️',
            bannerTitle: 'Requests Paused',
            bannerMessage: 'The host has paused new song requests while working through the current queue. You can still browse and save favorites.',
            compactBannerMessage: 'Song requests are currently paused by the host.',
            requestBlockedMessage: 'Song requests are currently paused by the host.',
            requestButtonLabel: '⏸ Paused',
            submitButtonLabel: '⏸ Requests Paused',
            queueHeaderLabel: 'Stage Rotation • Requests Paused',
            queueFooterMessage: 'New requests are paused while the host runs the current queue.',
            emptyQueueTitle: 'Requests are currently paused',
            emptyQueueSubtitle: 'The host has paused new song requests for now. Check back when intake reopens.',
            joinToast: (name) => `Connected to ${name} (song requests are currently paused)`,
        }
    }

    if (status === 'ended') {
        return {
            status: 'ended',
            isActive: false,
            isPaused: false,
            isEnded: true,
            canRequest: false,
            badgeLabel: 'Show Ended',
            dotColor: 'var(--color-danger)',
            dotGlow: false,
            bannerIcon: '🏁',
            bannerTitle: 'Show Ended',
            bannerMessage: 'This show has ended and song requests are closed. You can still browse the songbook and manage your favorites.',
            compactBannerMessage: 'This show has ended and is no longer accepting song requests.',
            requestBlockedMessage: 'This show has ended and is no longer accepting song requests.',
            requestButtonLabel: 'Show Ended',
            submitButtonLabel: 'Show Ended',
            queueHeaderLabel: 'Show Ended',
            queueFooterMessage: 'Thanks for singing! This show has ended.',
            emptyQueueTitle: 'This show has ended',
            emptyQueueSubtitle: 'Song requests are closed for this show.',
            joinToast: (name) => `Loaded ${name} (this show has ended)`,
        }
    }

    if (status === 'active') {
        return {
            status: 'active',
            isActive: true,
            isPaused: false,
            isEnded: false,
            canRequest: true,
            badgeLabel: 'Live',
            dotColor: 'var(--color-success)',
            dotGlow: true,
            bannerIcon: '',
            bannerTitle: '',
            bannerMessage: '',
            compactBannerMessage: '',
            requestBlockedMessage: '',
            requestButtonLabel: '🎤 Request',
            submitButtonLabel: '🎤 Send Request',
            queueHeaderLabel: 'Live Stage Rotation',
            queueFooterMessage: 'Enjoying the show? Grab a drink and cheer on the singers!',
            emptyQueueTitle: 'The mic is wide open!',
            emptyQueueSubtitle: 'Search the catalog and request the first song of the night.',
            joinToast: (name) => `Connected to tonight's show: ${name}!`,
        }
    }

    return {
        status: null,
        isActive: false,
        isPaused: false,
        isEnded: false,
        canRequest: false,
        badgeLabel: '',
        dotColor: 'var(--color-text-dim)',
        dotGlow: false,
        bannerIcon: '',
        bannerTitle: '',
        bannerMessage: '',
        compactBannerMessage: '',
        requestBlockedMessage: '',
        requestButtonLabel: '🎤 Request',
        submitButtonLabel: '🎤 Send Request',
        queueHeaderLabel: 'Live Stage Rotation',
        queueFooterMessage: 'Enjoying the show? Grab a drink and cheer on the singers!',
        emptyQueueTitle: 'The mic is wide open!',
        emptyQueueSubtitle: 'Search the catalog and request the first song of the night.',
        joinToast: (name) => `Connected to tonight's show: ${name}!`,
    }
}

export function getStatusTransitionToast(prevStatus, nextStatus) {
    const prev = normalizeShowStatus(prevStatus)
    const next = normalizeShowStatus(nextStatus)
    if (!prev || !next || prev === next) return null

    if (next === 'paused') {
        return { message: '⏸️ The host has paused new song requests.', type: 'info' }
    }
    if (next === 'ended') {
        return { message: '🏁 This show has ended. Song requests are now closed.', type: 'info' }
    }
    if (next === 'active' && prev === 'paused') {
        return { message: '🎤 Song requests are open again!', type: 'success' }
    }
    if (next === 'active' && prev === 'ended') {
        return { message: '🎤 The show has reopened and is accepting requests!', type: 'success' }
    }
    return null
}
