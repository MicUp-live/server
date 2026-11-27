import { useEffect, useState } from 'preact/hooks'
import { isLoggedIn, passwordLogin, register, loginWithPasskey, registerPasskey } from '../auth/store'
import { isPasskeySupported } from '../utils/webauthn'
import { setScreenName } from '../utils/screenNameStore'
import { route } from 'preact-router'
import { ErrorBanner } from '../components/Banner'

const MODE_COPY = {
    signin: {
        title: '👤 Sign In',
        subtitle: 'Sign in with a biometric passkey or password',
        footerPrompt: "Don't have an account?",
        footerActionLabel: 'Register',
        nextMode: 'register',
    },
    register: {
        title: '📝 Create Account',
        subtitle: 'Create an account to host karaoke shows or save songbooks',
        footerPrompt: 'Already have an account?',
        footerActionLabel: 'Sign In',
        nextMode: 'signin',
    },
    forgot: {
        title: '🔑 Reset Password',
        subtitle: 'Enter your email to receive a password reset link',
        footerPrompt: 'Remember your password?',
        footerActionLabel: 'Back to Sign In',
        nextMode: 'signin',
    },
}

const PASSKEY_BUTTON_STYLE = {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    gap: '10px',
    background: 'linear-gradient(135deg, #4f46e5 0%, #7c3aed 100%)',
    border: '1px solid rgba(255, 255, 255, 0.15)',
    boxShadow: '0 4px 15px rgba(79, 70, 229, 0.4)',
    fontWeight: 600,
}

const LINK_BUTTON_STYLE = {
    background: 'none',
    border: 'none',
    cursor: 'pointer',
    color: 'var(--color-accent)',
    padding: 0,
    font: 'inherit',
    textDecoration: 'underline',
}

function getInitialIsHost() {
    if (typeof window === 'undefined') return false
    return new URLSearchParams(window.location.search).get('isHost') === '1'
}

function getInitialMode() {
    if (typeof window === 'undefined') return 'signin'
    return new URLSearchParams(window.location.search).get('mode') === 'register' ? 'register' : 'signin'
}

function getRedirectTarget(returnTo) {
    const queryTarget = typeof window !== 'undefined'
        ? new URLSearchParams(window.location.search).get('returnTo')
        : null
    const dest = queryTarget || returnTo || '/'
    return (dest.startsWith('/login') || dest === '') ? '/' : dest
}

function validatePasswordAuth({ mode, cleanName, cleanEmail, password }) {
    if (mode === 'register' && !cleanName) return 'Name is required to create an account'
    if (!cleanEmail || !cleanEmail.includes('@')) return 'A valid email address is required'
    if (!password) return 'Password is required'
    if (mode === 'register' && password.length < 8) return 'Password must be at least 8 characters'
    return ''
}

async function submitPasskeyLogin({ email, returnTo, setError, setPasskeyLoading }) {
    setError('')
    setPasskeyLoading(true)
    try {
        // If email is provided, pass it to narrow credential; otherwise 1-tap discoverable login!
        await loginWithPasskey(email.trim())
        route(getRedirectTarget(returnTo), true)
    } catch (err) {
        setError(err.message || 'Passkey login failed')
    } finally {
        setPasskeyLoading(false)
    }
}

async function submitPasskeyRegister({ displayName, email, isKjHost, returnTo, setError, setPasskeyLoading }) {
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
        route(getRedirectTarget(returnTo), true)
    } catch (err) {
        setError(err.message || 'Passkey registration failed')
    } finally {
        setPasskeyLoading(false)
    }
}

async function submitPasswordAuth({ mode, email, password, displayName, isKjHost, returnTo, setError, setSubmitting }) {
    const cleanEmail = email.trim()
    const cleanName = displayName.trim()
    const validationError = validatePasswordAuth({ mode, cleanName, cleanEmail, password })
    if (validationError) {
        setError(validationError)
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
        route(getRedirectTarget(returnTo), true)
    } catch (err) {
        setError(err.message || 'Authentication failed')
    } finally {
        setSubmitting(false)
    }
}

