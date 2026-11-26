/**
 * Browser Notification & Screen WakeLock utility for live stage alerts.
 */

export const STAGE_ALERTS_KEY = 'micup_stage_alerts_enabled'

let wakeLockInstance = null
let swRegistrationPromise = null

/**
 * Registers the Service Worker for mobile notifications.
 */
export async function registerServiceWorker() {
    if (typeof navigator === 'undefined' || !('serviceWorker' in navigator)) {
        return null
    }
    if (!swRegistrationPromise) {
        swRegistrationPromise = navigator.serviceWorker
            .register('/sw.js')
            .then(reg => reg)
            .catch(err => {
                console.warn('Service worker registration failed:', err)
                return null
            })
    }
    return swRegistrationPromise
}

/**
 * Checks whether browser notification permission has been granted.
 */
export function isNotificationGranted() {
    return typeof window !== 'undefined' && 'Notification' in window && Notification.permission === 'granted'
}

/**
 * Checks whether stage alerts are enabled by the singer and supported by the browser.
 */
export function areStageAlertsEnabled() {
    if (!isNotificationGranted()) return false
    try {
        const pref = localStorage.getItem(STAGE_ALERTS_KEY)
        return pref !== 'false'
    } catch {
        return true
    }
}

/**
 * Updates the singer's preference for stage alerts.
 */
export function setStageAlertsEnabled(enabled) {
    if (typeof window === 'undefined') return
    try {
        localStorage.setItem(STAGE_ALERTS_KEY, enabled ? 'true' : 'false')
    } catch {}
}

/**
 * Requests notification permission from the user and initializes alert preference.
 */
export async function requestNotificationPermission() {
    if (typeof window === 'undefined' || !('Notification' in window)) {
        return 'unsupported'
    }
    if (Notification.permission === 'granted') {
        setStageAlertsEnabled(true)
        if ('serviceWorker' in navigator) {
            registerServiceWorker().catch(() => {})
        }
        return 'granted'
    }
    try {
        const res = await Notification.requestPermission()
        if (res === 'granted') {
            setStageAlertsEnabled(true)
            if ('serviceWorker' in navigator) {
                registerServiceWorker().catch(() => {})
            }
        }
        return res
    } catch {
        return Notification.permission
    }
}

/**
 * Dispatches a stage alert notification.
 * Uses ServiceWorkerRegistration.showNotification() for Android Chrome compatibility,
 * and falls back to new Notification() on desktop browsers.
 */
export async function sendSingerNotification(title, options = {}) {
    if (typeof window === 'undefined' || !('Notification' in window)) return null
    if (!areStageAlertsEnabled()) return null

    const notificationOptions = {
        icon: '/favicon.svg',
        badge: '/favicon.svg',
        vibrate: [300, 100, 300, 100, 500],
        requireInteraction: true,
        ...options
    }

    // Modern mobile browsers (e.g. Chrome on Android) require ServiceWorkerRegistration.showNotification()
    if (typeof navigator !== 'undefined' && 'serviceWorker' in navigator) {
        try {
            const reg = await navigator.serviceWorker.ready
            if (reg && typeof reg.showNotification === 'function') {
                await reg.showNotification(title, notificationOptions)
                return true
            }
        } catch {}
    }

    // Fallback to desktop window Notification constructor
    try {
        const notif = new Notification(title, notificationOptions)
        notif.onclick = () => {
            window.focus()
            notif.close()
        }
        return notif
    } catch {
        return null
    }
}

export async function requestScreenWakeLock() {
    if (typeof navigator === 'undefined' || !('wakeLock' in navigator)) return null
    try {
        if (!wakeLockInstance) {
            wakeLockInstance = await navigator.wakeLock.request('screen')
            wakeLockInstance.addEventListener('release', () => {
                wakeLockInstance = null
            })
        }
        return wakeLockInstance
    } catch {
        return null
    }
}

export function releaseScreenWakeLock() {
    try {
        if (wakeLockInstance) {
            wakeLockInstance.release()
            wakeLockInstance = null
        }
    } catch {}
}
