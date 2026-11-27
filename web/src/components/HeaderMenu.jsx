import { useState, useEffect } from 'preact/hooks'
import {
    FAVORITE_SONGS_STORAGE_KEY,
    FAVORITE_ARTISTS_STORAGE_KEY,
    getFavoritesCount,
} from '../utils/favoritesStore'

export function normalizeHeaderPath(currentPath) {
    const activePath = currentPath || (typeof window !== 'undefined' ? window.location.pathname : '')
    const pathOnly = activePath.split(/[?#]/)[0]
    let pathEnd = pathOnly.length
    while (pathEnd > 0 && pathOnly[pathEnd - 1] === '/') {
        pathEnd--
    }
    return pathOnly.slice(0, pathEnd) || '/'
}

export function getInitials(name) {
    if (!name) return '?'
    const parts = name.trim().split(/\s+/)
    if (parts.length >= 2) {
        return (parts[0][0] + parts[1][0]).toUpperCase()
    }
    return name.slice(0, 2).toUpperCase()
}

export function useFavoritesCount() {
    const [favoritesCount, setFavoritesCount] = useState(() => getFavoritesCount().total)

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

    return favoritesCount
}

export function useDropdownDismiss(dropdownOpen, menuRef, onClose) {
    useEffect(() => {
        if (!dropdownOpen) return
        const handleClickOutside = (e) => {
            if (menuRef.current && !menuRef.current.contains(e.target)) {
                onClose()
            }
        }
        const handleKeyDown = (e) => {
            if (e.key === 'Escape') onClose()
        }
        document.addEventListener('mousedown', handleClickOutside)
        document.addEventListener('keydown', handleKeyDown)
        return () => {
            document.removeEventListener('mousedown', handleClickOutside)
            document.removeEventListener('keydown', handleKeyDown)
        }
    }, [dropdownOpen, menuRef, onClose])
}

function getAvatarTitle(loggedIn, currentUser) {
    if (!loggedIn) return 'Menu'
    return currentUser?.display_name || 'Profile & Settings'
}

function GuestMenuIcon() {
    return (
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
    )
}

function LoggedInAvatarContent({ currentUser, initials }) {
    return (
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
    )
}

export function ProfileAvatarButton({ loggedIn, currentUser, initials, dropdownOpen, onToggle }) {
    return (
        <button
            id="btn-profile-avatar"
            type="button"
            class={`profile-avatar-btn${loggedIn ? '' : ' guest'}`}
            onClick={onToggle}
            aria-label={loggedIn ? 'User Profile and Settings' : 'Menu'}
            aria-expanded={dropdownOpen}
            title={getAvatarTitle(loggedIn, currentUser)}
        >
            {loggedIn ? (
                <LoggedInAvatarContent currentUser={currentUser} initials={initials} />
            ) : (
                <GuestMenuIcon />
            )}
        </button>
    )
}

function EmailVerificationBadge({ email, emailVerified }) {
    if (!email) return null
    if (emailVerified) {
        return (
            <span
                title="Email Verified"
                style={{
                    fontSize: 'var(--text-xs)',
                    color: 'var(--color-success)',
                    background: 'rgba(16, 185, 129, 0.15)',
                    padding: '1px 6px',
                    borderRadius: '10px',
                    border: '1px solid rgba(16, 185, 129, 0.35)',
                    fontWeight: 600,
                }}
            >
                ✓
            </span>
        )
    }
    return (
        <span
            title="Email Unverified"
            style={{
                fontSize: 'var(--text-xs)',
                color: 'var(--color-warning)',
                background: 'rgba(245, 158, 11, 0.15)',
                padding: '1px 6px',
                borderRadius: '10px',
                border: '1px solid rgba(245, 158, 11, 0.35)',
                fontWeight: 700,
            }}
        >
            !
        </span>
    )
}

function FavoritesMenuItem({ normalizedPath, favoritesCount, onClose }) {
    const isFavorites = normalizedPath === '/favorites'
    return (
        <a
            href="/favorites"
            class={`profile-dropdown-item${isFavorites ? ' active' : ''}`}
            aria-current={isFavorites ? 'page' : undefined}
            onClick={onClose}
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
    )
}

function DocsAndSupportLinks({ onClose }) {
    return (
        <>
            <a
                href="/docs"
                class="profile-dropdown-item"
                onClick={onClose}
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
                onClick={onClose}
                role="menuitem"
            >
                <div class="profile-item-left">
                    <span class="profile-item-icon">💬</span>
                    <span class="profile-item-label">Discord Support</span>
                </div>
                <span class="profile-item-arrow">↗</span>
            </a>
        </>
    )
}

function HostControlsSection({ onClose }) {
    return (
        <>
            <div class="profile-dropdown-section">
                <div class="profile-dropdown-title">Host Controls</div>
                <a
                    href="/kj/queue"
                    class="profile-dropdown-item"
                    onClick={onClose}
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
                    onClick={onClose}
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
    )
}

function AccountSettingsSection({
    currentUser,
    switchingRole,
    onToggleRole,
    onOpenEditProfile,
    onOpenPasskeys,
    onOpenDeveloperKeys,
}) {
    const isHost = Boolean(currentUser?.is_host)
    return (
        <div class="profile-dropdown-section">
            <div class="profile-dropdown-title">Account Settings</div>

            <button
                id="btn-profile-toggle-role"
                type="button"
                class="profile-dropdown-item"
                onClick={onToggleRole}
                disabled={switchingRole}
                role="menuitem"
            >
                <div class="profile-item-left">
                    <span class="profile-item-icon">{isHost ? '🎵' : '🎤'}</span>
                    <div>
                        <div class="profile-item-label">
                            {isHost ? 'Switch to Singer Mode' : 'Switch to Host (KJ) Mode'}
                        </div>
                        <div class="profile-item-desc">
                            {isHost ? 'Browse songbooks as a singer' : 'Host live shows, manage songbooks & queue'}
                        </div>
                    </div>
                </div>
                <span class="profile-item-arrow">⇄</span>
            </button>

            <button
                id="btn-profile-edit"
                type="button"
                class="profile-dropdown-item"
                onClick={onOpenEditProfile}
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

            <button
                id="btn-profile-passkeys"
                type="button"
                class="profile-dropdown-item"
                onClick={onOpenPasskeys}
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

            {isHost && (
                <button
                    id="btn-profile-developer-keys"
                    type="button"
                    class="profile-dropdown-item"
                    onClick={onOpenDeveloperKeys}
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
    )
}

function LoggedInDropdownMenu({
    currentUser,
    initials,
    normalizedPath,
    favoritesCount,
    switchingRole,
    onClose,
    onToggleRole,
    onOpenEditProfile,
    onOpenPasskeys,
    onOpenDeveloperKeys,
    onLogout,
}) {
    return (
        <>
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
                        <EmailVerificationBadge
                            email={currentUser?.email}
                            emailVerified={currentUser?.email_verified}
                        />
                    </div>
                </div>
            </div>

            <div class="profile-dropdown-divider" />

            {currentUser?.is_host && <HostControlsSection onClose={onClose} />}

            <div class="profile-dropdown-section">
                <FavoritesMenuItem
                    normalizedPath={normalizedPath}
                    favoritesCount={favoritesCount}
                    onClose={onClose}
                />
            </div>

            <div class="profile-dropdown-divider" />

            <AccountSettingsSection
                currentUser={currentUser}
                switchingRole={switchingRole}
                onToggleRole={onToggleRole}
                onOpenEditProfile={onOpenEditProfile}
                onOpenPasskeys={onOpenPasskeys}
                onOpenDeveloperKeys={onOpenDeveloperKeys}
            />

            <div class="profile-dropdown-divider" />

            <DocsAndSupportLinks onClose={onClose} />

            <div class="profile-dropdown-divider" />

            <button
                id="btn-profile-logout"
                type="button"
                class="profile-dropdown-item profile-logout-item"
                onClick={onLogout}
                role="menuitem"
            >
                <div class="profile-item-left">
                    <span class="profile-item-icon">🚪</span>
                    <span class="profile-item-label">Log Out</span>
                </div>
            </button>
        </>
    )
}

function GuestDropdownMenu({ normalizedPath, favoritesCount, onClose }) {
    return (
        <>
            <div class="profile-dropdown-section">
                <FavoritesMenuItem
                    normalizedPath={normalizedPath}
                    favoritesCount={favoritesCount}
                    onClose={onClose}
                />
                <DocsAndSupportLinks onClose={onClose} />
            </div>

            <div class="profile-dropdown-divider" />

            <div class="profile-dropdown-section">
                <a
                    href="/login"
                    class="profile-dropdown-item"
                    onClick={onClose}
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
    )
}

export function HeaderDropdown({ loggedIn, ...props }) {
    return (
        <div class="profile-dropdown" role="menu">
            {loggedIn ? (
                <LoggedInDropdownMenu {...props} />
            ) : (
                <GuestDropdownMenu
                    normalizedPath={props.normalizedPath}
                    favoritesCount={props.favoritesCount}
                    onClose={props.onClose}
                />
            )}
        </div>
    )
}

export function EmailVerificationBanner({ loggedIn, currentUser, sendingVerification, onSendVerification }) {
    if (!loggedIn || !currentUser?.email || currentUser.email_verified) {
        return null
    }
    return (
        <div
            id="email-verification-banner"
            style={{
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
            }}
        >
            <span>✉️ Your email (<strong>{currentUser.email}</strong>) is unverified.</span>
            <button
                id="btn-header-verify-resend"
                type="button"
                onClick={onSendVerification}
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
    )
}
