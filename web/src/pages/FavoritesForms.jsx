import { useState, useRef, useEffect } from 'preact/hooks'
import { clampPreferredKey } from '../utils/favoritesStore'
import { PITCH_KEY_OPTIONS } from './favoritesHelpers'

/**
 * Form for adding or editing a favorite song (title, artist, preferred pitch key).
 */
export function FavoriteSongForm({
    heading,
    headingAs = 'h2',
    submitLabel = 'Save Favorite Song',
    initialTitle = '',
    initialArtist = '',
    initialKey = 0,
    idPrefix = 'add-song',
    onSubmit,
    onCancel,
}) {
    const [title, setTitle] = useState(initialTitle)
    const [artist, setArtist] = useState(initialArtist)
    const [preferredKey, setPreferredKey] = useState(() => clampPreferredKey(initialKey))
    const [touched, setTouched] = useState({ title: false, artist: false })
    const [submitted, setSubmitted] = useState(false)
    const [formError, setFormError] = useState('')

    const titleRef = useRef(null)
    const artistRef = useRef(null)

    useEffect(() => {
        titleRef.current?.focus()
    }, [])

    const titleInvalid = (touched.title || submitted) && !title.trim()
    const artistInvalid = (touched.artist || submitted) && !artist.trim()

    const handleKeyDown = (e) => {
        if (e.key === 'Escape') {
            e.preventDefault()
            onCancel?.()
        }
    }

    const handleSubmit = (e) => {
        e.preventDefault()
        setSubmitted(true)
        setFormError('')

        if (!title.trim()) {
            titleRef.current?.focus()
            return
        }
        if (!artist.trim()) {
            artistRef.current?.focus()
            return
        }

        const result = onSubmit?.({
            title: title.trim(),
            artist: artist.trim(),
            preferred_key: clampPreferredKey(preferredKey),
        })

        if (result && !result.ok) {
            if (result.reason === 'duplicate') {
                setFormError('That song is already in your favorites.')
            } else if (result.reason === 'not_found') {
                setFormError('This favorite could not be found. It may have already been removed.')
            } else if (result.reason === 'storage_error') {
                setFormError('Could not save to browser storage. Check storage permissions or available space.')
            } else {
                setFormError('Please enter both a song title and an artist.')
            }
            return
        }
    }

    const HeadingTag = headingAs
    const headingId = `${idPrefix}-heading`
    const titleId = `${idPrefix}-title`
    const titleErrorId = `${idPrefix}-title-error`
    const artistId = `${idPrefix}-artist`
    const artistErrorId = `${idPrefix}-artist-error`
    const keyId = `${idPrefix}-key`

    return (
        <form
            class="favorites-form"
            aria-labelledby={heading ? headingId : undefined}
            onSubmit={handleSubmit}
            onKeyDown={handleKeyDown}
            noValidate
        >
            {heading && (
                <HeadingTag id={headingId} class="favorites-form-heading">{heading}</HeadingTag>
            )}

            {formError && (
                <div class="favorites-form-error" role="alert">
                    {formError}
                </div>
            )}

            <div class="favorites-field">
                <label class="favorites-label" for={titleId}>
                    Song Title<span class="favorites-required-mark" aria-hidden="true">*</span>
                </label>
                <input
                    ref={titleRef}
                    id={titleId}
                    name="song_title"
                    type="text"
                    class={`favorites-input${titleInvalid ? ' user-invalid-fallback' : ''}`}
                    placeholder="e.g. Bohemian Rhapsody"
                    value={title}
                    maxLength={120}
                    required
                    enterkeyhint="next"
                    aria-invalid={titleInvalid ? 'true' : 'false'}
                    aria-describedby={titleInvalid ? titleErrorId : undefined}
                    onInput={(e) => {
                        setTitle(e.target.value)
                        if (formError) setFormError('')
                    }}
                    onBlur={() => setTouched(prev => ({ ...prev, title: true }))}
                />
                {titleInvalid && (
                    <div id={titleErrorId} class="favorites-field-error">
                        <span aria-hidden="true">⚠</span> Please enter a song title.
                    </div>
                )}
            </div>

            <div class="favorites-field">
                <label class="favorites-label" for={artistId}>
                    Artist<span class="favorites-required-mark" aria-hidden="true">*</span>
                </label>
                <input
                    ref={artistRef}
                    id={artistId}
                    name="song_artist"
                    type="text"
                    class={`favorites-input${artistInvalid ? ' user-invalid-fallback' : ''}`}
                    placeholder="e.g. Queen"
                    value={artist}
                    maxLength={120}
                    required
                    enterkeyhint="done"
                    aria-invalid={artistInvalid ? 'true' : 'false'}
                    aria-describedby={artistInvalid ? artistErrorId : undefined}
                    onInput={(e) => {
                        setArtist(e.target.value)
                        if (formError) setFormError('')
                    }}
                    onBlur={() => setTouched(prev => ({ ...prev, artist: true }))}
                />
                {artistInvalid && (
                    <div id={artistErrorId} class="favorites-field-error">
                        <span aria-hidden="true">⚠</span> Please enter an artist name.
                    </div>
                )}
            </div>

            <div class="favorites-field">
                <label class="favorites-label" for={keyId}>
                    Preferred Pitch Key
                </label>
                <select
                    id={keyId}
                    name="preferred_key"
                    class="favorites-select"
                    value={preferredKey}
                    onChange={(e) => setPreferredKey(Number(e.target.value))}
                >
                    {PITCH_KEY_OPTIONS.map(opt => (
                        <option key={opt.value} value={opt.value}>{opt.label}</option>
                    ))}
                </select>
            </div>

            <div class="favorites-form-actions">
                <button type="submit" class="btn btn-primary favorites-action-btn">
                    {submitLabel}
                </button>
                <button type="button" class="btn btn-secondary favorites-action-btn" onClick={onCancel}>
                    Cancel
                </button>
            </div>
        </form>
    )
}

