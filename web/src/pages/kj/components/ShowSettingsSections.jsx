import { showToast } from '../../../components/Toast.jsx'

export const DEFAULT_SETTINGS_FORM = {
    rotation_mode: 'round_robin',
    auto_accept: false,
    submission_delay: 0,
    max_active_per_singer: 0,
    allow_key_change: true,
    allow_write_ins: false,
    allow_song_replacement: true,
    recurring: false,
    kiosk_pin: '',
}

export function buildSettingsForm(event) {
    return {
        rotation_mode: event.rotation_mode || 'round_robin',
        auto_accept: !!event.auto_accept,
        submission_delay: event.submission_delay || 0,
        max_active_per_singer: event.max_active_per_singer || 0,
        allow_key_change: event.allow_key_change !== false,
        allow_write_ins: !!event.allow_write_ins,
        allow_song_replacement: !!event.allow_song_replacement,
        recurring: !!event.recurring,
        kiosk_pin: event.kiosk_pin || '',
    }
}

export function buildSettingsPayload(settingsForm) {
    const payload = { ...settingsForm }
    const trimmedPin = (payload.kiosk_pin || '').trim()
    if (trimmedPin) {
        payload.kiosk_pin = trimmedPin
    } else {
        delete payload.kiosk_pin
    }
    return payload
}

const ROTATION_MODES = [
    {
        value: 'fair_priority',
        title: '⚖️ Fair Priority (Smart Weave)',
        badge: 'Popular',
        description: 'Least-recently-sung rotation: newcomers weave into the queue after the 3-song stage buffer without starving waiting regulars, with optional private VIP/cooldown singer weights.',
    },
    {
        value: 'round_robin',
        title: '🔄 Rounds of Singers',
        badge: null,
        description: 'Discrete rounds: 1 active song per singer in the current round. Subsequent requests wait in reserve rounds, with host-controlled round advancement.',
    },
    {
        value: 'fcfs',
        title: '⏱️ First-Come, First-Served (FCFS)',
        badge: null,
        description: 'Chronological submission order by default. Host reordering and stage locks are preserved.',
    },
]

function RotationModeOption({ mode, selected, onSelect }) {
    return (
        <label style={{
            display: 'flex',
            alignItems: 'flex-start',
            gap: 'var(--space-3)',
            padding: 'var(--space-3)',
            borderRadius: 'var(--radius-md)',
            border: `1px solid ${selected ? 'var(--color-accent)' : 'var(--color-border)'}`,
            background: selected ? 'rgba(255, 42, 95, 0.08)' : 'var(--color-surface)',
            cursor: 'pointer',
        }}>
            <input
                type="radio"
                name="settings_rotation_mode"
                value={mode.value}
                checked={selected}
                onChange={() => onSelect(mode.value)}
                style={{ marginTop: '2px' }}
            />
            <div>
                {mode.badge ? (
                    <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-2)' }}>
                        <span style={{ fontSize: 'var(--text-sm)', fontWeight: 'bold', color: 'var(--color-text)' }}>
                            {mode.title}
                        </span>
                        <span style={{
                            fontSize: '0.7rem',
                            fontWeight: 'bold',
                            padding: '1px 6px',
                            borderRadius: 'var(--radius-full)',
                            background: 'var(--color-accent)',
                            color: '#fff',
                            textTransform: 'uppercase',
                        }}>
                            {mode.badge}
                        </span>
                    </div>
                ) : (
                    <span style={{ fontSize: 'var(--text-sm)', fontWeight: 'bold', color: 'var(--color-text)' }}>
                        {mode.title}
                    </span>
                )}
                <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', marginTop: '2px', lineHeight: 1.4 }}>
                    {mode.description}
                </div>
            </div>
        </label>
    )
}

export function RotationModeSection({ rotationMode, onSelectMode }) {
    return (
        <div style={{
            background: 'var(--color-surface-alt)',
            border: '1px solid var(--color-border)',
            borderRadius: 'var(--radius-lg)',
            padding: 'var(--space-4)',
            marginBottom: 'var(--space-4)',
        }}>
            <div style={{ fontSize: 'var(--text-sm)', fontWeight: 'bold', marginBottom: 'var(--space-1)', color: 'var(--color-text)' }}>
                🎯 Rotation Style / Queue Order
            </div>
            <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-dim)', marginBottom: 'var(--space-3)', lineHeight: 1.4 }}>
                Controls how singers and songs are ordered in your live queue.
            </div>

            <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-2)' }}>
                {ROTATION_MODES.map(mode => (
                    <RotationModeOption
                        key={mode.value}
                        mode={mode}
                        selected={rotationMode === mode.value}
                        onSelect={onSelectMode}
                    />
                ))}
            </div>
            <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-chorus)', marginTop: 'var(--space-2)', fontStyle: 'italic' }}>
                💡 Switching queue modes mid-show preserves your top 3 stage positions (On Stage, On Deck, In the Hole) while seamlessly re-slotting the rest of the queue.
            </div>
        </div>
    )
}

