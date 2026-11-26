import { ShowStatusBanner } from '../../../components/ShowStatusBanner.jsx'
import { getShowStatusInfo } from '../../../utils/showStatus.js'

export function KioskRequestModal({
    song,
    event,
    singerName,
    onSingerNameChange,
    keyChange,
    onKeyChange,
    notes,
    onNotesChange,
    submitting,
    onSubmit,
    onClose,
}) {
    if (!song) return null

    const allowKeyChange = event?.allow_key_change !== false
    const statusInfo = getShowStatusInfo(event)

    return (
        <div style={{
            position: 'fixed',
            inset: 0,
            background: 'rgba(0, 0, 0, 0.8)',
            backdropFilter: 'blur(4px)',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            padding: 'var(--space-4)',
            zIndex: 1000,
        }}
            role="dialog"
            aria-modal="true"
            aria-labelledby="kiosk-request-title"
        >
            <div class="card" style={{
                maxWidth: '500px',
                width: '100%',
                maxHeight: 'min(90vh, 650px)',
                overflowY: 'auto',
                WebkitOverflowScrolling: 'touch',
                padding: 'var(--space-6)',
                borderRadius: 'var(--radius-lg)',
            }}>
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--space-4)' }}>
                    <h3 id="kiosk-request-title" style={{ margin: 0, fontSize: 'var(--text-xl)' }}>Request Song</h3>
                    <button type="button" class="btn btn-secondary btn-sm" onClick={onClose}>✕</button>
                </div>

                <ShowStatusBanner status={event?.status} compact />

                <div style={{ background: 'var(--color-surface-alt)', padding: 'var(--space-3) var(--space-4)', borderRadius: 'var(--radius-md)', marginBottom: 'var(--space-4)' }}>
                    <div style={{ fontWeight: 'var(--font-bold)', fontSize: 'var(--text-lg)', color: 'var(--color-accent)' }}>
                        {song.title || song.label}
                    </div>
                    <div style={{ color: 'var(--color-text-muted)' }}>
                        by {song.artist}
                    </div>
                </div>

                <form onSubmit={onSubmit}>
                    <div style={{ marginBottom: 'var(--space-4)' }}>
                        <label for="kiosk-singer-name" style={{ display: 'block', fontSize: 'var(--text-sm)', fontWeight: 'var(--font-medium)', marginBottom: 'var(--space-1)' }}>
                            Your Name / Stage Name *
                        </label>
                        <input
                            id="kiosk-singer-name"
                            type="text"
                            placeholder="Enter your name"
                            value={singerName}
                            onInput={(e) => onSingerNameChange(e.currentTarget.value)}
                            style={{
                                width: '100%',
                                padding: 'var(--space-3)',
                                fontSize: 'var(--text-base)',
                                borderRadius: 'var(--radius-md)',
                                border: '1px solid var(--color-border)',
                                background: 'var(--color-surface)',
                                color: 'var(--color-text)',
                            }}
                            autoFocus
                            required
                        />
                    </div>

                    {/* Pitch adjustment (-3 to +3) */}
                    {allowKeyChange && (
                        <div style={{ marginBottom: 'var(--space-4)' }}>
                            <label style={{ display: 'block', fontSize: 'var(--text-sm)', fontWeight: 'var(--font-medium)', marginBottom: 'var(--space-1)' }}>
                                Key / Pitch Change: {keyChange === 0 ? 'Standard Key (0)' : (keyChange > 0 ? `+${keyChange} Semitones` : `${keyChange} Semitones`)}
                            </label>
                            <div style={{ display: 'flex', gap: 'var(--space-2)' }}>
                                {[-3, -2, -1, 0, 1, 2, 3].map(k => (
                                    <button
                                        type="button"
                                        key={k}
                                        class={`btn btn-sm ${keyChange === k ? 'btn-primary' : 'btn-secondary'}`}
                                        style={{ flex: 1 }}
                                        onClick={() => onKeyChange(k)}
                                    >
                                        {k > 0 ? `+${k}` : k}
                                    </button>
                                ))}
                            </div>
                        </div>
                    )}

                    <div style={{ marginBottom: 'var(--space-6)' }}>
                        <label for="kiosk-notes" style={{ display: 'block', fontSize: 'var(--text-sm)', fontWeight: 'var(--font-medium)', marginBottom: 'var(--space-1)' }}>
                            Note to KJ (Optional)
                        </label>
                        <input
                            id="kiosk-notes"
                            type="text"
                            placeholder="e.g., Duet with Alex, Birthday!"
                            value={notes}
                            onInput={(e) => onNotesChange(e.currentTarget.value)}
                            style={{
                                width: '100%',
                                padding: 'var(--space-2) var(--space-3)',
                                fontSize: 'var(--text-sm)',
                                borderRadius: 'var(--radius-md)',
                                border: '1px solid var(--color-border)',
                                background: 'var(--color-surface)',
                                color: 'var(--color-text)',
                            }}
                        />
                    </div>

                    <div style={{ display: 'flex', gap: 'var(--space-3)', justifyContent: 'flex-end' }}>
                        <button type="button" class="btn btn-secondary" onClick={onClose}>
                            Cancel
                        </button>
                        <button type="submit" class="btn btn-primary" disabled={submitting || !singerName.trim() || statusInfo.isPaused || statusInfo.isEnded}>
                            {submitting ? 'Submitting...' : (statusInfo.isPaused || statusInfo.isEnded) ? statusInfo.submitButtonLabel : 'Send Request 🎤'}
                        </button>
                    </div>
                </form>
            </div>
        </div>
    )
}
