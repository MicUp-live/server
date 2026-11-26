import { route } from 'preact-router'
import { BrandIcon } from '../components/BrandIcon'

export function NotFound() {
    return (
        <div class="container-narrow" style={{
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'center',
            justifyContent: 'center',
            textAlign: 'center',
            padding: 'var(--space-12) var(--space-4)',
            minHeight: '60vh',
        }}>
            <div style={{ marginBottom: 'var(--space-6)', opacity: 0.85 }}>
                <BrandIcon size={64} />
            </div>
            <h1 style={{
                fontSize: 'var(--text-3xl)',
                fontWeight: 800,
                letterSpacing: '-0.02em',
                marginBottom: 'var(--space-3)',
                background: 'linear-gradient(135deg, var(--color-text), var(--color-text-muted))',
                WebkitBackgroundClip: 'text',
                WebkitTextFillColor: 'transparent',
            }}>
                Page Not Found
            </h1>
            <p style={{
                fontSize: 'var(--text-base)',
                color: 'var(--color-text-muted)',
                maxWidth: '420px',
                marginBottom: 'var(--space-8)',
                lineHeight: 1.6,
            }}>
                The page you're looking for doesn't exist, was moved, or the link may have expired.
            </p>
            <div style={{ display: 'flex', gap: 'var(--space-3)' }}>
                <button
                    class="btn btn-primary"
                    onClick={() => route('/')}
                >
                    Back to Home
                </button>
                <button
                    class="btn btn-secondary"
                    onClick={() => route('/docs')}
                >
                    Documentation
                </button>
            </div>
        </div>
    )
}
