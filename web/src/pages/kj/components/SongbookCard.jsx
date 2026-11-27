import { ActionPopover } from '../../../components/ui/ActionPopover.jsx'
import { resolveSongbookShowState } from '../hooks/useStartShowHandoff.js'
import { getSongbookPermissions } from '../../../utils/songbookPermissions.js'

function SongbookEditForm({ editing, saving, onChange, onSubmit, onCancel }) {
    return (
        <form
            onSubmit={event => {
                event.preventDefault()
                onSubmit({ name: editing.name, slug: editing.slug })
            }}
            style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-3)', padding: 'var(--space-2) 0' }}
        >
            <div style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
                <label style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-dim)', fontWeight: 'bold' }}>Songbook Name</label>
                <input value={editing.name} onInput={event => onChange({ ...editing, name: event.target.value })} required autoFocus />
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
                <label style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-dim)', fontWeight: 'bold' }}>Short Code / Custom Slug (e.g. DAVEMIC)</label>
                <input
                    value={editing.slug || ''}
                    onInput={event => onChange({ ...editing, slug: event.target.value.toLowerCase().replace(/[^a-z0-9_-]/g, '') })}
                    placeholder="optional short code, e.g. davemic"
                />
                <span style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)' }}>
                    Patrons can join via this code directly on the home page or at /book/{editing.slug || '[code]'}
                </span>
            </div>
            <div style={{ display: 'flex', gap: 'var(--space-2)' }}>
                <button class="btn btn-primary btn-sm" disabled={saving}>Save Changes</button>
                <button type="button" class="btn btn-secondary btn-sm" onClick={onCancel}>Cancel</button>
            </div>
        </form>
    )
}

function SharedBadge({ songbook, perms }) {
    const ownerSuffix = songbook.owner_display_name ? ` by ${songbook.owner_display_name}` : ''
    const roles = [perms.canEditCatalog && 'Edit', perms.canHostShows && 'Host'].filter(Boolean).join(' + ')

    return (
        <span style={{
            display: 'inline-flex',
            alignItems: 'center',
            gap: '4px',
            background: 'rgba(99, 102, 241, 0.14)',
            border: '1px solid rgba(99, 102, 241, 0.35)',
            borderRadius: 'var(--radius-sm)',
            padding: '1px 8px',
            fontSize: 'var(--text-xs)',
            fontWeight: 600,
            color: '#a5b4fc',
        }}>
            Shared{ownerSuffix}
            {' · '}
            {roles}
        </span>
    )
}

function SongbookBadges({ songbook, perms }) {
    return (
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-2)', flexWrap: 'wrap' }}>
            <strong style={{ fontSize: 'var(--text-lg)' }}>{songbook.name}</strong>
            {songbook.slug && (
                <span style={{
                    display: 'inline-flex',
                    alignItems: 'center',
                    gap: '4px',
                    background: 'rgba(255, 42, 95, 0.12)',
                    border: '1px solid rgba(255, 42, 95, 0.35)',
                    borderRadius: 'var(--radius-sm)',
                    padding: '1px 8px',
                    fontSize: 'var(--text-xs)',
                    fontWeight: 'bold',
                    color: 'var(--color-accent)',
                    fontFamily: 'var(--font-mono)',
                }}>
                    <span>CODE:</span> {songbook.slug.toUpperCase()}
                </span>
            )}
            {songbook.is_public ? (
                <span style={{ color: '#4ade80', fontSize: 'var(--text-xs)', fontWeight: 'bold' }}>● Public</span>
            ) : (
                <span style={{ color: 'var(--color-text-dim)', fontSize: 'var(--text-xs)' }}>○ Private</span>
            )}
            {!perms.isOwner && <SharedBadge songbook={songbook} perms={perms} />}
        </div>
    )
}

function LiveEventBadge({ liveEvent }) {
    if (!liveEvent) return null
    const label = liveEvent.venue_name || liveEvent.name

    return (
        <div style={{
            display: 'inline-flex',
            alignItems: 'center',
            gap: '6px',
            background: 'rgba(34, 197, 94, 0.12)',
            border: '1px solid rgba(34, 197, 94, 0.35)',
            borderRadius: 'var(--radius-md)',
            padding: '2px 8px',
            fontSize: 'var(--text-xs)',
            fontWeight: 'bold',
            color: '#4ade80',
            marginTop: '6px',
        }}>
            <span style={{ width: '6px', height: '6px', borderRadius: '50%', background: '#22c55e', boxShadow: '0 0 6px #22c55e' }}></span>
            <span>Live in: {label} ({liveEvent.join_code})</span>
        </div>
    )
}

