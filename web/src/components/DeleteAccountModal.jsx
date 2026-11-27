import { useState, useEffect } from 'preact/hooks'
import { deleteAccount } from '../auth/store'
import { showToast } from './Toast'
import { Spinner } from './Spinner'
import { ErrorBanner } from './Banner'

export function DeleteAccountModal({ isOpen, onClose }) {
    const [confirmText, setConfirmText] = useState('')
    const [deleting, setDeleting] = useState(false)
    const [error, setError] = useState('')

    useEffect(() => {
        if (!isOpen) {
            setConfirmText('')
            setError('')
            setDeleting(false)
            return
        }

        const handleKeyDown = (e) => {
            if (e.key === 'Escape') {
                e.stopPropagation()
                onClose?.()
            }
        }
        window.addEventListener('keydown', handleKeyDown)
        return () => window.removeEventListener('keydown', handleKeyDown)
    }, [isOpen])

    if (!isOpen) return null

    const isMatch = confirmText.trim() === 'DELETE'

    const handleDelete = async (e) => {
        e.preventDefault()
        if (!isMatch) {
            setError("Please type 'DELETE' to confirm account deletion.")
            return
        }

        setError('')
        setDeleting(true)
        try {
            await deleteAccount('DELETE')
            showToast('Your account and personal data have been permanently deleted.')
            onClose()
            window.location.href = '/'
        } catch (err) {
            setError(err.message || 'Failed to delete account. Please try again.')
            setDeleting(false)
        }
    }

    return (
        <dialog
            open
            class="modal-overlay"
            aria-modal="true"
            aria-labelledby="delete-account-modal-title"
            onCancel={(e) => {
                e.preventDefault()
                onClose()
            }}
            style={{
                position: 'fixed',
                inset: 0,
                backgroundColor: 'rgba(5, 5, 8, 0.88)',
                backdropFilter: 'blur(8px)',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                zIndex: 1000,
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
                    border: '1px solid rgba(239, 68, 68, 0.3)',
                    borderRadius: 'var(--radius-xl)',
                    width: '100%',
                    maxWidth: '460px',
                    maxHeight: 'min(90vh, 600px)',
                    display: 'flex',
                    flexDirection: 'column',
                    boxShadow: '0 20px 40px rgba(0, 0, 0, 0.8), 0 0 30px rgba(239, 68, 68, 0.15)',
                    overflow: 'hidden',
                    position: 'relative',
                }}
            >
                {/* Header */}
                <div
                    style={{
                        padding: 'var(--space-4) var(--space-6)',
                        background: 'linear-gradient(180deg, rgba(239, 68, 68, 0.15) 0%, transparent 100%)',
                        borderBottom: '1px solid rgba(239, 68, 68, 0.2)',
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
                                background: 'rgba(239, 68, 68, 0.2)',
                                border: '1px solid rgba(239, 68, 68, 0.4)',
                                display: 'flex',
                                alignItems: 'center',
                                justifyContent: 'center',
                                fontSize: '1.2rem',
                            }}
                        >
                            ⚠️
                        </div>
                        <div>
                            <h3 id="delete-account-modal-title" style={{ margin: 0, fontSize: 'var(--text-lg)', fontWeight: 600, color: '#f87171' }}>
                                Delete Account
                            </h3>
                            <p style={{ margin: 0, fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)' }}>
                                Permanent and irreversible action
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

                {/* Body Form */}
                <form
                    onSubmit={handleDelete}
                    style={{
                        display: 'flex',
                        flexDirection: 'column',
                        flex: 1,
                        minHeight: 0,
                        overflow: 'hidden',
                    }}
                >
                    <div
                        style={{
                            padding: 'var(--space-6)',
                            flex: 1,
                            overflowY: 'auto',
                            WebkitOverflowScrolling: 'touch',
                        }}
                    >
                        {error && <div style={{ marginBottom: 'var(--space-4)' }}><ErrorBanner message={error} /></div>}

                        <p style={{ fontSize: 'var(--text-sm)', color: 'var(--color-text)', lineHeight: 1.5, marginBottom: 'var(--space-4)' }}>
                            Deleting your account will <strong>permanently erase</strong> all your personal data, including your login sessions, registered passkeys, songbooks, song catalogs, and event history.
                        </p>

                        <div style={{
                            padding: 'var(--space-3)',
                            borderRadius: 'var(--radius-md)',
                            backgroundColor: 'rgba(239, 68, 68, 0.08)',
                            border: '1px dashed rgba(239, 68, 68, 0.3)',
                            marginBottom: 'var(--space-5)',
                            fontSize: 'var(--text-xs)',
                            color: 'var(--color-text-muted)'
                        }}>
                            To confirm this action, please type <strong>DELETE</strong> in all capitals below:
                        </div>

                        <div>
                            <input
                                id="confirm-delete-input"
                                type="text"
                                value={confirmText}
                                onInput={(e) => setConfirmText(e.target.value)}
                                placeholder="Type DELETE to confirm"
                                style={{
                                    width: '100%',
                                    padding: 'var(--space-3)',
                                    borderRadius: 'var(--radius-md)',
                                    background: 'rgba(0, 0, 0, 0.3)',
                                    border: isMatch ? '1px solid #ef4444' : '1px solid var(--color-border)',
                                    color: isMatch ? '#f87171' : 'var(--color-text)',
                                    fontSize: 'var(--text-sm)',
                                    fontWeight: isMatch ? 'bold' : 'normal',
                                    letterSpacing: isMatch ? '2px' : 'normal',
                                }}
                                autoFocus
                            />
                        </div>
                    </div>

                    <div style={{
                        padding: 'var(--space-4) var(--space-6)',
                        borderTop: '1px solid rgba(239, 68, 68, 0.2)',
                        background: 'var(--color-surface)',
                        display: 'flex',
                        gap: 'var(--space-3)',
                        justifyContent: 'flex-end',
                        flexShrink: 0,
                    }}>
                        <button
                            type="button"
                            onClick={onClose}
                            class="btn btn-secondary"
                            disabled={deleting}
                        >
                            Cancel
                        </button>
                        <button
                            id="btn-confirm-delete-account"
                            type="submit"
                            disabled={!isMatch || deleting}
                            style={{
                                minWidth: '150px',
                                display: 'inline-flex',
                                alignItems: 'center',
                                justifyContent: 'center',
                                gap: '6px',
                                padding: 'var(--space-2) var(--space-4)',
                                borderRadius: 'var(--radius-md)',
                                border: 'none',
                                background: isMatch ? '#dc2626' : 'rgba(239, 68, 68, 0.2)',
                                color: isMatch ? '#fff' : 'rgba(255, 255, 255, 0.4)',
                                cursor: isMatch && !deleting ? 'pointer' : 'not-allowed',
                                fontWeight: 600,
                                fontSize: 'var(--text-sm)',
                                transition: 'all 0.15s ease',
                            }}
                        >
                            {deleting && <Spinner size="14px" />}
                            <span>{deleting ? 'Deleting...' : 'Delete My Account'}</span>
                        </button>
                    </div>
                </form>
            </div>
        </dialog>
    )
}