/**
 * Form for adding or editing a favorite artist.
 */
export function FavoriteArtistForm({
    heading,
    headingAs = 'h2',
    submitLabel = 'Save Favorite Artist',
    initialName = '',
    idPrefix = 'add-artist',
    onSubmit,
    onCancel,
}) {
    const [name, setName] = useState(initialName)
    const [touched, setTouched] = useState(false)
    const [submitted, setSubmitted] = useState(false)
    const [formError, setFormError] = useState('')
    const nameRef = useRef(null)

    useEffect(() => {
        nameRef.current?.focus()
    }, [])

    const nameInvalid = (touched || submitted) && !name.trim()

    const handleKeyDown = (e) => {
        if (e.key === 'Escape') {
            e.preventDefault()
            onCancel?.()
        }
    }

    const handleSubmit = (e) => {
        e.preventDefault()
        setSubmitted(true)
        setFormError('')

        if (!name.trim()) {
            nameRef.current?.focus()
            return
        }

        const result = onSubmit?.(name.trim())
        if (result && !result.ok) {
            if (result.reason === 'duplicate') {
                setFormError('That artist is already in your favorites.')
            } else if (result.reason === 'not_found') {
                setFormError('This favorite artist could not be found. It may have already been removed.')
            } else if (result.reason === 'storage_error') {
                setFormError('Could not save to browser storage. Check storage permissions or available space.')
            } else {
                setFormError('Please enter an artist name.')
            }
            return
        }
    }

    const HeadingTag = headingAs
    const headingId = `${idPrefix}-heading`
    const nameId = `${idPrefix}-name`
    const nameErrorId = `${idPrefix}-name-error`

    return (
        <form
            class="favorites-form"
            aria-labelledby={heading ? headingId : undefined}
            onSubmit={handleSubmit}
            onKeyDown={handleKeyDown}
            noValidate
        >
            {heading && (
                <HeadingTag id={headingId} class="favorites-form-heading">{heading}</HeadingTag>
            )}

            {formError && (
                <div class="favorites-form-error" role="alert">
                    {formError}
                </div>
            )}

            <div class="favorites-field">
                <label class="favorites-label" for={nameId}>
                    Artist Name<span class="favorites-required-mark" aria-hidden="true">*</span>
                </label>
                <input
                    ref={nameRef}
                    id={nameId}
                    name="artist_name"
                    type="text"
                    class={`favorites-input${nameInvalid ? ' user-invalid-fallback' : ''}`}
                    placeholder="e.g. Fleetwood Mac"
                    value={name}
                    maxLength={120}
                    required
                    enterkeyhint="done"
                    aria-invalid={nameInvalid ? 'true' : 'false'}
                    aria-describedby={nameInvalid ? nameErrorId : undefined}
                    onInput={(e) => {
                        setName(e.target.value)
                        if (formError) setFormError('')
                    }}
                    onBlur={() => setTouched(true)}
                />
                {nameInvalid && (
                    <div id={nameErrorId} class="favorites-field-error">
                        <span aria-hidden="true">⚠</span> Please enter an artist name.
                    </div>
                )}
            </div>

            <div class="favorites-form-actions">
                <button type="submit" class="btn btn-primary favorites-action-btn">
                    {submitLabel}
                </button>
                <button type="button" class="btn btn-secondary favorites-action-btn" onClick={onCancel}>
                    Cancel
                </button>
            </div>
        </form>
    )
}