function SongbookShowAction({ canHostShows, showState, eventsLoading, onRetry, onStartShow }) {
    if (!canHostShows && showState.status !== 'live') return null

    if (showState.status === 'error') {
        return (
            <button
                type="button"
                class="btn btn-secondary btn-sm"
                onClick={onRetry}
                disabled={eventsLoading}
                title="Failed to check show status. Click to retry."
                style={{ display: 'inline-flex', alignItems: 'center', gap: '4px' }}
            >
                <span>⚠️</span>
                <span>Retry Show Check</span>
            </button>
        )
    }

    if (showState.status === 'loading') {
        return (
            <button
                type="button"
                class="btn btn-secondary btn-sm"
                disabled
                style={{ display: 'inline-flex', alignItems: 'center', gap: '4px' }}
            >
                <span>⏳</span>
                <span>Checking Shows...</span>
            </button>
        )
    }

    if (showState.status === 'live') {
        return (
            <a
                class="btn btn-primary btn-sm"
                href={showState.queueUrl}
                style={{ display: 'inline-flex', alignItems: 'center', gap: '4px' }}
            >
                <span>🎤</span>
                <span>Live Show Queue</span>
            </a>
        )
    }

    return (
        <button
            type="button"
            class="btn btn-primary btn-sm"
            onClick={onStartShow}
            style={{ display: 'inline-flex', alignItems: 'center', gap: '4px' }}
        >
            <span>🎤</span>
            <span>Start Show</span>
        </button>
    )
}

function CatalogMenuItems({ isExporting, close, onAssistant, onExportCSV }) {
    return (
        <>
            <button
                type="button"
                class="kj-action-item kj-mobile-only"
                onClick={() => { close(); onAssistant() }}
            >
                <span>⚡</span>
                <span>Assistant Sync</span>
            </button>
            <button
                type="button"
                class="kj-action-item"
                onClick={() => { close(); onExportCSV() }}
                disabled={isExporting}
            >
                <span>{isExporting ? '⏳' : '📥'}</span>
                <span>{isExporting ? 'Exporting CSV...' : 'Export CSV'}</span>
            </button>
        </>
    )
}

function PublicMenuItems({ songbook, close, onCopyLink, onQr }) {
    return (
        <>
            <button
                type="button"
                class="kj-action-item"
                onClick={() => { onCopyLink(); close() }}
            >
                <span>🔗</span>
                <span>Copy Share Link</span>
            </button>
            <button
                type="button"
                class="kj-action-item"
                onClick={() => { close(); onQr() }}
            >
                <span>📱</span>
                <span>Download QR Code</span>
            </button>
            <a
                class="kj-action-item"
                href={`/book/${songbook.slug || songbook.public_id}`}
                target="_blank"
                rel="noopener noreferrer"
                onClick={close}
            >
                <span>👁️</span>
                <span>Preview Public Book</span>
            </a>
        </>
    )
}

function ManageMenuItems({ songbook, hasLinkedEvents, close, onTogglePublic, onEdit, onDelete }) {
    return (
        <>
            <button
                type="button"
                class="kj-action-item"
                onClick={() => { close(); onTogglePublic() }}
            >
                <span>{songbook.is_public ? '🔒' : '🌐'}</span>
                <span>{songbook.is_public ? 'Make Private' : 'Publish Songbook'}</span>
            </button>
            <button
                type="button"
                class="kj-action-item"
                onClick={() => { close(); onEdit() }}
            >
                <span>✏️</span>
                <span>Edit Details</span>
            </button>
            <hr class="kj-action-divider" />
            <button
                type="button"
                class="kj-action-item kj-action-item-danger"
                onClick={() => { close(); onDelete() }}
                title={hasLinkedEvents ? 'Cannot delete while linked to events' : 'Delete songbook'}
            >
                <span>🗑️</span>
                <span>Delete Songbook</span>
            </button>
        </>
    )
}

