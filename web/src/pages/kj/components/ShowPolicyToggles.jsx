export function ShowPolicyToggles({ settingsForm, setSettingsForm }) {
    return (
        <>
            {/* Pitch / Key Adjustments Toggle */}
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
                        checked={settingsForm.allow_key_change}
                        onChange={e => setSettingsForm(prev => ({ ...prev, allow_key_change: e.target.checked }))}
                        style={{ marginTop: '3px' }}
                    />
                    <div>
                        <div style={{ fontSize: 'var(--text-sm)', fontWeight: 'bold', color: 'var(--color-text)' }}>
                            🎼 Allow Pitch / Key Changes
                        </div>
                        <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', marginTop: '2px', lineHeight: 1.4 }}>
                            {settingsForm.allow_key_change
                                ? 'Enabled: Singers can request pitch key adjustments (-3♭ to +3♯ semitones) in the song request modal.'
                                : 'Disabled: Pitch adjustments are hidden on singer and kiosk request screens.'}
                        </div>
                    </div>
                </label>
            </div>

            {/* Custom Write-In Requests Toggle */}
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
                        checked={!!settingsForm.allow_write_ins}
                        onChange={e => setSettingsForm(prev => ({ ...prev, allow_write_ins: e.target.checked }))}
                        style={{ marginTop: '3px' }}
                    />
                    <div>
                        <div style={{ fontSize: 'var(--text-sm)', fontWeight: 'bold', color: 'var(--color-text)' }}>
                            ✍️ Allow Custom Write-In Requests
                        </div>
                        <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-muted)', marginTop: '2px', lineHeight: 1.4 }}>
                            {settingsForm.allow_write_ins
                                ? 'Enabled: Singers can request songs not in your catalog by typing an Artist & Title.'
                                : 'Disabled: Singers can only request tracks from your imported songbook.'}
                        </div>
                    </div>
                </label>
            </div>
        </>
    )
}
