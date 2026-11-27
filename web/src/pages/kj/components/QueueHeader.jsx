import { ActionPopover } from '../../../components/ui/ActionPopover.jsx'

const STATUS_BADGE_MAP = {
    paused: {
        background: 'rgba(245, 158, 11, 0.15)',
        color: 'var(--color-chorus)',
        border: '1px solid rgba(245, 158, 11, 0.3)',
        label: 'Paused',
    },
    ended: {
        background: 'rgba(239, 68, 68, 0.15)',
        color: 'var(--color-danger)',
        border: '1px solid rgba(239, 68, 68, 0.3)',
        label: 'Ended',
    },
}

const DEFAULT_LIVE_BADGE = {
    background: 'rgba(34, 197, 94, 0.15)',
    color: 'var(--color-success)',
    border: '1px solid rgba(34, 197, 94, 0.3)',
    label: 'Live',
}

const NO_EVENT_BADGE = {
    background: 'rgba(255, 255, 255, 0.05)',
    color: 'var(--color-text-dim)',
    border: '1px solid var(--color-border)',
    label: 'No Event',
}

const STATUS_OPTION_SUFFIX = {
    paused: ' [Paused]',
    ended: ' [Ended]',
}

function getEventBadgeConfig(currentEvent) {
    if (!currentEvent) return NO_EVENT_BADGE
    return STATUS_BADGE_MAP[currentEvent.status] || DEFAULT_LIVE_BADGE
}

function formatEventTitle(currentEvent) {
    if (!currentEvent) return 'No Event Selected'
    if (currentEvent.venue_name) return `${currentEvent.venue_name} — ${currentEvent.name}`
    return currentEvent.name
}

function formatEventOptionLabel(ev) {
    const base = ev.venue_name ? `${ev.venue_name} (${ev.join_code})` : `${ev.name} (${ev.join_code})`
    const recurringSuffix = ev.recurring ? ' 🔄' : ''
    const statusSuffix = STATUS_OPTION_SUFFIX[ev.status] || ''
    return `${base}${recurringSuffix}${statusSuffix}`
}

function EventStatusBadge({ currentEvent }) {
    const badge = getEventBadgeConfig(currentEvent)
    const isActive = currentEvent?.status === 'active'
    return (
        <span style={{
            display: 'inline-flex',
            alignItems: 'center',
            gap: '6px',
            padding: '2px 8px',
            borderRadius: 'var(--radius-full)',
            fontSize: 'var(--text-xs)',
            fontWeight: 'var(--font-bold)',
            letterSpacing: '0.06em',
            textTransform: 'uppercase',
            background: badge.background,
            color: badge.color,
            border: badge.border,
        }}>
            <span style={{
                width: '6px',
                height: '6px',
                borderRadius: '50%',
                background: 'currentColor',
                boxShadow: isActive ? '0 0 8px currentColor' : 'none',
            }}></span>
            <span>{badge.label}</span>
        </span>
    )
}

function EventSwitcher({ events, selectedEventId, showSwitcherRef, onSelectEventId, onOpenCreateModal }) {
    return (
        <div class="kj-qh-switcher">
            {events.length > 0 && (
                <select
                    ref={showSwitcherRef}
                    aria-label="Select show"
                    value={selectedEventId}
                    onChange={e => onSelectEventId(e.target.value, document.activeElement === e.target)}
                    class="kj-qh-select"
                >
                    {!selectedEventId && <option value="">-- Select Event --</option>}
                    {events.map(ev => (
                        <option key={ev.id} value={ev.id}>
                            {formatEventOptionLabel(ev)}
                        </option>
                    ))}
                </select>
            )}

            <button
                type="button"
                onClick={onOpenCreateModal}
                class="btn btn-primary btn-sm"
                style={{ fontSize: 'var(--text-xs)', padding: '6px 10px', display: 'inline-flex', alignItems: 'center', gap: '4px', flexShrink: 0 }}
            >
                <span>+</span>
                <span>New Show</span>
            </button>
        </div>
    )
}

