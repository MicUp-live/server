import { useState, useEffect } from 'preact/hooks'
import { Spinner } from '../../../components/Spinner.jsx'
import { filterHostableSongbooks } from '../../../utils/songbookPermissions.js'
import { generateRandomKioskPin } from './KioskPinSection.jsx'

function generateRandomCode(prefix = '') {
    const chars = 'ABCDEFGHJKLMNPQRSTUVWXYZ23456789'
    let code = prefix ? prefix.toUpperCase().replace(/[^A-Z0-9]/g, '').slice(0, 4) : ''
    while (code.length < 6) {
        code += chars.charAt(Math.floor(Math.random() * chars.length))
    }
    return code
}

export function CreateShowModal({
    isOpen,
    onClose,
    songbooks = [],
    onCreateShow,
    creatingShow = false,
    initialSongbookId = '',
}) {
    const hostableSongbooks = filterHostableSongbooks(songbooks)
    const [createForm, setCreateForm] = useState({
        name: '',
        venue_name: '',
        join_code: '',
        songbook_id: initialSongbookId || (hostableSongbooks[0]?.id || ''),
        recurring: false,
        rotation_mode: 'round_robin',
        auto_accept: false,
        submission_delay: 0,
        max_active_per_singer: 0,
        allow_key_change: true,
        allow_write_ins: false,
        kiosk_pin: '',
    })

    useEffect(() => {
        if (isOpen) {
            const validInitial = initialSongbookId && hostableSongbooks.some(s => s.id === initialSongbookId) ? initialSongbookId : ''
            setCreateForm(prev => ({
                ...prev,
                join_code: prev.join_code || generateRandomCode(),
                songbook_id: validInitial || (hostableSongbooks.some(s => s.id === prev.songbook_id) ? prev.songbook_id : (hostableSongbooks[0]?.id || '')),
            }))
        }
    }, [isOpen, songbooks, initialSongbookId])

    if (!isOpen) return null

    const handleSubmit = (e) => {
        e.preventDefault()
        onCreateShow(createForm)
    }

    return (
        <div
            class="modal-overlay"
            onClick={onClose}
            role="dialog"
            aria-modal="true"
            aria-labelledby="create-show-title"
        >
            <div
                class="modal-sheet modal-sheet-wide"
                onClick={e => e.stopPropagation()}
            >
                <div class="modal-drag-handle" aria-hidden="true" />
                <div
                    class="kj-modal-header"
                    style={{ background: 'linear-gradient(180deg, rgba(255, 42, 95, 0.15) 0%, transparent 100%)' }}
                >
                    <div style={{ minWidth: 0 }}>
                        <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-accent)', fontWeight: 'bold', textTransform: 'uppercase', letterSpacing: '0.08em' }}>
                            Host Console
                        </div>
                        <h3 id="create-show-title" style={{ margin: '2px 0 0', fontSize: 'var(--text-xl)', color: 'var(--color-text)' }}>
                            Create a Karaoke Show
                        </h3>
                    </div>
                    <button
                        type="button"
                        onClick={onClose}
                        aria-label="Close create show modal"
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
                        <div style={{ marginBottom: 'var(--space-4)' }}>
                            <label style={{ display: 'block', fontSize: 'var(--text-sm)', fontWeight: 'bold', marginBottom: 'var(--space-1)' }}>
                                Venue Name <span style={{ color: 'var(--color-text-dim)', fontWeight: 'normal' }}>(optional)</span>
                            </label>
                            <input
                                type="text"
                                placeholder="e.g. O'Malley's Irish Pub"
                                value={createForm.venue_name}
                                onInput={e => setCreateForm(prev => ({ ...prev, venue_name: e.target.value }))}
                                style={{ width: '100%', fontSize: 'var(--text-base)' }}
                            />
                        </div>

                        <div style={{ marginBottom: 'var(--space-4)' }}>
                            <label style={{ display: 'block', fontSize: 'var(--text-sm)', fontWeight: 'bold', marginBottom: 'var(--space-1)' }}>
                                Show / Event Title <span style={{ color: 'var(--color-accent)' }}>*</span>
                            </label>
                            <input
                                type="text"
                                placeholder="e.g. Tuesday Night Live"
                                value={createForm.name}
                                onInput={e => setCreateForm(prev => ({ ...prev, name: e.target.value }))}
                                required
                                style={{ width: '100%', fontSize: 'var(--text-base)' }}
                            />
                        </div>

                        <div style={{ marginBottom: 'var(--space-4)' }}>
                            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--space-1)' }}>
                                <label style={{ fontSize: 'var(--text-sm)', fontWeight: 'bold' }}>
                                    Singer Join Code <span style={{ color: 'var(--color-accent)' }}>*</span>
                                </label>
                                <button
                                    type="button"
                                    onClick={() => setCreateForm(prev => ({ ...prev, join_code: generateRandomCode(prev.venue_name) }))}
                                    style={{ background: 'none', border: 'none', color: 'var(--color-chorus)', fontSize: 'var(--text-xs)', cursor: 'pointer', textDecoration: 'underline' }}
                                >
                                    🎲 Random Code
                                </button>
                            </div>
                            <input
                                type="text"
                                placeholder="e.g. OMALLEY"
                                value={createForm.join_code}
                                onInput={e => setCreateForm(prev => ({ ...prev, join_code: e.target.value.toUpperCase().replace(/[^A-Z0-9]/g, '') }))}
                                required
                                maxLength={16}
                                style={{ width: '100%', fontSize: 'var(--text-base)', textTransform: 'uppercase', fontFamily: 'var(--font-mono)' }}
                            />
                            <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-dim)', marginTop: '4px' }}>
                                Singers enter this short code at <code>micup.live</code> to request songs.
                            </div>
                        </div>

                        <div style={{ marginBottom: 'var(--space-4)' }}>
                            <label style={{ display: 'block', fontSize: 'var(--text-sm)', fontWeight: 'bold', marginBottom: 'var(--space-1)' }}>
                                Master Songbook Library <span style={{ color: 'var(--color-accent)' }}>*</span>
                            </label>
                            <select
                                value={createForm.songbook_id}
                                onChange={e => setCreateForm(prev => ({ ...prev, songbook_id: e.target.value }))}
                                style={{ width: '100%', fontSize: 'var(--text-base)' }}
                                required
                            >
                                {hostableSongbooks.map(sb => (
                                    <option key={sb.id} value={sb.id}>{sb.name} ({sb.song_count || 0} songs)</option>
                                ))}
                            </select>
                            {(() => {
                                const selected = hostableSongbooks.find(sb => sb.id === createForm.songbook_id)
                                const isUnpublished = selected && (
                                    (selected.song_count || 0) === 0 ||
                                    (!selected.catalog_version_id && !selected.catalog_version && !selected.static_ready)
                                )
                                if (!isUnpublished) return null
                                return (
                                    <div style={{
                                        marginTop: 'var(--space-2)',
                                        padding: 'var(--space-3)',
                                        borderRadius: 'var(--radius-md)',
                                        background: 'rgba(245, 158, 11, 0.1)',
                                        border: '1px solid rgba(245, 158, 11, 0.35)',
                                        color: 'var(--color-chorus)',
                                        fontSize: 'var(--text-xs)',
                                        display: 'flex',
                                        alignItems: 'center',
                                        gap: 'var(--space-2)',
                                    }}>
                                        <span>⚠️</span>
                                        <div>
                                            <strong>No published catalog:</strong> This songbook has no songs or published static catalog yet. Remember to import and publish songs before singers join!
                                        </div>
                                    </div>
                                )
                            })()}
                        </div>

                        {/* Queue Rotation Strategy */}
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

                            <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-2)', marginBottom: 'var(--space-4)' }}>
                                <label style={{
                                    display: 'flex',
                                    alignItems: 'flex-start',
                                    gap: 'var(--space-3)',
                                    padding: 'var(--space-3)',
                                    borderRadius: 'var(--radius-md)',
                                    border: `1px solid ${createForm.rotation_mode === 'fair_priority' ? 'var(--color-accent)' : 'var(--color-border)'}`,
                                    background: createForm.rotation_mode === 'fair_priority' ? 'rgba(255, 42, 95, 0.08)' : 'var(--color-surface)',
                                    cursor: 'pointer',
                                }}>
                                    <input
                                        type="radio"
                                        name="create_rotation_mode"
                                        value="fair_priority"
                                        checked={createForm.rotation_mode === 'fair_priority'}
                                        onChange={() => setCreateForm(prev => ({ ...prev, rotation_mode: 'fair_priority' }))}
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
                                    border: `1px solid ${createForm.rotation_mode === 'round_robin' ? 'var(--color-accent)' : 'var(--color-border)'}`,
                                    background: createForm.rotation_mode === 'round_robin' ? 'rgba(255, 42, 95, 0.08)' : 'var(--color-surface)',
                                    cursor: 'pointer',
                                }}>
                                    <input
                                        type="radio"
                                        name="create_rotation_mode"
                                        value="round_robin"
                                        checked={createForm.rotation_mode === 'round_robin'}
                                        onChange={() => setCreateForm(prev => ({ ...prev, rotation_mode: 'round_robin' }))}
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
                                    border: `1px solid ${createForm.rotation_mode === 'fcfs' ? 'var(--color-accent)' : 'var(--color-border)'}`,
                                    background: createForm.rotation_mode === 'fcfs' ? 'rgba(255, 42, 95, 0.08)' : 'var(--color-surface)',
                                    cursor: 'pointer',
                                }}>
                                    <input
                                        type="radio"
                                        name="create_rotation_mode"
                                        value="fcfs"
                                        checked={createForm.rotation_mode === 'fcfs'}
                                        onChange={() => setCreateForm(prev => ({ ...prev, rotation_mode: 'fcfs' }))}
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

                            <div style={{ fontSize: 'var(--text-xs)', fontWeight: 'bold', color: 'var(--color-text-dim)', marginBottom: 'var(--space-2)', textTransform: 'uppercase', letterSpacing: '0.05em' }}>
                                Singer Limits & Delay
                            </div>

                            <div class="kj-modal-two-col">
                                <label style={{ display: 'flex', alignItems: 'flex-start', gap: 'var(--space-2)', cursor: 'pointer' }}>
                                    <input
                                        type="checkbox"
                                        checked={createForm.auto_accept}
                                        onChange={e => setCreateForm(prev => ({ ...prev, auto_accept: e.target.checked }))}
                                        style={{ marginTop: '3px' }}
                                    />
                                    <div>
                                        <div style={{ fontSize: 'var(--text-xs)', fontWeight: 'bold' }}>Auto-Accept Requests</div>
                                        <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-dim)', marginTop: '2px' }}>
                                            {createForm.auto_accept ? 'Direct to live queue' : 'Hold in Staging Inbox'}
                                        </div>
                                    </div>
                                </label>

                                <div>
                                    <label style={{ display: 'block', fontSize: 'var(--text-xs)', fontWeight: 'bold', marginBottom: '2px' }}>
                                        Singer Cooldown Delay
                                    </label>
                                    <select
                                        value={createForm.submission_delay}
                                        onChange={e => setCreateForm(prev => ({ ...prev, submission_delay: Number(e.target.value) }))}
                                        style={{ width: '100%', fontSize: 'var(--text-xs)', padding: '4px 8px' }}
                                    >
                                        <option value={0}>0s (No delay)</option>
                                        <option value={30}>30s</option>
                                        <option value={60}>60s (1 min - Recommended)</option>
                                        <option value={90}>90s (1.5 min)</option>
                                        <option value={120}>120s (2 min)</option>
                                        <option value={180}>180s (3 min)</option>
                                        <option value={300}>300s (5 min)</option>
                                    </select>
                                </div>
                            </div>

                            <div style={{ marginTop: 'var(--space-3)' }}>
                                <label style={{ display: 'block', fontSize: 'var(--text-xs)', fontWeight: 'bold', marginBottom: '2px' }}>
                                    Active Requests Limit per Singer (0 for unlimited)
                                </label>
                                <select
                                    value={createForm.max_active_per_singer}
                                    onChange={e => setCreateForm(prev => ({ ...prev, max_active_per_singer: Number(e.target.value) }))}
                                    style={{ width: '100%', fontSize: 'var(--text-xs)', padding: '4px 8px' }}
                                >
                                    <option value={0}>0 (Unlimited - no limit)</option>
                                    <option value={1}>1 song in queue (Standard)</option>
                                    <option value={2}>2 songs in queue</option>
                                    <option value={3}>3 songs in queue</option>
                                    <option value={4}>4 songs in queue</option>
                                    <option value={5}>5 songs in queue</option>
                                    <option value={10}>10 songs in queue</option>
                                </select>
                            </div>
                        </div>

                        <div style={{ marginBottom: 'var(--space-3)', display: 'flex', alignItems: 'center', gap: 'var(--space-2)' }}>
                            <input
                                type="checkbox"
                                id="create-form-key-change"
                                checked={createForm.allow_key_change}
                                onChange={e => setCreateForm(prev => ({ ...prev, allow_key_change: e.target.checked }))}
                            />
                            <label htmlFor="create-form-key-change" style={{ fontSize: 'var(--text-sm)', cursor: 'pointer' }}>
                                <strong>🎼 Allow Pitch / Key Changes</strong> <span style={{ color: 'var(--color-text-dim)' }}>(-3♭ to +3♯ semitones)</span>
                            </label>
                        </div>

                        <div style={{ marginBottom: 'var(--space-3)', display: 'flex', alignItems: 'center', gap: 'var(--space-2)' }}>
                            <input
                                type="checkbox"
                                id="create-form-write-ins"
                                checked={!!createForm.allow_write_ins}
                                onChange={e => setCreateForm(prev => ({ ...prev, allow_write_ins: e.target.checked }))}
                            />
                            <label htmlFor="create-form-write-ins" style={{ fontSize: 'var(--text-sm)', cursor: 'pointer' }}>
                                <strong>✍️ Allow Write-In Requests</strong> <span style={{ color: 'var(--color-text-dim)' }}>(let singers request songs not in your book)</span>
                            </label>
                        </div>

                        <div style={{ marginBottom: 'var(--space-4)' }}>
                            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--space-1)' }}>
                                <label htmlFor="create-form-kiosk-pin" style={{ fontSize: 'var(--text-sm)', fontWeight: 'bold' }}>
                                    🔒 Venue Tablet Kiosk PIN <span style={{ color: 'var(--color-text-dim)', fontWeight: 'normal' }}>(optional)</span>
                                </label>
                                <button
                                    type="button"
                                    onClick={() => setCreateForm(prev => ({ ...prev, kiosk_pin: generateRandomKioskPin() }))}
                                    style={{ background: 'none', border: 'none', color: 'var(--color-chorus)', fontSize: 'var(--text-xs)', cursor: 'pointer', textDecoration: 'underline' }}
                                >
                                    🎲 Random PIN
                                </button>
                            </div>
                            <input
                                id="create-form-kiosk-pin"
                                name="kiosk_pin"
                                type="text"
                                inputMode="numeric"
                                minLength={4}
                                maxLength={8}
                                autoCorrect="off"
                                autoCapitalize="none"
                                spellCheck={false}
                                autoComplete="off"
                                enterKeyHint="next"
                                aria-describedby="create-form-kiosk-pin-help"
                                placeholder="Leave blank to auto-generate a 4-digit PIN"
                                value={createForm.kiosk_pin}
                                onInput={e => setCreateForm(prev => ({ ...prev, kiosk_pin: e.currentTarget.value }))}
                                style={{ width: '100%', fontSize: 'var(--text-base)', fontFamily: 'var(--font-mono)', letterSpacing: '0.1em' }}
                            />
                            <div id="create-form-kiosk-pin-help" style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-dim)', marginTop: '4px' }}>
                                4–8 digits required to exit Venue Tablet Kiosk mode. You can view or change this anytime in Show Settings.
                            </div>
                        </div>

                        <div style={{ marginBottom: 'var(--space-6)', display: 'flex', alignItems: 'center', gap: 'var(--space-2)' }}>
                            <input
                                type="checkbox"
                                id="create-form-recurring"
                                checked={createForm.recurring}
                                onChange={e => setCreateForm(prev => ({ ...prev, recurring: e.target.checked }))}
                            />
                            <label htmlFor="create-form-recurring" style={{ fontSize: 'var(--text-sm)', cursor: 'pointer' }}>
                                <strong>Recurring Show</strong> <span style={{ color: 'var(--color-text-dim)' }}>(e.g. weekly residency — easy to restart for new sessions)</span>
                            </label>
                        </div>
                    </div>

                    <div class="kj-modal-footer">
                        <button
                            type="button"
                            class="btn btn-secondary"
                            onClick={onClose}
                            disabled={creatingShow}
                        >
                            Cancel
                        </button>
                        <button
                            type="submit"
                            class="btn btn-primary"
                            disabled={creatingShow}
                            style={{ minWidth: '130px' }}
                        >
                            {creatingShow ? <Spinner size="sm" label="Creating..." inline /> : '🚀 Launch Show'}
                        </button>
                    </div>
                </form>
            </div>
        </div>
    )
}
