const PENDING_INVITE_KEY = 'micup_pending_invitation_return'

/**
 * Returns normalized effective permissions for a songbook.
 * Legacy songbook payloads without a `permissions` object default to owner permissions.
 */
export function getSongbookPermissions(songbook) {
    if (!songbook) {
        return {
            isOwner: false,
            canEditCatalog: false,
            canHostShows: false,
            canManageBook: false,
            canManageAccess: false,
        }
    }
    const p = songbook.permissions
    if (!p) {
        return {
            isOwner: true,
            canEditCatalog: true,
            canHostShows: true,
            canManageBook: true,
            canManageAccess: true,
        }
    }
    return {
        isOwner: !!p.is_owner,
        canEditCatalog: !!p.can_edit_catalog,
        canHostShows: !!p.can_host_shows,
        canManageBook: !!p.can_manage_book,
        canManageAccess: !!p.can_manage_access,
    }
}

/** Filters songbooks to those the caller can currently use to start or rebind a show. */
export function filterHostableSongbooks(songbooks = []) {
    return songbooks.filter(sb => getSongbookPermissions(sb).canHostShows)
}

const PENDING_INVITE_TTL_MS = 24 * 60 * 60 * 1000

export function savePendingInvitationReturn(path) {
    if (typeof window === 'undefined' || !path || !path.startsWith('/invitations/accept')) return
    try {
        window.localStorage.setItem(PENDING_INVITE_KEY, JSON.stringify({
            path,
            expiresAt: Date.now() + PENDING_INVITE_TTL_MS,
        }))
    } catch {
        // Ignore storage errors in restricted contexts
    }
}

export function peekPendingInvitationReturn() {
    if (typeof window === 'undefined') return ''
    try {
        const raw = window.localStorage.getItem(PENDING_INVITE_KEY)
        if (!raw) return ''
        const parsed = JSON.parse(raw)
        if (!parsed || typeof parsed.path !== 'string' || !parsed.path.startsWith('/invitations/accept')) {
            window.localStorage.removeItem(PENDING_INVITE_KEY)
            return ''
        }
        if (typeof parsed.expiresAt === 'number' && parsed.expiresAt <= Date.now()) {
            window.localStorage.removeItem(PENDING_INVITE_KEY)
            return ''
        }
        return parsed.path
    } catch {
        return ''
    }
}

export function clearPendingInvitationReturn() {
    if (typeof window === 'undefined') return
    try {
        window.localStorage.removeItem(PENDING_INVITE_KEY)
    } catch {
        // Ignore storage errors
    }
}
