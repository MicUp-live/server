import { ActionPopover } from '../../../components/ui/ActionPopover.jsx'

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
    return (
        <div class="kj-queue-header">
            {/* Row 1: Event Selector & Status */}
            <div class={`kj-qh-top${currentEvent ? '' : ' kj-qh-top-empty'}`}>
                {/* Left: Status, Show Title, Venue & Recurring Badge */}
                <div class="kj-qh-identity">
                    <div class="kj-qh-badges">
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
                            background: currentEvent?.status === 'paused' ? 'rgba(245, 158, 11, 0.15)' : (currentEvent?.status === 'ended' ? 'rgba(239, 68, 68, 0.15)' : (currentEvent ? 'rgba(34, 197, 94, 0.15)' : 'rgba(255, 255, 255, 0.05)')),
                            color: currentEvent?.status === 'paused' ? 'var(--color-chorus)' : (currentEvent?.status === 'ended' ? 'var(--color-danger)' : (currentEvent ? 'var(--color-success)' : 'var(--color-text-dim)')),
                            border: `1px solid ${currentEvent?.status === 'paused' ? 'rgba(245, 158, 11, 0.3)' : (currentEvent?.status === 'ended' ? 'rgba(239, 68, 68, 0.3)' : (currentEvent ? 'rgba(34, 197, 94, 0.3)' : 'var(--color-border)'))}`,
                        }}>
                            <span style={{
                                width: '6px',
                                height: '6px',
                                borderRadius: '50%',
                                background: 'currentColor',
                                boxShadow: currentEvent?.status === 'active' ? '0 0 8px currentColor' : 'none',
                            }}></span>
                            <span>{currentEvent ? (currentEvent.status === 'paused' ? 'Paused' : currentEvent.status === 'ended' ? 'Ended' : 'Live') : 'No Event'}</span>
                        </span>

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
                            {currentEvent ? (currentEvent.venue_name ? `${currentEvent.venue_name} — ${currentEvent.name}` : currentEvent.name) : 'No Event Selected'}
                        </h2>
                        {currentSongbook && currentEvent && (
                            <span class="kj-qh-songbook">
                                ({currentSongbook.name})
                            </span>
                        )}
                    </div>
                </div>

                {/* Right: Show Switcher & New Show */}
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
                                    {ev.venue_name ? `${ev.venue_name} (${ev.join_code})` : `${ev.name} (${ev.join_code})`}
                                    {ev.recurring ? ' 🔄' : ''}
                                    {ev.status === 'paused' ? ' [Paused]' : ''}
                                    {ev.status === 'ended' ? ' [Ended]' : ''}
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
            </div>

            {/* Row 2: Live Action & Control Toolbar */}
            {currentEvent && (
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

                        {currentEvent.status === 'active' ? (
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
                        ) : currentEvent.status === 'paused' ? (
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
                        ) : (
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
                        )}
                    </div>

                    {/* Desktop: Inline Display Links & Host Tools */}
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
                                borderColor: currentEvent.announcement ? 'var(--color-accent)' : 'var(--color-border)',
                                color: currentEvent.announcement ? 'var(--color-accent)' : 'var(--color-text-muted)',
                                display: 'inline-flex',
                                alignItems: 'center',
                                gap: '4px',
                            }}
                        >
                            <span>📢</span>
                            <span>Shout Out</span>
                            {currentEvent.announcement && (
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

                    {/* Mobile: Overflow Tools Popover */}
                    <div class="kj-mobile-only">
                        <ActionPopover
                            label="Show tools and links"
                            panelTitle="Show Tools"
                            triggerText="⋯ Tools"
                            badgeDot={!!currentEvent.announcement}
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
                                        class={`kj-action-item${currentEvent.announcement ? ' kj-action-item-accent' : ''}`}
                                        onClick={() => { close(); onOpenAnnouncementModal() }}
                                    >
                                        <span>📢</span>
                                        <span>{currentEvent.announcement ? 'Shout Out (Active)' : 'Broadcast Shout Out'}</span>
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
                </div>
            )}
        </div>
    )
}
