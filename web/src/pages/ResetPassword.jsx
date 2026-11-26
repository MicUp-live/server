import { useState } from 'preact/hooks'
import { route } from 'preact-router'
import { api } from '../api/client'
import { ErrorBanner } from '../components/Banner'

export function ResetPassword() {
    const [token, setToken] = useState(() => {
        if (typeof window === 'undefined') return ''
        return new URLSearchParams(window.location.search).get('token') || ''
    })
    const [password, setPassword] = useState('')
    const [confirmPassword, setConfirmPassword] = useState('')
    const [submitting, setSubmitting] = useState(false)
    const [error, setError] = useState('')
    const [success, setSuccess] = useState(false)

    const handleSubmit = async (e) => {
        e.preventDefault()
        setError('')

        if (!token.trim()) {
            setError('Missing or invalid password reset token.')
            return
        }
        if (password.length < 8) {
            setError('Password must be at least 8 characters long.')
            return
        }
        if (password !== confirmPassword) {
            setError('Passwords do not match.')
            return
        }

        setSubmitting(true)
        try {
            await api.post('/auth/reset-password', {
                token: token.trim(),
                password: password,
            })
            setSuccess(true)
        } catch (err) {
            setError(err.message || 'Failed to reset password. The link may have expired.')
        } finally {
            setSubmitting(false)
        }
    }

    return (
        <div class="container-narrow" style={{ paddingTop: 'var(--space-12)' }}>
            <div class="card" style={{ maxWidth: '440px', margin: '0 auto', boxShadow: '0 20px 40px rgba(0,0,0,0.6), 0 0 30px rgba(99, 102, 241, 0.1)' }}>
                <h2 style={{ marginBottom: 'var(--space-2)', textAlign: 'center' }}>
                    🔑 Set New Password
                </h2>
                <p style={{
                    textAlign: 'center', color: 'var(--color-text-muted)',
                    fontSize: 'var(--text-sm)', marginBottom: 'var(--space-6)',
                }}>
                    Choose a secure new password for your MicUp account.
                </p>

                {error && <ErrorBanner message={error} style={{ marginBottom: 'var(--space-4)' }} />}

                {success ? (
                    <div style={{ textAlign: 'center', padding: 'var(--space-4) 0' }}>
                        <div style={{ fontSize: '3rem', marginBottom: 'var(--space-2)' }}>🎉</div>
                        <h3 style={{ margin: '0 0 var(--space-2) 0', color: '#10b981' }}>Password Updated!</h3>
                        <p style={{ color: 'var(--color-text-muted)', fontSize: 'var(--text-sm)', marginBottom: 'var(--space-6)' }}>
                            Your password has been reset successfully. You can now sign in with your new credentials.
                        </p>
                        <button
                            type="button"
                            class="btn btn-primary btn-lg btn-full"
                            onClick={() => route('/login')}
                        >
                            Sign In Now
                        </button>
                    </div>
                ) : (
                    <form onSubmit={handleSubmit}>
                        {!token && (
                            <div style={{ marginBottom: 'var(--space-4)' }}>
                                <label for="reset-token" style={{
                                    display: 'block', marginBottom: 'var(--space-2)',
                                    color: 'var(--color-text-muted)', fontSize: 'var(--text-sm)',
                                }}>
                                    Reset Token
                                </label>
                                <input
                                    type="text"
                                    id="reset-token"
                                    value={token}
                                    onInput={(e) => setToken(e.target.value)}
                                    placeholder="Paste token from email"
                                    style={{ width: '100%' }}
                                    required
                                />
                            </div>
                        )}

                        <label for="new-password" style={{
                            display: 'block', marginBottom: 'var(--space-2)',
                            color: 'var(--color-text-muted)', fontSize: 'var(--text-sm)',
                        }}>
                            New Password
                        </label>
                        <input
                            type="password"
                            id="new-password"
                            autocomplete="new-password"
                            maxLength={128}
                            placeholder="At least 8 characters"
                            value={password}
                            onInput={(e) => setPassword(e.target.value)}
                            style={{ width: '100%', marginBottom: 'var(--space-4)' }}
                            required
                            autoFocus
                        />

                        <label for="confirm-password" style={{
                            display: 'block', marginBottom: 'var(--space-2)',
                            color: 'var(--color-text-muted)', fontSize: 'var(--text-sm)',
                        }}>
                            Confirm New Password
                        </label>
                        <input
                            type="password"
                            id="confirm-password"
                            autocomplete="new-password"
                            maxLength={128}
                            placeholder="Re-enter your new password"
                            value={confirmPassword}
                            onInput={(e) => setConfirmPassword(e.target.value)}
                            style={{ width: '100%', marginBottom: 'var(--space-6)' }}
                            required
                        />

                        <button
                            type="submit"
                            class="btn btn-primary btn-lg btn-full"
                            disabled={submitting}
                        >
                            {submitting ? 'Resetting password...' : 'Reset Password'}
                        </button>

                        <p style={{
                            textAlign: 'center', marginTop: 'var(--space-5)',
                            fontSize: 'var(--text-sm)', color: 'var(--color-text-muted)',
                        }}>
                            <a href="/login" style={{ color: 'var(--color-accent)', textDecoration: 'underline' }}>
                                Back to Sign In
                            </a>
                        </p>
                    </form>
                )}
            </div>
        </div>
    )
}
