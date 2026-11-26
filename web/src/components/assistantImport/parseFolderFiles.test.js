import assert from 'node:assert/strict'
import test from 'node:test'
import { parseFolderFiles } from './parseFolderFiles.js'

function mockParser(t, parse) {
    return t.mock.method(globalThis, 'fetch', async (url, options) => {
        assert.equal(url, '/api/v1/songbooks/fixture/parse-files')
        assert.equal(options.method, 'POST')
        const { files, pattern } = JSON.parse(options.body)
        assert.equal(pattern, 'artist-title')
        assert.ok(Buffer.byteLength(options.body) <= 512 * 1024)
        assert.ok(files.length > 0 && files.length <= 1000)
        return Response.json({ results: await parse(files) })
    })
}

test('folder parsing batches a 64,693-file library and preserves result order', async t => {
    const files = Array.from({ length: 64_693 }, (_, index) => ({
        name: `Artist - Track ${index}.zip`,
        path: `Karaoke music/Collection ${Math.floor(index / 200)}/Artist - Track ${index}.zip`,
        size: 0,
    }))
    const fetch = mockParser(t, batch => batch.map(file => file.path))

    const results = await parseFolderFiles('fixture', files, 'artist-title', () => true)

    assert.equal(fetch.mock.callCount(), 65)
    assert.deepEqual(results, files.map(file => file.path))
})

test('folder parse batch size is measured in UTF-8 bytes', async t => {
    const files = Array.from({ length: 160 }, (_, index) => ({
        name: `Track ${index}.cdg`,
        path: `Karaoke music/${Array(20).fill('歌'.repeat(60)).join('/')}/Track ${index}.cdg`,
        size: 0,
    }))
    const fetch = mockParser(t, batch => batch.map(file => file.path))

    assert.deepEqual(await parseFolderFiles('fixture', files, 'artist-title', () => true), files.map(file => file.path))
    assert.equal(fetch.mock.callCount(), 2)
})

test('a file at the exact request byte limit takes one batch', async t => {
    const file = { name: 'Track.zip', path: '/', size: 0 }
    const envelopeBytes = Buffer.byteLength(JSON.stringify({ files: [file], pattern: 'artist-title' }))
    file.path += 'a'.repeat(512 * 1024 - envelopeBytes)
    const fetch = mockParser(t, batch => batch)

    assert.deepEqual(await parseFolderFiles('fixture', [file], 'artist-title', () => true), [file])
    assert.equal(fetch.mock.callCount(), 1)
})

test('folder parsing discards earlier results if a batch is incomplete', async t => {
    let calls = 0
    const fetch = mockParser(t, files => ++calls === 2 ? [] : files)
    const files = Array.from({ length: 2500 }, (_, index) => ({ name: `Track ${index}.mp3`, size: 0 }))

    await assert.rejects(parseFolderFiles('fixture', files, 'artist-title', () => true), /Incomplete folder parse response/)
    assert.equal(fetch.mock.callCount(), 2)
})

test('folder parsing stops sending batches when superseded', async t => {
    let current = true
    const fetch = mockParser(t, files => {
        current = false
        return files
    })
    const files = Array.from({ length: 2500 }, (_, index) => ({ name: `Track ${index}.mp3`, size: 0 }))

    assert.equal(await parseFolderFiles('fixture', files, 'artist-title', () => current), null)
    assert.equal(fetch.mock.callCount(), 1)
})