async function submitForgotPassword({ email, setError, setSubmitting, setForgotSuccess }) {
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

function PasskeySection({ supported, buttonId, marginBottom, onClick, disabled, loading, loadingLabel, idleLabel, caption, dividerLabel }) {
    if (!supported) return null
    return (
        <div style={{ marginBottom }}>
            <button
                type="button"
                id={buttonId}
                onClick={onClick}
                disabled={disabled}
                class="btn btn-primary btn-lg btn-full"
                style={PASSKEY_BUTTON_STYLE}
            >
                <span style={{ fontSize: '1.25rem' }}>🔑</span>
                <span>{loading ? loadingLabel : idleLabel}</span>
            </button>
            <p style={{
                textAlign: 'center',
                fontSize: '0.75rem',
                color: 'var(--color-text-muted)',
                marginTop: 'var(--space-2)',
            }}>
                {caption}
            </p>

            <div style={{
                display: 'flex',
                alignItems: 'center',
                margin: 'var(--space-5) 0 var(--space-4) 0',
                gap: 'var(--space-3)',
            }}>
                <div style={{ flex: 1, height: '1px', background: 'var(--color-border)' }} />
                <span style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', textTransform: 'uppercase', letterSpacing: '0.05em' }}>
                    {dividerLabel}
                </span>
                <div style={{ flex: 1, height: '1px', background: 'var(--color-border)' }} />
            </div>
        </div>
    )
}

function SignInView({
    supported,
    email,
    password,
    passkeyLoading,
    submitting,
    onEmailInput,
    onPasswordInput,
    onPasskeyLogin,
    onForgotClick,
    onSubmit,
}) {
    const busy = passkeyLoading || submitting
    return (
        <>
            <PasskeySection
                supported={supported}
                buttonId="btn-passkey-login"
                marginBottom="var(--space-6)"
                onClick={onPasskeyLogin}
                disabled={busy}
                loading={passkeyLoading}
                loadingLabel="Prompting Biometrics..."
                idleLabel="Sign In with Passkey"
                caption="Touch ID • Face ID • Windows Hello • Security Key"
                dividerLabel="or use password"
            />

            <form onSubmit={onSubmit} noValidate>
                <label htmlFor="email" style={{
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
                    onInput={(e) => onEmailInput(e.target.value)}
                    style={{ width: '100%', marginBottom: 'var(--space-4)' }}
                />

                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--space-2)' }}>
                    <label htmlFor="password" style={{
                        margin: 0,
                        color: 'var(--color-text-muted)', fontSize: 'var(--text-sm)',
                    }}>
                        Password
                    </label>
                    <button
                        type="button"
                        onClick={onForgotClick}
                        style={{ ...LINK_BUTTON_STYLE, fontSize: 'var(--text-xs)' }}
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
                    onInput={(e) => onPasswordInput(e.target.value)}
                    style={{ width: '100%', marginBottom: 'var(--space-6)' }}
                />

                <button
                    type="submit"
                    class="btn btn-secondary btn-lg btn-full"
                    disabled={busy}
                >
                    {submitting ? 'Signing in...' : 'Sign In with Password'}
                </button>
            </form>
        </>
    )
}

function RegisterView({
    supported,
    displayName,
    email,
    password,
    isKjHost,
    passkeyLoading,
    submitting,
    onDisplayNameInput,
    onEmailInput,
    onPasswordInput,
    onIsKjHostChange,
    onPasskeyRegister,
    onSubmit,
}) {
    const busy = passkeyLoading || submitting
    return (
        <form onSubmit={onSubmit} noValidate>
            <label htmlFor="display-name" style={{
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
                onInput={(e) => onDisplayNameInput(e.target.value)}
                style={{ width: '100%', marginBottom: 'var(--space-4)' }}
                required
            />

            <label htmlFor="email" style={{
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
                onInput={(e) => onEmailInput(e.target.value)}
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
                    onChange={(e) => onIsKjHostChange(e.target.checked)}
                />
                <span>I am a Karaoke Host (KJ) creating shows</span>
            </label>

            <PasskeySection
                supported={supported}
                buttonId="btn-passkey-register"
                marginBottom="var(--space-5)"
                onClick={onPasskeyRegister}
                disabled={busy}
                loading={passkeyLoading}
                loadingLabel="Creating Passkey..."
                idleLabel="Sign Up with Passkey"
                caption="No password required • Instant biometrics with Touch ID / Face ID"
                dividerLabel="or create with password"
            />

            <label htmlFor="password" style={{
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
                onInput={(e) => onPasswordInput(e.target.value)}
                style={{ width: '100%', marginBottom: 'var(--space-6)' }}
            />

            <button
                type="submit"
                class="btn btn-secondary btn-lg btn-full"
                disabled={busy}
            >
                {submitting ? 'Creating account...' : 'Create Account with Password'}
            </button>
        </form>
    )
}

function ForgotPasswordView({ email, submitting, onEmailInput, onSubmit }) {
    return (
        <form onSubmit={onSubmit} noValidate>
            <label htmlFor="email" style={{
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
                onInput={(e) => onEmailInput(e.target.value)}
                style={{ width: '100%', marginBottom: 'var(--space-6)' }}
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
    )
}

export function Login({ returnTo }) {
    const [email, setEmail] = useState('')
    const [password, setPassword] = useState('')
    const [displayName, setDisplayName] = useState('')
    const [isKjHost, setIsKjHost] = useState(getInitialIsHost)
    const [error, setError] = useState('')
    const [submitting, setSubmitting] = useState(false)
    const [passkeyLoading, setPasskeyLoading] = useState(false)
    const [mode, setMode] = useState(getInitialMode)  // 'signin' | 'register' | 'forgot'
    const [forgotSuccess, setForgotSuccess] = useState('')
    const supported = isPasskeySupported()

    useEffect(() => {
        if (isLoggedIn.value) route(getRedirectTarget(returnTo), true)
    }, [returnTo, isLoggedIn.value])

    if (isLoggedIn.value) return null

    const switchMode = (nextMode) => {
        setMode(nextMode)
        setError('')
        setForgotSuccess('')
    }

    const handlePasskeyLogin = () => submitPasskeyLogin({ email, returnTo, setError, setPasskeyLoading })
    const handlePasskeyRegister = () => submitPasskeyRegister({
        displayName, email, isKjHost, returnTo, setError, setPasskeyLoading,
    })
    const handlePasswordAuth = (e) => {
        e.preventDefault()
        submitPasswordAuth({
            mode, email, password, displayName, isKjHost, returnTo, setError, setSubmitting,
        })
    }
    const handleForgotPassword = (e) => {
        e.preventDefault()
        submitForgotPassword({ email, setError, setSubmitting, setForgotSuccess })
    }

    const copy = MODE_COPY[mode] || MODE_COPY.signin
    let modeView
    if (mode === 'signin') {
        modeView = (
            <SignInView
                supported={supported}
                email={email}
                password={password}
                passkeyLoading={passkeyLoading}
                submitting={submitting}
                onEmailInput={setEmail}
                onPasswordInput={setPassword}
                onPasskeyLogin={handlePasskeyLogin}
                onForgotClick={() => switchMode('forgot')}
                onSubmit={handlePasswordAuth}
            />
        )
    } else if (mode === 'register') {
        modeView = (
            <RegisterView
                supported={supported}
                displayName={displayName}
                email={email}
                password={password}
                isKjHost={isKjHost}
                passkeyLoading={passkeyLoading}
                submitting={submitting}
                onDisplayNameInput={setDisplayName}
                onEmailInput={setEmail}
                onPasswordInput={setPassword}
                onIsKjHostChange={setIsKjHost}
                onPasskeyRegister={handlePasskeyRegister}
                onSubmit={handlePasswordAuth}
            />
        )
    } else {
        modeView = (
            <ForgotPasswordView
                email={email}
                submitting={submitting}
                onEmailInput={setEmail}
                onSubmit={handleForgotPassword}
            />
        )
    }

    return (
        <div class="container-narrow" style={{ paddingTop: 'var(--space-12)' }}>
            <div class="card" style={{ maxWidth: '440px', margin: '0 auto', boxShadow: '0 20px 40px rgba(0,0,0,0.6), 0 0 30px rgba(99, 102, 241, 0.1)' }}>
                <h2 style={{ marginBottom: 'var(--space-2)', textAlign: 'center', display: 'flex', alignItems: 'center', justifyContent: 'center', gap: '8px' }}>
                    {copy.title}
                </h2>
                <p style={{
                    textAlign: 'center', color: 'var(--color-text-muted)',
                    fontSize: 'var(--text-sm)', marginBottom: 'var(--space-6)',
                }}>
                    {copy.subtitle}
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

                {modeView}

                <p style={{
                    textAlign: 'center', marginTop: 'var(--space-5)',
                    fontSize: 'var(--text-sm)', color: 'var(--color-text-muted)',
                }}>
                    {copy.footerPrompt}{' '}
                    <button onClick={() => switchMode(copy.nextMode)} style={LINK_BUTTON_STYLE}>
                        {copy.footerActionLabel}
                    </button>
                </p>
            </div>
        </div>
    )
}
