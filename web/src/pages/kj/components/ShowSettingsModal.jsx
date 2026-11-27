import { useState, useEffect } from 'preact/hooks'
import { ShowPolicyToggles } from './ShowPolicyToggles'
import { KioskPinSection } from './KioskPinSection'
import { Spinner } from '../../../components/Spinner.jsx'
import {
    DEFAULT_SETTINGS_FORM,
    buildSettingsForm,
    buildSettingsPayload,
    RotationModeSection,
    MaxActivePerSingerSection,
    OpenKJSyncSection,
    ShowDangerZone,
} from './ShowSettingsSections.jsx'

function openAccountSettings(onClose) {
    onClose()
    const btn = document.getElementById('btn-profile-avatar')
    if (btn) btn.click()
}

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
    const [settingsForm, setSettingsForm] = useState(DEFAULT_SETTINGS_FORM)

    useEffect(() => {
        if (isOpen && currentEvent) {
            setSettingsForm(buildSettingsForm(currentEvent))
        }
    }, [isOpen, currentEvent])

    if (!isOpen || !currentEvent) return null

    const handleSubmit = async (e) => {
        e.preventDefault()
        const ok = await onSaveSettings(buildSettingsPayload(settingsForm))
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
                        <RotationModeSection
                            rotationMode={settingsForm.rotation_mode}
                            onSelectMode={mode => setSettingsForm(prev => ({ ...prev, rotation_mode: mode }))}
                        />

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

                        <MaxActivePerSingerSection
                            maxActive={settingsForm.max_active_per_singer}
                            onChangeMaxActive={val => setSettingsForm(prev => ({ ...prev, max_active_per_singer: val }))}
                        />

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

                        <OpenKJSyncSection
                            openkjKey={currentEvent.openkj_key}
                            rotatingKey={rotatingKey}
                            onRotateOpenKJKey={onRotateOpenKJKey}
                        />

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
                                onClick={() => openAccountSettings(onClose)}
                                class="btn btn-secondary btn-sm"
                                style={{ fontSize: 'var(--text-xs)', padding: '4px 10px', minHeight: '32px', whiteSpace: 'nowrap', flexShrink: 0 }}
                            >
                                Account Settings →
                            </button>
                        </div>

                        <ShowDangerZone
                            currentEvent={currentEvent}
                            onClose={onClose}
                            onEndShow={onEndShow}
                            onDeleteEvent={onDeleteEvent}
                            deletingEvent={deletingEvent}
                        />
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
