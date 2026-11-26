import { useState, useEffect } from 'preact/hooks'
import { route } from 'preact-router'
import { isLoggedIn, isHost } from '../auth/store'
import { BrandIcon } from '../components/BrandIcon'
import { api } from '../api/client'
import {
    FAVORITE_SONGS_STORAGE_KEY,
    FAVORITE_ARTISTS_STORAGE_KEY,
    getFavoritesCount,
} from '../utils/favoritesStore'
import './Home.css'

export function Home() {
    const [bookCode, setBookCode] = useState('')
    const [searching, setSearching] = useState(false)
    const [searchError, setSearchError] = useState('')
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

    const handleOpenBook = async (e) => {
        e.preventDefault()
        const trimmed = bookCode.trim()
        if (!trimmed) return
        setSearchError('')

        // If it's an event URL or book URL
        if (trimmed.includes('/e/')) {
            const code = trimmed.split('/e/')[1].split(/[?#]/)[0]
            route(`/e/${code}`)
            return
        }
        if (trimmed.includes('/book/')) {
            const code = trimmed.split('/book/')[1].split(/[?#]/)[0]
            route(`/book/${code}`)
            return
        }

        // Try resolving as an Event Join Code or Songbook Short Code/Slug (e.g. OMALLEY or DAVEMIC)
        setSearching(true)
        try {
            const data = await api.get(`/join/${encodeURIComponent(trimmed.toUpperCase())}`)
            if (data?.join_code) {
                route(`/e/${data.join_code}`)
                return
            }
            if (data?.type === 'songbook' || data?.public_songbook_id) {
                const target = data.slug || data.public_songbook_id
                route(`/book/${target}`)
                return
            }
        } catch {
            // Not an active event join code or short code, fall through to direct book route
        } finally {
            setSearching(false)
        }

        route(`/book/${trimmed}`)
    }

    return (
        <div class="home container-narrow">
            <div class="hero">
                <div class="stage-tagline">
                    <span class="live-dot"></span> The Stage Is Calling
                </div>
                <div class="hero-brandmark">
                    <BrandIcon size={56} glowing />
                </div>
                <h1 class="hero-title">
                    The mic is hot.<br />
                    <span class="hero-highlight">The room is ready.</span>
                </h1>
                <p class="hero-desc">
                    Your entire karaoke library in every pocket in the room.
                    Instant search, zero binders, and no app download required.
                </p>
            </div>

            <div class="home-actions">
                <form onSubmit={handleOpenBook} class="card stage-code-card">
                    <label for="book-code-input" class="stage-code-label">
                        Sing tonight? Enter show code, songbook code, or link
                    </label>
                    <div class="stage-code-input-group">
                        <input
                            id="book-code-input"
                            type="text"
                            class="stage-code-input"
                            placeholder="e.g. OMALLEY, DAVEMIC, or link"
                            value={bookCode}
                            onInput={(e) => setBookCode(e.target.value)}
                            aria-label="Show code, songbook code, or link"
                            autoComplete="off"
                            autoCapitalize="characters"
                            spellCheck={false}
                        />
                        <button type="submit" class="btn btn-primary stage-code-btn" disabled={!bookCode.trim() || searching}>
                            {searching ? 'Joining...' : 'Explore 🎤'}
                        </button>
                    </div>
                </form>

                <a href="/favorites" class="btn btn-secondary btn-lg btn-full home-favorites-cta">
                    <span class="home-favorites-star" aria-hidden="true">★</span>
                    <span>My Favorites</span>
                    {favoritesCount > 0 && (
                        <span class="home-favorites-badge">{favoritesCount}</span>
                    )}
                </a>

                {isLoggedIn.value && isHost.value && (
                    <a href="/kj/songbooks" class="btn btn-secondary btn-lg btn-full">
                        📚 Manage Songbooks
                    </a>
                )}

                {!isLoggedIn.value && (
                    <p class="home-host-cta">
                        Hosting tonight? <a href="/login">Sign in</a> to sync your library & run the show.
                    </p>
                )}

                <nav class="home-footer-links" aria-label="Learn more about MicUp.live">
                    <a href="/why" class="home-footer-link">
                        <span>Why MicUp?</span>
                        <span class="home-footer-arrow" aria-hidden="true">→</span>
                    </a>
                    <a href="/docs" class="home-footer-link">
                        <span>Host guides &amp; docs</span>
                        <span class="home-footer-arrow" aria-hidden="true">→</span>
                    </a>
                    <a href="https://discord.gg/ZUPwCTWXSx" target="_blank" rel="noopener noreferrer" class="home-footer-link">
                        <span>Discord Support</span>
                        <span class="home-footer-arrow" aria-hidden="true">↗</span>
                    </a>
                </nav>
            </div>
        </div>
    )
}
