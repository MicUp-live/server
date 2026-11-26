import test from 'node:test'
import assert from 'node:assert/strict'
import { isVirtualDJDatabaseXML, parseVirtualDJDatabase, decodeXmlEntities } from './virtualDjParser.js'

test('isVirtualDJDatabaseXML detects VirtualDJ XML headers', () => {
    assert.equal(isVirtualDJDatabaseXML('<?xml version="1.0"?><VirtualDJ_Database Version="8.5"></VirtualDJ_Database>', 'database.xml'), true)
    assert.equal(isVirtualDJDatabaseXML('Artist,Title\nQueen,Bohemian Rhapsody', 'songs.csv'), false)
})

test('parseVirtualDJDatabase filters hybrid DJ+KJ library to karaoke tracks and decodes XML entities', () => {
    const xml = `<?xml version="1.0" encoding="UTF-8"?>
<VirtualDJ_Database Version="8.5">
    <Song FilePath="C:\\Music\\Dance\\Daft Punk - One More Time.mp3" Flag="1">
        <Tags Author="Daft Punk" Title="One More Time" Genre="House" />
    </Song>
    <Song FilePath="C:\\Karaoke\\Queen - Bohemian Rhapsody.zip" Flag="32">
        <Tags Author="Queen &amp; Co" Title="Bohemian &quot;Rhapsody&quot;" Genre="Rock" Label="Sound Choice" />
    </Song>
    <Song FilePath="C:\\Karaoke\\Journey - Don&#39;t Stop Believin.cdg" Flag="0">
        <Tags Author="Journey" Title="Don&#39;t Stop Believin" />
    </Song>
</VirtualDJ_Database>`

    const res = parseVirtualDJDatabase(xml)
    assert.equal(res.totalScanned, 3)
    assert.equal(res.songs.length, 2)
    assert.equal(res.songs[0].artist, 'Queen & Co')
    assert.equal(res.songs[0].title, 'Bohemian "Rhapsody"')
    assert.equal(res.songs[0].source, 'Sound Choice')
    assert.equal(res.songs[1].artist, 'Journey')
    assert.equal(res.songs[1].title, "Don't Stop Believin")
    assert.equal(res.songs[1].source, '')
})

test('parseVirtualDJDatabase falls back to FilePath stem when Tags are missing', () => {
    const xml = `<VirtualDJ_Database Version="8.5">
    <Song FilePath="/Users/kj/Karaoke/Fleetwood Mac - Dreams.cdg" Flag="32" />
</VirtualDJ_Database>`

    const res = parseVirtualDJDatabase(xml)
    assert.equal(res.songs.length, 1)
    assert.equal(res.songs[0].artist, 'Fleetwood Mac')
    assert.equal(res.songs[0].title, 'Dreams')
})

test('decodeXmlEntities handles standard, numeric, and out-of-range entities safely', () => {
    assert.equal(decodeXmlEntities('A &amp; B &#39;C&#39; &#x26; D'), "A & B 'C' & D")
    assert.equal(decodeXmlEntities('Bad &#9999999; and &#xFFFFFF;'), 'Bad &#9999999; and &#xFFFFFF;')
})

test('parseVirtualDJDatabase handles single quotes, comments, > in attributes, and malformed XML', () => {
    const xml = `<?xml version="1.0"?>
<!-- <Song FilePath='C:\\Karaoke\\Ignored - Song.cdg' Flag='32' /> -->
<VirtualDJ_Database Version="8.5">
    <Song FilePath='C:\\Karaoke\\Artist - A > B.cdg' Flag='32'>
        <Tags Author='Artist' Title='A > B' Genre='Karaoke' />
    </Song>
    <Song FilePath="C:\\Karaoke\\Unclosed - Track.cdg" Flag="32">
</VirtualDJ_Database>`

    const res = parseVirtualDJDatabase(xml)
    assert.equal(res.songs.length, 2)
    assert.equal(res.songs[0].title, 'A > B')
    assert.equal(res.songs[1].artist, 'Unclosed')
    assert.equal(res.songs[1].title, 'Track')

    assert.equal(parseVirtualDJDatabase('').songs.length, 0)
    assert.equal(isVirtualDJDatabaseXML('<rekordbox><COLLECTION /></rekordbox>', 'rekordbox.xml'), false)
})