function EventStatusActionButton({ status, onUpdateEventStatus, onOpenRestartModal }) {
    if (status === 'active') {
        return (
            <button
                type="button"
                onClick={() => onUpdateEventStatus('paused')}
                class="btn btn-secondary btn-sm"
                style={{
                    fontSize: 'var(--text-xs)',
                    padding: '4px 10px',
                    borderColor: 'var(--color-chorus)',
                    color: 'var(--color-chorus)',
                    display: 'inline-flex',
                    alignItems: 'center',
                    gap: '4px',
                }}
                title="Temporarily pause incoming singer requests"
            >
                <span>⏸️</span>
                <span>Pause</span>
            </button>
        )
    }

    if (status === 'paused') {
        return (
            <button
                type="button"
                onClick={() => onUpdateEventStatus('active')}
                class="btn btn-primary btn-sm"
                style={{
                    fontSize: 'var(--text-xs)',
                    padding: '4px 10px',
                    background: 'var(--color-success)',
                    borderColor: 'var(--color-success)',
                    display: 'inline-flex',
                    alignItems: 'center',
                    gap: '4px',
                }}
                title="Resume accepting singer requests"
            >
                <span>▶️</span>
                <span>Resume</span>
            </button>
        )
    }

    return (
        <button
            type="button"
            onClick={onOpenRestartModal}
            class="btn btn-primary btn-sm"
            style={{
                fontSize: 'var(--text-xs)',
                padding: '4px 10px',
                background: 'var(--color-success)',
                borderColor: 'var(--color-success)',
                display: 'inline-flex',
                alignItems: 'center',
                gap: '4px',
            }}
        >
            <span>🔄</span>
            <span>Restart Show</span>
        </button>
    )
}

function DesktopToolbarControls({
    currentEvent,
    onOpenAnnouncementModal,
    onOpenSettingsModal,
    soundEnabled,
    onToggleSound,
}) {
    const hasAnnouncement = Boolean(currentEvent.announcement)
    return (
        <div class="kj-qh-secondary-group kj-desktop-only">
            <div class="kj-qh-views-group">
                <a
                    href={`/stage/event/${currentEvent.id}`}
                    target="_blank"
                    rel="noopener noreferrer"
                    class="kj-qh-view-link"
                    title="Open TV Stage View for Bar Monitor"
                >
                    <span>📺</span>
                    <span>Stage</span>
                </a>
                <a
                    href={`/kiosk/event/${currentEvent.id}`}
                    target="_blank"
                    rel="noopener noreferrer"
                    class="kj-qh-view-link"
                    title="Open Venue Tablet Kiosk"
                >
                    <span>📱</span>
                    <span>Kiosk</span>
                </a>
                <a
                    href={`/e/${currentEvent.join_code}`}
                    target="_blank"
                    rel="noopener noreferrer"
                    class="kj-qh-view-link"
                    title="Open Singer View in new tab"
                >
                    <span>📖</span>
                    <span>Singer</span>
                </a>
            </div>

            <button
                type="button"
                class="btn btn-secondary btn-sm"
                onClick={onOpenAnnouncementModal}
                title="Broadcast Shout-Out Announcement to Stage"
                style={{
                    fontSize: 'var(--text-xs)',
                    padding: '4px 8px',
                    borderColor: hasAnnouncement ? 'var(--color-accent)' : 'var(--color-border)',
                    color: hasAnnouncement ? 'var(--color-accent)' : 'var(--color-text-muted)',
                    display: 'inline-flex',
                    alignItems: 'center',
                    gap: '4px',
                }}
            >
                <span>📢</span>
                <span>Shout Out</span>
                {hasAnnouncement && (
                    <span style={{ width: '5px', height: '5px', borderRadius: '50%', background: 'var(--color-accent)' }}></span>
                )}
            </button>

            <button
                type="button"
                onClick={onOpenSettingsModal}
                class="btn btn-secondary btn-sm"
                style={{
                    fontSize: 'var(--text-xs)',
                    padding: '4px 8px',
                    display: 'inline-flex',
                    alignItems: 'center',
                    gap: '4px',
                    borderColor: 'var(--color-border)',
                }}
                title="Queue rules, residency options, and show settings"
            >
                <span>⚙️</span>
                <span>Settings</span>
            </button>

            <button
                type="button"
                onClick={onToggleSound}
                class="btn btn-ghost btn-sm"
                style={{
                    fontSize: 'var(--text-xs)',
                    padding: '4px 6px',
                    border: '1px solid var(--color-border)',
                    color: soundEnabled ? 'var(--color-text)' : 'var(--color-text-dim)',
                }}
                title={soundEnabled ? 'Chime sound is ON' : 'Chime sound is OFF'}
            >
                {soundEnabled ? '🔔' : '🔕'}
            </button>
        </div>
    )
}

