import { showToast } from '../../../components/Toast.jsx'

export function generateRandomKioskPin() {
    if (typeof crypto !== 'undefined' && typeof crypto.getRandomValues === 'function') {
        const buf = new Uint32Array(1)
        crypto.getRandomValues(buf)
        return String(buf[0] % 10000).padStart(4, '0')
    }
    return String(Math.floor(Math.random() * 10000)).padStart(4, '0')
}

export function KioskPinSection({ value, onChange, hasExistingPin = false }) {
    const trimmed = (value || '').trim()

    const handleCopy = async () => {
        if (!trimmed) return
        try {
            await navigator.clipboard.writeText(trimmed)
            showToast('Copied Kiosk PIN!')
        } catch {
            showToast(`Kiosk PIN: ${trimmed}`)
        }
    }

    return (
        <div style={{
            background: 'var(--color-surface-alt)',
            border: '1px solid var(--color-border)',
            borderRadius: 'var(--radius-lg)',
            padding: 'var(--space-4)',
            marginBottom: 'var(--space-6)',
        }}>
            <label
                htmlFor="settings-kiosk-pin"
                style={{ display: 'block', fontSize: 'var(--text-sm)', fontWeight: 'bold', marginBottom: 'var(--space-1)', color: 'var(--color-text)' }}
            >
                🔒 Venue Tablet Kiosk PIN
            </label>
            <div
                id="settings-kiosk-pin-help"
                style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', marginBottom: 'var(--space-3)', lineHeight: 1.4 }}
            >
                Required to exit Venue Tablet Kiosk mode back to host navigation (4–8 digits). Keep this private from singers.
            </div>

            <div style={{ display: 'flex', gap: 'var(--space-2)', alignItems: 'center', flexWrap: 'wrap' }}>
                <input
                    id="settings-kiosk-pin"
                    name="kiosk_pin"
                    type="text"
                    inputMode="numeric"
                    minLength={4}
                    maxLength={8}
                    autoCorrect="off"
                    autoCapitalize="none"
                    spellCheck={false}
                    autoComplete="off"
                    enterKeyHint="done"
                    aria-describedby="settings-kiosk-pin-help"
                    placeholder="e.g. 4829"
                    value={value}
                    onInput={e => onChange(e.currentTarget.value)}
                    style={{
                        width: '140px',
                        fontSize: 'var(--text-base)',
                        fontFamily: 'var(--font-mono)',
                        letterSpacing: '0.15em',
                        padding: 'var(--space-2) var(--space-3)',
                        background: 'var(--color-surface)',
                        border: '1px solid var(--color-border)',
                        borderRadius: 'var(--radius-md)',
                        color: 'var(--color-text)',
                    }}
                />
                <button
                    type="button"
                    class="btn btn-secondary btn-sm"
                    onClick={() => onChange(generateRandomKioskPin())}
                    style={{ fontSize: 'var(--text-xs)', padding: '6px 12px', minHeight: '36px' }}
                >
                    🎲 Random
                </button>
                <button
                    type="button"
                    class="btn btn-secondary btn-sm"
                    disabled={!trimmed}
                    onClick={handleCopy}
                    style={{ fontSize: 'var(--text-xs)', padding: '6px 12px', minHeight: '36px' }}
                >
                    Copy PIN
                </button>
            </div>

            {!trimmed && hasExistingPin && (
                <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-dim)', marginTop: 'var(--space-2)' }}>
                    Leaving this blank keeps your current PIN unchanged.
                </div>
            )}
        </div>
    )
}
