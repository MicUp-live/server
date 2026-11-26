export function KioskExitModal({
    isOpen,
    pinInput,
    onPinInputChange,
    pinError,
    verifyingPin,
    onVerifyPin,
    onClose,
}) {
    if (!isOpen) return null

    return (
        <div style={{
            position: 'fixed',
            inset: 0,
            background: 'rgba(0, 0, 0, 0.85)',
            backdropFilter: 'blur(4px)',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            padding: 'var(--space-4)',
            zIndex: 1100,
        }}
            role="dialog"
            aria-modal="true"
            aria-labelledby="kiosk-exit-title"
        >
            <div class="card" style={{ maxWidth: '360px', width: '100%', padding: 'var(--space-6)', textAlign: 'center' }}>
                <span style={{ fontSize: '2.5rem', display: 'block', marginBottom: 'var(--space-2)' }}>🔒</span>
                <h3 id="kiosk-exit-title" style={{ margin: '0 0 var(--space-2) 0' }}>Exit Kiosk Mode</h3>
                <label
                    htmlFor="kiosk-exit-pin"
                    style={{ display: 'block', color: 'var(--color-text-muted)', fontSize: 'var(--text-sm)', margin: '0 0 var(--space-4) 0' }}
                >
                    Enter Host Kiosk PIN to return to navigation:
                </label>

                <input
                    id="kiosk-exit-pin"
                    name="pin"
                    type="password"
                    inputMode="numeric"
                    autoComplete="off"
                    enterKeyHint="done"
                    placeholder="Enter PIN"
                    value={pinInput}
                    maxLength={8}
                    disabled={verifyingPin}
                    onInput={(e) => onPinInputChange(e.currentTarget.value)}
                    onKeyDown={(e) => { if (e.key === 'Enter' && !e.isComposing && !verifyingPin && pinInput.trim()) onVerifyPin() }}
                    style={{
                        width: '100%',
                        padding: 'var(--space-3)',
                        fontSize: 'var(--text-xl)',
                        textAlign: 'center',
                        letterSpacing: '0.2em',
                        borderRadius: 'var(--radius-md)',
                        border: '1px solid var(--color-border)',
                        background: 'var(--color-surface)',
                        color: 'var(--color-text)',
                        marginBottom: 'var(--space-2)',
                    }}
                    autoFocus
                />

                {pinError && (
                    <p style={{ color: 'var(--color-danger)', fontSize: 'var(--text-xs)', margin: '0 0 var(--space-3) 0' }}>
                        {pinError}
                    </p>
                )}

                <div style={{ display: 'flex', gap: 'var(--space-2)', justifyContent: 'center', marginTop: 'var(--space-4)' }}>
                    <button type="button" class="btn btn-secondary" disabled={verifyingPin} onClick={onClose}>
                        Cancel
                    </button>
                    <button
                        type="button"
                        class="btn btn-primary"
                        disabled={verifyingPin || !pinInput.trim()}
                        onClick={onVerifyPin}
                    >
                        {verifyingPin ? 'Verifying...' : 'Unlock'}
                    </button>
                </div>
            </div>
        </div>
    )
}
