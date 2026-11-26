const STORAGE_KEY = 'micup_singer_name'
const EVENT_NAME = 'micup_screen_name_changed'

/**
 * Gets the current active screen name for the singer/user.
 * Falls back to userObj display_name or localStorage.
 */
export function getScreenName(userObj) {
    if (userObj?.display_name) {
        return userObj.display_name
    }
    if (typeof window !== 'undefined') {
        return localStorage.getItem(STORAGE_KEY) || ''
    }
    return ''
}

/**
 * Sets the active screen name in localStorage and dispatches an event.
 */
export function setScreenName(name) {
    const trimmed = (name || '').trim()
    if (!trimmed) {
        throw new Error('Screen name cannot be empty')
    }

    if (typeof window !== 'undefined') {
        localStorage.setItem(STORAGE_KEY, trimmed)
        window.dispatchEvent(new CustomEvent(EVENT_NAME, { detail: trimmed }))
    }

    return trimmed
}

/**
 * Subscribe to screen name changes.
 */
export function onScreenNameChange(callback) {
    if (typeof window === 'undefined') return () => {}
    const handler = (e) => callback(e.detail)
    window.addEventListener(EVENT_NAME, handler)
    return () => window.removeEventListener(EVENT_NAME, handler)
}