function MobileToolsPopover({
    currentEvent,
    onOpenAnnouncementModal,
    onOpenSettingsModal,
    soundEnabled,
    onToggleSound,
}) {
    const hasAnnouncement = Boolean(currentEvent.announcement)
    return (
        <div class="kj-mobile-only">
            <ActionPopover
                label="Show tools and links"
                panelTitle="Show Tools"
                triggerText="⋯ Tools"
                badgeDot={hasAnnouncement}
            >
                {(close) => (
                    <>
                        <div class="kj-action-section-label">Live Views</div>
                        <a
                            href={`/stage/event/${currentEvent.id}`}
                            target="_blank"
                            rel="noopener noreferrer"
                            class="kj-action-item"
                            onClick={close}
                        >
                            <span>📺</span>
                            <span>TV Stage View</span>
                        </a>
                        <a
                            href={`/kiosk/event/${currentEvent.id}`}
                            target="_blank"
                            rel="noopener noreferrer"
                            class="kj-action-item"
                            onClick={close}
                        >
                            <span>📱</span>
                            <span>Venue Tablet Kiosk</span>
                        </a>
                        <a
                            href={`/e/${currentEvent.join_code}`}
                            target="_blank"
                            rel="noopener noreferrer"
                            class="kj-action-item"
                            onClick={close}
                        >
                            <span>📖</span>
                            <span>Singer View</span>
                        </a>

                        <hr class="kj-action-divider" />
                        <div class="kj-action-section-label">Host Controls</div>

                        <button
                            type="button"
                            class={`kj-action-item${hasAnnouncement ? ' kj-action-item-accent' : ''}`}
                            onClick={() => { close(); onOpenAnnouncementModal() }}
                        >
                            <span>📢</span>
                            <span>{hasAnnouncement ? 'Shout Out (Active)' : 'Broadcast Shout Out'}</span>
                        </button>
                        <button
                            type="button"
                            class="kj-action-item"
                            onClick={() => { close(); onOpenSettingsModal() }}
                        >
                            <span>⚙️</span>
                            <span>Show Settings & Rules</span>
                        </button>
                        <button
                            type="button"
                            class="kj-action-item"
                            onClick={() => { onToggleSound(); close() }}
                        >
                            <span>{soundEnabled ? '🔔' : '🔕'}</span>
                            <span>{soundEnabled ? 'Chime Sound: On' : 'Chime Sound: Off'}</span>
                        </button>
                    </>
                )}
            </ActionPopover>
        </div>
    )
}

