import { useState, useEffect } from 'preact/hooks'
import { user, isLoggedIn, confirmEmailVerification, requestEmailVerification } from '../auth/store'
import { Spinner } from '../components/Spinner'
import { showToast } from '../components/Toast'
import { peekPendingInvitationReturn } from '../utils/songbookPermissions.js'

export function VerifyEmail() {
    const [status, setStatus] = useState('verifying') // 'verifying', 'success', 'error', 'prompt'
    const [errorMessage, setErrorMessage] = useState('')
    const [resending, setResending] = useState(false)

    useEffect(() => {
        const searchParams = new URLSearchParams(window.location.search)
        const token = searchParams.get('token')

        if (!token) {
            setStatus('prompt')
            return
        }

        async function verify() {
            try {
                await confirmEmailVerification(token)
                setStatus('success')
                showToast('Email verified successfully! 🎉')
            } catch (err) {
                setStatus('error')
                setErrorMessage(err.message || 'The verification link is invalid or has expired.')
            }
        }

        // Errors are caught and reflected in component state inside verify().
        void verify()
    }, [])

    const handleResend = async () => {
        setResending(true)
        try {
            await requestEmailVerification()
            showToast('New verification email sent! Please check your inbox. ✉️')
        } catch (err) {
            showToast(err.message || 'Failed to send verification email.', 'error')
        } finally {
            setResending(false)
        }
    }

    const currentUser = user.value
    const pendingInviteReturn = peekPendingInvitationReturn()

    return (
        <div style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            minHeight: 'calc(100vh - 80px)',
            padding: 'var(--space-6) var(--space-4)',
        }}>
            <div style={{
                background: 'var(--color-surface)',
                border: '1px solid var(--color-border)',
                borderRadius: 'var(--radius-xl)',
                width: '100%',
                maxWidth: '480px',
                padding: 'var(--space-8)',
                textAlign: 'center',
                boxShadow: '0 20px 40px rgba(0, 0, 0, 0.6), 0 0 30px rgba(99, 102, 241, 0.1)',
            }}>
                {status === 'verifying' && (
                    <div>
                        <div style={{ marginBottom: 'var(--space-4)' }}>
                            <Spinner size="40px" />
                        </div>
                        <h2 style={{ fontSize: 'var(--text-xl)', fontWeight: 600, color: 'var(--color-text)', marginBottom: 'var(--space-2)' }}>
                            Verifying Email Address
                        </h2>
                        <p style={{ fontSize: 'var(--text-sm)', color: 'var(--color-text-muted)' }}>
                            Confirming your cryptographic token with MicUp...
                        </p>
                    </div>
                )}

                {status === 'success' && (
                    <div>
                        <div style={{
                            width: '64px',
                            height: '64px',
                            borderRadius: 'var(--radius-full)',
                            background: 'rgba(34, 197, 94, 0.15)',
                            border: '1px solid rgba(34, 197, 94, 0.4)',
                            display: 'flex',
                            alignItems: 'center',
                            justifyContent: 'center',
                            fontSize: '2rem',
                            margin: '0 auto var(--space-4)',
                        }}>
                            ✅
                        </div>
                        <h2 style={{ fontSize: 'var(--text-xl)', fontWeight: 700, color: '#4ade80', marginBottom: 'var(--space-2)' }}>
                            Email Verified!
                        </h2>
                        <p style={{ fontSize: 'var(--text-sm)', color: 'var(--color-text-muted)', lineHeight: 1.5, marginBottom: 'var(--space-6)' }}>
                            Your email address has been successfully verified. You have full access to your MicUp account and notifications.
                        </p>
                        <a
                            href={pendingInviteReturn || (currentUser?.is_host ? '/kj/songbooks' : '/')}
                            class="btn btn-primary"
                            style={{ display: 'inline-block', width: '100%', textAlign: 'center', padding: 'var(--space-3)' }}
                        >
                            {pendingInviteReturn
                                ? 'Return to Songbook Invitation'
                                : (currentUser?.is_host ? 'Go to KJ Dashboard' : 'Explore Karaoke Shows')}
                        </a>
                    </div>
                )}

                {status === 'error' && (
                    <div>
                        <div style={{
                            width: '64px',
                            height: '64px',
                            borderRadius: 'var(--radius-full)',
                            background: 'rgba(239, 68, 68, 0.15)',
                            border: '1px solid rgba(239, 68, 68, 0.4)',
                            display: 'flex',
                            alignItems: 'center',
                            justifyContent: 'center',
                            fontSize: '2rem',
                            margin: '0 auto var(--space-4)',
                        }}>
                            ⚠️
                        </div>
                        <h2 style={{ fontSize: 'var(--text-xl)', fontWeight: 700, color: '#f87171', marginBottom: 'var(--space-2)' }}>
                            Verification Link Expired
                        </h2>
                        <p style={{ fontSize: 'var(--text-sm)', color: 'var(--color-text-muted)', lineHeight: 1.5, marginBottom: 'var(--space-6)' }}>
                            {errorMessage || 'This verification link is invalid, altered, or has expired after 24 hours.'}
                        </p>

                        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-3)' }}>
                            {isLoggedIn.value && (
                                <button
                                    id="btn-resend-verification"
                                    type="button"
                                    onClick={handleResend}
                                    disabled={resending}
                                    class="btn btn-primary"
                                    style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', gap: '8px' }}
                                >
                                    {resending && <Spinner size="14px" />}
                                    <span>{resending ? 'Sending...' : 'Resend Verification Email'}</span>
                                </button>
                            )}
                            <a
                                href="/"
                                class="btn btn-secondary"
                                style={{ display: 'inline-block', width: '100%', textAlign: 'center' }}
                            >
                                Back to MicUp
                            </a>
                        </div>
                    </div>
                )}

                {status === 'prompt' && (
                    <div>
                        <div style={{
                            width: '64px',
                            height: '64px',
                            borderRadius: 'var(--radius-full)',
                            background: 'rgba(99, 102, 241, 0.15)',
                            border: '1px solid rgba(99, 102, 241, 0.4)',
                            display: 'flex',
                            alignItems: 'center',
                            justifyContent: 'center',
                            fontSize: '2rem',
                            margin: '0 auto var(--space-4)',
                        }}>
                            ✉️
                        </div>
                        <h2 style={{ fontSize: 'var(--text-xl)', fontWeight: 600, color: 'var(--color-text)', marginBottom: 'var(--space-2)' }}>
                            Email Verification
                        </h2>
                        {isLoggedIn.value ? (
                            currentUser?.email_verified ? (
                                <div>
                                    <p style={{ fontSize: 'var(--text-sm)', color: 'var(--color-text-muted)', lineHeight: 1.5, marginBottom: 'var(--space-6)' }}>
                                        Your email address (<strong>{currentUser.email}</strong>) is already verified!
                                    </p>
                                    <a href="/" class="btn btn-primary" style={{ display: 'inline-block', width: '100%' }}>
                                        Return Home
                                    </a>
                                </div>
                            ) : (
                                <div>
                                    <p style={{ fontSize: 'var(--text-sm)', color: 'var(--color-text-muted)', lineHeight: 1.5, marginBottom: 'var(--space-6)' }}>
                                        Your email address (<strong>{currentUser.email}</strong>) is currently unverified. Click below to receive a secure verification link.
                                    </p>
                                    <button
                                        id="btn-send-verification"
                                        type="button"
                                        onClick={handleResend}
                                        disabled={resending}
                                        class="btn btn-primary"
                                        style={{ width: '100%', display: 'flex', alignItems: 'center', justifyContent: 'center', gap: '8px' }}
                                    >
                                        {resending && <Spinner size="14px" />}
                                        <span>{resending ? 'Sending...' : 'Send Verification Email'}</span>
                                    </button>
                                </div>
                            )
                        ) : (
                            <div>
                                <p style={{ fontSize: 'var(--text-sm)', color: 'var(--color-text-muted)', lineHeight: 1.5, marginBottom: 'var(--space-6)' }}>
                                    Please sign in to verify or manage your account email address.
                                </p>
                                <a href="/login" class="btn btn-primary" style={{ display: 'inline-block', width: '100%' }}>
                                    Sign In
                                </a>
                            </div>
                        )}
                    </div>
                )}
            </div>
        </div>
    )
}
