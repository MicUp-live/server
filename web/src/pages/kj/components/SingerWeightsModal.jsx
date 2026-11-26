import { useState, useEffect, useMemo } from 'preact/hooks'

export const MIN_SINGER_WEIGHT = -10
export const MAX_SINGER_WEIGHT = 10

export function normalizeSingerKey(name) {
    return (name || '').trim().toLowerCase()
}

export function clampSingerWeight(value) {
    const n = Number.isFinite(value) ? Math.trunc(value) : 0
    if (n < MIN_SINGER_WEIGHT) return MIN_SINGER_WEIGHT
    if (n > MAX_SINGER_WEIGHT) return MAX_SINGER_WEIGHT
    return n
}

export function sanitizeSingerWeightsPayload(weights) {
    const clean = {}
    for (const [rawKey, rawVal] of Object.entries(weights || {})) {
        const key = normalizeSingerKey(rawKey)
        const val = clampSingerWeight(rawVal)
        if (key && val !== 0) {
            clean[key] = val
        }
    }
    return clean
}

function formatWeightLabel(weight) {
    if (weight > 0) return `VIP +${weight}`
    if (weight < 0) return `Cooldown ${weight}`
    return 'Normal (0)'
}

export function SingerWeightsModal({
    isOpen,
    onClose,
    currentEvent,
    requests = [],
    initialSinger = '',
    onSaveSingerWeights,
    savingWeights = false,
}) {
    const [draftWeights, setDraftWeights] = useState({})
    const [initialOrderWeights, setInitialOrderWeights] = useState({})
    const [extraNames, setExtraNames] = useState({})
    const [newSingerName, setNewSingerName] = useState('')
    const [newSingerWeight, setNewSingerWeight] = useState(3)
    const [addError, setAddError] = useState('')

    useEffect(() => {
        if (!isOpen) return
        const seeded = sanitizeSingerWeightsPayload(currentEvent?.singer_weights)
        setDraftWeights(seeded)
        setInitialOrderWeights(seeded)
        const initialKey = normalizeSingerKey(initialSinger)
        setExtraNames(initialKey ? { [initialKey]: initialSinger.trim() } : {})
        setNewSingerName('')
        setNewSingerWeight(3)
        setAddError('')
    }, [isOpen, currentEvent?.id, currentEvent?.singer_weights, initialSinger])

    const singerRows = useMemo(() => {
        const displayByKey = new Map()
        for (const req of requests) {
            const raw = (req?.singer_name || '').trim()
            const key = normalizeSingerKey(raw)
            if (key && !displayByKey.has(key)) {
                displayByKey.set(key, raw)
            }
        }
        for (const [key, display] of Object.entries(extraNames)) {
            if (key && !displayByKey.has(key)) {
                displayByKey.set(key, display)
            }
        }
        for (const key of Object.keys(initialOrderWeights)) {
            if (key && !displayByKey.has(key)) {
                displayByKey.set(key, key)
            }
        }
        for (const key of Object.keys(draftWeights)) {
            if (key && !displayByKey.has(key)) {
                displayByKey.set(key, key)
            }
        }

        const focusKey = normalizeSingerKey(initialSinger)
        return Array.from(displayByKey.entries())
            .map(([key, displayName]) => ({
                key,
                displayName,
                weight: clampSingerWeight(draftWeights[key] || 0),
                initialWeight: clampSingerWeight(initialOrderWeights[key] || 0),
                isFocused: focusKey !== '' && key === focusKey,
                isAdded: Object.prototype.hasOwnProperty.call(extraNames, key) && key !== focusKey,
            }))
            .sort((a, b) => {
                if (a.isFocused !== b.isFocused) return a.isFocused ? -1 : 1
                if (a.isAdded !== b.isAdded) return a.isAdded ? -1 : 1
                const aWeighted = a.initialWeight !== 0
                const bWeighted = b.initialWeight !== 0
                if (aWeighted !== bWeighted) return aWeighted ? -1 : 1
                if (Math.abs(a.initialWeight) !== Math.abs(b.initialWeight)) {
                    return Math.abs(b.initialWeight) - Math.abs(a.initialWeight)
                }
                return a.displayName.localeCompare(b.displayName)
            })
    }, [requests, extraNames, initialOrderWeights, draftWeights, initialSinger])

    if (!isOpen) return null

    const adjustWeight = (key, delta) => {
        setDraftWeights(prev => {
            const nextVal = clampSingerWeight((prev[key] || 0) + delta)
            const next = { ...prev }
            if (nextVal === 0) {
                delete next[key]
            } else {
                next[key] = nextVal
            }
            return next
        })
    }

    const resetWeight = (key) => {
        setDraftWeights(prev => {
            const next = { ...prev }
            delete next[key]
            return next
        })
    }

    const handleAddSinger = () => {
        const trimmed = newSingerName.trim()
        const key = normalizeSingerKey(trimmed)
        if (!key) {
            setAddError('Enter a singer name to add a weight.')
            return
        }
        setAddError('')
        setExtraNames(prev => ({ ...prev, [key]: trimmed }))
        const weight = clampSingerWeight(newSingerWeight)
        setDraftWeights(prev => {
            const next = { ...prev }
            if (weight === 0) {
                delete next[key]
            } else {
                next[key] = weight
            }
            return next
        })
        setNewSingerName('')
    }

    const handleClearAll = () => {
        setDraftWeights({})
    }

    const handleSubmit = async (e) => {
        e.preventDefault()
        const payload = sanitizeSingerWeightsPayload(draftWeights)
        const ok = await onSaveSingerWeights(payload)
        if (ok) {
            onClose()
        }
    }

    const activeWeightCount = Object.keys(sanitizeSingerWeightsPayload(draftWeights)).length

    return (
        <div
            class="modal-overlay"
            onClick={onClose}
            role="dialog"
            aria-modal="true"
            aria-labelledby="singer-weights-title"
        >
            <div
                class="modal-sheet"
                onClick={e => e.stopPropagation()}
                style={{ maxWidth: '560px' }}
            >
                <div class="modal-drag-handle" aria-hidden="true" />
                <div class="kj-modal-header">
                    <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-2)' }}>
                        <span style={{ fontSize: '1.35rem' }} aria-hidden="true">⚖️</span>
                        <h3 id="singer-weights-title" style={{ margin: 0 }}>Private Singer Weights</h3>
                    </div>
                    <button
                        type="button"
                        class="btn btn-secondary btn-sm"
                        onClick={onClose}
                        aria-label="Close singer weights modal"
                    >
                        ✕
                    </button>
                </div>

                <form
                    onSubmit={handleSubmit}
                    aria-labelledby="singer-weights-title"
                    style={{ display: 'flex', flexDirection: 'column', flex: 1, minHeight: 0, overflow: 'hidden' }}
                >
                    <div class="kj-modal-body">
                        <div style={{
                            padding: 'var(--space-3)',
                            borderRadius: 'var(--radius-md)',
                            background: 'var(--color-surface-alt)',
                            border: '1px solid var(--color-border)',
                            marginBottom: 'var(--space-4)',
                            fontSize: 'var(--text-xs)',
                            color: 'var(--color-text-muted)',
                            lineHeight: 1.5,
                        }}>
                            <strong style={{ color: 'var(--color-text)' }}>🔒 Private to host</strong> — never shown to singers or on the stage TV.
                            Positive values (<code>+1</code> to <code>+10</code>) gently boost VIPs; negative values (<code>-1</code> to <code>-10</code>) quietly cool down singers within their cycle without breaking the 3-song stage lock.
                        </div>

                        {/* Add singer by name */}
                        <div style={{ marginBottom: 'var(--space-4)' }}>
                            <label
                                for="singer-weight-name"
                                style={{
                                    display: 'block',
                                    fontSize: 'var(--text-xs)',
                                    fontWeight: 'var(--font-bold)',
                                    color: 'var(--color-text)',
                                    marginBottom: 'var(--space-1)',
                                }}
                            >
                                Add or pre-set a singer
                            </label>
                            <div style={{ display: 'flex', gap: 'var(--space-2)', flexWrap: 'wrap', alignItems: 'center' }}>
                                <input
                                    id="singer-weight-name"
                                    name="new_singer_name"
                                    type="text"
                                    value={newSingerName}
                                    onInput={e => {
                                        setNewSingerName(e.currentTarget.value)
                                        if (addError) setAddError('')
                                    }}
                                    onKeyDown={e => {
                                        if (e.key === 'Enter' && !e.isComposing) {
                                            e.preventDefault()
                                            handleAddSinger()
                                        }
                                    }}
                                    placeholder="Singer name (e.g. Birthday Sarah)"
                                    maxLength={60}
                                    aria-describedby="singer-weight-help"
                                    aria-invalid={addError ? 'true' : undefined}
                                    style={{
                                        flex: '1 1 180px',
                                        minHeight: '40px',
                                        padding: 'var(--space-2) var(--space-3)',
                                        fontSize: 'max(1rem, 1em)',
                                        borderRadius: 'var(--radius-md)',
                                        border: `1px solid ${addError ? 'var(--color-danger)' : 'var(--color-border)'}`,
                                        background: 'var(--color-surface-alt)',
                                        color: 'var(--color-text)',
                                    }}
                                />
                                <div style={{ display: 'inline-flex', alignItems: 'center', gap: '4px' }}>
                                    <button
                                        type="button"
                                        class="btn btn-secondary btn-sm"
                                        onClick={() => setNewSingerWeight(w => clampSingerWeight(w - 1))}
                                        disabled={newSingerWeight <= MIN_SINGER_WEIGHT}
                                        aria-label="Decrease new singer weight"
                                    >
                                        −
                                    </button>
                                    <span style={{
                                        minWidth: '36px',
                                        textAlign: 'center',
                                        fontSize: 'var(--text-sm)',
                                        fontWeight: 'var(--font-bold)',
                                        color: newSingerWeight > 0 ? 'var(--color-success)' : (newSingerWeight < 0 ? 'var(--color-danger)' : 'var(--color-text-dim)'),
                                    }}>
                                        {newSingerWeight > 0 ? `+${newSingerWeight}` : newSingerWeight}
                                    </span>
                                    <button
                                        type="button"
                                        class="btn btn-secondary btn-sm"
                                        onClick={() => setNewSingerWeight(w => clampSingerWeight(w + 1))}
                                        disabled={newSingerWeight >= MAX_SINGER_WEIGHT}
                                        aria-label="Increase new singer weight"
                                    >
                                        +
                                    </button>
                                </div>
                                <button
                                    type="button"
                                    class="btn btn-secondary btn-sm"
                                    onClick={handleAddSinger}
                                    style={{ minHeight: '40px' }}
                                >
                                    Add
                                </button>
                            </div>
                            <div
                                id="singer-weight-help"
                                role="status"
                                aria-live="polite"
                                style={{
                                    marginTop: 'var(--space-1)',
                                    fontSize: 'var(--text-xs)',
                                    color: addError ? 'var(--color-danger)' : 'var(--color-text-dim)',
                                }}
                            >
                                {addError || 'Matched by singer name (case-insensitive). Takes effect on future queue placements.'}
                            </div>
                        </div>

                        {/* Singer list */}
                        {singerRows.length === 0 ? (
                            <div style={{
                                textAlign: 'center',
                                padding: 'var(--space-6)',
                                borderRadius: 'var(--radius-md)',
                                border: '1px dashed var(--color-border)',
                                color: 'var(--color-text-dim)',
                                fontSize: 'var(--text-sm)',
                            }}>
                                No singers in tonight's show yet. Add a singer above to pre-set a VIP boost.
                            </div>
                        ) : (
                            <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-2)' }}>
                                {singerRows.map(row => {
                                    const badgeColor = row.weight > 0
                                        ? 'var(--color-success)'
                                        : (row.weight < 0 ? 'var(--color-danger)' : 'var(--color-text-dim)')
                                    return (
                                        <div
                                            key={row.key}
                                            style={{
                                                display: 'flex',
                                                justifyContent: 'space-between',
                                                alignItems: 'center',
                                                gap: 'var(--space-2)',
                                                padding: 'var(--space-2) var(--space-3)',
                                                borderRadius: 'var(--radius-md)',
                                                background: row.isFocused ? 'rgba(255, 42, 95, 0.08)' : 'var(--color-surface-alt)',
                                                border: `1px solid ${row.isFocused ? 'var(--color-accent)' : 'var(--color-border)'}`,
                                            }}
                                        >
                                            <div style={{ minWidth: 0, flex: 1 }}>
                                                <div style={{
                                                    fontSize: 'var(--text-sm)',
                                                    fontWeight: 'var(--font-bold)',
                                                    color: 'var(--color-text)',
                                                    overflow: 'hidden',
                                                    textOverflow: 'ellipsis',
                                                    whiteSpace: 'nowrap',
                                                }}>
                                                    {row.displayName}
                                                </div>
                                                <div style={{ fontSize: 'var(--text-xs)', color: badgeColor, fontWeight: 'var(--font-semibold)' }}>
                                                    {formatWeightLabel(row.weight)}
                                                </div>
                                            </div>
                                            <div style={{ display: 'inline-flex', alignItems: 'center', gap: '6px', flexShrink: 0 }}>
                                                <button
                                                    type="button"
                                                    class="btn btn-secondary btn-sm"
                                                    onClick={() => adjustWeight(row.key, -1)}
                                                    disabled={row.weight <= MIN_SINGER_WEIGHT}
                                                    aria-label={`Decrease weight for ${row.displayName}`}
                                                >
                                                    −
                                                </button>
                                                <span style={{
                                                    minWidth: '34px',
                                                    textAlign: 'center',
                                                    fontSize: 'var(--text-sm)',
                                                    fontWeight: 'var(--font-bold)',
                                                    color: badgeColor,
                                                }}>
                                                    {row.weight > 0 ? `+${row.weight}` : row.weight}
                                                </span>
                                                <button
                                                    type="button"
                                                    class="btn btn-secondary btn-sm"
                                                    onClick={() => adjustWeight(row.key, 1)}
                                                    disabled={row.weight >= MAX_SINGER_WEIGHT}
                                                    aria-label={`Increase weight for ${row.displayName}`}
                                                >
                                                    +
                                                </button>
                                                {row.weight !== 0 && (
                                                    <button
                                                        type="button"
                                                        class="btn btn-secondary btn-sm"
                                                        onClick={() => resetWeight(row.key)}
                                                        aria-label={`Reset weight for ${row.displayName} to 0`}
                                                        title="Reset to 0"
                                                    >
                                                        Reset
                                                    </button>
                                                )}
                                            </div>
                                        </div>
                                    )
                                })}
                            </div>
                        )}
                    </div>

                    <div class="kj-modal-footer" style={{ justifyContent: 'space-between' }}>
                        <div>
                            {activeWeightCount > 0 && (
                                <button
                                    type="button"
                                    class="btn btn-secondary btn-sm"
                                    onClick={handleClearAll}
                                    style={{ color: 'var(--color-danger)', borderColor: 'rgba(239, 68, 68, 0.4)' }}
                                >
                                    Clear All ({activeWeightCount})
                                </button>
                            )}
                        </div>
                        <div style={{ display: 'flex', gap: 'var(--space-2)' }}>
                            <button type="button" class="btn btn-secondary" onClick={onClose}>Cancel</button>
                            <button type="submit" class="btn btn-primary" disabled={savingWeights}>
                                {savingWeights ? 'Saving...' : 'Save Weights'}
                            </button>
                        </div>
                    </div>
                </form>
            </div>
        </div>
    )
}
