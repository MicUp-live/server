export function KioskSuccessSplash({ successSinger, countdown, onReset }) {
    return (
        <div style={{
            height: '100vh',
            display: 'flex',
            flexDirection: 'column',
            justifyContent: 'center',
            alignItems: 'center',
            background: 'radial-gradient(circle at center, #241438 0%, #0b0b10 80%)',
            padding: 'var(--space-6)',
            textAlign: 'center',
            boxSizing: 'border-box',
        }}>
            <span style={{ fontSize: '5rem', display: 'block', marginBottom: 'var(--space-4)' }}>🎉</span>
            <h1 style={{ fontSize: 'var(--text-3xl)', fontFamily: 'var(--font-display)', margin: '0 0 var(--space-2) 0', color: 'var(--color-text)' }}>
                You're In Line, {successSinger}!
            </h1>
            <p style={{ fontSize: 'var(--text-xl)', color: 'var(--color-text-muted)', maxWidth: '500px', margin: '0 0 var(--space-8) 0' }}>
                Your song request has been sent to the host. Listen for your name!
            </p>

            <div style={{ marginBottom: 'var(--space-8)' }}>
                <span style={{
                    background: 'rgba(255, 255, 255, 0.08)',
                    padding: 'var(--space-3) var(--space-6)',
                    borderRadius: 'var(--radius-full)',
                    fontSize: 'var(--text-sm)',
                    color: 'var(--color-text-dim)',
                }}>
                    Screen resets in <strong>{countdown}s</strong>
                </span>
            </div>

            <button
                type="button"
                class="btn btn-primary btn-lg"
                style={{ padding: 'var(--space-4) var(--space-10)', fontSize: 'var(--text-lg)' }}
                onClick={onReset}
            >
                ✨ All Done! Next Singer →
            </button>
        </div>
    )
}
