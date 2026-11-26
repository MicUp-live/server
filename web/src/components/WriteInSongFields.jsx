/**
 * Editable song title & artist inputs shown when submitting a custom write-in request.
 */
export function WriteInSongFields({ writeInTitle, setWriteInTitle, writeInArtist, setWriteInArtist }) {
    return (
        <div style={{ marginBottom: 'var(--space-5)', display: 'flex', flexDirection: 'column', gap: 'var(--space-3)' }}>
            <div>
                <label
                    htmlFor="write-in-song-title"
                    style={{
                        display: 'block',
                        fontSize: 'var(--text-sm)',
                        fontWeight: 'var(--font-semibold)',
                        color: 'var(--color-text)',
                        marginBottom: 'var(--space-1)',
                    }}
                >
                    Song Title <span style={{ color: 'var(--color-accent)' }}>*</span>
                </label>
                <input
                    id="write-in-song-title"
                    type="text"
                    placeholder="e.g. Pink Pony Club"
                    value={writeInTitle}
                    onInput={e => setWriteInTitle(e.target.value)}
                    maxLength={150}
                    required
                    style={{ width: '100%', fontSize: 'var(--text-sm)', padding: 'var(--space-3) var(--space-4)' }}
                />
            </div>
            <div>
                <label
                    htmlFor="write-in-song-artist"
                    style={{
                        display: 'block',
                        fontSize: 'var(--text-sm)',
                        fontWeight: 'var(--font-semibold)',
                        color: 'var(--color-text)',
                        marginBottom: 'var(--space-1)',
                    }}
                >
                    Artist <span style={{ color: 'var(--color-text-dim)', fontWeight: 'var(--font-normal)' }}>(optional)</span>
                </label>
                <input
                    id="write-in-song-artist"
                    type="text"
                    placeholder="e.g. Chappell Roan"
                    value={writeInArtist}
                    onInput={e => setWriteInArtist(e.target.value)}
                    maxLength={150}
                    style={{ width: '100%', fontSize: 'var(--text-sm)', padding: 'var(--space-3) var(--space-4)' }}
                />
            </div>
        </div>
    )
}

export function StageAlertsOptIn({ stageAlerts, setStageAlerts }) {
    if (typeof window === 'undefined' || !('Notification' in window)) return null
    return (
        <div style={{
            marginBottom: 'var(--space-5)',
            background: 'var(--color-surface-alt)',
            border: '1px solid var(--color-border)',
            borderRadius: 'var(--radius-md)',
            padding: 'var(--space-3) var(--space-4)',
            display: 'flex',
            alignItems: 'center',
            gap: 'var(--space-3)',
        }}>
            <input
                id="request-stage-alerts"
                type="checkbox"
                checked={stageAlerts}
                onChange={e => setStageAlerts(e.target.checked)}
                style={{ width: '18px', height: '18px', accentColor: 'var(--color-accent)', cursor: 'pointer' }}
            />
            <label
                htmlFor="request-stage-alerts"
                style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text)', cursor: 'pointer', userSelect: 'none', lineHeight: '1.4' }}
            >
                <span style={{ fontWeight: 'var(--font-semibold)', color: 'var(--color-text)' }}>🔔 Stage Alerts</span>
                <span style={{ display: 'block', color: 'var(--color-text-muted)' }}>
                    Buzz and notify my device when I'm called to the stage
                </span>
            </label>
        </div>
    )
}
