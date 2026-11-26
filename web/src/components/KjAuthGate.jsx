/**
 * KjAuthGate — wraps KJ pages with an auth + host check.
 *
 * If the user is not logged in, redirects to /login with a returnTo parameter.
 * If logged in but not a host, shows a message with a link to enable host mode.
 * Once authenticated and host-enabled, renders children.
 *
 * Usage:
 *   <KjAuthGate>
 *     <YourKjPageContent />
 *   </KjAuthGate>
 */

import { useState } from 'preact/hooks'
import { isLoggedIn, isHost, toggleHostMode } from '../auth/store'
import { route } from 'preact-router'
import { Spinner } from './Spinner'
import { showToast } from './Toast'

export function KjAuthGate({ children }) {
    const [enabling, setEnabling] = useState(false)

    if (!isLoggedIn.value) {
        // Redirect to login with return URL
        const returnTo = encodeURIComponent(window.location.pathname)
        setTimeout(() => route(`/login?returnTo=${returnTo}`, true), 0)

        return <Spinner label="Redirecting to sign in..." />
    }

    if (!isHost.value) {
        return (
            <div class="container-narrow" style={{ paddingTop: 'var(--space-12)' }}>
                <div class="card" style={{ textAlign: 'center' }}>
                    <div style={{ fontSize: '2.5rem', marginBottom: 'var(--space-2)' }}>🎤</div>
                    <h2 style={{ marginBottom: 'var(--space-4)' }}>
                        Host Mode Not Enabled
                    </h2>
                    <p style={{
                        color: 'var(--color-text-muted)',
                        fontSize: 'var(--text-sm)',
                        marginBottom: 'var(--space-6)',
                    }}>
                        Your account is currently in Singer mode. Switch to Host mode to create songbooks, launch events, and run the live queue.
                    </p>
                    <div style={{ display: 'flex', gap: 'var(--space-3)', justifyContent: 'center', flexWrap: 'wrap' }}>
                        <button
                            type="button"
                            class="btn btn-primary btn-sm"
                            disabled={enabling}
                            onClick={async () => {
                                setEnabling(true)
                                try {
                                    await toggleHostMode()
                                    showToast('Switched to KJ Host mode! 🎤')
                                } catch (err) {
                                    showToast(err.message || 'Failed to switch to host mode', 'error')
                                } finally {
                                    setEnabling(false)
                                }
                            }}
                        >
                            {enabling ? 'Enabling Host Mode...' : '⚡ Switch to Host Mode Now'}
                        </button>
                        <a href="/" class="btn btn-secondary btn-sm">
                            Return Home
                        </a>
                    </div>
                </div>
            </div>
        )
    }

    return children
}
