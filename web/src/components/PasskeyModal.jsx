import { useState, useEffect } from 'preact/hooks'
import { listPasskeys, registerPasskey, deletePasskey, isHost } from '../auth/store'
import { isPasskeySupported } from '../utils/webauthn'
import { showToast } from './Toast'
import { Spinner } from './Spinner'

export function PasskeyModal({ isOpen, onClose }) {
    const supported = isPasskeySupported()
    const [passkeys, setPasskeys] = useState([])
    const [loading, setLoading] = useState(true)
    const [adding, setAdding] = useState(false)
    const [newDeviceName, setNewDeviceName] = useState('')
    const [showAddForm, setShowAddForm] = useState(false)
    const [error, setError] = useState('')

    const loadKeys = async () => {
        setLoading(true)
        setError('')
        try {
            const keys = await listPasskeys()
            setPasskeys(keys || [])
        } catch (err) {
            setError(err.message || 'Failed to load passkeys')
        } finally {
            setLoading(false)
        }
    }

    useEffect(() => {
        if (!isOpen) return
        loadKeys()

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

    const handleAddPasskey = async (e) => {
        e.preventDefault()
        if (!supported) {
            setError('Passkeys are not supported in this browser.')
            return
        }

        setError('')
        setAdding(true)
        try {
            const friendlyName = newDeviceName.trim() || (isHost.value ? 'KJ Host Passkey' : 'Singer Device')
            await registerPasskey({
                friendlyName,
                isHost: isHost.value,
            })
            showToast('Passkey added successfully! 🎉')
            setNewDeviceName('')
            setShowAddForm(false)
            await loadKeys()
        } catch (err) {
            setError(err.message || 'Failed to add passkey')
        } finally {
            setAdding(false)
        }
    }

    const handleDeletePasskey = async (id, name) => {
        if (!confirm(`Revoke passkey "${name || 'Passkey'}"? You will not be able to use it to sign in.`)) {
            return
        }

        try {
            await deletePasskey(id)
            showToast('Passkey revoked')
            await loadKeys()
        } catch (err) {
            setError(err.message || 'Failed to revoke passkey')
        }
    }

    const formatDate = (isoStr) => {
        if (!isoStr) return 'Never'
        try {
            const d = new Date(isoStr)
            return d.toLocaleDateString(undefined, {
                month: 'short',
                day: 'numeric',
                year: 'numeric',
                hour: '2-digit',
                minute: '2-digit',
            })
        } catch {
            return isoStr
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
            aria-labelledby="passkey-modal-title"
        >
            <div
                style={{
                    background: 'var(--color-surface)',
                    border: '1px solid var(--color-border)',
                    borderRadius: 'var(--radius-xl)',
                    width: '100%',
                    maxWidth: '520px',
                    maxHeight: 'min(90vh, 700px)',
                    display: 'flex',
                    flexDirection: 'column',
                    boxShadow: '0 20px 40px rgba(0, 0, 0, 0.7), 0 0 30px rgba(6, 182, 212, 0.15)',
                    overflow: 'hidden',
                    position: 'relative',
                }}
                onClick={(e) => e.stopPropagation()}
            >
                {/* Header */}
                <div
                    style={{
                        padding: 'var(--space-4) var(--space-6)',
                        background: 'linear-gradient(180deg, rgba(6, 182, 212, 0.12) 0%, transparent 100%)',
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
                                background: 'rgba(6, 182, 212, 0.15)',
                                border: '1px solid rgba(6, 182, 212, 0.4)',
                                display: 'flex',
                                alignItems: 'center',
                                justifyContent: 'center',
                                fontSize: '1.2rem',
                            }}
                        >
                            🔑
                        </div>
                        <div>
                            <h3 id="passkey-modal-title" style={{ margin: 0, fontSize: 'var(--text-lg)', fontWeight: 600, color: 'var(--color-text)' }}>
                                Passkeys & Biometrics
                            </h3>
                            <p style={{ margin: 0, fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)' }}>
                                Passwordless sign-in with Touch ID, Face ID, or Windows Hello
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

                {/* Content */}
                <div style={{
                    padding: 'var(--space-6)',
                    flex: 1,
                    overflowY: 'auto',
                    WebkitOverflowScrolling: 'touch',
                }}>
                    {error && (
                        <div
                            style={{
                                background: 'rgba(239, 68, 68, 0.15)',
                                border: '1px solid rgba(239, 68, 68, 0.3)',
                                borderRadius: 'var(--radius-md)',
                                padding: 'var(--space-3) var(--space-4)',
                                color: '#fca5a5',
                                fontSize: 'var(--text-sm)',
                                marginBottom: 'var(--space-4)',
                            }}
                        >
                            {error}
                        </div>
                    )}

                    {!supported && (
                        <div
                            style={{
                                background: 'rgba(234, 179, 8, 0.1)',
                                border: '1px solid rgba(234, 179, 8, 0.3)',
                                borderRadius: 'var(--radius-md)',
                                padding: 'var(--space-3) var(--space-4)',
                                color: '#fef08a',
                                fontSize: 'var(--text-xs)',
                                marginBottom: 'var(--space-4)',
                            }}
                        >
                            ⚠️ Passkeys are not supported or are disabled in this browser. Please use Chrome, Safari, Edge, or modern Firefox.
                        </div>
                    )}

                    {loading ? (
                        <div style={{ padding: 'var(--space-8) 0', textAlign: 'center' }}>
                            <Spinner size="md" />
                            <p style={{ marginTop: 'var(--space-3)', color: 'var(--color-text-muted)', fontSize: 'var(--text-sm)' }}>
                                Loading passkeys...
                            </p>
                        </div>
                    ) : (
                        <div>
                            {passkeys.length === 0 ? (
                                <div
                                    style={{
                                        textAlign: 'center',
                                        padding: 'var(--space-6) var(--space-4)',
                                        background: 'var(--color-bg)',
                                        borderRadius: 'var(--radius-lg)',
                                        border: '1px dashed var(--color-border)',
                                        marginBottom: 'var(--space-4)',
                                    }}
                                >
                                    <div style={{ fontSize: '2rem', marginBottom: 'var(--space-2)' }}>🔐</div>
                                    <p style={{ fontWeight: 500, margin: '0 0 var(--space-1) 0' }}>No Passkeys Registered</p>
                                    <p style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', margin: 0 }}>
                                        Add a passkey to sign in instantly without typing your password.
                                    </p>
                                </div>
                            ) : (
                                <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-3)', marginBottom: 'var(--space-5)' }}>
                                    {passkeys.map((pk) => (
                                        <div
                                            key={pk.id}
                                            style={{
                                                display: 'flex',
                                                alignItems: 'center',
                                                justifyContent: 'space-between',
                                                padding: 'var(--space-3) var(--space-4)',
                                                background: 'var(--color-bg)',
                                                border: '1px solid var(--color-border)',
                                                borderRadius: 'var(--radius-md)',
                                            }}
                                        >
                                            <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-3)' }}>
                                                <span style={{ fontSize: '1.3rem' }}>🔑</span>
                                                <div>
                                                    <div style={{ fontWeight: 600, fontSize: 'var(--text-sm)' }}>
                                                        {pk.friendly_name || 'Passkey Device'}
                                                    </div>
                                                    <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', marginTop: '2px' }}>
                                                        Added {formatDate(pk.created_at)} • Last used {formatDate(pk.last_used_at)}
                                                    </div>
                                                </div>
                                            </div>
                                            <button
                                                onClick={() => handleDeletePasskey(pk.id, pk.friendly_name)}
                                                class="btn btn-danger btn-sm"
                                                title="Revoke passkey"
                                                style={{ padding: '4px 10px', fontSize: 'var(--text-xs)' }}
                                            >
                                                Revoke
                                            </button>
                                        </div>
                                    ))}
                                </div>
                            )}

                            {showAddForm ? (
                                <form
                                    onSubmit={handleAddPasskey}
                                    style={{
                                        background: 'var(--color-bg)',
                                        border: '1px solid var(--color-border)',
                                        borderRadius: 'var(--radius-lg)',
                                        padding: 'var(--space-4)',
                                    }}
                                >
                                    <label
                                        for="passkey-device-name"
                                        style={{
                                            display: 'block',
                                            fontSize: 'var(--text-xs)',
                                            color: 'var(--color-text-muted)',
                                            marginBottom: 'var(--space-2)',
                                            fontWeight: 500,
                                        }}
                                    >
                                        Passkey Name (e.g. "MacBook Touch ID", "iPhone 15")
                                    </label>
                                    <input
                                        type="text"
                                        id="passkey-device-name"
                                        placeholder="Device or key name"
                                        value={newDeviceName}
                                        onInput={(e) => setNewDeviceName(e.target.value)}
                                        maxLength={50}
                                        style={{ width: '100%', marginBottom: 'var(--space-3)' }}
                                        autoFocus
                                    />
                                    <div style={{ display: 'flex', gap: 'var(--space-2)', justifyContent: 'flex-end' }}>
                                        <button
                                            type="button"
                                            class="btn btn-secondary btn-sm"
                                            onClick={() => setShowAddForm(false)}
                                            disabled={adding}
                                        >
                                            Cancel
                                        </button>
                                        <button
                                            type="submit"
                                            class="btn btn-primary btn-sm"
                                            disabled={adding || !supported}
                                        >
                                            {adding ? 'Prompting Biometrics...' : 'Register Passkey'}
                                        </button>
                                    </div>
                                </form>
                            ) : (
                                <button
                                    type="button"
                                    class="btn btn-primary btn-full"
                                    onClick={() => setShowAddForm(true)}
                                    disabled={!supported}
                                    style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', gap: '8px' }}
                                >
                                    <span>➕</span>
                                    <span>Add Passkey for This Device</span>
                                </button>
                            )}
                        </div>
                    )}
                </div>
            </div>
        </div>
    )
}