const MAX_ACTIVE_PRESETS = [
    { label: '0 (Unlimited)', val: 0 },
    { label: '1 (Standard)', val: 1 },
    { label: '2', val: 2 },
    { label: '3', val: 3 },
    { label: '5', val: 5 },
]

function MaxActivePresetButton({ preset, active, onSelect }) {
    return (
        <button
            type="button"
            onClick={() => onSelect(preset.val)}
            style={{
                background: active ? 'var(--color-accent)' : 'var(--color-surface)',
                color: active ? '#fff' : 'var(--color-text-muted)',
                border: '1px solid var(--color-border)',
                borderRadius: 'var(--radius-sm)',
                padding: '4px 10px',
                fontSize: 'var(--text-xs)',
                cursor: 'pointer',
                fontWeight: active ? 'bold' : 'normal',
            }}
        >
            {preset.label}
        </button>
    )
}

export function MaxActivePerSingerSection({ maxActive, onChangeMaxActive }) {
    const isUnlimited = maxActive === 0

    return (
        <div style={{
            background: 'var(--color-surface-alt)',
            border: '1px solid var(--color-border)',
            borderRadius: 'var(--radius-lg)',
            padding: 'var(--space-4)',
            marginBottom: 'var(--space-6)',
        }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--space-1)' }}>
                <label style={{ fontSize: 'var(--text-sm)', fontWeight: 'bold', color: 'var(--color-text)' }}>
                    🎤 Active Requests Limit per Singer
                </label>
                <span style={{
                    fontSize: 'var(--text-xs)',
                    fontWeight: 'bold',
                    color: isUnlimited ? '#86efac' : 'var(--color-chorus)',
                    background: isUnlimited ? 'rgba(34, 197, 94, 0.15)' : 'rgba(245, 158, 11, 0.15)',
                    padding: '2px 8px',
                    borderRadius: 'var(--radius-sm)',
                    border: `1px solid ${isUnlimited ? 'rgba(34, 197, 94, 0.3)' : 'rgba(245, 158, 11, 0.3)'}`,
                }}>
                    {isUnlimited ? '✨ Unlimited' : `Max ${maxActive} Active`}
                </span>
            </div>
            <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', marginBottom: 'var(--space-3)', lineHeight: 1.4 }}>
                Limits how many songs each singer can have waiting in the active queue at one time. Set to 0 for unlimited.
            </div>

            <div style={{ display: 'flex', gap: 'var(--space-2)', alignItems: 'center', flexWrap: 'wrap', marginBottom: 'var(--space-2)' }}>
                <input
                    type="number"
                    min={0}
                    max={100}
                    value={maxActive}
                    onChange={e => onChangeMaxActive(Math.max(0, parseInt(e.target.value, 10) || 0))}
                    style={{
                        width: '90px',
                        fontSize: 'var(--text-sm)',
                        padding: 'var(--space-2) var(--space-3)',
                        background: 'var(--color-surface)',
                        border: '1px solid var(--color-border)',
                        borderRadius: 'var(--radius-md)',
                        color: 'var(--color-text)',
                    }}
                />
                <div style={{ display: 'flex', gap: '4px', flexWrap: 'wrap' }}>
                    {MAX_ACTIVE_PRESETS.map(preset => (
                        <MaxActivePresetButton
                            key={preset.val}
                            preset={preset}
                            active={maxActive === preset.val}
                            onSelect={onChangeMaxActive}
                        />
                    ))}
                </div>
            </div>
            <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-dim)' }}>
                {isUnlimited
                    ? '✨ Unlimited: Singers can submit as many songs as they want without restriction.'
                    : `Singers can have at most ${maxActive} active song(s) in line. Once sung or marked done, they can request again.`}
            </div>
        </div>
    )
}

function getOpenKJEndpointUrl() {
    const origin = typeof window !== 'undefined' ? window.location.origin : 'https://micup.live'
    return `${origin}/api/v1/openkj`
}

