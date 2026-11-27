import { useState, useEffect, useMemo } from 'preact/hooks'
import { describeVersion, formatVersionLabel } from '../pages/singer/utils/groupSongVersions.js'

/**
 * Progressive-disclosure backing-track picker for multi-version songs.
 * Defaults to "Host's Choice" (empty string) for zero-friction requests.
 */
export function SongVersionPicker({ versions = [], selectedVersionId = '', onSelectVersionId }) {
    const [expanded, setExpanded] = useState(false)

    useEffect(() => {
        setExpanded(false)
    }, [versions])

    const described = useMemo(() => {
        if (!Array.isArray(versions) || versions.length <= 1) return []
        return versions.map((v, idx) => ({
            version: v,
            id: v.song_id || v.id || '',
            info: describeVersion(v, idx, versions),
        }))
    }, [versions])

    if (described.length <= 1) {
        return null
    }

    const selectedEntry = selectedVersionId
        ? described.find(entry => entry.id === selectedVersionId)
        : null
    const selectedSummary = selectedEntry
        ? formatVersionLabel(selectedEntry.version, described.indexOf(selectedEntry), versions)
        : ''
    const hasAnyMetadata = described.some(entry => entry.info.hasMetadata)

    return (
        <div style={{ marginBottom: 'var(--space-5)' }}>
            <button
                type="button"
                onClick={() => setExpanded(prev => !prev)}
                aria-expanded={expanded}
                style={{
                    background: 'none',
                    border: 'none',
                    padding: 0,
                    color: 'var(--color-info)',
                    fontSize: 'var(--text-xs)',
                    fontWeight: 'var(--font-semibold)',
                    cursor: 'pointer',
                    display: 'inline-flex',
                    alignItems: 'center',
                    gap: 'var(--space-1)',
                    textAlign: 'left',
                }}
            >
                <span>{expanded ? '▾' : '▸'}</span>
                <span>
                    {selectedSummary
                        ? `Version: ${selectedSummary} (${versions.length} available)`
                        : `Select specific version (${versions.length} available)`}
                </span>
            </button>

            {expanded && (
                <div style={{
                    marginTop: 'var(--space-2)',
                    background: 'var(--color-surface-alt)',
                    border: '1px solid var(--color-border)',
                    borderRadius: 'var(--radius-md)',
                    padding: 'var(--space-2)',
                    display: 'flex',
                    flexDirection: 'column',
                    gap: 'var(--space-1)',
                }}>
                    <label
                        htmlFor="song-version-host-choice"
                        style={{
                            display: 'flex',
                            alignItems: 'center',
                            gap: 'var(--space-2)',
                            padding: 'var(--space-2)',
                            borderRadius: 'var(--radius-sm)',
                            cursor: 'pointer',
                            fontSize: 'var(--text-xs)',
                            color: 'var(--color-text)',
                            background: !selectedVersionId ? 'var(--color-surface)' : 'transparent',
                            border: !selectedVersionId ? '1px solid var(--color-border)' : '1px solid transparent',
                        }}
                    >
                        <input
                            id="song-version-host-choice"
                            type="radio"
                            name="song-version-choice"
                            value=""
                            checked={!selectedVersionId}
                            onChange={() => onSelectVersionId('')}
                            style={{ accentColor: 'var(--color-accent)', flexShrink: 0 }}
                        />
                        <span style={{ display: 'flex', flexDirection: 'column', gap: '2px', fontWeight: 'var(--font-semibold)' }}>
                            <span>Host's Choice / Best Available</span>
                            <span style={{ fontWeight: 'var(--font-normal)', color: 'var(--color-text-dim)' }}>
                                Let the host pick the best backing track
                            </span>
                        </span>
                    </label>

                    {described.map(({ id: vId, info }, idx) => {
                        const isSelected = Boolean(selectedVersionId) && selectedVersionId === vId
                        const showDiscPill = Boolean(info.disc && info.disc !== info.label)
                        const radioId = `song-version-option-${idx}`
                        return (
                            <label
                                key={vId || idx}
                                htmlFor={radioId}
                                style={{
                                    display: 'flex',
                                    alignItems: 'flex-start',
                                    gap: 'var(--space-2)',
                                    padding: 'var(--space-2)',
                                    borderRadius: 'var(--radius-sm)',
                                    cursor: 'pointer',
                                    fontSize: 'var(--text-xs)',
                                    color: 'var(--color-text)',
                                    background: isSelected ? 'var(--color-surface)' : 'transparent',
                                    border: isSelected ? '1px solid var(--color-border)' : '1px solid transparent',
                                }}
                            >
                                <input
                                    id={radioId}
                                    type="radio"
                                    name="song-version-choice"
                                    value={vId}
                                    checked={isSelected}
                                    onChange={() => onSelectVersionId(vId)}
                                    style={{ accentColor: 'var(--color-accent)', marginTop: '2px', flexShrink: 0 }}
                                />
                                <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-1)', minWidth: 0, flex: 1 }}>
                                    <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-2)', flexWrap: 'wrap' }}>
                                        <span style={{ fontWeight: 'var(--font-semibold)', color: 'var(--color-text)' }}>
                                            {info.label}
                                        </span>
                                        {showDiscPill && (
                                            <span style={{
                                                fontFamily: 'var(--font-mono)',
                                                fontSize: '0.7rem',
                                                color: 'var(--color-text-muted)',
                                                background: 'var(--color-bg)',
                                                border: '1px solid var(--color-border)',
                                                borderRadius: 'var(--radius-sm)',
                                                padding: '1px var(--space-1)',
                                            }}>
                                                {info.disc}
                                            </span>
                                        )}
                                    </div>
                                    {info.details.length > 0 && (
                                        <div style={{
                                            display: 'flex',
                                            alignItems: 'center',
                                            gap: 'var(--space-1)',
                                            flexWrap: 'wrap',
                                            color: 'var(--color-text-dim)',
                                        }}>
                                            {info.details.map((detail, detailIdx) => (
                                                <span
                                                    key={detailIdx}
                                                    style={{
                                                        background: 'var(--color-surface)',
                                                        border: '1px solid var(--color-border)',
                                                        borderRadius: 'var(--radius-sm)',
                                                        padding: '1px var(--space-2)',
                                                        color: 'var(--color-text-muted)',
                                                    }}
                                                >
                                                    {detail}
                                                </span>
                                            ))}
                                        </div>
                                    )}
                                </div>
                            </label>
                        )
                    })}

                    {!hasAnyMetadata && (
                        <div style={{
                            padding: 'var(--space-1) var(--space-2)',
                            fontSize: 'var(--text-xs)',
                            color: 'var(--color-text-dim)',
                        }}>
                            No publisher or version details were provided in this host's catalog.
                        </div>
                    )}
                </div>
            )}
        </div>
    )
}
