import { Spinner } from '../../Spinner.jsx'
import { RESULTS_PAGE_SIZE } from '../hooks/useAssistantImport.js'

export function ImportAuditView({
    importMode = 'folder',
    results = [],
    excluded = new Set(),
    visibleResultCount = RESULTS_PAGE_SIZE,
    onSetVisibleResultCount,
    onToggleExcluded,
    onSetExcluded,
    pruneMissing = false,
    onSetPruneMissing,
    pattern = 'artist-title',
    onPatternChange,
    parsing = false,
    spreadsheetFile,
    headers = [],
    softwareProfile,
    columnMapping,
    showMappingEditor = false,
    onToggleMappingEditor,
    onUpdateMapping,
    invertedDetected = false,
    onSwapColumns,
    auditing = false,
    auditSummary,
    auditResults = [],
    onApplyAllSuggestions,
    onApplySingleSuggestion,
    onResetSource,
    onImport,
}) {
    const patterns = [
        { id: 'auto', label: 'Auto-Detect (Per File)', example: 'Handles mixed folder formats automatically' },
        { id: 'artist-title', label: 'Artist - Title', example: 'Queen - Bohemian Rhapsody.mp3' },
        { id: 'title-artist', label: 'Title - Artist', example: 'Bohemian Rhapsody - Queen.mp3' },
        { id: 'disc-artist-title', label: 'Disc - Artist - Title', example: 'SC8100 - Queen - Bohemian Rhapsody.mp3' },
    ]

    const totalResultCount = results.length
    const includedResultCount = Math.max(0, totalResultCount - excluded.size)
    const visibleResults = results.slice(0, visibleResultCount)
    const hiddenResultCount = Math.max(0, totalResultCount - visibleResults.length)
    const allSelected = excluded.size === 0 && totalResultCount > 0

    const handleToggleAll = () => {
        if (excluded.size > 0) {
            onSetExcluded(new Set())
        } else {
            onSetExcluded(new Set(results.map((_, i) => i)))
        }
    }

    return (
        <div class="assistant-audit">
            {/* Software profile badge or pattern selector */}
            {importMode === 'spreadsheet' ? (
                <div class="assistant-profile-card">
                    <div class="assistant-profile-header">
                        <div class="assistant-profile-meta">
                            <span class="assistant-profile-eyebrow">Format Profile</span>
                            <h4 class="assistant-profile-title">
                                <span aria-hidden="true">🎯 </span>
                                {softwareProfile?.name || 'Standard CSV'}{' '}
                                <span class="assistant-profile-filename">({spreadsheetFile})</span>
                            </h4>
                        </div>
                        <button
                            type="button"
                            class="btn btn-secondary btn-sm"
                            onClick={onToggleMappingEditor}
                        >
                            {showMappingEditor ? 'Hide Column Mapping' : '⚙️ Customize Columns'}
                        </button>
                    </div>

                    {showMappingEditor && headers.length > 0 && columnMapping && (
                        <div class="assistant-mapping-grid">
                            <div>
                                <label class="assistant-mapping-label" for="assistant-map-artist">Artist Column *</label>
                                <select
                                    id="assistant-map-artist"
                                    class="assistant-mapping-select"
                                    value={columnMapping.artistIndex}
                                    onChange={(e) => onUpdateMapping('artistIndex', e.currentTarget.value)}
                                >
                                    <option value="-1">-- Unmapped --</option>
                                    {headers.map((h, i) => <option key={i} value={i}>{h} (col {i + 1})</option>)}
                                </select>
                            </div>
                            <div>
                                <label class="assistant-mapping-label" for="assistant-map-title">Title Column *</label>
                                <select
                                    id="assistant-map-title"
                                    class="assistant-mapping-select"
                                    value={columnMapping.titleIndex}
                                    onChange={(e) => onUpdateMapping('titleIndex', e.currentTarget.value)}
                                >
                                    <option value="-1">-- Unmapped --</option>
                                    {headers.map((h, i) => <option key={i} value={i}>{h} (col {i + 1})</option>)}
                                </select>
                            </div>
                            <div>
                                <label class="assistant-mapping-label" for="assistant-map-disc">Disc / Track Code</label>
                                <select
                                    id="assistant-map-disc"
                                    class="assistant-mapping-select"
                                    value={columnMapping.discIndex}
                                    onChange={(e) => onUpdateMapping('discIndex', e.currentTarget.value)}
                                >
                                    <option value="-1">-- None --</option>
                                    {headers.map((h, i) => <option key={i} value={i}>{h} (col {i + 1})</option>)}
                                </select>
                            </div>
                            <div>
                                <label class="assistant-mapping-label" for="assistant-map-source">Publisher / Source</label>
                                <select
                                    id="assistant-map-source"
                                    class="assistant-mapping-select"
                                    value={columnMapping.sourceIndex ?? -1}
                                    onChange={(e) => onUpdateMapping('sourceIndex', e.currentTarget.value)}
                                >
                                    <option value="-1">-- None --</option>
                                    {headers.map((h, i) => <option key={i} value={i}>{h} (col {i + 1})</option>)}
                                </select>
                            </div>
                            <div>
                                <label class="assistant-mapping-label" for="assistant-map-genre">Genre / Tags Column</label>
                                <select
                                    id="assistant-map-genre"
                                    class="assistant-mapping-select"
                                    value={columnMapping.genreIndex}
                                    onChange={(e) => onUpdateMapping('genreIndex', e.currentTarget.value)}
                                >
                                    <option value="-1">-- None --</option>
                                    {headers.map((h, i) => <option key={i} value={i}>{h} (col {i + 1})</option>)}
                                </select>
                            </div>
                            <div>
                                <label class="assistant-mapping-label" for="assistant-map-video-url">Video URL Column</label>
                                <select
                                    id="assistant-map-video-url"
                                    class="assistant-mapping-select"
                                    value={columnMapping.videoUrlIndex ?? -1}
                                    onChange={(e) => onUpdateMapping('videoUrlIndex', e.currentTarget.value)}
                                >
                                    <option value="-1">-- None --</option>
                                    {headers.map((h, i) => <option key={i} value={i}>{h} (col {i + 1})</option>)}
                                </select>
                            </div>
                        </div>
                    )}
                </div>
            ) : (
                <div class="assistant-pattern-bar">
                    <label class="assistant-pattern-label" for="assistant-naming-pattern">Naming Pattern</label>
                    <select
                        id="assistant-naming-pattern"
                        class="assistant-pattern-select"
                        disabled={parsing}
                        value={pattern}
                        onChange={(e) => onPatternChange(e.currentTarget.value)}
                    >
                        {patterns.map(p => <option key={p.id} value={p.id}>{p.label}</option>)}
                    </select>
                    {parsing && <Spinner size="sm" label="Parsing files..." />}
                </div>
            )}

            <div class="assistant-prune-card">
                <label class="assistant-prune-label">
                    <input type="checkbox" checked={pruneMissing} onChange={(e) => onSetPruneMissing(e.currentTarget.checked)} />
                    <span>Hide songs missing from this import (Soft-Prune)</span>
                </label>
                <p class="assistant-prune-hint">
                    Leave unchecked to add or update songs. Enable only when replacing your full catalog.
                </p>
            </div>

            {/* Inverted Column Warning Banner */}
            {invertedDetected && (
                <div class="assistant-warning-banner" role="alert">
                    <div class="assistant-warning-copy">
                        <span class="assistant-warning-icon" aria-hidden="true">⚠️</span>
                        <div>
                            <strong class="assistant-warning-title">Possible Inverted Columns Detected</strong>
                            <span class="assistant-warning-desc">
                                Artist and Title appear swapped in this file. Tap below to invert automatically.
                            </span>
                        </div>
                    </div>
                    <button
                        type="button"
                        class="btn btn-secondary btn-sm assistant-swap-btn"
                        onClick={onSwapColumns}
                    >
                        🔄 Swap Artist &amp; Title
                    </button>
                </div>
            )}

            {/* Health Summary Bar */}
            {auditSummary && (
                <div class="assistant-audit-summary">
                    <div class="assistant-audit-summary-top">
                        <div class="assistant-audit-summary-heading">
                            <span>Verification Audit</span>
                            {auditing && <Spinner size="sm" label="Auditing..." />}
                        </div>
                        {auditSummary.suggestions > 0 && (
                            <button
                                type="button"
                                class="btn btn-secondary btn-sm assistant-apply-all-btn"
                                onClick={onApplyAllSuggestions}
                            >
                                ✨ Apply All Suggestions ({auditSummary.suggestions})
                            </button>
                        )}
                    </div>
                    <div class="assistant-audit-stats">
                        <div class="assistant-audit-stat">
                            <span class="assistant-audit-stat-value assistant-stat--verified">
                                {auditSummary.verified.toLocaleString()}
                            </span>
                            <span class="assistant-audit-stat-label">🟢 Verified</span>
                        </div>
                        <div class="assistant-audit-stat">
                            <span class="assistant-audit-stat-value assistant-stat--suggestion">
                                {auditSummary.suggestions.toLocaleString()}
                            </span>
                            <span class="assistant-audit-stat-label">🟡 Suggestions</span>
                        </div>
                        <div class="assistant-audit-stat">
                            <span class="assistant-audit-stat-value assistant-stat--custom">
                                {auditSummary.custom.toLocaleString()}
                            </span>
                            <span class="assistant-audit-stat-label">⚪ Custom</span>
                        </div>
                    </div>
                </div>
            )}

            {/* Mobile Select-All Bar (shown when table switches to stacked cards) */}
            <div class="assistant-mobile-select-bar">
                <label class="assistant-mobile-select-label">
                    <input
                        type="checkbox"
                        checked={allSelected}
                        onChange={handleToggleAll}
                    />
                    <span>{allSelected ? 'Deselect all' : 'Select all'}</span>
                </label>
                <span>{includedResultCount.toLocaleString()} / {totalResultCount.toLocaleString()} selected</span>
            </div>

            <div class="assistant-table-wrap" tabIndex={0} role="region" aria-label="Parsed songs preview">
                <table class="assistant-table">
                    <thead>
                        <tr>
                            <th>
                                <input
                                    type="checkbox"
                                    checked={allSelected}
                                    onChange={handleToggleAll}
                                    aria-label="Toggle select all"
                                />
                            </th>
                            <th>Artist</th>
                            <th>Title</th>
                            <th>Disc / Brand</th>
                            <th>Genre</th>
                            <th>Status</th>
                        </tr>
                    </thead>
                    <tbody>
                        {visibleResults.map((r, i) => {
                            const audit = auditResults?.[i]
                            const publisherDisplay = r.source || audit?.provider || ''
                            const baseDiscDisplay = r.disc
                                ? (publisherDisplay && !r.disc.toLowerCase().includes(publisherDisplay.toLowerCase()) ? `${r.disc} (${publisherDisplay})` : r.disc)
                                : publisherDisplay
                            const discDisplay = r.video_url
                                ? (baseDiscDisplay ? `${baseDiscDisplay} • Video` : 'Video')
                                : baseDiscDisplay
                            const hasGenreSuggestion = audit?.status === 'suggestion' && (audit.suggested_genre || audit.suggested_year)
                            const genreEmpty = !r.genre && !hasGenreSuggestion
                            return (
                                <tr key={i} class={excluded.has(i) ? 'is-excluded' : ''}>
                                    <td class="assistant-col-check">
                                        <label class="assistant-row-check-label">
                                            <input
                                                type="checkbox"
                                                checked={!excluded.has(i)}
                                                onChange={() => onToggleExcluded(i)}
                                                aria-label={`Import ${r.title || 'song'}`}
                                            />
                                        </label>
                                    </td>
                                    <td class="assistant-col-artist">
                                        <span class="assistant-mobile-sr-label">Artist: </span>
                                        {r.artist || '—'}
                                        {audit?.status === 'suggestion' && audit.suggested_artist && audit.suggested_artist !== r.artist && (
                                            <div class="assistant-suggestion-note">
                                                Suggested: {audit.suggested_artist}
                                            </div>
                                        )}
                                    </td>
                                    <td class="assistant-col-title">
                                        <span class="assistant-mobile-sr-label">Title: </span>
                                        {r.title || '—'}
                                        {audit?.status === 'suggestion' && audit.suggested_title && audit.suggested_title !== r.title && (
                                            <div class="assistant-suggestion-note">
                                                Suggested: {audit.suggested_title}
                                            </div>
                                        )}
                                        {r.location && (
                                            <div title={r.location} style={{ fontFamily: 'var(--font-mono)', fontSize: '0.7rem', color: 'var(--color-text-dim)', marginTop: '2px', overflowWrap: 'anywhere', wordBreak: 'break-word' }}>
                                                {r.location}
                                            </div>
                                        )}
                                    </td>
                                    <td class="assistant-col-disc" data-empty={!discDisplay ? 'true' : undefined}>
                                        {discDisplay || '—'}
                                    </td>
                                    <td class="assistant-col-genre" data-empty={genreEmpty ? 'true' : undefined}>
                                        {r.genre || '—'}
                                        {hasGenreSuggestion && (
                                            <div class="assistant-suggestion-note">
                                                Suggested: {audit.suggested_genre || ''}{audit.suggested_genre && audit.suggested_year ? ` (${audit.suggested_year})` : audit.suggested_year || ''}
                                            </div>
                                        )}
                                    </td>
                                    <td class="assistant-col-status">
                                        <span class="assistant-mobile-sr-label">Status: </span>
                                        {audit?.status === 'verified' && audit.provider && (
                                            <span
                                                title={`Authoritative backing track verified from ${audit.provider} (${audit.disc_code || r.disc || ''})`}
                                                class="assistant-status-verified"
                                            >
                                                🟢 Verified • 💿 {audit.provider}
                                            </span>
                                        )}
                                        {audit?.status === 'verified' && !audit.provider && (
                                            <span
                                                title="Universal canonical song matched for singer favorites & pitch memory (Host Default audio)"
                                                class="assistant-status-verified"
                                            >
                                                🟢 Verified Song
                                            </span>
                                        )}
                                        {audit?.status === 'suggestion' && (
                                            <div class="assistant-status-suggestion">
                                                <span title={audit.change_reason || 'Suggestion available'} class="assistant-status-suggestion-label">
                                                    🟡 Suggestion
                                                </span>
                                                <button
                                                    type="button"
                                                    class="btn btn-secondary btn-xs assistant-status-apply-btn"
                                                    onClick={() => onApplySingleSuggestion(i)}
                                                    title={audit.change_reason || 'Apply suggestion'}
                                                >
                                                    Apply
                                                </button>
                                            </div>
                                        )}
                                        {(!audit || audit?.status === 'custom') && (
                                            <span title="Custom host track - preserved untouched" class="assistant-status-custom">
                                                ⚪ Custom Track
                                            </span>
                                        )}
                                    </td>
                                </tr>
                            )
                        })}
                    </tbody>
                </table>
                {hiddenResultCount > 0 && (
                    <div class="assistant-table-more">
                        <p class="assistant-table-more-text">
                            Showing {visibleResults.length} of {totalResultCount} parsed songs. All non-excluded songs will be imported.
                        </p>
                        <button
                            type="button"
                            class="btn btn-secondary btn-sm"
                            onClick={() => onSetVisibleResultCount(count => Math.min(count + RESULTS_PAGE_SIZE, results.length))}
                        >
                            Show next {Math.min(RESULTS_PAGE_SIZE, hiddenResultCount)} in table
                        </button>
                    </div>
                )}
            </div>

            <div class="assistant-audit-footer">
                <button type="button" class="btn btn-secondary" onClick={onResetSource}>
                    Choose Different Source
                </button>
                <button type="button" class="btn btn-primary" onClick={onImport} disabled={parsing || includedResultCount === 0}>
                    Import {includedResultCount} Songs
                </button>
            </div>
        </div>
    )
}
