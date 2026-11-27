import { useState, useRef, useCallback } from 'preact/hooks'
import { user, logout, isLoggedIn, requestEmailVerification, toggleHostMode } from '../auth/store'
import { BrandIcon } from './BrandIcon'
import { PasskeyModal } from './PasskeyModal'
import { EditProfileModal } from './EditProfileModal'
import { DeveloperKeysModal } from './DeveloperKeysModal'
import { showToast } from './Toast'
import {
    normalizeHeaderPath,
    getInitials,
    useFavoritesCount,
    useDropdownDismiss,
    ProfileAvatarButton,
    HeaderDropdown,
    EmailVerificationBanner,
} from './HeaderMenu'
import './Header.css'

export function Header({ currentPath = '' }) {
    const normalizedPath = normalizeHeaderPath(currentPath)

    const [showPasskeys, setShowPasskeys] = useState(false)
    const [showEditProfile, setShowEditProfile] = useState(false)
    const [showDeveloperKeys, setShowDeveloperKeys] = useState(false)
    const [dropdownOpen, setDropdownOpen] = useState(false)
    const [sendingVerification, setSendingVerification] = useState(false)
    const [switchingRole, setSwitchingRole] = useState(false)
    const menuRef = useRef(null)

    const loggedIn = isLoggedIn.value
    const currentUser = user.value
    const favoritesCount = useFavoritesCount()

    const closeDropdown = useCallback(() => setDropdownOpen(false), [])
    useDropdownDismiss(dropdownOpen, menuRef, closeDropdown)

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
        const nextIsHost = !currentUser?.is_host
        try {
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
                            <ProfileAvatarButton
                                loggedIn={loggedIn}
                                currentUser={currentUser}
                                initials={initials}
                                dropdownOpen={dropdownOpen}
                                onToggle={() => setDropdownOpen(!dropdownOpen)}
                            />

                            {dropdownOpen && (
                                <HeaderDropdown
                                    loggedIn={loggedIn}
                                    currentUser={currentUser}
                                    initials={initials}
                                    normalizedPath={normalizedPath}
                                    favoritesCount={favoritesCount}
                                    switchingRole={switchingRole}
                                    onClose={closeDropdown}
                                    onToggleRole={handleToggleRole}
                                    onOpenEditProfile={() => { setDropdownOpen(false); setShowEditProfile(true) }}
                                    onOpenPasskeys={() => { setDropdownOpen(false); setShowPasskeys(true) }}
                                    onOpenDeveloperKeys={() => { setDropdownOpen(false); setShowDeveloperKeys(true) }}
                                    onLogout={handleLogout}
                                />
                            )}
                        </div>
                    </div>
                </nav>
            </header>
            <EmailVerificationBanner
                loggedIn={loggedIn}
                currentUser={currentUser}
                sendingVerification={sendingVerification}
                onSendVerification={handleSendVerification}
            />
            <PasskeyModal isOpen={showPasskeys} onClose={() => setShowPasskeys(false)} />
            <EditProfileModal isOpen={showEditProfile} onClose={() => setShowEditProfile(false)} />
            <DeveloperKeysModal isOpen={showDeveloperKeys} onClose={() => setShowDeveloperKeys(false)} />
        </>
    )
}
