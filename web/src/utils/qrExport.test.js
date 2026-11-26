import test from 'node:test'
import assert from 'node:assert/strict'
import {
    buildSongbookPublicUrl,
    buildQrFilename,
    generateSongbookQrPngDataUrl,
    generateSongbookQrSvg,
} from './qrExport.js'

test('buildSongbookPublicUrl prefers slug over public_id and strips trailing slash', () => {
    assert.equal(
        buildSongbookPublicUrl({ slug: 'sullys', public_id: 'pub-123' }, 'https://micup.live/'),
        'https://micup.live/book/sullys'
    )
    assert.equal(
        buildSongbookPublicUrl({ public_id: 'pub-123' }, 'https://micup.live'),
        'https://micup.live/book/pub-123'
    )
    assert.equal(
        buildSongbookPublicUrl({ name: 'Private Only' }, 'https://micup.live'),
        ''
    )
})

test('buildQrFilename produces clean lowercase filenames', () => {
    assert.equal(
        buildQrFilename({ slug: 'sullys', name: "Sully's Karaoke!" }, 'png'),
        'micup-sullys-qr.png'
    )
    assert.equal(
        buildQrFilename({ name: "Sully's Tuesday Night!" }, 'svg'),
        'micup-sully-s-tuesday-night-qr.svg'
    )
    assert.equal(
        buildQrFilename({ name: '   !!!   ' }, '.PNG'),
        'micup-songbook-qr.png'
    )
})

test('generateSongbookQrPngDataUrl produces a PNG data URL', async () => {
    const dataUrl = await generateSongbookQrPngDataUrl('https://micup.live/book/sullys', 512)
    assert.ok(dataUrl.startsWith('data:image/png;base64,'), 'expected PNG data URL')
    assert.ok(dataUrl.length > 200, 'expected non-empty PNG payload')
})

test('generateSongbookQrSvg produces valid SVG markup', async () => {
    const svg = await generateSongbookQrSvg('https://micup.live/book/sullys')
    assert.ok(svg.includes('<svg'), 'expected <svg> element')
    assert.ok(svg.includes('</svg>'), 'expected closing </svg> tag')
})

test('QR generators reject empty URLs', async () => {
    await assert.rejects(() => generateSongbookQrPngDataUrl(''), /URL is required/)
    await assert.rejects(() => generateSongbookQrSvg(''), /URL is required/)
})
