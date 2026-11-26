import { useState, useEffect, useRef } from 'preact/hooks'

export function JoinShowModal({
    isOpen,
    onClose,
    onJoin,
    joining = false,
    joinError = '',
}) {
    const [joinCodeInput, setJoinCodeInput] = useState('')
    const onCloseRef = useRef(onClose)
    onCloseRef.current = onClose

    useEffect(() => {
        if (isOpen) {
            setJoinCodeInput('')
        }
    }, [isOpen])

    useEffect(() => {
        if (!isOpen || typeof document === 'undefined') return
        const handleKeyDown = (e) => {
            if (e.key === 'Escape' && !e.defaultPrevented) {
                e.preventDefault()
                onCloseRef.current?.()
            }
        }
        document.addEventListener('keydown', handleKeyDown)
        return () => document.removeEventListener('keydown', handleKeyDown)
    }, [isOpen])

    if (!isOpen) return null

    const handleSubmit = (e) => {
        e.preventDefault()
        const code = joinCodeInput.trim().toUpperCase()
        if (code) {
            onJoin(code)
        }
    }

    return (
        <div
            style={{
                position: 'fixed',
                inset: 0,
                backgroundColor: 'rgba(5, 5, 8, 0.85)',
                backdropFilter: 'blur(8px)',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                zIndex: 1000,
                padding: 'var(--space-4)',
            }}
            onClick={onClose}
            role="dialog"
            aria-modal="true"
            aria-labelledby="join-show-title"
        >
            <div
                style={{
                    background: 'var(--color-surface)',
                    border: '1px solid var(--color-border)',
                    borderRadius: 'var(--radius-xl)',
                    width: '100%',
                    maxWidth: '420px',
                    maxHeight: 'min(90vh, 500px)',
                    overflowY: 'auto',
                    WebkitOverflowScrolling: 'touch',
                    padding: 'var(--space-6)',
                    boxShadow: '0 20px 40px rgba(0,0,0,0.7), 0 0 30px var(--color-accent-glow)',
                }}
                onClick={e => e.stopPropagation()}
            >
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--space-4)' }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-2)' }}>
                        <span style={{ fontSize: '1.5rem' }}>🎤</span>
                        <h3 id="join-show-title" style={{ margin: 0, fontSize: 'var(--text-lg)' }}>Join Tonight's Show</h3>
                    </div>
                    <button
                        type="button"
                        onClick={onClose}
                        aria-label="Close"
                        style={{ background: 'none', border: 'none', color: 'var(--color-text-dim)', fontSize: '1.25rem', cursor: 'pointer' }}
                    >
                        ✕
                    </button>
                </div>
                <p style={{ margin: '0 0 var(--space-4)', fontSize: 'var(--text-sm)', color: 'var(--color-text-muted)' }}>
                    A live queue requires an active event. Enter the Show Code from your KJ to connect to tonight's queue:
                </p>
                {joinError && (
                    <div style={{
                        padding: 'var(--space-2) var(--space-3)',
                        background: 'rgba(239, 68, 68, 0.15)',
                        border: '1px solid var(--color-danger)',
                        borderRadius: 'var(--radius-md)',
                        color: '#fca5a5',
                        fontSize: 'var(--text-xs)',
                        marginBottom: 'var(--space-3)',
                    }}>
                        {joinError}
                    </div>
                )}
                <form onSubmit={handleSubmit}>
                    <input
                        type="text"
                        placeholder="e.g. OMALLEY"
                        value={joinCodeInput}
                        onInput={e => setJoinCodeInput(e.target.value.toUpperCase())}
                        maxLength={16}
                        required
                        autoFocus
                        style={{
                            width: '100%',
                            textAlign: 'center',
                            fontWeight: 'bold',
                            fontSize: 'var(--text-lg)',
                            fontFamily: 'var(--font-mono)',
                            textTransform: 'uppercase',
                            letterSpacing: '0.08em',
                            marginBottom: 'var(--space-4)',
                            padding: 'var(--space-3)',
                        }}
                    />
                    <div style={{ display: 'flex', gap: 'var(--space-2)', justifyContent: 'flex-end' }}>
                        <button type="button" class="btn btn-secondary" onClick={onClose}>
                            Cancel
                        </button>
                        <button type="submit" class="btn btn-primary" disabled={joining}>
                            {joining ? 'Connecting...' : 'Connect to Show 🚀'}
                        </button>
                    </div>
                </form>
            </div>
        </div>
    )
}
