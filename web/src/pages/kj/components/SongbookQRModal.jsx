import { useEffect, useId, useRef, useState } from 'preact/hooks'
import {
    buildQrFilename,
    buildSongbookPublicUrl,
    downloadDataUrl,
    downloadSvg,
    generateSongbookQrPngDataUrl,
    generateSongbookQrSvg,
} from '../../../utils/qrExport.js'
import './SongbookQRModal.css'

export function SongbookQRModal({ songbook, onClose }) {
    const dialogRef = useRef(null)
    const feedbackTimerRef = useRef(null)
    const titleId = useId()
    const [pngDataUrl, setPngDataUrl] = useState('')
    const [loading, setLoading] = useState(false)
    const [error, setError] = useState('')
    const [downloading, setDownloading] = useState('')
    const [feedback, setFeedback] = useState({ type: 'info', text: '' })

    const url = songbook
        ? buildSongbookPublicUrl(songbook, typeof window !== 'undefined' ? window.location.origin : '')
        : ''

    const showFeedback = (text, type = 'info') => {
        if (feedbackTimerRef.current) clearTimeout(feedbackTimerRef.current)
        setFeedback({ type, text })
        feedbackTimerRef.current = setTimeout(() => {
            setFeedback({ type: 'info', text: '' })
            feedbackTimerRef.current = null
        }, 3000)
    }

    useEffect(() => () => {
        if (feedbackTimerRef.current) clearTimeout(feedbackTimerRef.current)
    }, [])

    // Sync open/closed state with native <dialog>.showModal()
    useEffect(() => {
        const dialog = dialogRef.current
        if (!dialog) return

        if (songbook) {
            if (!dialog.open && typeof dialog.showModal === 'function') {
                dialog.showModal()
            }
        } else if (dialog.open) {
            dialog.close()
        }
    }, [songbook])

    // Fallback light-dismiss for browsers without native <dialog closedby="any">
    useEffect(() => {
        const dialog = dialogRef.current
        if (!dialog || typeof HTMLDialogElement === 'undefined') return
        if ('closedBy' in HTMLDialogElement.prototype) return

        const isOutside = (e) => {
            const rect = dialog.getBoundingClientRect()
            return (
                e.clientX < rect.left ||
                e.clientX > rect.right ||
                e.clientY < rect.top ||
                e.clientY > rect.bottom
            )
        }

        let pressedOutside = false
        const handlePointerDown = (e) => {
            pressedOutside = isOutside(e)
        }
        const handlePointerUp = (e) => {
            if (pressedOutside && isOutside(e)) {
                dialog.close()
            }
            pressedOutside = false
        }

        dialog.addEventListener('pointerdown', handlePointerDown)
        dialog.addEventListener('pointerup', handlePointerUp)
        return () => {
            dialog.removeEventListener('pointerdown', handlePointerDown)
            dialog.removeEventListener('pointerup', handlePointerUp)
        }
    }, [])

    // Generate high-res PNG whenever the target URL changes
    useEffect(() => {
        if (!url) {
            setPngDataUrl('')
            setError('')
            setLoading(false)
            setFeedback({ type: 'info', text: '' })
            return
        }

        let active = true
        setPngDataUrl('')
        setLoading(true)
        setError('')
        setFeedback({ type: 'info', text: '' })

        generateSongbookQrPngDataUrl(url, 1200)
            .then((dataUrl) => {
                if (active) setPngDataUrl(dataUrl)
            })
            .catch((err) => {
                console.error('Failed to generate songbook QR code:', err)
                if (active) setError('Unable to generate QR code.')
            })
            .finally(() => {
                if (active) setLoading(false)
            })

        return () => {
            active = false
        }
    }, [url])

    const handleDialogClose = () => {
        const dialog = dialogRef.current
        if (dialog) {
            dialog.returnValue = ''
        }
        onClose?.()
    }

    const handleCopyLink = async () => {
        if (!url) return
        try {
            await navigator.clipboard.writeText(url)
            showFeedback('Share link copied')
        } catch {
            // Mount fallback textarea inside the open <dialog> because the rest of the page is inert
            const container = dialogRef.current || document.body
            const input = document.createElement('textarea')
            input.value = url
            input.setAttribute('readonly', '')
            input.style.position = 'fixed'
            input.style.opacity = '0'
            container.append(input)
            input.select()
            input.setSelectionRange(0, input.value.length)
            const copied = document.execCommand('copy')
            input.remove()
            if (copied) {
                showFeedback('Share link copied')
            } else {
                showFeedback('Could not copy link automatically', 'error')
            }
        }
    }

    const handleDownloadPng = async () => {
        if (!songbook || !url || downloading) return
        setDownloading('png')
        try {
            const dataUrl = pngDataUrl || await generateSongbookQrPngDataUrl(url, 1200)
            downloadDataUrl(dataUrl, buildQrFilename(songbook, 'png'))
            showFeedback('QR code PNG downloaded')
        } catch (err) {
            console.error('Failed to download QR PNG:', err)
            showFeedback('Failed to download PNG', 'error')
        } finally {
            setDownloading('')
        }
    }

    const handleDownloadSvg = async () => {
        if (!songbook || !url || downloading) return
        setDownloading('svg')
        try {
            const svg = await generateSongbookQrSvg(url)
            downloadSvg(svg, buildQrFilename(songbook, 'svg'))
            showFeedback('QR code SVG downloaded')
        } catch (err) {
            console.error('Failed to download QR SVG:', err)
            showFeedback('Failed to download SVG', 'error')
        } finally {
            setDownloading('')
        }
    }

    return (
        <dialog
            ref={dialogRef}
            class="songbook-qr-dialog"
            closedby="any"
            aria-labelledby={titleId}
            onClose={handleDialogClose}
        >
            {songbook && (
                <>
                    <div class="songbook-qr-header">
                        <div style={{ minWidth: 0 }}>
                            <div class="songbook-qr-eyebrow">Songbook QR Code</div>
                            <h2 id={titleId} class="songbook-qr-title">{songbook.name}</h2>
                        </div>
                        <button
                            type="button"
                            class="songbook-qr-close"
                            onClick={() => dialogRef.current?.close()}
                            aria-label="Close QR code dialog"
                        >
                            ✕
                        </button>
                    </div>

                    <div class="songbook-qr-body">
                        <div class="songbook-qr-preview-card">
                            {pngDataUrl ? (
                                <img
                                    src={pngDataUrl}
                                    alt={`QR code for ${songbook.name}`}
                                    class="songbook-qr-preview-img"
                                    width="224"
                                    height="224"
                                />
                            ) : error ? (
                                <div class="songbook-qr-status">{error}</div>
                            ) : (
                                <div class="songbook-qr-status">{loading ? 'Generating QR...' : 'No public link'}</div>
                            )}
                        </div>

                        <div class="songbook-qr-url-box">
                            <span class="songbook-qr-url-text" title={url}>{url}</span>
                            <button
                                type="button"
                                class="btn btn-secondary btn-sm"
                                onClick={handleCopyLink}
                                disabled={!url}
                            >
                                Copy
                            </button>
                        </div>

                        <p class="songbook-qr-hint">
                            High-contrast print-ready QR code. Use PNG (1200×1200) for flyers &amp; table tents, or SVG for large posters and print shops.
                        </p>

                        <div class="songbook-qr-actions">
                            <button
                                type="button"
                                class="btn btn-primary"
                                onClick={handleDownloadPng}
                                disabled={!url || !!downloading}
                                autofocus
                            >
                                {downloading === 'png' ? 'Preparing...' : 'Download PNG'}
                            </button>
                            <button
                                type="button"
                                class="btn btn-secondary"
                                onClick={handleDownloadSvg}
                                disabled={!url || !!downloading}
                            >
                                {downloading === 'svg' ? 'Preparing...' : 'Download SVG'}
                            </button>
                        </div>

                        <div
                            class={`songbook-qr-feedback ${feedback.type === 'error' ? 'songbook-qr-feedback-error' : ''}`}
                            role={feedback.type === 'error' ? 'alert' : 'status'}
                            aria-live="polite"
                        >
                            {feedback.text}
                        </div>
                    </div>
                </>
            )}
        </dialog>
    )
}
