import test from 'node:test'
import assert from 'node:assert/strict'
import zlib from 'node:zlib'
import {
    fetchAllSongsForSongbook,
    generateSongbookCSV,
    downloadCSV
} from './songbookExport.js'

test('generateSongbookCSV formats songs into CSV with escaping, source, video_url, and BOM', () => {
    const songs = [
        { artist: 'Queen', title: 'Bohemian Rhapsody', disc: 'SC8100-01', source: 'Sound Choice', genre: 'Rock', video_url: 'https://youtu.be/abc' },
        { artist: 'AC/DC', title: 'You Shook Me "All Night" Long', disc: 'CDG-1', genre: 'Hard Rock' },
        { artist: 'Prince', label: '1999', disc: '', genre: 'Pop' }, // label fallback when title is missing
    ]

    const csv = generateSongbookCSV(songs)
    assert.ok(csv.startsWith('\uFEFFArtist,Title,Disc,Source,Genre,Video URL\r\n'))
    assert.ok(csv.includes('"Queen","Bohemian Rhapsody","SC8100-01","Sound Choice","Rock","https://youtu.be/abc"'))
    assert.ok(csv.includes('"AC/DC","You Shook Me ""All Night"" Long","CDG-1","","Hard Rock",""'))
    assert.ok(csv.includes('"Prince","1999","","","Pop",""'))
})

test('fetchAllSongsForSongbook fetches across multiple pages when paginating API', async () => {
    const songbook = { id: 'sb-paginated', name: 'Large Songbook' }

    // Mock API that returns 5000 songs on offset=0 and 1234 songs on offset=5000
    const mockApi = {
        get: async (path) => {
            if (path === `/songbooks/${songbook.id}/managed-manifest`) {
                return { static_ready: false }
            }
            if (path.startsWith(`/songbooks/${songbook.id}/songs?offset=0`)) {
                return Array.from({ length: 5000 }, (_, i) => ({
                    song_id: `song-${i}`,
                    artist: `Artist ${String(i).padStart(5, '0')}`,
                    title: `Title ${i}`,
                    disc: 'D1',
                    genre: 'Rock',
                }))
            }
            if (path.startsWith(`/songbooks/${songbook.id}/songs?offset=5000`)) {
                return Array.from({ length: 1234 }, (_, i) => ({
                    song_id: `song-${5000 + i}`,
                    artist: `Artist ${String(5000 + i).padStart(5, '0')}`,
                    title: `Title ${5000 + i}`,
                    disc: 'D2',
                    genre: 'Pop',
                }))
            }
            throw new Error('Unexpected path: ' + path)
        }
    }

    const all = await fetchAllSongsForSongbook(songbook, mockApi)
    assert.equal(all.length, 6234)
})

test('fetchAllSongsForSongbook loads static catalog asset and merges catalog overrides', async () => {
    const songbook = { id: 'sb-static', name: 'Static Songbook' }
    const staticSongs = [
        { song_id: 's-1', artist: 'Beatles', title: 'Hey Jude', disc: 'CD1', genre: 'Rock' },
        { song_id: 's-2', artist: 'Beatles', title: 'Yesterday', disc: 'CD1', genre: 'Rock' },
        { song_id: 's-3', artist: 'Queen', title: 'Radio Ga Ga', disc: 'CD2', genre: 'Rock' },
    ]
    const gzipped = zlib.gzipSync(JSON.stringify({ songs: staticSongs }))

    const originalFetch = global.fetch
    try {
        global.fetch = async (url) => {
            if (url === '/catalogs/cat-v1/manifest.json') {
                return {
                    ok: true,
                    json: async () => ({ search_path: 'search.json.gz', total: 3 })
                }
            }
            if (url === '/catalogs/cat-v1/search.json.gz') {
                return new Response(gzipped, {
                    status: 200,
                    headers: { 'Content-Type': 'application/gzip' }
                })
            }
            throw new Error('Unexpected fetch: ' + url)
        }

        const mockApi = {
            get: async (path) => {
                if (path === `/songbooks/${songbook.id}/managed-manifest`) {
                    return { static_ready: true, manifest_url: '/catalogs/cat-v1/manifest.json' }
                }
                if (path === `/songbooks/${songbook.id}/catalog-overrides`) {
                    return [
                        // Delete s-2
                        { song_id: 's-2', deleted: true },
                        // Add s-4
                        { song_id: 's-4', song: { song_id: 's-4', artist: 'ABBA', title: 'Dancing Queen', disc: 'D1', genre: 'Disco' } },
                        // Edit s-1
                        { song_id: 's-1', song: { song_id: 's-1', artist: 'The Beatles', title: 'Hey Jude (Remastered)', disc: 'CD1', genre: 'Classic Rock' } }
                    ]
                }
                throw new Error('Unexpected path: ' + path)
            }
        }

        const all = await fetchAllSongsForSongbook(songbook, mockApi)
        assert.equal(all.length, 3)

        // Verify s-2 was deleted
        assert.equal(all.some(s => s.song_id === 's-2'), false)

        // Verify s-4 was added (and sorted first because ABBA < Beatles < Queen)
        assert.equal(all[0].artist, 'ABBA')
        assert.equal(all[0].title, 'Dancing Queen')

        // Verify s-1 was updated
        const s1 = all.find(s => s.song_id === 's-1')
        assert.ok(s1)
        assert.equal(s1.artist, 'The Beatles')
        assert.equal(s1.title, 'Hey Jude (Remastered)')
        assert.equal(s1.genre, 'Classic Rock')
    } finally {
        global.fetch = originalFetch
    }
})

test('downloadCSV does not crash when document is undefined', () => {
    assert.doesNotThrow(() => {
        downloadCSV('test,csv', 'test.csv')
    })
})
