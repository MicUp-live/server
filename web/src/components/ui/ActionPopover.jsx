import { useEffect, useMemo, useRef, useState } from 'preact/hooks'
import './ActionPopover.css'

let nextPopoverSeq = 0

function supportsNativePopover() {
    return typeof HTMLElement !== 'undefined' && Object.hasOwn(HTMLElement.prototype, 'popover')
}

export function ActionPopover({
    label = 'More actions',
    triggerText = '⋯',
    triggerTitle,
    triggerClass = 'btn btn-secondary btn-sm kj-more-btn',
    panelTitle,
    badgeDot = false,
    children,
}) {
    const panelId = useMemo(() => `kj-action-popover-${++nextPopoverSeq}`, [])
    const panelRef = useRef(null)
    const triggerRef = useRef(null)
    const [fallbackOpen, setFallbackOpen] = useState(false)
    const nativeSupported = useMemo(() => supportsNativePopover(), [])

    const close = () => {
        if (nativeSupported) {
            try {
                panelRef.current?.hidePopover?.()
            } catch {
                // Already closed
            }
        } else {
            setFallbackOpen(false)
            triggerRef.current?.focus?.()
        }
    }

    useEffect(() => {
        if (nativeSupported || !fallbackOpen) return
        const onKeyDown = (e) => {
            if (e.key === 'Escape') {
                setFallbackOpen(false)
                triggerRef.current?.focus?.()
            }
        }
        const onPointerDown = (e) => {
            if (
                panelRef.current &&
                !panelRef.current.contains(e.target) &&
                triggerRef.current &&
                !triggerRef.current.contains(e.target)
            ) {
                setFallbackOpen(false)
            }
        }
        document.addEventListener('keydown', onKeyDown)
        document.addEventListener('pointerdown', onPointerDown)
        return () => {
            document.removeEventListener('keydown', onKeyDown)
            document.removeEventListener('pointerdown', onPointerDown)
        }
    }, [nativeSupported, fallbackOpen])

    return (
        <>
            <button
                ref={triggerRef}
                type="button"
                class={triggerClass}
                aria-label={label}
                title={triggerTitle || label}
                popovertarget={panelId}
                aria-expanded={!nativeSupported ? fallbackOpen : undefined}
                aria-controls={!nativeSupported ? panelId : undefined}
                onClick={!nativeSupported ? () => setFallbackOpen(prev => !prev) : undefined}
            >
                <span aria-hidden="true">{triggerText}</span>
                {badgeDot && <span class="kj-more-dot" aria-hidden="true" />}
            </button>

            <div
                ref={panelRef}
                id={panelId}
                popover="auto"
                role="region"
                data-fallback-open={!nativeSupported && fallbackOpen ? 'true' : undefined}
                class="kj-action-popover"
                aria-label={panelTitle || label}
            >
                {panelTitle && (
                    <div class="kj-action-popover-header">
                        <span>{panelTitle}</span>
                        <button
                            type="button"
                            class="kj-action-popover-close"
                            onClick={close}
                            aria-label="Close actions"
                        >
                            ✕
                        </button>
                    </div>
                )}
                <div class="kj-action-popover-list">
                    {typeof children === 'function' ? children(close) : children}
                </div>
            </div>
        </>
    )
}
