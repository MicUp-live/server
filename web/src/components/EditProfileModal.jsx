import { useState, useEffect } from 'preact/hooks'
import { user, updateProfile, exportUserData } from '../auth/store'
import { showToast } from './Toast'
import { Spinner } from './Spinner'
import { ErrorBanner } from './Banner'
import { DeleteAccountModal } from './DeleteAccountModal'

export function EditProfileModal({ isOpen, onClose }) {
    const currentUser = user.value
    const [displayName, setDisplayName] = useState(currentUser?.display_name || '')
    const [email, setEmail] = useState(currentUser?.email || '')
    const [isHostRole, setIsHostRole] = useState(!!currentUser?.is_host)
    const [newPassword, setNewPassword] = useState('')
    const [confirmPassword, setConfirmPassword] = useState('')
    const [savingPassword, setSavingPassword] = useState(false)
    const [saving, setSaving] = useState(false)
    const [exporting, setExporting] = useState(false)
    const [isDeleteModalOpen, setIsDeleteModalOpen] = useState(false)
    const [error, setError] = useState('')
    const [passwordError, setPasswordError] = useState('')

    useEffect(() => {
        if (isOpen) {
            setDisplayName(currentUser?.display_name || '')
            setEmail(currentUser?.email || '')
            setIsHostRole(!!currentUser?.is_host)
            setError('')
            setPasswordError('')
            setNewPassword('')
            setConfirmPassword('')
            setExporting(false)
            setIsDeleteModalOpen(false)
        }

        const handleKeyDown = (e) => {
            if (e.key === 'Escape') {
                e.stopPropagation()
                onClose?.()
            }
        }
        if (isOpen) {
            window.addEventListener('keydown', handleKeyDown)
            return () => window.removeEventListener('keydown', handleKeyDown)
        }
    }, [isOpen, currentUser])

    if (!isOpen) return null

    const handleExportData = async () => {
        setExporting(true)
        try {
            await exportUserData()
            showToast('Personal data archive downloaded! 📥')
        } catch (err) {
            showToast(err.message || 'Failed to export personal data', 'error')
        } finally {
            setExporting(false)
        }
    }

    const handleSave = async (e) => {
        e.preventDefault()
        const trimmedName = displayName.trim()
        const trimmedEmail = email.trim()

        if (!trimmedName) {
            setError('Display name cannot be empty.')
            return
        }

        if (trimmedEmail && !trimmedEmail.includes('@')) {
            setError('Please enter a valid email address.')
            return
        }

        setError('')
        setSaving(true)
        try {
            await updateProfile({
                displayName: trimmedName,
                email: trimmedEmail || undefined,
                isHost: isHostRole,
            })
            if (typeof window !== 'undefined') {
                localStorage.setItem('micup_singer_name', trimmedName)
                window.dispatchEvent(new CustomEvent('micup_screen_name_changed', { detail: trimmedName }))
            }
            showToast('Profile & screen name updated! ✨')
            onClose()
        } catch (err) {
            setError(err.message || 'Failed to update profile')
        } finally {
            setSaving(false)
        }
    }

    const handlePasswordSave = async (e) => {
        e.preventDefault()
        setPasswordError('')
        if (newPassword.length < 8) {
            setPasswordError('Password must be at least 8 characters.')
            return
        }
        if (newPassword !== confirmPassword) {
            setPasswordError('Passwords do not match.')
            return
        }
        setSavingPassword(true)
        try {
            await updateProfile({ password: newPassword })
            showToast('Password updated! 🔒')
            setNewPassword('')
            setConfirmPassword('')
        } catch (err) {
            setPasswordError(err.message || 'Failed to update password')
        } finally {
            setSavingPassword(false)
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
            aria-labelledby="edit-profile-modal-title"
        >
            <div
                style={{
                    background: 'var(--color-surface)',
                    border: '1px solid var(--color-border)',
                    borderRadius: 'var(--radius-xl)',
                    width: '100%',
                    maxWidth: '480px',
                    maxHeight: 'min(90vh, 750px)',
                    display: 'flex',
                    flexDirection: 'column',
                    boxShadow: '0 20px 40px rgba(0, 0, 0, 0.7), 0 0 30px var(--color-accent-glow)',
                    overflow: 'hidden',
                    position: 'relative',
                }}
                onClick={(e) => e.stopPropagation()}
            >
                {/* Header */}
                <div
                    style={{
                        padding: 'var(--space-4) var(--space-6)',
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
                                border: '1px solid rgba(255, 42, 95, 0.4)',
                                display: 'flex',
                                alignItems: 'center',
                                justifyContent: 'center',
                                fontSize: '1.2rem',
                            }}
                        >
                            ✏️
                        </div>
                        <div>
                            <h3 id="edit-profile-modal-title" style={{ margin: 0, fontSize: 'var(--text-lg)', fontWeight: 600, color: 'var(--color-text)' }}>
                                Edit Screen Name & Profile
                            </h3>
                            <p style={{ margin: 0, fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)' }}>
                                Update your screen name and contact email
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
                    onSubmit={handleSave}
                    style={{
                        display: 'flex',
                        flexDirection: 'column',
                        flex: 1,
                        minHeight: 0,
                        overflow: 'hidden',
                    }}
                >
                    {/* Scrollable Form Content */}
                    <div
                        style={{
                            flex: 1,
                            overflowY: 'auto',
                            WebkitOverflowScrolling: 'touch',
                            padding: 'var(--space-6)',
                        }}
                    >
                        {error && <div style={{ marginBottom: 'var(--space-4)' }}><ErrorBanner message={error} /></div>}

                        <div style={{ marginBottom: 'var(--space-4)' }}>
                            <label
                                for="edit-display-name"
                                style={{
                                    display: 'block',
                                    marginBottom: 'var(--space-2)',
                                    fontSize: 'var(--text-sm)',
                                    fontWeight: 500,
                                    color: 'var(--color-text)',
                                }}
                            >
                                Screen Name / Stage Name <span style={{ color: 'var(--color-accent)' }}>*</span>
                            </label>
                            <input
                                id="edit-display-name"
                                type="text"
                                maxLength={50}
                                value={displayName}
                                onInput={(e) => setDisplayName(e.target.value)}
                                placeholder="Your screen name (e.g. KJ Dave or Sarah)"
                                style={{
                                    width: '100%',
                                    padding: 'var(--space-3)',
                                    borderRadius: 'var(--radius-md)',
                                    background: 'rgba(0, 0, 0, 0.3)',
                                    border: '1px solid var(--color-border)',
                                    color: 'var(--color-text)',
                                    fontSize: 'var(--text-sm)',
                                }}
                                autoFocus
                                required
                            />
                            <span style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', display: 'block', marginTop: '4px' }}>
                                Your public name shown on the TV stage screen and the live queue
                            </span>
                        </div>

                        <div style={{ marginBottom: 'var(--space-6)' }}>
                            <label
                                for="edit-email"
                                style={{
                                    display: 'block',
                                    marginBottom: 'var(--space-2)',
                                    fontSize: 'var(--text-sm)',
                                    fontWeight: 500,
                                    color: 'var(--color-text)',
                                }}
                            >
                                Email Address
                            </label>
                            <input
                                id="edit-email"
                                type="email"
                                maxLength={320}
                                value={email}
                                onInput={(e) => setEmail(e.target.value)}
                                placeholder="you@example.com"
                                style={{
                                    width: '100%',
                                    padding: 'var(--space-3)',
                                    borderRadius: 'var(--radius-md)',
                                    background: 'rgba(0, 0, 0, 0.3)',
                                    border: '1px solid var(--color-border)',
                                    color: 'var(--color-text-main)',
                                    fontSize: 'var(--text-sm)',
                                }}
                            />
                            <span style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', display: 'block', marginTop: '4px' }}>
                                Used for account sign in and critical alerts
                            </span>
                        </div>

                        {/* Account Role / Mode Selector */}
                        <div style={{ marginBottom: 'var(--space-6)' }}>
                            <label
                                style={{
                                    display: 'block',
                                    marginBottom: 'var(--space-2)',
                                    fontSize: 'var(--text-sm)',
                                    fontWeight: 500,
                                    color: 'var(--color-text)',
                                }}
                            >
                                Account Mode
                            </label>
                            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--space-3)' }}>
                                <button
                                    type="button"
                                    onClick={() => setIsHostRole(false)}
                                    style={{
                                        display: 'flex',
                                        flexDirection: 'column',
                                        alignItems: 'center',
                                        textAlign: 'center',
                                        padding: 'var(--space-3)',
                                        borderRadius: 'var(--radius-md)',
                                        border: !isHostRole ? '2px solid var(--color-info)' : '1px solid var(--color-border)',
                                        background: !isHostRole ? 'rgba(6, 182, 212, 0.15)' : 'rgba(0, 0, 0, 0.2)',
                                        color: 'var(--color-text)',
                                        cursor: 'pointer',
                                        transition: 'all var(--transition-fast)',
                                    }}
                                >
                                    <span style={{ fontSize: '1.4rem', marginBottom: '4px' }}>🎵</span>
                                    <span style={{ fontWeight: 600, fontSize: 'var(--text-sm)' }}>Singer</span>
                                    <span style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', marginTop: '2px' }}>
                                        Browse & request songs
                                    </span>
                                </button>
                                <button
                                    type="button"
                                    onClick={() => setIsHostRole(true)}
                                    style={{
                                        display: 'flex',
                                        flexDirection: 'column',
                                        alignItems: 'center',
                                        textAlign: 'center',
                                        padding: 'var(--space-3)',
                                        borderRadius: 'var(--radius-md)',
                                        border: isHostRole ? '2px solid var(--color-chorus)' : '1px solid var(--color-border)',
                                        background: isHostRole ? 'rgba(245, 158, 11, 0.15)' : 'rgba(0, 0, 0, 0.2)',
                                        color: 'var(--color-text)',
                                        cursor: 'pointer',
                                        transition: 'all var(--transition-fast)',
                                    }}
                                >
                                    <span style={{ fontSize: '1.4rem', marginBottom: '4px' }}>🎤</span>
                                    <span style={{ fontWeight: 600, fontSize: 'var(--text-sm)' }}>KJ Host</span>
                                    <span style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', marginTop: '2px' }}>
                                        Host shows & live queue
                                    </span>
                                </button>
                            </div>
                            <span style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', display: 'block', marginTop: '6px' }}>
                                You can switch back and forth anytime. Your library and events are always saved.
                            </span>
                        </div>

                        {/* Password Section */}
                        <div style={{
                            marginTop: 'var(--space-6)',
                            paddingTop: 'var(--space-5)',
                            borderTop: '1px solid var(--color-border)',
                            marginBottom: 'var(--space-6)',
                        }}>
                            <h4 style={{ margin: '0 0 var(--space-2) 0', fontSize: 'var(--text-sm)', fontWeight: 600, color: 'var(--color-text)' }}>
                                Password
                            </h4>
                            <p style={{ margin: '0 0 var(--space-4) 0', fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', lineHeight: 1.4 }}>
                                Set or change your password. Leave blank to keep using passkeys only.
                            </p>
                            {passwordError && <div style={{ marginBottom: 'var(--space-3)' }}><ErrorBanner message={passwordError} /></div>}
                            <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-3)' }}>
                                <input
                                    id="edit-new-password"
                                    type="password"
                                    autocomplete="new-password"
                                    maxLength={128}
                                    placeholder="New password (min 8 characters)"
                                    value={newPassword}
                                    onInput={(e) => setNewPassword(e.target.value)}
                                    onKeyDown={(e) => {
                                        if (e.key === 'Enter') {
                                            e.preventDefault()
                                            handlePasswordSave(e)
                                        }
                                    }}
                                    style={{
                                        width: '100%',
                                        padding: 'var(--space-3)',
                                        borderRadius: 'var(--radius-md)',
                                        background: 'rgba(0, 0, 0, 0.3)',
                                        border: '1px solid var(--color-border)',
                                        color: 'var(--color-text)',
                                        fontSize: 'var(--text-sm)',
                                    }}
                                />
                                <input
                                    id="edit-confirm-password"
                                    type="password"
                                    autocomplete="new-password"
                                    maxLength={128}
                                    placeholder="Confirm new password"
                                    value={confirmPassword}
                                    onInput={(e) => setConfirmPassword(e.target.value)}
                                    onKeyDown={(e) => {
                                        if (e.key === 'Enter') {
                                            e.preventDefault()
                                            handlePasswordSave(e)
                                        }
                                    }}
                                    style={{
                                        width: '100%',
                                        padding: 'var(--space-3)',
                                        borderRadius: 'var(--radius-md)',
                                        background: 'rgba(0, 0, 0, 0.3)',
                                        border: '1px solid var(--color-border)',
                                        color: 'var(--color-text)',
                                        fontSize: 'var(--text-sm)',
                                    }}
                                />
                                <button
                                    id="btn-save-password"
                                    type="button"
                                    onClick={handlePasswordSave}
                                    class="btn btn-secondary"
                                    disabled={savingPassword || !newPassword}
                                    style={{ alignSelf: 'flex-end', fontSize: 'var(--text-xs)', padding: '6px 14px', minHeight: '36px', display: 'inline-flex', alignItems: 'center', gap: '6px' }}
                                >
                                    {savingPassword && <Spinner size="12px" />}
                                    <span>{savingPassword ? 'Saving...' : '🔒 Set Password'}</span>
                                </button>
                            </div>
                        </div>

                        {/* Data & Privacy Section */}
                        <div style={{
                            marginTop: 'var(--space-6)',
                            paddingTop: 'var(--space-5)',
                            borderTop: '1px solid var(--color-border)',
                            marginBottom: 'var(--space-2)',
                        }}>
                            <h4 style={{ margin: '0 0 var(--space-2) 0', fontSize: 'var(--text-sm)', fontWeight: 600, color: 'var(--color-text)' }}>
                                Account Data & Privacy
                            </h4>
                            <p style={{ margin: '0 0 var(--space-4) 0', fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', lineHeight: 1.4 }}>
                                Download a copy of your information or permanently delete your account.
                            </p>

                            <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-3)' }}>
                                <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: 'var(--space-3)', background: 'rgba(255, 255, 255, 0.03)', borderRadius: 'var(--radius-md)', border: '1px solid var(--color-border)' }}>
                                    <div>
                                        <div style={{ fontSize: 'var(--text-xs)', fontWeight: 500, color: 'var(--color-text)' }}>
                                            Download My Data
                                        </div>
                                        <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)' }}>
                                            Download all profile, songbook, and event records (JSON)
                                        </div>
                                    </div>
                                    <button
                                        id="btn-export-data"
                                        type="button"
                                        onClick={handleExportData}
                                        disabled={exporting}
                                        class="btn btn-secondary"
                                        style={{ fontSize: 'var(--text-xs)', padding: '6px 12px', display: 'inline-flex', alignItems: 'center', gap: '6px' }}
                                    >
                                        {exporting && <Spinner size="12px" />}
                                        <span>{exporting ? 'Exporting...' : '📥 Download JSON'}</span>
                                    </button>
                                </div>

                                <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: 'var(--space-3)', background: 'rgba(239, 68, 68, 0.05)', borderRadius: 'var(--radius-md)', border: '1px solid rgba(239, 68, 68, 0.2)' }}>
                                    <div>
                                        <div style={{ fontSize: 'var(--text-xs)', fontWeight: 500, color: '#f87171' }}>
                                            Delete Account
                                        </div>
                                        <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)' }}>
                                            Permanently delete your account and all associated data
                                        </div>
                                    </div>
                                    <button
                                        id="btn-open-delete-account"
                                        type="button"
                                        onClick={() => setIsDeleteModalOpen(true)}
                                        style={{
                                            background: 'rgba(239, 68, 68, 0.15)',
                                            border: '1px solid rgba(239, 68, 68, 0.3)',
                                            color: '#f87171',
                                            padding: '6px 12px',
                                            borderRadius: 'var(--radius-md)',
                                            fontSize: 'var(--text-xs)',
                                            fontWeight: 600,
                                            cursor: 'pointer',
                                        }}
                                    >
                                        Delete Account
                                    </button>
                                </div>
                            </div>
                        </div>
                    </div>

                    {/* Pinned Action Buttons */}
                    <div
                        style={{
                            padding: 'var(--space-4) var(--space-6)',
                            borderTop: '1px solid var(--color-border)',
                            background: 'var(--color-surface)',
                            display: 'flex',
                            gap: 'var(--space-3)',
                            justifyContent: 'flex-end',
                            flexShrink: 0,
                        }}
                    >
                        <button
                            type="button"
                            onClick={onClose}
                            class="btn btn-secondary"
                            disabled={saving}
                        >
                            Cancel
                        </button>
                        <button
                            id="btn-save-profile"
                            type="submit"
                            class="btn btn-primary"
                            disabled={saving}
                            style={{ minWidth: '110px', display: 'inline-flex', alignItems: 'center', justifyContent: 'center', gap: '6px' }}
                        >
                            {saving && <Spinner size="14px" />}
                            <span>{saving ? 'Saving...' : 'Save Changes'}</span>
                        </button>
                    </div>
                </form>
            </div>

            <DeleteAccountModal
                isOpen={isDeleteModalOpen}
                onClose={() => setIsDeleteModalOpen(false)}
            />
        </div>
    )
}
