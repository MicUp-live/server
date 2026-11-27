import { useState, useEffect, useRef } from 'preact/hooks'
import { user, isLoggedIn, updateProfile } from '../auth/store'
import { getScreenName, setScreenName } from '../utils/screenNameStore'
import { showToast } from './Toast'
import { Spinner } from './Spinner'

export function EditScreenNameModal({ isOpen, onClose, onUpdated }) {
    const currentUser = user.value
    const currentName = getScreenName(currentUser) || ''
    const currentScreenName = currentName
    const [nameInput, setNameInput] = useState(currentName)
    const [saving, setSaving] = useState(false)
    const [error, setError] = useState('')
    const inputRef = useRef(null)

    useEffect(() => {
        if (!isOpen) return
        setNameInput(currentScreenName || '')
        setError('')
        inputRef.current?.focus()

        const handleKeyDown = (e) => {
            if (e.key === 'Escape') {
                e.stopPropagation()
                onClose?.()
            }
        }
        window.addEventListener('keydown', handleKeyDown)
        return () => window.removeEventListener('keydown', handleKeyDown)
    }, [isOpen, currentScreenName])

    if (!isOpen) return null

    const handleSave = async (e) => {
        e.preventDefault()
        const trimmed = nameInput.trim()
        if (!trimmed) {
            setError('Screen name cannot be empty.')
            return
        }

        setError('')
        setSaving(true)
        try {
            setScreenName(trimmed)

            if (isLoggedIn.value && user.value) {
                try {
                    await updateProfile({ displayName: trimmed })
                } catch (err) {
                    console.warn('Failed to update remote profile:', err)
                }
            }

            showToast(`Screen name set to "${trimmed}"! 🎤`)
            if (onUpdated) onUpdated(trimmed)
            onClose()
        } catch (err) {
            setError(err.message || 'Failed to update screen name.')
        } finally {
            setSaving(false)
        }
    }

    return (
        <dialog
            open
            class="modal-overlay"
            aria-modal="true"
            aria-labelledby="screen-name-modal-title"
            onCancel={(e) => {
                e.preventDefault()
                onClose?.()
            }}
            style={{
                position: 'fixed',
                inset: 0,
                backgroundColor: 'rgba(5, 5, 8, 0.85)',
                backdropFilter: 'blur(8px)',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                zIndex: 1100,
                padding: 'var(--space-4)',
                border: 'none',
                margin: 0,
                maxWidth: 'none',
                maxHeight: 'none',
                width: '100%',
                height: '100%',
            }}
        >
            <div
                style={{
                    background: 'var(--color-surface)',
                    border: '1px solid var(--color-border)',
                    borderRadius: 'var(--radius-xl)',
                    width: '100%',
                    maxWidth: '420px',
                    maxHeight: 'min(90vh, 500px)',
                    display: 'flex',
                    flexDirection: 'column',
                    boxShadow: '0 20px 40px rgba(0, 0, 0, 0.7), 0 0 30px rgba(255, 42, 95, 0.15)',
                    overflow: 'hidden',
                    position: 'relative',
                }}
            >
                {/* Header */}
                <div
                    style={{
                        padding: 'var(--space-4) var(--space-5)',
                        background: 'linear-gradient(180deg, rgba(255, 42, 95, 0.12) 0%, transparent 100%)',
                        borderBottom: '1px solid var(--color-border)',
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'space-between',
                        flexShrink: 0,
                    }}
                >
                    <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-3)' }}>
                        <div
                            style={{
                                width: '40px',
                                height: '40px',
                                borderRadius: 'var(--radius-full)',
                                background: 'rgba(255, 42, 95, 0.15)',
                                border: '1px solid var(--color-accent)',
                                display: 'flex',
                                alignItems: 'center',
                                justifyContent: 'center',
                                fontSize: '1.1rem',
                            }}
                        >
                            🎤
                        </div>
                        <div>
                            <h3 id="screen-name-modal-title" style={{ margin: 0, fontSize: 'var(--text-base)', fontWeight: 600, color: 'var(--color-text)' }}>
                                Change Screen Name
                            </h3>
                            <p style={{ margin: 0, fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)' }}>
                                Name shown on the stage screen & host queue
                            </p>
                        </div>
                    </div>
                    <button
                        type="button"
                        onClick={onClose}
                        style={{
                            minWidth: 'var(--touch-target-min, 48px)',
                            minHeight: 'var(--touch-target-min, 48px)',
                            background: 'transparent',
                            border: 'none',
                            color: 'var(--color-text-muted)',
                            fontSize: '1.4rem',
                            cursor: 'pointer',
                            padding: 0,
                            display: 'inline-flex',
                            alignItems: 'center',
                            justifyContent: 'center',
                            borderRadius: 'var(--radius-full)',
                            lineHeight: 1,
                        }}
                        aria-label="Close"
                    >
                        ✕
                    </button>
                </div>

                {/* Form */}
                <form
                    onSubmit={handleSave}
                    style={{
                        padding: 'var(--space-5)',
                        flex: 1,
                        overflowY: 'auto',
                        WebkitOverflowScrolling: 'touch',
                    }}
                >
                    {error && (
                        <div
                            style={{
                                padding: 'var(--space-2) var(--space-3)',
                                background: 'rgba(239, 68, 68, 0.15)',
                                border: '1px solid var(--color-danger)',
                                borderRadius: 'var(--radius-md)',
                                color: '#fca5a5',
                                fontSize: 'var(--text-xs)',
                                marginBottom: 'var(--space-3)',
                            }}
                        >
                            {error}
                        </div>
                    )}

                    <div style={{ marginBottom: 'var(--space-4)' }}>
                        <label
                            htmlFor="input-screen-name"
                            style={{
                                display: 'block',
                                marginBottom: 'var(--space-2)',
                                fontSize: 'var(--text-xs)',
                                fontWeight: 600,
                                textTransform: 'uppercase',
                                letterSpacing: '0.05em',
                                color: 'var(--color-text-muted)',
                            }}
                        >
                            Your Stage / Screen Name <span style={{ color: 'var(--color-accent)' }}>*</span>
                        </label>
                        <input
                            ref={inputRef}
                            id="input-screen-name"
                            type="text"
                            maxLength={50}
                            value={nameInput}
                            onInput={(e) => setNameInput(e.target.value)}
                            placeholder="e.g. Rockin Sarah or Dave The Wave"
                            style={{
                                width: '100%',
                                padding: 'var(--space-3)',
                                borderRadius: 'var(--radius-md)',
                                background: 'rgba(0, 0, 0, 0.35)',
                                border: '1px solid var(--color-border)',
                                color: 'var(--color-text)',
                                fontSize: 'var(--text-sm)',
                                fontWeight: 500,
                            }}
                            required
                        />
                        <span style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', display: 'block', marginTop: '4px' }}>
                            The KJ and audience see this when you're up next!
                        </span>
                    </div>

                    <div style={{ display: 'flex', gap: 'var(--space-2)', justifyContent: 'flex-end' }}>
                        <button
                            type="button"
                            onClick={onClose}
                            class="btn btn-secondary btn-sm"
                            disabled={saving}
                        >
                            Cancel
                        </button>
                        <button
                            id="btn-save-screen-name"
                            type="submit"
                            class="btn btn-primary btn-sm"
                            disabled={saving}
                            style={{ minWidth: '100px', display: 'inline-flex', alignItems: 'center', justifyContent: 'center', gap: '4px' }}
                        >
                            {saving && <Spinner size="12px" />}
                            <span>{saving ? 'Saving...' : 'Save Name'}</span>
                        </button>
                    </div>
                </form>
            </div>
        </dialog>
    )
}