function SongbookMoreMenu({
    songbook,
    perms,
    exportingId,
    hasLinkedEvents,
    onAssistant,
    onExportCSV,
    onCopyLink,
    onQr,
    onTogglePublic,
    onEdit,
    onDelete,
}) {
    const canShowMenu = perms.canEditCatalog || perms.canManageBook || songbook.is_public
    if (!canShowMenu) return null

    return (
        <ActionPopover
            label={`More actions for ${songbook.name}`}
            panelTitle={songbook.name}
            triggerText="⋯ More"
        >
            {(close) => (
                <>
                    {perms.canEditCatalog && (
                        <CatalogMenuItems
                            isExporting={exportingId === songbook.id}
                            close={close}
                            onAssistant={onAssistant}
                            onExportCSV={onExportCSV}
                        />
                    )}
                    {songbook.is_public && songbook.public_id && (
                        <PublicMenuItems
                            songbook={songbook}
                            close={close}
                            onCopyLink={onCopyLink}
                            onQr={onQr}
                        />
                    )}
                    {perms.canManageBook && (
                        <ManageMenuItems
                            songbook={songbook}
                            hasLinkedEvents={hasLinkedEvents}
                            close={close}
                            onTogglePublic={onTogglePublic}
                            onEdit={onEdit}
                            onDelete={onDelete}
                        />
                    )}
                </>
            )}
        </ActionPopover>
    )
}

export function SongbookCard({
    songbook,
    events,
    eventsLoading,
    eventsError,
    editing,
    saving,
    exportingId,
    onEditChange,
    onUpdate,
    onCancelEdit,
    onStartEdit,
    onRetryEvents,
    onStartShow,
    onManageSongs,
    onAssistant,
    onManageAccess,
    onExportCSV,
    onCopyLink,
    onQr,
    onDelete,
}) {
    const perms = getSongbookPermissions(songbook)
    const showState = resolveSongbookShowState(songbook.id, events, { eventsLoading, eventsError })
    const { hasLiveShow, liveEvent } = showState

    if (editing?.id === songbook.id) {
        return (
            <div class="card kj-songbook-card">
                <SongbookEditForm
                    editing={editing}
                    saving={saving}
                    onChange={onEditChange}
                    onSubmit={patch => onUpdate(songbook, patch)}
                    onCancel={onCancelEdit}
                />
            </div>
        )
    }

    const songCountLabel = songbook.song_count === 1 ? 'song' : 'songs'
    const hasLinkedEvents = events.some(e => e.songbook_id === songbook.id)

    return (
        <div class="card kj-songbook-card">
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', flexWrap: 'wrap', gap: 'var(--space-2)' }}>
                <div>
                    <SongbookBadges songbook={songbook} perms={perms} />
                    <div style={{ color: 'var(--color-text-muted)', fontSize: 'var(--text-sm)', marginTop: '2px' }}>
                        {songbook.song_count} {songCountLabel}
                    </div>
                    {hasLiveShow && <LiveEventBadge liveEvent={liveEvent} />}
                </div>
            </div>

            <div class="kj-songbook-actions">
                <SongbookShowAction
                    canHostShows={perms.canHostShows}
                    showState={showState}
                    eventsLoading={eventsLoading}
                    onRetry={onRetryEvents}
                    onStartShow={() => onStartShow(songbook.id)}
                />

                {perms.canEditCatalog && (
                    <>
                        <button type="button" class="btn btn-secondary btn-sm" onClick={() => onManageSongs(songbook)}>
                            🎵 Manage Songs
                        </button>
                        <button type="button" class="btn btn-secondary btn-sm kj-desktop-only" onClick={() => onAssistant(songbook)}>
                            ⚡ Assistant Sync
                        </button>
                    </>
                )}

                {perms.canManageAccess && (
                    <button type="button" class="btn btn-secondary btn-sm" onClick={() => onManageAccess(songbook)}>
                        👥 Manage Access
                    </button>
                )}

                <SongbookMoreMenu
                    songbook={songbook}
                    perms={perms}
                    exportingId={exportingId}
                    hasLinkedEvents={hasLinkedEvents}
                    onAssistant={() => onAssistant(songbook)}
                    onExportCSV={() => onExportCSV(songbook)}
                    onCopyLink={() => onCopyLink(songbook)}
                    onQr={() => onQr(songbook)}
                    onTogglePublic={() => onUpdate(songbook, { is_public: !songbook.is_public })}
                    onEdit={() => onStartEdit({ id: songbook.id, name: songbook.name, slug: songbook.slug || '' })}
                    onDelete={() => onDelete(songbook)}
                />
            </div>
        </div>
    )
}
