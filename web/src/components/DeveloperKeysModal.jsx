import { useState, useEffect } from 'preact/hooks'
import { api } from '../api/client'
import { showToast } from './Toast'
import { Spinner } from './Spinner'

export function DeveloperKeysModal({ isOpen, onClose }) {
    const [developerKeys, setDeveloperKeys] = useState([])
    const [loading, setLoading] = useState(true)
    const [newKeyName, setNewKeyName] = useState('')
    const [creating, setCreating] = useState(false)
    const [createdKey, setCreatedKey] = useState(null)
    const [revokingId, setRevokingId] = useState(null)
    const [error, setError] = useState('')

    const loadKeys = async () => {
        setLoading(true)
        setError('')
        try {
            const keys = await api.get('/api-keys')
            setDeveloperKeys(Array.isArray(keys) ? keys : [])
        } catch (err) {
            setError(err.message || 'Failed to load developer keys')
        } finally {
            setLoading(false)
        }
    }

    useEffect(() => {
        if (isOpen) {
            void loadKeys()
            const handleKeyDown = (e) => {
                if (e.key === 'Escape') onClose()
            }
            document.addEventListener('keydown', handleKeyDown)
            return () => document.removeEventListener('keydown', handleKeyDown)
        }
    }, [isOpen])

    if (!isOpen) return null

    const handleCreateKey = async (e) => {
        e.preventDefault()
        const name = newKeyName.trim()
        if (!name || creating) return

        setError('')
        setCreating(true)
        try {
            const res = await api.post('/api-keys', {
                name,
                // The standard hoster integration needs the complete route
                // scope set, including event controls and catalog imports.
                scopes: ['events:read', 'events:write', 'requests:read', 'requests:write', 'catalogs:write'],
            })
            if (res && res.raw_key) {
                setCreatedKey(res)
                setNewKeyName('')
                setDeveloperKeys(prev => [res, ...prev.filter(k => k.id !== res.id)])
                showToast('Developer API key created! Copy it now.')
            }
        } catch (err) {
            setError(err.message || 'Failed to create developer key')
            showToast(err.message || 'Failed to create developer key', 'error')
        } finally {
            setCreating(false)
        }
    }

    const handleRevokeKey = async (key) => {
        if (!key?.id || revokingId) return
        if (!confirm(`Revoke developer key "${key.name}"? Any external application or script using this key will lose access immediately.`)) {
            return
        }

        setRevokingId(key.id)
        try {
            await api.delete(`/api-keys/${key.id}`)
            setDeveloperKeys(prev => prev.filter(k => k.id !== key.id))
            if (createdKey?.id === key.id) {
                setCreatedKey(null)
            }
            showToast('Developer key revoked')
        } catch (err) {
            showToast(err.message || 'Failed to revoke key', 'error')
        } finally {
            setRevokingId(null)
        }
    }

    const formatDate = (isoStr) => {
        if (!isoStr || isoStr.startsWith('0001-01-01')) return 'Never'
        try {
            const d = new Date(isoStr)
            return d.toLocaleDateString(undefined, {
                month: 'short',
                day: 'numeric',
                year: 'numeric',
            })
        } catch {
            return isoStr
        }
    }

    const baseUrl = `${typeof window !== 'undefined' ? window.location.origin : 'https://micup.live'}/api/v1`

    return (
        <dialog
            open
            class="modal-overlay"
            aria-modal="true"
            aria-labelledby="developer-keys-modal-title"
            onCancel={(e) => {
                e.preventDefault()
                onClose()
            }}
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
                    maxWidth: '560px',
                    maxHeight: 'min(90vh, 720px)',
                    display: 'flex',
                    flexDirection: 'column',
                    boxShadow: '0 20px 40px rgba(0, 0, 0, 0.7), 0 0 30px rgba(6, 182, 212, 0.15)',
                    overflow: 'hidden',
                    position: 'relative',
                }}
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
                            ⚡
                        </div>
                        <div>
                            <h3 id="developer-keys-modal-title" style={{ margin: 0, fontSize: 'var(--text-lg)', fontWeight: 600, color: 'var(--color-text)' }}>
                                Developer API Keys
                            </h3>
                            <p style={{ margin: 0, fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)' }}>
                                Account-scoped Personal Access Tokens for desktop tools and integrations
                            </p>
                        </div>
                    </div>
                    <button
                        type="button"
                        onClick={onClose}
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
                        aria-label="Close"
                    >
                        ✕
                    </button>
                </div>

                {/* Content */}
                <div
                    style={{
                        padding: 'var(--space-5) var(--space-6)',
                        overflowY: 'auto',
                        display: 'flex',
                        flexDirection: 'column',
                        gap: 'var(--space-4)',
                    }}
                >
                    {/* Base URL Row */}
                    <div
                        style={{
                            display: 'flex',
                            justifyContent: 'space-between',
                            alignItems: 'center',
                            background: 'var(--color-bg)',
                            padding: '8px 12px',
                            borderRadius: 'var(--radius-md)',
                            border: '1px solid var(--color-border)',
                            fontSize: 'var(--text-xs)',
                        }}
                    >
                        <span>
                            <strong style={{ color: 'var(--color-text-muted)' }}>API Base URL:</strong>{' '}
                            <code style={{ color: 'var(--color-accent)' }}>{baseUrl}</code>
                        </span>
                        <button
                            type="button"
                            onClick={() => {
                                void navigator.clipboard.writeText(baseUrl)
                                showToast('Copied API Base URL! 📋')
                            }}
                            class="btn btn-secondary btn-sm"
                            style={{ fontSize: 'var(--text-xs)', padding: '4px 10px', minHeight: '32px' }}
                        >
                            Copy URL
                        </button>
                    </div>

                    {/* Create New Key Form */}
                    <p style={{ margin: 0, fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', lineHeight: 1.4 }}>
                        New keys include event reads and controls, queue reads and mutations, and catalog/import access. Scope-restricted integrations can request a smaller set through the API.
                    </p>
                    <form onSubmit={handleCreateKey} style={{ display: 'flex', gap: '8px' }}>
                        <input
                            type="text"
                            value={newKeyName}
                            onInput={(e) => setNewKeyName(e.target.value)}
                            placeholder="Key name (e.g. Stage Deck 1, OBS Overlay, Studio Script)"
                            class="input"
                            style={{ flex: 1, fontSize: 'var(--text-sm)', padding: '8px 12px' }}
                            disabled={creating}
                            autoFocus
                        />
                        <button
                            type="submit"
                            disabled={creating || !newKeyName.trim()}
                            class="btn btn-primary btn-md"
                            style={{ whiteSpace: 'nowrap', display: 'flex', alignItems: 'center', gap: '6px' }}
                        >
                            {creating ? <Spinner size="sm" /> : '+ Generate Key'}
                        </button>
                    </form>

                    {error && (
                        <div
                            style={{
                                color: 'var(--color-danger)',
                                fontSize: 'var(--text-xs)',
                                background: 'rgba(239, 68, 68, 0.1)',
                                padding: '8px 12px',
                                borderRadius: 'var(--radius-md)',
                                border: '1px solid rgba(239, 68, 68, 0.2)',
                            }}
                        >
                            {error}
                        </div>
                    )}

                    {/* One-time Raw Key Alert Banner */}
                    {createdKey && createdKey.raw_key && (
                        <div
                            style={{
                                background: 'rgba(234, 179, 8, 0.12)',
                                border: '1px solid rgba(234, 179, 8, 0.4)',
                                borderRadius: 'var(--radius-md)',
                                padding: '12px 14px',
                            }}
                        >
                            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: '6px' }}>
                                <span style={{ fontWeight: 'bold', color: '#fde047', fontSize: 'var(--text-sm)' }}>
                                    ⚠️ Secret Key Generated — Copy it now!
                                </span>
                                <button
                                    type="button"
                                    onClick={() => setCreatedKey(null)}
                                    style={{ background: 'none', border: 'none', color: 'var(--color-text-dim)', cursor: 'pointer', fontSize: '14px', padding: 0 }}
                                    title="Dismiss"
                                >
                                    ✕
                                </button>
                            </div>
                            <p style={{ margin: '0 0 8px 0', fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', lineHeight: 1.4 }}>
                                For security, the raw token for <strong>"{createdKey.name}"</strong> will <strong>never be shown again</strong>. Copy and save it securely now:
                            </p>
                            <div
                                style={{
                                    display: 'flex',
                                    justifyContent: 'space-between',
                                    alignItems: 'center',
                                    background: 'var(--color-bg)',
                                    padding: '8px 10px',
                                    borderRadius: 'var(--radius-sm)',
                                    border: '1px solid var(--color-border)',
                                    gap: '8px',
                                }}
                            >
                                <code style={{ color: 'var(--color-accent)', wordBreak: 'break-all', fontSize: 'var(--text-xs)', userSelect: 'all' }}>
                                    {createdKey.raw_key}
                                </code>
                                <button
                                    type="button"
                                    onClick={() => {
                                        void navigator.clipboard.writeText(createdKey.raw_key)
                                        showToast('Copied Raw Secret Key! 📋')
                                    }}
                                    class="btn btn-primary btn-sm"
                                    style={{ fontSize: 'var(--text-xs)', padding: '4px 10px', minHeight: '32px', flexShrink: 0 }}
                                >
                                    Copy Secret
                                </button>
                            </div>
                        </div>
                    )}

                    {/* Key List Header */}
                    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginTop: 'var(--space-1)' }}>
                        <span style={{ fontSize: 'var(--text-xs)', fontWeight: 600, color: 'var(--color-text-muted)', textTransform: 'uppercase', letterSpacing: '0.05em' }}>
                            Active Personal Access Tokens ({developerKeys.length})
                        </span>
                    </div>

                    {/* Key List */}
                    {loading ? (
                        <div style={{ display: 'flex', justifyContent: 'center', padding: 'var(--space-6)' }}>
                            <Spinner size="md" label="Loading API keys..." />
                        </div>
                    ) : developerKeys.length === 0 ? (
                        <div
                            style={{
                                textAlign: 'center',
                                padding: 'var(--space-6) var(--space-4)',
                                background: 'var(--color-bg)',
                                borderRadius: 'var(--radius-md)',
                                border: '1px dashed var(--color-border)',
                                color: 'var(--color-text-dim)',
                                fontSize: 'var(--text-sm)',
                            }}
                        >
                            No developer keys created yet. Generate one above to connect desktop hosters or automation tools.
                        </div>
                    ) : (
                        <div style={{ display: 'flex', flexDirection: 'column', gap: '8px' }}>
                            {developerKeys.map((k) => (
                                <div
                                    key={k.id}
                                    style={{
                                        display: 'flex',
                                        justifyContent: 'space-between',
                                        alignItems: 'center',
                                        background: 'var(--color-bg)',
                                        padding: '10px 14px',
                                        borderRadius: 'var(--radius-md)',
                                        border: '1px solid var(--color-border)',
                                        gap: '12px',
                                    }}
                                >
                                    <div style={{ display: 'flex', flexDirection: 'column', gap: '3px', overflow: 'hidden' }}>
                                        <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                                            <strong style={{ color: 'var(--color-text)', fontSize: 'var(--text-sm)' }}>
                                                {k.name}
                                            </strong>
                                            <span style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-dim)', fontFamily: 'var(--font-mono)' }}>
                                                {k.key_prefix ? `${k.key_prefix}...` : 'mk_live_...'}
                                            </span>
                                        </div>
                                        <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-dim)' }}>
                                            Created {formatDate(k.created_at)} • Last used: {formatDate(k.last_used_at)}
                                        </div>
                                        {Array.isArray(k.scopes) && k.scopes.length > 0 && (
                                            <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-dim)' }}>
                                                Scopes: {k.scopes.join(', ')}
                                            </div>
                                        )}
                                    </div>
                                    <button
                                        type="button"
                                        disabled={revokingId === k.id}
                                        onClick={() => handleRevokeKey(k)}
                                        class="btn btn-secondary btn-sm"
                                        style={{
                                            fontSize: 'var(--text-xs)',
                                            padding: '4px 8px',
                                            color: 'var(--color-danger)',
                                            borderColor: 'rgba(239, 68, 68, 0.3)',
                                            flexShrink: 0,
                                        }}
                                    >
                                        {revokingId === k.id ? '...' : 'Revoke'}
                                    </button>
                                </div>
                            ))}
                        </div>
                    )}
                </div>

                {/* Footer */}
                <div
                    style={{
                        padding: 'var(--space-4) var(--space-6)',
                        borderTop: '1px solid var(--color-border)',
                        background: 'rgba(255, 255, 255, 0.02)',
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'space-between',
                        flexShrink: 0,
                        fontSize: 'var(--text-xs)',
                    }}
                >
                    <div style={{ display: 'flex', gap: 'var(--space-3)' }}>
                        <a
                            href="/docs#developer-api"
                            target="_blank"
                            rel="noopener noreferrer"
                            style={{ color: 'var(--color-accent)', textDecoration: 'none' }}
                        >
                            📖 API Docs
                        </a>
                        <span style={{ color: 'var(--color-text-dim)' }}>•</span>
                        <a
                            href="/swagger.html"
                            target="_blank"
                            rel="noopener noreferrer"
                            style={{ color: 'var(--color-accent)', textDecoration: 'none' }}
                        >
                            🚀 Swagger UI
                        </a>
                    </div>
                    <button type="button" class="btn btn-secondary btn-sm" onClick={onClose}>
                        Done
                    </button>
                </div>
            </div>
        </dialog>
    )
}
