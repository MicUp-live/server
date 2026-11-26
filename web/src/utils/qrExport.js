import QRCode from 'qrcode'

const DEFAULT_QR_OPTIONS = {
    margin: 2,
    errorCorrectionLevel: 'H',
    color: {
        dark: '#090d16',
        light: '#ffffff',
    },
}

/**
 * Builds the canonical public songbook URL for sharing and QR encoding.
 */
export function buildSongbookPublicUrl(songbook, origin = '') {
    const slugOrId = songbook?.slug || songbook?.public_id
    if (!slugOrId) return ''
    const cleanOrigin = origin.replace(/\/+$/, '')
    return `${cleanOrigin}/book/${encodeURIComponent(slugOrId)}`
}

/**
 * Builds a filesystem-safe download filename for a songbook QR export.
 */
export function buildQrFilename(songbook, ext = 'png') {
    const raw = (songbook?.slug || songbook?.name || songbook?.public_id || 'songbook').trim().toLowerCase()
    const safe = raw
        .replace(/[^a-z0-9]+/g, '-')
        .replace(/^-+|-+$/g, '') || 'songbook'
    const cleanExt = String(ext || 'png').replace(/^\.+/, '').toLowerCase()
    return `micup-${safe}-qr.${cleanExt}`
}

/**
 * Generates a high-resolution PNG data URL suitable for posters and table tents.
 */
export async function generateSongbookQrPngDataUrl(url, width = 1200) {
    if (!url) throw new Error('A songbook URL is required to generate a QR code')
    return QRCode.toDataURL(url, {
        ...DEFAULT_QR_OPTIONS,
        width,
    })
}

/**
 * Generates a crisp vector SVG string suitable for print shops and large signage.
 */
export async function generateSongbookQrSvg(url) {
    if (!url) throw new Error('A songbook URL is required to generate a QR code')
    return QRCode.toString(url, {
        ...DEFAULT_QR_OPTIONS,
        type: 'svg',
    })
}

/**
 * Triggers a browser download from a data URL.
 */
export function downloadDataUrl(dataUrl, filename) {
    const link = document.createElement('a')
    link.href = dataUrl
    link.download = filename
    link.rel = 'noopener'
    document.body.append(link)
    link.click()
    link.remove()
}

/**
 * Triggers a browser download from an SVG string.
 */
export function downloadSvg(svgMarkup, filename) {
    const blob = new Blob([svgMarkup], { type: 'image/svg+xml;charset=utf-8' })
    const objectUrl = URL.createObjectURL(blob)
    try {
        downloadDataUrl(objectUrl, filename)
    } finally {
        setTimeout(() => URL.revokeObjectURL(objectUrl), 1000)
    }
}
