import { useEffect, useState } from 'preact/hooks'
import { route } from 'preact-router'
import { api } from '../api/client.js'
import { user, isLoggedIn, logout, requestEmailVerification } from '../auth/store.js'
import { Spinner } from '../components/Spinner.jsx'
import { ErrorBanner } from '../components/Banner.jsx'
import { showToast } from '../components/Toast.jsx'
import { savePendingInvitationReturn, clearPendingInvitationReturn } from '../utils/songbookPermissions.js'

export function AcceptInvitation() {
    const [token, setToken] = useState('')
    const [preview, setPreview] = useState(null)
    const [loading, setLoading] = useState(true)
    const [error, setError] = useState('')
    const [accepting, setAccepting] = useState(false)
    const [resendingVerify, setResendingVerify] = useState(false)

    const currentUser = user.value
    const loggedIn = isLoggedIn.value

    useEffect(() => {
        const params = new URLSearchParams(window.location.search)
        const raw = (params.get('token') || '').trim()
        setToken(raw)
        if (!raw) {
            setError('Missing invitation token.')
            setLoading(false)
            return
        }
        const returnPath = `/invitations/accept?token=${encodeURIComponent(raw)}`
        savePendingInvitationReturn(returnPath)

        let cancelled = false
        async function loadPreview() {
            setLoading(true)
            setError('')
            try {
                const data = await api.get(`/songbook-invitations/preview?token=${encodeURIComponent(raw)}`)
                if (!cancelled) setPreview(data)
            } catch (err) {
                if (!cancelled) {
                    setPreview(null)
                    setError(err.message || 'This invitation link is invalid, canceled, or has already been used.')
                }
            } finally {
                if (!cancelled) setLoading(false)
            }
        }
        loadPreview()
        return () => { cancelled = true }
    }, [loggedIn, currentUser?.id, currentUser?.email_verified])

    const handleAccept = async () => {
        setAccepting(true)
        setError('')
        try {
            await api.post('/songbook-invitations/accept', { token })
            clearPendingInvitationReturn()
            showToast(`Joined "${preview?.songbook_name || 'songbook'}"!`)
            route('/kj/songbooks', true)
        } catch (err) {
            setError(err.message || 'Unable to accept invitation')
        } finally {
            setAccepting(false)
        }
    }

    const handleResendVerification = async () => {
        setResendingVerify(true)
        try {
            await requestEmailVerification()
            showToast('Verification email sent! Check your inbox and return here after verifying.')
        } catch (err) {
            showToast(err.message || 'Failed to send verification email', 'error')
        } finally {
            setResendingVerify(false)
        }
    }

    const handleSwitchAccount = async () => {
        await logout()
        const returnPath = `/invitations/accept?token=${encodeURIComponent(token)}`
        route(`/login?returnTo=${encodeURIComponent(returnPath)}`, true)
    }

    const returnPath = token ? `/invitations/accept?token=${encodeURIComponent(token)}` : '/kj/songbooks'
    const encodedReturn = encodeURIComponent(returnPath)

    return (
        <div class="container-narrow" style={{ paddingTop: 'var(--space-10)', paddingBottom: 'var(--space-12)' }}>
            <div class="card" style={{ maxWidth: '520px', margin: '0 auto', padding: 'var(--space-6)' }}>
                <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-accent)', fontWeight: 'bold', textTransform: 'uppercase', letterSpacing: '0.08em', marginBottom: '4px' }}>
                    Songbook Collaboration
                </div>
                <h2 style={{ marginTop: 0, marginBottom: 'var(--space-3)' }}>Songbook Invitation</h2>

                {loading && <Spinner label="Checking invitation..." />}
                {!loading && error && <ErrorBanner message={error} />}

                {!loading && preview && (
                    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-4)' }}>
                        <div style={{ padding: 'var(--space-4)', borderRadius: 'var(--radius-md)', background: 'var(--color-surface-alt)', border: '1px solid var(--color-border)' }}>
                            <div style={{ fontSize: 'var(--text-lg)', fontWeight: 700, marginBottom: '4px' }}>
                                {preview.songbook_name}
                            </div>
                            <div style={{ fontSize: 'var(--text-sm)', color: 'var(--color-text-muted)', marginBottom: 'var(--space-3)' }}>
                                Invited by <strong>{preview.inviter_name}</strong> for <strong>{preview.recipient_email}</strong>
                            </div>
                            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--space-2)' }}>
                                {preview.can_edit_catalog && (
                                    <span style={{ padding: '3px 10px', borderRadius: 'var(--radius-sm)', background: 'rgba(99, 102, 241, 0.15)', border: '1px solid rgba(99, 102, 241, 0.35)', fontSize: 'var(--text-xs)', fontWeight: 600 }}>
                                        Edit catalog
                                    </span>
                                )}
                                {preview.can_host_shows && (
                                    <span style={{ padding: '3px 10px', borderRadius: 'var(--radius-sm)', background: 'rgba(34, 197, 94, 0.15)', border: '1px solid rgba(34, 197, 94, 0.35)', fontSize: 'var(--text-xs)', fontWeight: 600 }}>
                                        Host shows
                                    </span>
                                )}
                            </div>
                            {preview.expires_at && (
                                <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-dim)', marginTop: 'var(--space-3)' }}>
                                    {preview.expired
                                        ? 'This invitation has expired.'
                                        : `Expires ${new Date(preview.expires_at).toLocaleString()}`}
                                </div>
                            )}
                        </div>

                        {preview.expired || preview.status !== 'pending' ? (
                            <ErrorBanner message={preview.expired ? 'This invitation has expired. Ask the songbook owner to send a new one.' : 'This invitation is no longer active.'} />
                        ) : !loggedIn ? (
                            <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-3)' }}>
                                <p style={{ margin: 0, fontSize: 'var(--text-sm)', color: 'var(--color-text-muted)' }}>
                                    Sign in or create a MicUp host account using <strong>{preview.recipient_email}</strong> to accept this invitation.
                                </p>
                                <a class="btn btn-primary" href={`/login?returnTo=${encodedReturn}`} style={{ textAlign: 'center' }}>
                                    Sign In to Accept
                                </a>
                                <a class="btn btn-secondary" href={`/login?mode=register&isHost=1&returnTo=${encodedReturn}`} style={{ textAlign: 'center' }}>
                                    Create Host Account
                                </a>
                            </div>
                        ) : !preview.email_matches ? (
                            <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-3)' }}>
                                <ErrorBanner message={`You are signed in as ${currentUser?.email}, but this invitation was sent to ${preview.recipient_email}.`} />
                                <button type="button" class="btn btn-secondary" onClick={handleSwitchAccount}>
                                    Sign Out &amp; Switch Account
                                </button>
                            </div>
                        ) : !preview.is_host ? (
                            <ErrorBanner message="Your account is not enabled as a karaoke host. Update your profile to enable host features before accepting." />
                        ) : !preview.email_verified ? (
                            <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-3)' }}>
                                <ErrorBanner message="Please verify your email address before accepting this invitation. Once verified, you can return right back here." />
                                <button type="button" class="btn btn-primary" onClick={handleResendVerification} disabled={resendingVerify}>
                                    {resendingVerify ? 'Sending...' : 'Resend Verification Email'}
                                </button>
                            </div>
                        ) : (
                            <button type="button" class="btn btn-primary" onClick={handleAccept} disabled={accepting}>
                                {accepting ? 'Accepting...' : 'Accept Invitation'}
                            </button>
                        )}
                    </div>
                )}
            </div>
        </div>
    )
}
