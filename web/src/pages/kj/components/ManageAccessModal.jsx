import { useEffect, useState } from 'preact/hooks'
import { api } from '../../../api/client.js'
import { ErrorBanner } from '../../../components/Banner.jsx'
import { Spinner } from '../../../components/Spinner.jsx'
import { showToast } from '../../../components/Toast.jsx'

export function ManageAccessModal({ isOpen, songbook, onClose }) {
    const [loading, setLoading] = useState(true)
    const [members, setMembers] = useState([])
    const [invitations, setInvitations] = useState([])
    const [email, setEmail] = useState('')
    const [canEditCatalog, setCanEditCatalog] = useState(false)
    const [canHostShows, setCanHostShows] = useState(true)
    const [submitting, setSubmitting] = useState(false)
    const [busyId, setBusyId] = useState('')
    const [error, setError] = useState('')
    const [confirmRemove, setConfirmRemove] = useState(null)

    const loadAccess = async () => {
        if (!songbook?.id) return
        setLoading(true)
        setError('')
        try {
            const data = await api.get(`/songbooks/${songbook.id}/access`)
            setMembers(data?.members || [])
            setInvitations(data?.invitations || [])
        } catch (err) {
            setError(err.message || 'Failed to load access settings')
        } finally {
            setLoading(false)
        }
    }

    useEffect(() => {
        if (isOpen && songbook?.id) {
            setConfirmRemove(null)
            loadAccess()
        }
    }, [isOpen, songbook?.id])

    if (!isOpen || !songbook) return null

    const handleInvite = async (e) => {
        e.preventDefault()
        if (!email.trim() || (!canEditCatalog && !canHostShows)) return
        setSubmitting(true)
        setError('')
        try {
            const res = await api.post(`/songbooks/${songbook.id}/invitations`, {
                email: email.trim(),
                can_edit_catalog: canEditCatalog,
                can_host_shows: canHostShows,
            })
            setEmail('')
            if (res?.delivery_failed) {
                showToast('Invitation saved as pending, but email delivery failed. You can retry below.', 'error')
            } else {
                showToast('Invitation sent')
            }
            await loadAccess()
        } catch (err) {
            setError(err.message || 'Failed to send invitation')
        } finally {
            setSubmitting(false)
        }
    }

    const handleResend = async (inv) => {
        setBusyId(inv.id)
        setError('')
        try {
            const res = await api.post(`/songbooks/${songbook.id}/invitations/${inv.id}/resend`, {})
            if (res?.delivery_failed) {
                showToast('New link created, but email delivery failed. Try again shortly.', 'error')
            } else {
                showToast(`Resent invitation to ${inv.recipient_email}`)
            }
            await loadAccess()
        } catch (err) {
            setError(err.message || 'Failed to resend invitation')
        } finally {
            setBusyId('')
        }
    }

    const handleCancelInvite = async (inv) => {
        setBusyId(inv.id)
        setError('')
        try {
            await api.delete(`/songbooks/${songbook.id}/invitations/${inv.id}`)
            showToast('Invitation canceled')
            await loadAccess()
        } catch (err) {
            setError(err.message || 'Failed to cancel invitation')
        } finally {
            setBusyId('')
        }
    }

    const handleToggleMemberPermission = async (member, field) => {
        const nextEdit = field === 'edit' ? !member.can_edit_catalog : member.can_edit_catalog
        const nextHost = field === 'host' ? !member.can_host_shows : member.can_host_shows
        if (!nextEdit && !nextHost) {
            setConfirmRemove(member)
            return
        }
        setBusyId(member.user_id)
        setError('')
        try {
            await api.patch(`/songbooks/${songbook.id}/members/${member.user_id}`, {
                can_edit_catalog: nextEdit,
                can_host_shows: nextHost,
            })
            showToast('Permissions updated')
            await loadAccess()
        } catch (err) {
            setError(err.message || 'Failed to update member permissions')
        } finally {
            setBusyId('')
        }
    }

    const handleConfirmRemove = async () => {
        if (!confirmRemove) return
        setBusyId(confirmRemove.user_id)
        setError('')
        try {
            await api.delete(`/songbooks/${songbook.id}/members/${confirmRemove.user_id}`)
            showToast(`Removed ${confirmRemove.display_name || confirmRemove.email}`)
            setConfirmRemove(null)
            await loadAccess()
        } catch (err) {
            setError(err.message || 'Failed to remove collaborator')
        } finally {
            setBusyId('')
        }
    }

    return (
        <div class="modal-overlay" onClick={onClose} role="dialog" aria-modal="true" aria-labelledby="manage-access-title">
            <div class="modal-sheet modal-sheet-wide" onClick={e => e.stopPropagation()}>
                <div class="modal-drag-handle" aria-hidden="true" />
                <div class="kj-modal-header">
                    <div style={{ minWidth: 0 }}>
                        <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-accent)', fontWeight: 'bold', textTransform: 'uppercase', letterSpacing: '0.08em' }}>
                            Manage Access
                        </div>
                        <h3 id="manage-access-title" style={{ margin: '2px 0 0', fontSize: 'var(--text-xl)' }}>
                            {songbook.name}
                        </h3>
                    </div>
                    <button type="button" onClick={onClose} aria-label="Close manage access modal" style={{ background: 'none', border: 'none', color: 'var(--color-text-dim)', fontSize: '1.25rem', cursor: 'pointer' }}>
                        ✕
                    </button>
                </div>

                <div class="kj-modal-body" style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-5)' }}>
                    {error && <ErrorBanner message={error} />}

                    <form onSubmit={handleInvite} style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-3)', padding: 'var(--space-4)', borderRadius: 'var(--radius-md)', background: 'var(--color-surface-alt)', border: '1px solid var(--color-border)' }}>
                        <div style={{ fontWeight: 700, fontSize: 'var(--text-sm)' }}>Invite a collaborator</div>
                        <input
                            type="email"
                            value={email}
                            onInput={e => setEmail(e.target.value)}
                            placeholder="Recipient email address"
                            aria-label="Recipient email address"
                            required
                        />
                        <label style={{ display: 'flex', alignItems: 'flex-start', gap: 'var(--space-2)', fontSize: 'var(--text-sm)', cursor: 'pointer' }}>
                            <input
                                type="checkbox"
                                checked={canEditCatalog}
                                onChange={e => setCanEditCatalog(e.target.checked)}
                            />
                            <span>
                                <strong>Edit catalog</strong>
                                <span style={{ display: 'block', fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)' }}>
                                    Read managed song data, add/edit/remove tracks, import and sync. Includes catalog-wide actions such as clear and replacement imports.
                                </span>
                            </span>
                        </label>
                        <label style={{ display: 'flex', alignItems: 'flex-start', gap: 'var(--space-2)', fontSize: 'var(--text-sm)', cursor: 'pointer' }}>
                            <input
                                type="checkbox"
                                checked={canHostShows}
                                onChange={e => setCanHostShows(e.target.checked)}
                            />
                            <span>
                                <strong>Host shows</strong>
                                <span style={{ display: 'block', fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)' }}>
                                    Start and run their own shows using this book, even when the book is private.
                                </span>
                            </span>
                        </label>
                        <div>
                            <button
                                type="submit"
                                class="btn btn-primary btn-sm"
                                disabled={submitting || !email.trim() || (!canEditCatalog && !canHostShows)}
                            >
                                {submitting ? 'Sending...' : 'Send invitation'}
                            </button>
                        </div>
                    </form>

                    {confirmRemove && (
                        <div role="alert" style={{ padding: 'var(--space-4)', borderRadius: 'var(--radius-md)', border: '1px solid rgba(239, 68, 68, 0.45)', background: 'rgba(239, 68, 68, 0.1)', display: 'flex', flexDirection: 'column', gap: 'var(--space-2)' }}>
                            <strong>Remove {confirmRemove.display_name || confirmRemove.email}?</strong>
                            <p style={{ margin: 0, fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', lineHeight: 1.5 }}>
                                Removing editing access stops new edits immediately (an import already accepted for processing can finish; incomplete uploads cannot submit). Removing hosting access prevents starting new shows, switching an event onto this book, or restarting an ended show with it; any already active or paused show can still finish, including resuming from pause.
                            </p>
                            <div style={{ display: 'flex', gap: 'var(--space-2)', marginTop: 'var(--space-2)' }}>
                                <button type="button" class="btn btn-danger btn-sm" onClick={handleConfirmRemove} disabled={busyId === confirmRemove.user_id}>
                                    {busyId === confirmRemove.user_id ? 'Removing...' : 'Confirm Remove'}
                                </button>
                                <button type="button" class="btn btn-secondary btn-sm" onClick={() => setConfirmRemove(null)}>
                                    Cancel
                                </button>
                            </div>
                        </div>
                    )}

                    {loading ? (
                        <Spinner label="Loading collaborators..." />
                    ) : (
                        <>
                            <div>
                                <div style={{ fontWeight: 700, fontSize: 'var(--text-sm)', marginBottom: 'var(--space-2)' }}>
                                    Accepted members ({members.length})
                                </div>
                                {members.length === 0 ? (
                                    <p style={{ margin: 0, fontSize: 'var(--text-sm)', color: 'var(--color-text-dim)' }}>
                                        Only you currently have access to this songbook.
                                    </p>
                                ) : (
                                    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-2)' }}>
                                        {members.map(m => (
                                            <div key={m.user_id} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: 'var(--space-2)', padding: 'var(--space-3)', borderRadius: 'var(--radius-md)', border: '1px solid var(--color-border)' }}>
                                                <div>
                                                    <div style={{ fontWeight: 600, fontSize: 'var(--text-sm)' }}>{m.display_name || m.email}</div>
                                                    {m.email && <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)' }}>{m.email}</div>}
                                                </div>
                                                <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-3)', flexWrap: 'wrap' }}>
                                                    <label style={{ display: 'inline-flex', alignItems: 'center', gap: '4px', fontSize: 'var(--text-xs)', cursor: 'pointer' }}>
                                                        <input type="checkbox" checked={m.can_edit_catalog} disabled={busyId === m.user_id} onChange={() => handleToggleMemberPermission(m, 'edit')} />
                                                        Edit catalog
                                                    </label>
                                                    <label style={{ display: 'inline-flex', alignItems: 'center', gap: '4px', fontSize: 'var(--text-xs)', cursor: 'pointer' }}>
                                                        <input type="checkbox" checked={m.can_host_shows} disabled={busyId === m.user_id} onChange={() => handleToggleMemberPermission(m, 'host')} />
                                                        Host shows
                                                    </label>
                                                    <button type="button" class="btn btn-secondary btn-sm" onClick={() => setConfirmRemove(m)} disabled={busyId === m.user_id}>
                                                        Remove
                                                    </button>
                                                </div>
                                            </div>
                                        ))}
                                    </div>
                                )}
                            </div>

                            <div>
                                <div style={{ fontWeight: 700, fontSize: 'var(--text-sm)', marginBottom: 'var(--space-2)' }}>
                                    Pending invitations ({invitations.length})
                                </div>
                                {invitations.length === 0 ? (
                                    <p style={{ margin: 0, fontSize: 'var(--text-sm)', color: 'var(--color-text-dim)' }}>
                                        No pending invitations.
                                    </p>
                                ) : (
                                    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-2)' }}>
                                        {invitations.map(inv => (
                                            <div key={inv.id} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: 'var(--space-2)', padding: 'var(--space-3)', borderRadius: 'var(--radius-md)', border: '1px solid var(--color-border)' }}>
                                                <div>
                                                    <div style={{ fontWeight: 600, fontSize: 'var(--text-sm)' }}>{inv.recipient_email}</div>
                                                    <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)' }}>
                                                        {[inv.can_edit_catalog && 'Edit catalog', inv.can_host_shows && 'Host shows'].filter(Boolean).join(' · ')}
                                                        {inv.expires_at ? ` · Expires ${new Date(inv.expires_at).toLocaleDateString()}` : ''}
                                                    </div>
                                                    {inv.send_error ? (
                                                        <div style={{ fontSize: 'var(--text-xs)', color: '#f87171', marginTop: '2px' }}>
                                                            Email delivery failed — click Retry to send a fresh link.
                                                        </div>
                                                    ) : !inv.last_sent_at ? (
                                                        <div style={{ fontSize: 'var(--text-xs)', color: '#f87171', marginTop: '2px' }}>
                                                            Email delivery was not confirmed — click Retry to send a fresh link.
                                                        </div>
                                                    ) : null}
                                                </div>
                                                <div style={{ display: 'flex', gap: 'var(--space-2)' }}>
                                                    <button type="button" class="btn btn-secondary btn-sm" onClick={() => handleResend(inv)} disabled={busyId === inv.id}>
                                                        {(inv.send_error || !inv.last_sent_at) ? 'Retry' : 'Resend'}
                                                    </button>
                                                    <button type="button" class="btn btn-secondary btn-sm" onClick={() => handleCancelInvite(inv)} disabled={busyId === inv.id}>
                                                        Cancel
                                                    </button>
                                                </div>
                                            </div>
                                        ))}
                                    </div>
                                )}
                            </div>
                        </>
                    )}
                </div>
            </div>
        </div>
    )
}
