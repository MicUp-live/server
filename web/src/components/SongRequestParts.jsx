import { Spinner } from './Spinner'
import { formatKeyPitchLabel, getKeyButtonStyle } from './songRequestHelpers'

const KEY_OPTIONS = [
    { value: -3, label: '-3 ♭' },
    { value: -2, label: '-2 ♭' },
    { value: -1, label: '-1 ♭' },
    { value: 0, label: 'Standard' },
    { value: 1, label: '+1 ♯' },
    { value: 2, label: '+2 ♯' },
    { value: 3, label: '+3 ♯' },
]

export function renderSubmitButtonContent({ resolvingCode, submitting, statusInfo, cooldownRemaining }) {
    if (resolvingCode) return <Spinner size="sm" label="Connecting..." inline />
    if (submitting) return <Spinner size="sm" label="Sending..." inline />
    if (statusInfo.isPaused || statusInfo.isEnded) return statusInfo.submitButtonLabel
    if (cooldownRemaining > 0) return <span>⏱️ Wait {cooldownRemaining}s</span>
    return '🎤 Send Request'
}

export function SongRequestHeader({ isWriteIn, titleText, artistText, isFav, keyChange, onToggleFavorite, onClose }) {
    return (
        <div style={{
            padding: 'var(--space-4) var(--space-6)',
            background: 'linear-gradient(180deg, rgba(255, 42, 95, 0.12) 0%, transparent 100%)',
            borderBottom: '1px solid var(--color-border)',
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'flex-start',
            flexShrink: 0,
        }}>
            <div>
                <div style={{
                    fontSize: 'var(--text-xs)',
                    fontWeight: 'var(--font-bold)',
                    color: 'var(--color-accent)',
                    textTransform: 'uppercase',
                    letterSpacing: '0.08em',
                    marginBottom: 'var(--space-1)',
                    display: 'flex',
                    alignItems: 'center',
                    gap: 'var(--space-2)'
                }}>
                    <span>🎤 Step Up To The Mic</span>
                </div>
                <h3 id="song-request-title" style={{
                    margin: 0,
                    fontSize: 'var(--text-xl)',
                    fontWeight: 'var(--font-bold)',
                    color: 'var(--color-text)',
                    lineHeight: 1.2,
                }}>
                    {isWriteIn ? (titleText || 'Custom Write-In Request') : titleText}
                </h3>
                {!isWriteIn && artistText && (
                    <div style={{ color: 'var(--color-text-muted)', fontSize: 'var(--text-sm)', marginTop: '2px' }}>
                        {artistText}
                    </div>
                )}
                {!isWriteIn && (
                    <div style={{ marginTop: 'var(--space-2)' }}>
                        <button
                            type="button"
                            onClick={onToggleFavorite}
                            style={{
                                background: isFav ? 'rgba(245, 158, 11, 0.15)' : 'rgba(255, 255, 255, 0.05)',
                                border: `1px solid ${isFav ? 'var(--color-chorus)' : 'var(--color-border)'}`,
                                color: isFav ? 'var(--color-chorus)' : 'var(--color-text-muted)',
                                borderRadius: 'var(--radius-full)',
                                padding: '4px 12px',
                                minHeight: '36px',
                                fontSize: 'var(--text-xs)',
                                fontWeight: 'var(--font-semibold)',
                                cursor: 'pointer',
                                display: 'inline-flex',
                                alignItems: 'center',
                                gap: '5px',
                                transition: 'all 0.15s ease'
                            }}
                        >
                            <span>{isFav ? '★ Favorited Track' : '☆ Save to Favorites'}</span>
                            {isFav && keyChange !== 0 && (
                                <span style={{ fontSize: '0.85em', opacity: 0.9 }}>
                                    ({keyChange > 0 ? `+${keyChange}♯` : `${keyChange}♭`})
                                </span>
                            )}
                        </button>
                    </div>
                )}
            </div>
            <button
                type="button"
                onClick={onClose}
                aria-label="Close"
                style={{
                    minWidth: 'var(--touch-target-min, 48px)',
                    minHeight: 'var(--touch-target-min, 48px)',
                    background: 'none',
                    border: 'none',
                    color: 'var(--color-text-dim)',
                    fontSize: '1.4rem',
                    cursor: 'pointer',
                    padding: 0,
                    display: 'inline-flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    borderRadius: 'var(--radius-full)',
                    lineHeight: 1,
                }}
            >
                ✕
            </button>
        </div>
    )
}

export function KeyPitchSelector({ allowKeyChange, keyChange, onSelectKey }) {
    if (!allowKeyChange) return null
    return (
        <fieldset aria-labelledby="request-key-pitch-label" style={{ border: 'none', padding: 0, margin: '0 0 var(--space-5)' }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--space-2)' }}>
                <span id="request-key-pitch-label" style={{ fontSize: 'var(--text-sm)', fontWeight: 'var(--font-semibold)', color: 'var(--color-text)' }}>
                    Key Pitch Adjustment
                </span>
                <span style={{
                    fontSize: 'var(--text-xs)',
                    color: keyChange !== 0 ? 'var(--color-chorus)' : 'var(--color-text-dim)',
                    fontWeight: 'var(--font-bold)'
                }}>
                    {formatKeyPitchLabel(keyChange)}
                </span>
            </div>
            <div style={{
                display: 'grid',
                gridTemplateColumns: 'repeat(7, 1fr)',
                gap: 'var(--space-1)',
                background: 'var(--color-surface-alt)',
                padding: 'var(--space-1)',
                borderRadius: 'var(--radius-lg)',
                border: '1px solid var(--color-border)',
            }}>
                {KEY_OPTIONS.map(opt => (
                    <button
                        key={opt.value}
                        type="button"
                        onClick={() => onSelectKey(opt.value)}
                        style={getKeyButtonStyle(keyChange === opt.value, opt.value)}
                    >
                        {opt.label}
                    </button>
                ))}
            </div>
        </fieldset>
    )
}
