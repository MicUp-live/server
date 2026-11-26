import { useState, useEffect, useRef } from 'preact/hooks'
import { user, logout, isLoggedIn, requestEmailVerification, toggleHostMode } from '../auth/store'
import { BrandIcon } from './BrandIcon'
import { PasskeyModal } from './PasskeyModal'
import { EditProfileModal } from './EditProfileModal'
import { DeveloperKeysModal } from './DeveloperKeysModal'
import { showToast } from './Toast'
import {
    FAVORITE_SONGS_STORAGE_KEY,
    FAVORITE_ARTISTS_STORAGE_KEY,
    getFavoritesCount,
} from '../utils/favoritesStore'
import './Header.css'

export function Header({ currentPath = '' }) {
    const activePath = currentPath || (typeof window !== 'undefined' ? window.location.pathname : '')
    const normalizedPath = activePath.split(/[?#]/)[0].replace(/\/+$/, '') || '/'

    const [showPasskeys, setShowPasskeys] = useState(false)
    const [showEditProfile, setShowEditProfile] = useState(false)
    const [showDeveloperKeys, setShowDeveloperKeys] = useState(false)
    const [dropdownOpen, setDropdownOpen] = useState(false)
    const [sendingVerification, setSendingVerification] = useState(false)
    const [switchingRole, setSwitchingRole] = useState(false)
    const [favoritesCount, setFavoritesCount] = useState(() => getFavoritesCount().total)
    const menuRef = useRef(null)

    const loggedIn = isLoggedIn.value
    const currentUser = user.value

    useEffect(() => {
        const syncFavs = () => setFavoritesCount(getFavoritesCount().total)
        const handleStorage = (e) => {
            if (!e.key || e.key === FAVORITE_SONGS_STORAGE_KEY || e.key === FAVORITE_ARTISTS_STORAGE_KEY) {
                syncFavs()
            }
        }
        window.addEventListener('micup_favorites_changed', syncFavs)
        window.addEventListener('storage', handleStorage)
        return () => {
            window.removeEventListener('micup_favorites_changed', syncFavs)
            window.removeEventListener('storage', handleStorage)
        }
    }, [])

    useEffect(() => {
        if (!dropdownOpen) return
        const handleClickOutside = (e) => {
            if (menuRef.current && !menuRef.current.contains(e.target)) {
                setDropdownOpen(false)
            }
        }
        const handleKeyDown = (e) => {
            if (e.key === 'Escape') setDropdownOpen(false)
        }
        document.addEventListener('mousedown', handleClickOutside)
        document.addEventListener('keydown', handleKeyDown)
        return () => {
            document.removeEventListener('mousedown', handleClickOutside)
            document.removeEventListener('keydown', handleKeyDown)
        }
    }, [dropdownOpen])

    const getInitials = (name) => {
        if (!name) return '?'
        const parts = name.trim().split(/\s+/)
        if (parts.length >= 2) {
            return (parts[0][0] + parts[1][0]).toUpperCase()
        }
        return name.slice(0, 2).toUpperCase()
    }

    const handleLogout = async () => {
        setDropdownOpen(false)
        await logout()
        window.location.href = '/'
    }

    const handleSendVerification = async () => {
        if (sendingVerification) return
        setSendingVerification(true)
        try {
            await requestEmailVerification()
            showToast('Verification email sent! Please check your inbox. ✉️')
        } catch (err) {
            showToast(err.message || 'Failed to send verification email', 'error')
        } finally {
            setSendingVerification(false)
        }
    }

    const handleToggleRole = async () => {
        if (switchingRole) return
        setSwitchingRole(true)
        try {
            const nextIsHost = !currentUser?.is_host
            await toggleHostMode()
            showToast(nextIsHost ? 'Switched to KJ Host mode! 🎤' : 'Switched to Singer mode! 🎵')
        } catch (err) {
            showToast(err.message || 'Failed to switch account mode', 'error')
        } finally {
            setSwitchingRole(false)
        }
    }

    const initials = getInitials(currentUser?.display_name || currentUser?.email || 'Singer')

    if (normalizedPath.startsWith('/stage')) {
        return null
    }

    return (
        <>
            <header class="app-header">
                <a href="/" class="logo" aria-label="MicUp.live Home">
                    <BrandIcon size={26} glowing />
                    <span class="logo-text">MicUp<span class="dot">.live</span></span>
                </a>
                <nav class="header-nav" aria-label="Main navigation">
                    <div class="header-user">
                        <div class="profile-menu-container" ref={menuRef}>
                            <button
                                id="btn-profile-avatar"
                                type="button"
                                class={`profile-avatar-btn${loggedIn ? '' : ' guest'}`}
                                onClick={() => setDropdownOpen(!dropdownOpen)}
                                aria-label={loggedIn ? 'User Profile and Settings' : 'Menu'}
                                aria-expanded={dropdownOpen}
                                title={loggedIn ? (currentUser?.display_name || 'Profile & Settings') : 'Menu'}
                            >
                                {loggedIn ? (
                                    <>
                                        {currentUser?.avatar_url ? (
                                            <img src={currentUser.avatar_url} alt="" class="profile-avatar-img" />
                                        ) : (
                                            <span class="profile-avatar-initials">{initials}</span>
                                        )}
                                        {currentUser?.is_host && (
                                            <span class="profile-host-badge" title="KJ Host">🎤</span>
                                        )}
                                    </>
                                ) : (
                                    <svg
                                        class="profile-guest-icon"
                                        width="20"
                                        height="20"
                                        viewBox="0 0 24 24"
                                        fill="none"
                                        stroke="currentColor"
                                        stroke-width="2"
                                        stroke-linecap="round"
                                        stroke-linejoin="round"
                                        aria-hidden="true"
                                    >
                                        <line x1="4" y1="7" x2="20" y2="7" />
                                        <line x1="4" y1="12" x2="20" y2="12" />
                                        <line x1="4" y1="17" x2="20" y2="17" />
                                    </svg>
                                )}
                            </button>

                            {dropdownOpen && (
                                <div class="profile-dropdown" role="menu">
                                    {loggedIn ? (
                                        <>
                                            {/* Dropdown Header Info */}
                                            <div class="profile-dropdown-header">
                                                <div class="profile-dropdown-avatar">
                                                    {currentUser?.avatar_url ? (
                                                        <img src={currentUser.avatar_url} alt="" style={{ width: '100%', height: '100%', borderRadius: '50%' }} />
                                                    ) : (
                                                        <span>{initials}</span>
                                                    )}
                                                </div>
                                                <div class="profile-dropdown-info">
                                                    <div class="profile-dropdown-name">{currentUser?.display_name || 'Singer'}</div>
                                                    <div style={{ display: 'flex', alignItems: 'center', gap: '6px', flexWrap: 'wrap', marginTop: '2px' }}>
                                                        {currentUser?.email && (
                                                            <span class="profile-dropdown-email">{currentUser.email}</span>
                                                        )}
                                                        {currentUser?.email && (
                                                            currentUser.email_verified ? (
                                                                <span title="Email Verified" style={{ fontSize: 'var(--text-xs)', color: 'var(--color-success)', background: 'rgba(16, 185, 129, 0.15)', padding: '1px 6px', borderRadius: '10px', border: '1px solid rgba(16, 185, 129, 0.35)', fontWeight: 600 }}>
                                                                    ✓
                                                                </span>
                                                            ) : (
                                                                <span title="Email Unverified" style={{ fontSize: 'var(--text-xs)', color: 'var(--color-warning)', background: 'rgba(245, 158, 11, 0.15)', padding: '1px 6px', borderRadius: '10px', border: '1px solid rgba(245, 158, 11, 0.35)', fontWeight: 700 }}>
                                                                    !
                                                                </span>
                                                            )
                                                        )}
                                                    </div>
                                                </div>
                                            </div>

                                            <div class="profile-dropdown-divider" />

                                            {/* Host Controls (Elevated for direct navigation) */}
                                            {currentUser?.is_host && (
                                                <>
                                                    <div class="profile-dropdown-section">
                                                        <div class="profile-dropdown-title">Host Controls</div>
                                                        <a
                                                            href="/kj/queue"
                                                            class="profile-dropdown-item"
                                                            onClick={() => setDropdownOpen(false)}
                                                            role="menuitem"
                                                        >
                                                            <div class="profile-item-left">
                                                                <span class="profile-item-icon">🎤</span>
                                                                <div>
                                                                    <div class="profile-item-label">Live Events & Queue</div>
                                                                    <div class="profile-item-desc">Run shows, manage rotation & stage</div>
                                                                </div>
                                                            </div>
                                                            <span class="profile-item-arrow">›</span>
                                                        </a>
                                                        <a
                                                            href="/kj/songbooks"
                                                            class="profile-dropdown-item"
                                                            onClick={() => setDropdownOpen(false)}
                                                            role="menuitem"
                                                        >
                                                            <div class="profile-item-left">
                                                                <span class="profile-item-icon">📚</span>
                                                                <div>
                                                                    <div class="profile-item-label">Songbooks Catalog</div>
                                                                    <div class="profile-item-desc">Upload, edit & sync master songbooks</div>
                                                                </div>
                                                            </div>
                                                            <span class="profile-item-arrow">›</span>
                                                        </a>
                                                    </div>
                                                    <div class="profile-dropdown-divider" />
                                                </>
                                            )}

                                            {/* Singer Repertoire / Favorites */}
                                            <div class="profile-dropdown-section">
                                                <a
                                                    href="/favorites"
                                                    class={`profile-dropdown-item${normalizedPath === '/favorites' ? ' active' : ''}`}
                                                    aria-current={normalizedPath === '/favorites' ? 'page' : undefined}
                                                    onClick={() => setDropdownOpen(false)}
                                                    role="menuitem"
                                                >
                                                    <div class="profile-item-left">
                                                        <span class="profile-item-icon profile-item-star" aria-hidden="true">★</span>
                                                        <div>
                                                            <div class="profile-item-label-row">
                                                                <span class="profile-item-label">My Favorites</span>
                                                                {favoritesCount > 0 && (
                                                                    <span class="profile-item-badge">{favoritesCount}</span>
                                                                )}
                                                            </div>
                                                            <div class="profile-item-desc">Saved songs, artists &amp; pitch keys</div>
                                                        </div>
                                                    </div>
                                                    <span class="profile-item-arrow">›</span>
                                                </a>
                                            </div>

                                            <div class="profile-dropdown-divider" />

                                            {/* Profile Settings Section */}
                                            <div class="profile-dropdown-section">
                                                <div class="profile-dropdown-title">Account Settings</div>

                                                {/* Switch Role / Mode */}
                                                <button
                                                    id="btn-profile-toggle-role"
                                                    type="button"
                                                    class="profile-dropdown-item"
                                                    onClick={handleToggleRole}
                                                    disabled={switchingRole}
                                                    role="menuitem"
                                                >
                                                    <div class="profile-item-left">
                                                        <span class="profile-item-icon">{currentUser?.is_host ? '🎵' : '🎤'}</span>
                                                        <div>
                                                            <div class="profile-item-label">
                                                                {currentUser?.is_host ? 'Switch to Singer Mode' : 'Switch to Host (KJ) Mode'}
                                                            </div>
                                                            <div class="profile-item-desc">
                                                                {currentUser?.is_host ? 'Browse songbooks as a singer' : 'Host live shows, manage songbooks & queue'}
                                                            </div>
                                                        </div>
                                                    </div>
                                                    <span class="profile-item-arrow">⇄</span>
                                                </button>

                                                {/* Edit Profile & Screen Name */}
                                                <button
                                                    id="btn-profile-edit"
                                                    type="button"
                                                    class="profile-dropdown-item"
                                                    onClick={() => { setDropdownOpen(false); setShowEditProfile(true) }}
                                                    role="menuitem"
                                                >
                                                    <div class="profile-item-left">
                                                        <span class="profile-item-icon">✏️</span>
                                                        <div>
                                                            <div class="profile-item-label">Change Screen Name & Profile</div>
                                                            <div class="profile-item-desc">Change stage/screen name & email</div>
                                                        </div>
                                                    </div>
                                                    <span class="profile-item-arrow">›</span>
                                                </button>

                                                {/* Passkeys & Biometrics */}
                                                <button
                                                    id="btn-profile-passkeys"
                                                    type="button"
                                                    class="profile-dropdown-item"
                                                    onClick={() => { setDropdownOpen(false); setShowPasskeys(true) }}
                                                    role="menuitem"
                                                >
                                                    <div class="profile-item-left">
                                                        <span class="profile-item-icon">🔑</span>
                                                        <div>
                                                            <div class="profile-item-label">Passkeys & Biometrics</div>
                                                            <div class="profile-item-desc">Manage Touch ID, Face ID, keys</div>
                                                        </div>
                                                    </div>
                                                    <span class="profile-item-arrow">›</span>
                                                </button>

                                                {/* Developer API Keys (Host Only) */}
                                                {currentUser?.is_host && (
                                                    <button
                                                        id="btn-profile-developer-keys"
                                                        type="button"
                                                        class="profile-dropdown-item"
                                                        onClick={() => { setDropdownOpen(false); setShowDeveloperKeys(true) }}
                                                        role="menuitem"
                                                    >
                                                        <div class="profile-item-left">
                                                            <span class="profile-item-icon">⚡</span>
                                                            <div>
                                                                <div class="profile-item-label">Developer API Keys</div>
                                                                <div class="profile-item-desc">Personal access tokens for tools & scripts</div>
                                                            </div>
                                                        </div>
                                                        <span class="profile-item-arrow">›</span>
                                                    </button>
                                                )}
                                            </div>

                                            <div class="profile-dropdown-divider" />

                                            {/* Docs Link */}
                                            <a
                                                href="/docs"
                                                class="profile-dropdown-item"
                                                onClick={() => setDropdownOpen(false)}
                                                role="menuitem"
                                            >
                                                <div class="profile-item-left">
                                                    <span class="profile-item-icon">📖</span>
                                                    <span class="profile-item-label">Documentation & Guides</span>
                                                </div>
                                                <span class="profile-item-arrow">›</span>
                                            </a>

                                            <a
                                                href="https://discord.gg/ZUPwCTWXSx"
                                                target="_blank"
                                                rel="noopener noreferrer"
                                                class="profile-dropdown-item"
                                                onClick={() => setDropdownOpen(false)}
                                                role="menuitem"
                                            >
                                                <div class="profile-item-left">
                                                    <span class="profile-item-icon">💬</span>
                                                    <span class="profile-item-label">Discord Support</span>
                                                </div>
                                                <span class="profile-item-arrow">↗</span>
                                            </a>

                                            <div class="profile-dropdown-divider" />

                                            {/* Logout Button */}
                                            <button
                                                id="btn-profile-logout"
                                                type="button"
                                                class="profile-dropdown-item profile-logout-item"
                                                onClick={handleLogout}
                                                role="menuitem"
                                            >
                                                <div class="profile-item-left">
                                                    <span class="profile-item-icon">🚪</span>
                                                    <span class="profile-item-label">Log Out</span>
                                                </div>
                                            </button>
                                        </>
                                    ) : (
                                        <>
                                            <div class="profile-dropdown-section">
                                                <a
                                                    href="/favorites"
                                                    class={`profile-dropdown-item${normalizedPath === '/favorites' ? ' active' : ''}`}
                                                    aria-current={normalizedPath === '/favorites' ? 'page' : undefined}
                                                    onClick={() => setDropdownOpen(false)}
                                                    role="menuitem"
                                                >
                                                    <div class="profile-item-left">
                                                        <span class="profile-item-icon profile-item-star" aria-hidden="true">★</span>
                                                        <div>
                                                            <div class="profile-item-label-row">
                                                                <span class="profile-item-label">My Favorites</span>
                                                                {favoritesCount > 0 && (
                                                                    <span class="profile-item-badge">{favoritesCount}</span>
                                                                )}
                                                            </div>
                                                            <div class="profile-item-desc">Saved songs, artists &amp; pitch keys</div>
                                                        </div>
                                                    </div>
                                                    <span class="profile-item-arrow">›</span>
                                                </a>

                                                <a
                                                    href="/docs"
                                                    class="profile-dropdown-item"
                                                    onClick={() => setDropdownOpen(false)}
                                                    role="menuitem"
                                                >
                                                    <div class="profile-item-left">
                                                        <span class="profile-item-icon">📖</span>
                                                        <span class="profile-item-label">Documentation & Guides</span>
                                                    </div>
                                                    <span class="profile-item-arrow">›</span>
                                                </a>

                                                <a
                                                    href="https://discord.gg/ZUPwCTWXSx"
                                                    target="_blank"
                                                    rel="noopener noreferrer"
                                                    class="profile-dropdown-item"
                                                    onClick={() => setDropdownOpen(false)}
                                                    role="menuitem"
                                                >
                                                    <div class="profile-item-left">
                                                        <span class="profile-item-icon">💬</span>
                                                        <span class="profile-item-label">Discord Support</span>
                                                    </div>
                                                    <span class="profile-item-arrow">↗</span>
                                                </a>
                                            </div>

                                            <div class="profile-dropdown-divider" />

                                            <div class="profile-dropdown-section">
                                                <a
                                                    href="/login"
                                                    class="profile-dropdown-item"
                                                    onClick={() => setDropdownOpen(false)}
                                                    role="menuitem"
                                                >
                                                    <div class="profile-item-left">
                                                        <span class="profile-item-icon">🔑</span>
                                                        <div>
                                                            <div class="profile-item-label">Sign In</div>
                                                            <div class="profile-item-desc">Host shows or manage your account</div>
                                                        </div>
                                                    </div>
                                                    <span class="profile-item-arrow">›</span>
                                                </a>
                                            </div>
                                        </>
                                    )}
                                </div>
                            )}
                        </div>
                    </div>
                </nav>
            </header>
            {loggedIn && currentUser?.email && !currentUser.email_verified && (
                <div id="email-verification-banner" style={{
                    background: 'linear-gradient(90deg, rgba(245, 158, 11, 0.15) 0%, rgba(255, 42, 95, 0.12) 100%)',
                    borderBottom: '1px solid rgba(245, 158, 11, 0.3)',
                    padding: '8px 16px',
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    gap: '12px',
                    fontSize: 'var(--text-xs)',
                    color: 'var(--color-text)',
                    textAlign: 'center',
                    flexWrap: 'wrap',
                }}>
                    <span>✉️ Your email (<strong>{currentUser.email}</strong>) is unverified.</span>
                    <button
                        id="btn-header-verify-resend"
                        type="button"
                        onClick={handleSendVerification}
                        disabled={sendingVerification}
                        style={{
                            background: 'rgba(245, 158, 11, 0.2)',
                            border: '1px solid rgba(245, 158, 11, 0.4)',
                            color: '#fef08a',
                            padding: '4px 12px',
                            minHeight: '32px',
                            borderRadius: 'var(--radius-sm)',
                            cursor: 'pointer',
                            fontWeight: 600,
                            fontSize: 'var(--text-xs)',
                        }}
                    >
                        {sendingVerification ? 'Sending...' : 'Resend Verification Link'}
                    </button>
                </div>
            )}
            <PasskeyModal isOpen={showPasskeys} onClose={() => setShowPasskeys(false)} />
            <EditProfileModal isOpen={showEditProfile} onClose={() => setShowEditProfile(false)} />
            <DeveloperKeysModal isOpen={showDeveloperKeys} onClose={() => setShowDeveloperKeys(false)} />
        </>
    )
}
