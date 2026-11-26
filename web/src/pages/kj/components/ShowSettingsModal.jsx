import { useState, useEffect } from 'preact/hooks'
import { ShowPolicyToggles } from './ShowPolicyToggles'
import { KioskPinSection } from './KioskPinSection'
import { Spinner } from '../../../components/Spinner.jsx'
import { showToast } from '../../../components/Toast.jsx'

export function ShowSettingsModal({
    isOpen,
    onClose,
    currentEvent,
    onSaveSettings,
    savingSettings = false,
    onRotateOpenKJKey,
    rotatingKey = false,
    onEndShow,
    onDeleteEvent,
    deletingEvent = false,
}) {
    const [settingsForm, setSettingsForm] = useState({
        rotation_mode: 'round_robin',
        auto_accept: false,
        submission_delay: 0,
        max_active_per_singer: 0,
        allow_key_change: true,
        allow_write_ins: false,
        recurring: false,
        kiosk_pin: '',
    })

    useEffect(() => {
        if (isOpen && currentEvent) {
            setSettingsForm({
                rotation_mode: currentEvent.rotation_mode || 'round_robin',
                auto_accept: !!currentEvent.auto_accept,
                submission_delay: currentEvent.submission_delay || 0,
                max_active_per_singer: currentEvent.max_active_per_singer || 0,
                allow_key_change: currentEvent.allow_key_change !== false,
                allow_write_ins: !!currentEvent.allow_write_ins,
                recurring: !!currentEvent.recurring,
                kiosk_pin: currentEvent.kiosk_pin || '',
            })
        }
    }, [isOpen, currentEvent])

    if (!isOpen || !currentEvent) return null

    const handleSubmit = async (e) => {
        e.preventDefault()
        const payload = { ...settingsForm }
        const trimmedPin = (payload.kiosk_pin || '').trim()
        if (trimmedPin) {
            payload.kiosk_pin = trimmedPin
        } else {
            delete payload.kiosk_pin
        }
        const ok = await onSaveSettings(payload)
        if (ok) {
            onClose()
        }
    }

    return (
        <div
            class="modal-overlay"
            onClick={onClose}
            role="dialog"
            aria-modal="true"
            aria-labelledby="settings-modal-title"
        >
            <div
                class="modal-sheet modal-sheet-wide"
                style={{ boxShadow: '0 20px 40px rgba(0, 0, 0, 0.7), 0 0 30px rgba(168, 85, 247, 0.2)' }}
                onClick={e => e.stopPropagation()}
            >
                <div class="modal-drag-handle" aria-hidden="true" />
                <div
                    class="kj-modal-header"
                    style={{ background: 'linear-gradient(180deg, rgba(168, 85, 247, 0.15) 0%, transparent 100%)' }}
                >
                    <div style={{ minWidth: 0 }}>
                        <div style={{ fontSize: 'var(--text-xs)', color: '#c084fc', fontWeight: 'bold', textTransform: 'uppercase', letterSpacing: '0.08em' }}>
                            Show Settings
                        </div>
                        <h3 id="settings-modal-title" style={{ margin: '2px 0 0', fontSize: 'var(--text-xl)', color: 'var(--color-text)' }}>
                            ⚙️ Queue & Staging Rules
                        </h3>
                        <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-dim)', marginTop: '2px' }}>
                            {currentEvent.venue_name ? `${currentEvent.venue_name} — ` : ''}{currentEvent.name}
                        </div>
                    </div>
                    <button
                        type="button"
                        onClick={onClose}
                        aria-label="Close show settings"
                        style={{ background: 'none', border: 'none', color: 'var(--color-text-dim)', fontSize: '1.25rem', cursor: 'pointer', flexShrink: 0 }}
                    >
                        ✕
                    </button>
                </div>

                <form
                    onSubmit={handleSubmit}
                    style={{
                        display: 'flex',
                        flexDirection: 'column',
                        flex: 1,
                        minHeight: 0,
                        overflow: 'hidden',
                    }}
                >
                    <div class="kj-modal-body">

                        {/* Rotation Style / Mode */}
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
                                <label style={{
                                    display: 'flex',
                                    alignItems: 'flex-start',
                                    gap: 'var(--space-3)',
                                    padding: 'var(--space-3)',
                                    borderRadius: 'var(--radius-md)',
                                    border: `1px solid ${settingsForm.rotation_mode === 'fair_priority' ? 'var(--color-accent)' : 'var(--color-border)'}`,
                                    background: settingsForm.rotation_mode === 'fair_priority' ? 'rgba(255, 42, 95, 0.08)' : 'var(--color-surface)',
                                    cursor: 'pointer',
                                }}>
                                    <input
                                        type="radio"
                                        name="settings_rotation_mode"
                                        value="fair_priority"
                                        checked={settingsForm.rotation_mode === 'fair_priority'}
                                        onChange={() => setSettingsForm(prev => ({ ...prev, rotation_mode: 'fair_priority' }))}
                                        style={{ marginTop: '2px' }}
                                    />
                                    <div>
                                        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-2)' }}>
                                            <span style={{ fontSize: 'var(--text-sm)', fontWeight: 'bold', color: 'var(--color-text)' }}>
                                                ⚖️ Fair Priority (Smart Weave)
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
                                                Popular
                                            </span>
                                        </div>
                                        <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', marginTop: '2px', lineHeight: 1.4 }}>
                                            Least-recently-sung rotation: newcomers weave into the queue after the 3-song stage buffer without starving waiting regulars, with optional private VIP/cooldown singer weights.
                                        </div>
                                    </div>
                                </label>

                                <label style={{
                                    display: 'flex',
                                    alignItems: 'flex-start',
                                    gap: 'var(--space-3)',
                                    padding: 'var(--space-3)',
                                    borderRadius: 'var(--radius-md)',
                                    border: `1px solid ${settingsForm.rotation_mode === 'round_robin' ? 'var(--color-accent)' : 'var(--color-border)'}`,
                                    background: settingsForm.rotation_mode === 'round_robin' ? 'rgba(255, 42, 95, 0.08)' : 'var(--color-surface)',
                                    cursor: 'pointer',
                                }}>
                                    <input
                                        type="radio"
                                        name="settings_rotation_mode"
                                        value="round_robin"
                                        checked={settingsForm.rotation_mode === 'round_robin'}
                                        onChange={() => setSettingsForm(prev => ({ ...prev, rotation_mode: 'round_robin' }))}
                                        style={{ marginTop: '2px' }}
                                    />
                                    <div>
                                        <span style={{ fontSize: 'var(--text-sm)', fontWeight: 'bold', color: 'var(--color-text)' }}>
                                            🔄 Rounds of Singers
                                        </span>
                                        <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', marginTop: '2px', lineHeight: 1.4 }}>
                                            Discrete rounds: 1 active song per singer in the current round. Subsequent requests wait in reserve rounds, with host-controlled round advancement.
                                        </div>
                                    </div>
                                </label>

                                <label style={{
                                    display: 'flex',
                                    alignItems: 'flex-start',
                                    gap: 'var(--space-3)',
                                    padding: 'var(--space-3)',
                                    borderRadius: 'var(--radius-md)',
                                    border: `1px solid ${settingsForm.rotation_mode === 'fcfs' ? 'var(--color-accent)' : 'var(--color-border)'}`,
                                    background: settingsForm.rotation_mode === 'fcfs' ? 'rgba(255, 42, 95, 0.08)' : 'var(--color-surface)',
                                    cursor: 'pointer',
                                }}>
                                    <input
                                        type="radio"
                                        name="settings_rotation_mode"
                                        value="fcfs"
                                        checked={settingsForm.rotation_mode === 'fcfs'}
                                        onChange={() => setSettingsForm(prev => ({ ...prev, rotation_mode: 'fcfs' }))}
                                        style={{ marginTop: '2px' }}
                                    />
                                    <div>
                                        <span style={{ fontSize: 'var(--text-sm)', fontWeight: 'bold', color: 'var(--color-text)' }}>
                                            ⏱️ First-Come, First-Served (FCFS)
                                        </span>
                                        <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', marginTop: '2px', lineHeight: 1.4 }}>
                                            Chronological submission order by default. Host reordering and stage locks are preserved.
                                        </div>
                                    </div>
                                </label>
                            </div>
                            <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-chorus)', marginTop: 'var(--space-2)', fontStyle: 'italic' }}>
                                💡 Switching queue modes mid-show preserves your top 3 stage positions (On Stage, On Deck, In the Hole) while seamlessly re-slotting the rest of the queue.
                            </div>
                        </div>

                        {/* Auto Accept */}
                        <div style={{
                            background: 'var(--color-surface-alt)',
                            border: '1px solid var(--color-border)',
                            borderRadius: 'var(--radius-lg)',
                            padding: 'var(--space-4)',
                            marginBottom: 'var(--space-4)',
                        }}>
                            <label style={{ display: 'flex', alignItems: 'flex-start', gap: 'var(--space-3)', cursor: 'pointer' }}>
                                <input
                                    type="checkbox"
                                    checked={settingsForm.auto_accept}
                                    onChange={e => setSettingsForm(prev => ({ ...prev, auto_accept: e.target.checked }))}
                                    style={{ marginTop: '3px' }}
                                />
                                <div>
                                    <div style={{ fontSize: 'var(--text-sm)', fontWeight: 'bold', color: 'var(--color-text)' }}>
                                        ⚡ Auto-Accept Requests
                                    </div>
                                    <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', marginTop: '2px', lineHeight: 1.4 }}>
                                        {settingsForm.auto_accept
                                            ? 'Enabled: Incoming singer requests immediately enter the live rotation.'
                                            : 'Disabled: Incoming singer requests wait in your Staging Inbox until you click "Accept" or "Call to Stage".'}
                                    </div>
                                </div>
                            </label>
                        </div>

                        {/* Cooldown Burst Protection */}
                        <div style={{
                            background: 'var(--color-surface-alt)',
                            border: '1px solid var(--color-border)',
                            borderRadius: 'var(--radius-lg)',
                            padding: 'var(--space-4)',
                            marginBottom: 'var(--space-6)',
                        }}>
                            <label style={{ display: 'block', fontSize: 'var(--text-sm)', fontWeight: 'bold', marginBottom: 'var(--space-1)', color: 'var(--color-text)' }}>
                                🛡️ Singer Submission Cooldown Delay
                            </label>
                            <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', marginBottom: 'var(--space-3)', lineHeight: 1.4 }}>
                                Enforces a wait timer between song submissions from the same singer device to prevent queue flooding.
                            </div>
                            <select
                                value={settingsForm.submission_delay}
                                onChange={e => setSettingsForm(prev => ({ ...prev, submission_delay: Number(e.target.value) }))}
                                style={{
                                    width: '100%',
                                    fontSize: 'var(--text-sm)',
                                    padding: 'var(--space-2) var(--space-3)',
                                    background: 'var(--color-surface)',
                                    border: '1px solid var(--color-border)',
                                    borderRadius: 'var(--radius-md)',
                                    color: 'var(--color-text)',
                                }}
                            >
                                <option value={0}>0 seconds (No cooldown)</option>
                                <option value={30}>30 seconds</option>
                                <option value={60}>60 seconds (1 minute - Recommended)</option>
                                <option value={90}>90 seconds (1.5 minutes)</option>
                                <option value={120}>120 seconds (2 minutes)</option>
                                <option value={180}>180 seconds (3 minutes)</option>
                                <option value={300}>300 seconds (5 minutes)</option>
                            </select>
                        </div>

                        {/* Active Requests Limit per Singer */}
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
                                    color: settingsForm.max_active_per_singer === 0 ? '#86efac' : 'var(--color-chorus)',
                                    background: settingsForm.max_active_per_singer === 0 ? 'rgba(34, 197, 94, 0.15)' : 'rgba(245, 158, 11, 0.15)',
                                    padding: '2px 8px',
                                    borderRadius: 'var(--radius-sm)',
                                    border: `1px solid ${settingsForm.max_active_per_singer === 0 ? 'rgba(34, 197, 94, 0.3)' : 'rgba(245, 158, 11, 0.3)'}`,
                                }}>
                                    {settingsForm.max_active_per_singer === 0 ? '✨ Unlimited' : `Max ${settingsForm.max_active_per_singer} Active`}
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
                                    value={settingsForm.max_active_per_singer}
                                    onChange={e => setSettingsForm(prev => ({ ...prev, max_active_per_singer: Math.max(0, parseInt(e.target.value, 10) || 0) }))}
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
                                    {[
                                        { label: '0 (Unlimited)', val: 0 },
                                        { label: '1 (Standard)', val: 1 },
                                        { label: '2', val: 2 },
                                        { label: '3', val: 3 },
                                        { label: '5', val: 5 },
                                    ].map(preset => (
                                        <button
                                            key={preset.val}
                                            type="button"
                                            onClick={() => setSettingsForm(prev => ({ ...prev, max_active_per_singer: preset.val }))}
                                            style={{
                                                background: settingsForm.max_active_per_singer === preset.val ? 'var(--color-accent)' : 'var(--color-surface)',
                                                color: settingsForm.max_active_per_singer === preset.val ? '#fff' : 'var(--color-text-muted)',
                                                border: '1px solid var(--color-border)',
                                                borderRadius: 'var(--radius-sm)',
                                                padding: '4px 10px',
                                                fontSize: 'var(--text-xs)',
                                                cursor: 'pointer',
                                                fontWeight: settingsForm.max_active_per_singer === preset.val ? 'bold' : 'normal',
                                            }}
                                        >
                                            {preset.label}
                                        </button>
                                    ))}
                                </div>
                            </div>
                            <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-dim)' }}>
                                {settingsForm.max_active_per_singer === 0
                                    ? '✨ Unlimited: Singers can submit as many songs as they want without restriction.'
                                    : `Singers can have at most ${settingsForm.max_active_per_singer} active song(s) in line. Once sung or marked done, they can request again.`}
                            </div>
                        </div>

                        <ShowPolicyToggles settingsForm={settingsForm} setSettingsForm={setSettingsForm} />

                        <KioskPinSection
                            value={settingsForm.kiosk_pin}
                            onChange={pin => setSettingsForm(prev => ({ ...prev, kiosk_pin: pin }))}
                            hasExistingPin={!!currentEvent.kiosk_pin}
                        />

                        {/* Recurring Gig */}
                        <div style={{
                            background: 'var(--color-surface-alt)',
                            border: '1px solid var(--color-border)',
                            borderRadius: 'var(--radius-lg)',
                            padding: 'var(--space-4)',
                            marginBottom: 'var(--space-6)',
                        }}>
                            <label style={{ display: 'flex', alignItems: 'flex-start', gap: 'var(--space-3)', cursor: 'pointer' }}>
                                <input
                                    type="checkbox"
                                    checked={settingsForm.recurring}
                                    onChange={e => setSettingsForm(prev => ({ ...prev, recurring: e.target.checked }))}
                                    style={{ marginTop: '3px' }}
                                />
                                <div>
                                    <div style={{ fontSize: 'var(--text-sm)', fontWeight: 'bold', color: 'var(--color-text)' }}>
                                        🔄 Weekly Residency / Recurring Show
                                    </div>
                                    <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', marginTop: '2px', lineHeight: 1.4 }}>
                                        When enabled, this show stays in your event list and can be restarted each week with fresh or cleared queues without changing the join code.
                                    </div>
                                </div>
                            </label>
                        </div>

                        {/* OpenKJ Desktop Software Sync */}
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
                                        <code>{typeof window !== 'undefined' ? window.location.origin : 'https://micup.live'}/api/v1/openkj</code>
                                    </div>
                                    <div class="kj-openkj-actions">
                                        <button
                                            type="button"
                                            onClick={() => {
                                                const url = `${typeof window !== 'undefined' ? window.location.origin : 'https://micup.live'}/api/v1/openkj`
                                                navigator.clipboard.writeText(url)
                                                showToast('Copied OpenKJ Endpoint URL!')
                                            }}
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
                                        <code style={{ color: 'var(--color-accent)', fontSize: 'var(--text-xs)' }}>{currentEvent.openkj_key || 'No key generated'}</code>
                                    </div>
                                    <div class="kj-openkj-actions">
                                        <button
                                            type="button"
                                            onClick={() => {
                                                if (!currentEvent.openkj_key) return
                                                navigator.clipboard.writeText(currentEvent.openkj_key)
                                                showToast('Copied Venue Key!')
                                            }}
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

                        {/* Developer API Keys Info Notice */}
                        <div
                            class="kj-modal-row"
                            style={{
                                background: 'var(--color-surface-alt)',
                                border: '1px solid var(--color-border)',
                                borderRadius: 'var(--radius-lg)',
                                padding: 'var(--space-4)',
                                marginBottom: 'var(--space-6)',
                            }}
                        >
                            <div>
                                <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '2px' }}>
                                    <span style={{ fontSize: '1.1rem' }}>⚡</span>
                                    <strong style={{ fontSize: 'var(--text-sm)', color: 'var(--color-text)' }}>Developer API Keys</strong>
                                </div>
                                <p style={{ margin: 0, fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', lineHeight: 1.4 }}>
                                    Personal Access Tokens are account-scoped credentials. Manage your keys in <strong>Account Settings</strong>.
                                </p>
                            </div>
                            <button
                                type="button"
                                onClick={() => {
                                    onClose()
                                    const btn = document.getElementById('btn-profile-avatar')
                                    if (btn) btn.click()
                                }}
                                class="btn btn-secondary btn-sm"
                                style={{ fontSize: 'var(--text-xs)', padding: '4px 10px', minHeight: '32px', whiteSpace: 'nowrap', flexShrink: 0 }}
                            >
                                Account Settings →
                            </button>
                        </div>

                        {/* Danger Zone: End Show */}
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
                                    onClick={() => {
                                        if (confirm(`Are you sure you want to end "${currentEvent.name}"?`)) {
                                            onClose()
                                            onEndShow()
                                        }
                                    }}
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
                    </div>

                    {/* Pinned Action Buttons */}
                    <div class="kj-modal-footer">
                        <button
                            type="button"
                            class="btn btn-secondary"
                            onClick={onClose}
                            disabled={savingSettings}
                        >
                            Cancel
                        </button>
                        <button
                            type="submit"
                            class="btn btn-primary"
                            disabled={savingSettings}
                            style={{ minWidth: '140px', background: 'linear-gradient(135deg, #a855f7 0%, #7c3aed 100%)', borderColor: '#7c3aed' }}
                        >
                            {savingSettings ? <Spinner size="sm" label="Saving..." inline /> : 'Save Rules'}
                        </button>
                    </div>
                </form>
            </div>
        </div>
    )
}
