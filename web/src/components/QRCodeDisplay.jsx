import { useState, useEffect } from 'preact/hooks'
import QRCode from 'qrcode'
import { BrandIcon } from './BrandIcon'
import './QRCodeDisplay.css'

export function QRCodeDisplay({
    url,
    size = 240,
    showLogo = true,
    showCode = true,
    joinCode = '',
    title = 'Scan To Sing',
    subtitle = 'Point your phone camera to browse & request songs',
    allowEnlarge = true,
    enlarged = false,
    onToggleEnlarge = null,
    class: className = '',
}) {
    const [qrDataUrl, setQrDataUrl] = useState('')
    const [internalEnlarged, setInternalEnlarged] = useState(false)
    const [error, setError] = useState(null)

    const isEnlarged = onToggleEnlarge ? enlarged : internalEnlarged
    const setEnlarged = (val) => {
        if (onToggleEnlarge) onToggleEnlarge(val)
        else setInternalEnlarged(val)
    }

    useEffect(() => {
        if (!url) return
        let active = true

        QRCode.toDataURL(url, {
            width: 480, // High-res rendering for crispness on 4K/1080p TVs
            margin: 1,
            errorCorrectionLevel: 'H', // 30% error correction so center logo doesn't disrupt scanning
            color: {
                dark: '#090d16',
                light: '#ffffff',
            },
        })
            .then((dataUrl) => {
                if (active) setQrDataUrl(dataUrl)
            })
            .catch((err) => {
                console.error('Failed to generate QR code:', err)
                if (active) setError(err)
            })

        return () => {
            active = false
        }
    }, [url])

    // Close on Escape key
    useEffect(() => {
        if (!isEnlarged) return
        const handleKeyDown = (e) => {
            if (e.key === 'Escape') {
                setEnlarged(false)
            }
        }
        window.addEventListener('keydown', handleKeyDown)
        return () => window.removeEventListener('keydown', handleKeyDown)
    }, [isEnlarged])

    if (!url) return null

    const centerLogoSize = Math.max(24, Math.round(size * 0.22))
    const displayUrl = url.replace(/^https?:\/\//, '')

    const cardContent = (
        <>
            {/* QR Code Canvas/Image Wrapper */}
            <div
                class="qr-frame"
                style={{ width: `${size}px`, height: `${size}px` }}
            >
                {qrDataUrl ? (
                    <img
                        src={qrDataUrl}
                        alt={`QR code to join at ${url}`}
                        class="qr-image"
                        width={size}
                        height={size}
                    />
                ) : error ? (
                    <div class="qr-fallback">Unable to load QR</div>
                ) : (
                    <div class="qr-loading">Generating QR...</div>
                )}

                {/* Center Brand Icon Badge */}
                {showLogo && qrDataUrl && (
                    <div
                        class="qr-center-badge"
                        style={{
                            width: `${centerLogoSize}px`,
                            height: `${centerLogoSize}px`,
                        }}
                    >
                        <BrandIcon size={Math.round(centerLogoSize * 0.75)} glowing={false} />
                    </div>
                )}
            </div>

            {/* Subtext info */}
            {title && <div class="qr-title">{title}</div>}
            {subtitle && <div class="qr-subtitle">{subtitle}</div>}

            {/* Prominent Join Code Badge */}
            {showCode && joinCode && (
                <div class="qr-code-pill">
                    <span class="qr-code-label">CODE</span>
                    <span class="qr-code-value">{joinCode}</span>
                </div>
            )}

            <div class="qr-url-text">{displayUrl}</div>

            {allowEnlarge && (
                <div class="qr-hint">⛶ Click to Enlarge</div>
            )}
        </>
    )

    return (
        <div class={`qr-container ${className}`}>
            {allowEnlarge ? (
                <button
                    type="button"
                    class="qr-card qr-card-interactive"
                    onClick={() => setEnlarged(true)}
                    title="Click to enlarge QR code"
                    style={{ font: 'inherit', color: 'inherit' }}
                >
                    {cardContent}
                </button>
            ) : (
                <div class="qr-card">
                    {cardContent}
                </div>
            )}

            {/* Fullscreen Enlarge Modal for Bar / TV View */}
            {isEnlarged && (
                <dialog
                    open
                    class="qr-modal-backdrop"
                    aria-modal="true"
                    aria-labelledby="qr-modal-title"
                    onCancel={(e) => {
                        e.preventDefault()
                        setEnlarged(false)
                    }}
                    style={{
                        border: 'none',
                        margin: 0,
                        maxWidth: 'none',
                        maxHeight: 'none',
                        width: '100%',
                        height: '100%',
                    }}
                >
                    <div class="qr-modal-content">
                        <button
                            type="button"
                            class="qr-modal-close"
                            onClick={() => setEnlarged(false)}
                            aria-label="Close enlarged QR code"
                        >
                            ✕
                        </button>

                        <div class="qr-modal-header">
                            <BrandIcon size={36} glowing />
                            <h2 id="qr-modal-title" class="qr-modal-title">Join The Show</h2>
                            <p class="qr-modal-desc">
                                Point your camera at the screen to open the digital songbook
                            </p>
                        </div>

                        <div
                            class="qr-frame qr-frame-enlarged"
                            style={{ width: '320px', height: '320px' }}
                        >
                            <img
                                src={qrDataUrl}
                                alt={`QR code to join at ${url}`}
                                class="qr-image"
                                width={320}
                                height={320}
                            />
                            {showLogo && (
                                <div
                                    class="qr-center-badge"
                                    style={{ width: '70px', height: '70px' }}
                                >
                                    <BrandIcon size={52} />
                                </div>
                            )}
                        </div>

                        {joinCode && (
                            <div class="qr-modal-code-banner">
                                <span class="qr-modal-code-label">Show Code:</span>
                                <span class="qr-modal-code-value">{joinCode}</span>
                            </div>
                        )}

                        <div class="qr-modal-url">{url}</div>
                    </div>
                </dialog>
            )}
        </div>
    )
}
