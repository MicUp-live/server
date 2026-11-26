import { useEffect, useState } from 'preact/hooks'
import { isLoggedIn, passwordLogin, register, loginWithPasskey, registerPasskey } from '../auth/store'
import { isPasskeySupported } from '../utils/webauthn'
import { setScreenName } from '../utils/screenNameStore'
import { route } from 'preact-router'
import { ErrorBanner } from '../components/Banner'

export function Login({ returnTo }) {
    const [email, setEmail] = useState('')
    const [password, setPassword] = useState('')
    const [displayName, setDisplayName] = useState('')
    const [isKjHost, setIsKjHost] = useState(() => {
        if (typeof window === 'undefined') return false
        return new URLSearchParams(window.location.search).get('isHost') === '1'
    })
    const [error, setError] = useState('')
    const [submitting, setSubmitting] = useState(false)
    const [passkeyLoading, setPasskeyLoading] = useState(false)
    const [mode, setMode] = useState(() => {
        if (typeof window === 'undefined') return 'signin'
        return new URLSearchParams(window.location.search).get('mode') === 'register' ? 'register' : 'signin'
    })  // 'signin' | 'register' | 'forgot'
    const [forgotSuccess, setForgotSuccess] = useState('')
    const supported = isPasskeySupported()

    const getTarget = () => {
        const queryTarget = typeof window !== 'undefined'
            ? new URLSearchParams(window.location.search).get('returnTo')
            : null
        const dest = queryTarget || returnTo || '/'
        return (dest.startsWith('/login') || dest === '') ? '/' : dest
    }

    useEffect(() => {
        if (isLoggedIn.value) route(getTarget(), true)
    }, [returnTo, isLoggedIn.value])

    if (isLoggedIn.value) return null

    const handlePasskeyLogin = async () => {
        setError('')
        setPasskeyLoading(true)
        try {
            // If email is provided, pass it to narrow credential; otherwise 1-tap discoverable login!
            await loginWithPasskey(email.trim())
            route(getTarget(), true)
        } catch (err) {
            setError(err.message || 'Passkey login failed')
        } finally {
            setPasskeyLoading(false)
        }
    }

    const handlePasskeyRegister = async () => {
        const cleanName = displayName.trim()
        const cleanEmail = email.trim()
        if (!cleanName) {
            setError('Name is required to sign up with a passkey')
            return
        }
        if (!cleanEmail || !cleanEmail.includes('@')) {
            setError('A valid email address is required to sign up with a passkey')
            return
        }
        setError('')
        setPasskeyLoading(true)
        try {
            await registerPasskey({
                displayName: cleanName,
                email: cleanEmail,
                isHost: isKjHost,
                friendlyName: `${cleanName}'s Device`,
            })
            setScreenName(cleanName)
            route(getTarget(), true)
        } catch (err) {
            setError(err.message || 'Passkey registration failed')
        } finally {
            setPasskeyLoading(false)
        }
    }

    const handlePasswordAuth = async (e) => {
        e.preventDefault()
        const cleanEmail = email.trim()
        const cleanName = displayName.trim()

        if (mode === 'register' && !cleanName) {
            setError('Name is required to create an account')
            return
        }
        if (!cleanEmail || !cleanEmail.includes('@')) {
            setError('A valid email address is required')
            return
        }
        if (!password) { setError('Password is required'); return }
        if (mode === 'register' && password.length < 8) {
            setError('Password must be at least 8 characters')
            return
        }
        setError('')
        setSubmitting(true)

        try {
            if (mode === 'register') {
                await register(cleanEmail, password, cleanName, isKjHost)
                setScreenName(cleanName)
            } else {
                await passwordLogin(cleanEmail, password)
            }
            route(getTarget(), true)
        } catch (err) {
            setError(err.message || 'Authentication failed')
        } finally {
            setSubmitting(false)
        }
    }

    const handleForgotPassword = async (e) => {
        e.preventDefault()
        const cleanEmail = email.trim()
        if (!cleanEmail || !cleanEmail.includes('@')) {
            setError('A valid email address is required')
            return
        }
        setError('')
        setSubmitting(true)
        try {
            const res = await api.post('/auth/forgot-password', { email: cleanEmail })
            setForgotSuccess(res?.message || 'Password reset link sent! Check your inbox.')
        } catch (err) {
            setError(err.message || 'Failed to send password reset email')
        } finally {
            setSubmitting(false)
        }
    }

    return (
        <div class="container-narrow" style={{ paddingTop: 'var(--space-12)' }}>
            <div class="card" style={{ maxWidth: '440px', margin: '0 auto', boxShadow: '0 20px 40px rgba(0,0,0,0.6), 0 0 30px rgba(99, 102, 241, 0.1)' }}>
                <h2 style={{ marginBottom: 'var(--space-2)', textAlign: 'center', display: 'flex', alignItems: 'center', justifyContent: 'center', gap: '8px' }}>
                    {mode === 'register' ? '📝 Create Account' : (mode === 'forgot' ? '🔑 Reset Password' : '👤 Sign In')}
                </h2>
                <p style={{
                    textAlign: 'center', color: 'var(--color-text-muted)',
                    fontSize: 'var(--text-sm)', marginBottom: 'var(--space-6)',
                }}>
                    {mode === 'register'
                        ? 'Create an account to host karaoke shows or save songbooks'
                        : (mode === 'forgot'
                            ? "Enter your email to receive a password reset link"
                            : 'Sign in with a biometric passkey or password')}
                </p>

                {error && <ErrorBanner message={error} />}
                {forgotSuccess && (
                    <div style={{
                        padding: 'var(--space-3) var(--space-4)',
                        borderRadius: 'var(--radius-md)',
                        background: 'rgba(16, 185, 129, 0.1)',
                        border: '1px solid rgba(16, 185, 129, 0.3)',
                        color: '#10b981',
                        fontSize: 'var(--text-sm)',
                        marginBottom: 'var(--space-4)',
                        textAlign: 'center',
                    }}>
                        {forgotSuccess}
                    </div>
                )}

                {mode === 'signin' ? (
                    <>
                        {/* 1-Tap Passkey Section for Sign In */}
                        {supported && (
                            <div style={{ marginBottom: 'var(--space-6)' }}>
                                <button
                                    type="button"
                                    id="btn-passkey-login"
                                    onClick={handlePasskeyLogin}
                                    disabled={passkeyLoading || submitting}
                                    class="btn btn-primary btn-lg btn-full"
                                    style={{
                                        display: 'flex',
                                        alignItems: 'center',
                                        justifyContent: 'center',
                                        gap: '10px',
                                        background: 'linear-gradient(135deg, #4f46e5 0%, #7c3aed 100%)',
                                        border: '1px solid rgba(255, 255, 255, 0.15)',
                                        boxShadow: '0 4px 15px rgba(79, 70, 229, 0.4)',
                                        fontWeight: 600,
                                    }}
                                >
                                    <span style={{ fontSize: '1.25rem' }}>🔑</span>
                                    <span>{passkeyLoading ? 'Prompting Biometrics...' : 'Sign In with Passkey'}</span>
                                </button>
                                <p style={{
                                    textAlign: 'center',
                                    fontSize: '0.75rem',
                                    color: 'var(--color-text-muted)',
                                    marginTop: 'var(--space-2)',
                                }}>
                                    Touch ID • Face ID • Windows Hello • Security Key
                                </p>

                                <div style={{
                                    display: 'flex',
                                    alignItems: 'center',
                                    margin: 'var(--space-5) 0 var(--space-4) 0',
                                    gap: 'var(--space-3)',
                                }}>
                                    <div style={{ flex: 1, height: '1px', background: 'var(--color-border)' }} />
                                    <span style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', textTransform: 'uppercase', letterSpacing: '0.05em' }}>
                                        or use password
                                    </span>
                                    <div style={{ flex: 1, height: '1px', background: 'var(--color-border)' }} />
                                </div>
                            </div>
                        )}

                        <form onSubmit={handlePasswordAuth} noValidate>
                            <label for="email" style={{
                                display: 'block', marginBottom: 'var(--space-2)',
                                color: 'var(--color-text-muted)', fontSize: 'var(--text-sm)',
                            }}>
                                Email
                            </label>
                            <input
                                type="email"
                                id="email"
                                autocomplete="email"
                                maxLength={320}
                                placeholder="you@example.com"
                                value={email}
                                onInput={(e) => setEmail(e.target.value)}
                                style={{ width: '100%', marginBottom: 'var(--space-4)' }}
                                autoFocus
                            />

                            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--space-2)' }}>
                                <label for="password" style={{
                                    margin: 0,
                                    color: 'var(--color-text-muted)', fontSize: 'var(--text-sm)',
                                }}>
                                    Password
                                </label>
                                <button
                                    type="button"
                                    onClick={() => { setMode('forgot'); setError(''); setForgotSuccess('') }}
                                    style={{
                                        background: 'none', border: 'none', cursor: 'pointer',
                                        color: 'var(--color-accent)', padding: 0, font: 'inherit',
                                        fontSize: 'var(--text-xs)', textDecoration: 'underline'
                                    }}
                                >
                                    Forgot password?
                                </button>
                            </div>
                            <input
                                type="password"
                                id="password"
                                autocomplete="current-password"
                                maxLength={128}
                                placeholder="Your password"
                                value={password}
                                onInput={(e) => setPassword(e.target.value)}
                                style={{ width: '100%', marginBottom: 'var(--space-6)' }}
                            />

                            <button
                                type="submit"
                                class="btn btn-secondary btn-lg btn-full"
                                disabled={submitting || passkeyLoading}
                            >
                                {submitting ? 'Signing in...' : 'Sign In with Password'}
                            </button>
                        </form>
                    </>
                ) : mode === 'register' ? (
                    /* Register Mode */
                    <form onSubmit={handlePasswordAuth} noValidate>
                        <label for="display-name" style={{
                            display: 'block', marginBottom: 'var(--space-2)',
                            color: 'var(--color-text-muted)', fontSize: 'var(--text-sm)',
                        }}>
                            Screen Name / Stage Name <span style={{ color: 'var(--color-accent)' }}>*</span>
                        </label>
                        <input
                            type="text"
                            id="display-name"
                            autocomplete="name"
                            maxLength={50}
                            placeholder="Your screen name (e.g. KJ Dave or Rockin Singer)"
                            value={displayName}
                            onInput={(e) => setDisplayName(e.target.value)}
                            style={{ width: '100%', marginBottom: 'var(--space-4)' }}
                            autoFocus
                            required
                        />

                        <label for="email" style={{
                            display: 'block', marginBottom: 'var(--space-2)',
                            color: 'var(--color-text-muted)', fontSize: 'var(--text-sm)',
                        }}>
                            Email Address <span style={{ color: 'var(--color-accent)' }}>*</span>
                        </label>
                        <input
                            type="email"
                            id="email"
                            autocomplete="email"
                            maxLength={320}
                            placeholder="you@example.com"
                            value={email}
                            onInput={(e) => setEmail(e.target.value)}
                            style={{ width: '100%', marginBottom: 'var(--space-4)' }}
                            required
                        />

                        <label style={{
                            display: 'flex',
                            alignItems: 'center',
                            gap: '8px',
                            marginBottom: 'var(--space-5)',
                            cursor: 'pointer',
                            fontSize: 'var(--text-sm)',
                            color: 'var(--color-text)',
                        }}>
                            <input
                                type="checkbox"
                                checked={isKjHost}
                                onChange={(e) => setIsKjHost(e.target.checked)}
                            />
                            <span>I am a Karaoke Host (KJ) creating shows</span>
                        </label>

                        {/* Primary action: Passkey sign up */}
                        {supported && (
                            <div style={{ marginBottom: 'var(--space-5)' }}>
                                <button
                                    type="button"
                                    id="btn-passkey-register"
                                    onClick={handlePasskeyRegister}
                                    disabled={passkeyLoading || submitting}
                                    class="btn btn-primary btn-lg btn-full"
                                    style={{
                                        display: 'flex',
                                        alignItems: 'center',
                                        justifyContent: 'center',
                                        gap: '10px',
                                        background: 'linear-gradient(135deg, #4f46e5 0%, #7c3aed 100%)',
                                        border: '1px solid rgba(255, 255, 255, 0.15)',
                                        boxShadow: '0 4px 15px rgba(79, 70, 229, 0.4)',
                                        fontWeight: 600,
                                    }}
                                >
                                    <span style={{ fontSize: '1.25rem' }}>🔑</span>
                                    <span>{passkeyLoading ? 'Creating Passkey...' : 'Sign Up with Passkey'}</span>
                                </button>
                                <p style={{
                                    textAlign: 'center',
                                    fontSize: '0.75rem',
                                    color: 'var(--color-text-muted)',
                                    marginTop: 'var(--space-2)',
                                }}>
                                    No password required • Instant biometrics with Touch ID / Face ID
                                </p>

                                <div style={{
                                    display: 'flex',
                                    alignItems: 'center',
                                    margin: 'var(--space-5) 0 var(--space-4) 0',
                                    gap: 'var(--space-3)',
                                }}>
                                    <div style={{ flex: 1, height: '1px', background: 'var(--color-border)' }} />
                                    <span style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', textTransform: 'uppercase', letterSpacing: '0.05em' }}>
                                        or create with password
                                    </span>
                                    <div style={{ flex: 1, height: '1px', background: 'var(--color-border)' }} />
                                </div>
                            </div>
                        )}

                        <label for="password" style={{
                            display: 'block', marginBottom: 'var(--space-2)',
                            color: 'var(--color-text-muted)', fontSize: 'var(--text-sm)',
                        }}>
                            Password
                        </label>
                        <input
                            type="password"
                            id="password"
                            autocomplete="new-password"
                            maxLength={128}
                            placeholder="At least 8 characters"
                            value={password}
                            onInput={(e) => setPassword(e.target.value)}
                            style={{ width: '100%', marginBottom: 'var(--space-6)' }}
                        />

                        <button
                            type="submit"
                            class="btn btn-secondary btn-lg btn-full"
                            disabled={submitting || passkeyLoading}
                        >
                            {submitting ? 'Creating account...' : 'Create Account with Password'}
                        </button>
                    </form>
                ) : (
                    /* Forgot Password Mode */
                    <form onSubmit={handleForgotPassword} noValidate>
                        <label for="email" style={{
                            display: 'block', marginBottom: 'var(--space-2)',
                            color: 'var(--color-text-muted)', fontSize: 'var(--text-sm)',
                        }}>
                            Account Email Address <span style={{ color: 'var(--color-accent)' }}>*</span>
                        </label>
                        <input
                            type="email"
                            id="email"
                            autocomplete="email"
                            maxLength={320}
                            placeholder="you@example.com"
                            value={email}
                            onInput={(e) => setEmail(e.target.value)}
                            style={{ width: '100%', marginBottom: 'var(--space-6)' }}
                            autoFocus
                            required
                        />

                        <button
                            type="submit"
                            class="btn btn-primary btn-lg btn-full"
                            disabled={submitting}
                        >
                            {submitting ? 'Sending link...' : 'Send Password Reset Link'}
                        </button>
                    </form>
                )}

                <p style={{
                    textAlign: 'center', marginTop: 'var(--space-5)',
                    fontSize: 'var(--text-sm)', color: 'var(--color-text-muted)',
                }}>
                    {mode === 'signin' ? (
                        <>Don't have an account?{' '}
                            <button onClick={() => { setMode('register'); setError(''); setForgotSuccess('') }}
                                style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--color-accent)', padding: 0, font: 'inherit', textDecoration: 'underline' }}>Register</button>
                        </>
                    ) : mode === 'register' ? (
                        <>Already have an account?{' '}
                            <button onClick={() => { setMode('signin'); setError(''); setForgotSuccess('') }}
                                style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--color-accent)', padding: 0, font: 'inherit', textDecoration: 'underline' }}>Sign In</button>
                        </>
                    ) : (
                        <>Remember your password?{' '}
                            <button onClick={() => { setMode('signin'); setError(''); setForgotSuccess('') }}
                                style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--color-accent)', padding: 0, font: 'inherit', textDecoration: 'underline' }}>Back to Sign In</button>
                        </>
                    )}
                </p>

            </div>
        </div>
    )
}