function QueueToolbar({
    currentEvent,
    onCopySingerLink,
    onUpdateEventStatus,
    onOpenRestartModal,
    onOpenAnnouncementModal,
    onOpenSettingsModal,
    soundEnabled,
    onToggleSound,
}) {
    return (
        <div class="kj-qh-toolbar">
            {/* Left Group: Singer Join Code Chip + Pause/Resume / Restart */}
            <div class="kj-qh-primary-group">
                <button
                    type="button"
                    onClick={() => onCopySingerLink(currentEvent.join_code)}
                    class="btn btn-ghost btn-sm"
                    title="Click to copy singer link (micup.live/e/...)"
                    style={{
                        background: 'var(--color-surface-alt)',
                        border: '1px solid var(--color-border)',
                        padding: '4px 10px',
                        borderRadius: 'var(--radius-md)',
                        display: 'inline-flex',
                        alignItems: 'center',
                        gap: '6px',
                        cursor: 'pointer',
                        transition: 'all 0.15s ease',
                    }}
                >
                    <span style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-dim)', textTransform: 'uppercase', letterSpacing: '0.04em' }}>Join:</span>
                    <span style={{ fontFamily: 'var(--font-mono)', fontSize: 'var(--text-sm)', fontWeight: 'var(--font-bold)', color: 'var(--color-accent)' }}>
                        {currentEvent.join_code}
                    </span>
                    <span style={{ fontSize: '12px', color: 'var(--color-text-dim)' }}>📋</span>
                </button>

                <EventStatusActionButton
                    status={currentEvent.status}
                    onUpdateEventStatus={onUpdateEventStatus}
                    onOpenRestartModal={onOpenRestartModal}
                />
            </div>

            {/* Desktop: Inline Display Links & Host Tools */}
            <DesktopToolbarControls
                currentEvent={currentEvent}
                onOpenAnnouncementModal={onOpenAnnouncementModal}
                onOpenSettingsModal={onOpenSettingsModal}
                soundEnabled={soundEnabled}
                onToggleSound={onToggleSound}
            />

            {/* Mobile: Overflow Tools Popover */}
            <MobileToolsPopover
                currentEvent={currentEvent}
                onOpenAnnouncementModal={onOpenAnnouncementModal}
                onOpenSettingsModal={onOpenSettingsModal}
                soundEnabled={soundEnabled}
                onToggleSound={onToggleSound}
            />
        </div>
    )
}

export function QueueHeader({
    currentEvent,
    currentSongbook,
    currentRound = 1,
    events = [],
    selectedEventId = '',
    onSelectEventId,
    showSwitcherRef,
    onOpenCreateModal,
    onCopySingerLink,
    onUpdateEventStatus,
    onOpenRestartModal,
    onOpenAnnouncementModal,
    onOpenSettingsModal,
    soundEnabled = true,
    onToggleSound,
}) {
    const showSongbook = Boolean(currentSongbook && currentEvent)

    return (
        <div class="kj-queue-header">
            {/* Row 1: Event Selector & Status */}
            <div class={`kj-qh-top${currentEvent ? '' : ' kj-qh-top-empty'}`}>
                {/* Left: Status, Show Title, Venue & Recurring Badge */}
                <div class="kj-qh-identity">
                    <div class="kj-qh-badges">
                        <EventStatusBadge currentEvent={currentEvent} />

                        {currentEvent?.recurring && (
                            <span style={{
                                fontSize: 'var(--text-xs)',
                                color: 'var(--color-chorus)',
                                background: 'rgba(245, 158, 11, 0.1)',
                                border: '1px solid rgba(245, 158, 11, 0.25)',
                                padding: '2px 8px',
                                borderRadius: 'var(--radius-full)',
                                fontWeight: '500',
                            }}>
                                🔄 Recurring
                            </span>
                        )}
                    </div>

                    <div class="kj-qh-title-row">
                        <h2 class="kj-qh-title">
                            {formatEventTitle(currentEvent)}
                        </h2>
                        {showSongbook && (
                            <span class="kj-qh-songbook">
                                ({currentSongbook.name})
                            </span>
                        )}
                    </div>
                </div>

                {/* Right: Show Switcher & New Show */}
                <EventSwitcher
                    events={events}
                    selectedEventId={selectedEventId}
                    showSwitcherRef={showSwitcherRef}
                    onSelectEventId={onSelectEventId}
                    onOpenCreateModal={onOpenCreateModal}
                />
            </div>

            {/* Row 2: Live Action & Control Toolbar */}
            {currentEvent && (
                <QueueToolbar
                    currentEvent={currentEvent}
                    onCopySingerLink={onCopySingerLink}
                    onUpdateEventStatus={onUpdateEventStatus}
                    onOpenRestartModal={onOpenRestartModal}
                    onOpenAnnouncementModal={onOpenAnnouncementModal}
                    onOpenSettingsModal={onOpenSettingsModal}
                    soundEnabled={soundEnabled}
                    onToggleSound={onToggleSound}
                />
            )}
        </div>
    )
}