export function OpenKJSyncSection({ openkjKey, rotatingKey, onRotateOpenKJKey }) {
    const endpointUrl = getOpenKJEndpointUrl()

    const handleCopyEndpoint = () => {
        navigator.clipboard.writeText(endpointUrl)
        showToast('Copied OpenKJ Endpoint URL!')
    }

    const handleCopyKey = () => {
        if (!openkjKey) return
        navigator.clipboard.writeText(openkjKey)
        showToast('Copied Venue Key!')
    }

    return (
        <div style={{
            background: 'var(--color-surface-alt)',
            border: '1px solid var(--color-border)',
            borderRadius: 'var(--radius-lg)',
            padding: 'var(--space-4)',
            marginBottom: 'var(--space-6)',
        }}>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 'var(--space-2)' }}>
                <div style={{ fontSize: 'var(--text-sm)', fontWeight: 'bold', color: 'var(--color-text)' }}>
                    📡 OpenKJ Desktop Software Sync
                </div>
                <span style={{
                    fontSize: 'var(--text-xs)',
                    fontWeight: 'bold',
                    padding: '2px 8px',
                    borderRadius: 'var(--radius-full)',
                    background: 'rgba(59, 130, 246, 0.15)',
                    color: '#93c5fd',
                    border: '1px solid rgba(59, 130, 246, 0.3)',
                }}>
                    Ready to Connect
                </span>
            </div>
            <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', marginBottom: 'var(--space-3)', lineHeight: 1.4 }}>
                Connect the OpenKJ desktop player on your laptop to receive live singer requests and sync catalogs in real-time.
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-2)', fontSize: 'var(--text-xs)' }}>
                <div class="kj-openkj-row">
                    <div class="kj-openkj-value">
                        <strong>Remote Endpoint:</strong>{' '}
                        <code>{endpointUrl}</code>
                    </div>
                    <div class="kj-openkj-actions">
                        <button
                            type="button"
                            onClick={handleCopyEndpoint}
                            class="btn btn-secondary btn-sm"
                            style={{ fontSize: 'var(--text-xs)', padding: '4px 10px', minHeight: '32px' }}
                        >
                            Copy URL
                        </button>
                    </div>
                </div>
                <div class="kj-openkj-row">
                    <div class="kj-openkj-value" style={{ display: 'flex', flexDirection: 'column', gap: '2px' }}>
                        <span><strong>Venue Key:</strong></span>
                        <code style={{ color: 'var(--color-accent)', fontSize: 'var(--text-xs)' }}>{openkjKey || 'No key generated'}</code>
                    </div>
                    <div class="kj-openkj-actions">
                        <button
                            type="button"
                            onClick={handleCopyKey}
                            class="btn btn-secondary btn-sm"
                            style={{ fontSize: 'var(--text-xs)', padding: '4px 10px', minHeight: '32px' }}
                        >
                            Copy Key
                        </button>
                        <button
                            type="button"
                            disabled={rotatingKey}
                            onClick={onRotateOpenKJKey}
                            class="btn btn-secondary btn-sm"
                            style={{ fontSize: 'var(--text-xs)', padding: '4px 10px', minHeight: '32px' }}
                        >
                            {rotatingKey ? '...' : 'Rotate'}
                        </button>
                    </div>
                </div>
            </div>
        </div>
    )
}

export function ShowDangerZone({ currentEvent, onClose, onEndShow, onDeleteEvent, deletingEvent }) {
    const handleEndShow = () => {
        if (!confirm(`Are you sure you want to end "${currentEvent.name}"?`)) return
        onClose()
        onEndShow()
    }

    return (
        <>
            {currentEvent.status !== 'ended' && (
                <div
                    class="kj-modal-row"
                    style={{
                        marginTop: 'var(--space-6)',
                        paddingTop: 'var(--space-4)',
                        borderTop: '1px solid rgba(239, 68, 68, 0.2)',
                    }}
                >
                    <div>
                        <div style={{ fontSize: 'var(--text-xs)', fontWeight: 'bold', color: 'var(--color-danger)' }}>
                            End Tonight's Show
                        </div>
                        <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-dim)' }}>
                            Conclude tonight's gig and close the queue.
                        </div>
                    </div>
                    <button
                        type="button"
                        onClick={handleEndShow}
                        class="btn btn-secondary btn-sm"
                        style={{ fontSize: 'var(--text-xs)', color: 'var(--color-danger)', borderColor: 'rgba(239, 68, 68, 0.4)', minHeight: '32px', flexShrink: 0 }}
                    >
                        End Show
                    </button>
                </div>
            )}

            <div
                class="kj-modal-row"
                style={{ marginTop: 'var(--space-6)', paddingTop: 'var(--space-4)', borderTop: '1px solid rgba(239, 68, 68, 0.2)' }}
            >
                <div>
                    <div style={{ fontSize: 'var(--text-xs)', fontWeight: 'bold', color: 'var(--color-danger)' }}>Delete Event</div>
                    <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-dim)' }}>Permanently remove this event and its queue history.</div>
                </div>
                <button type="button" onClick={onDeleteEvent} class="btn btn-danger btn-sm" disabled={deletingEvent} style={{ flexShrink: 0 }}>
                    {deletingEvent ? 'Deleting...' : 'Delete Event'}
                </button>
            </div>
        </>
    )
}
