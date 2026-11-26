import test from 'node:test'
import assert from 'node:assert/strict'
import { groupSongVersions, formatVersionLabel, describeVersion, songGroupKey } from './groupSongVersions.js'

test('groupSongVersions preserves single-version songs without adding versions array', () => {
    const songs = [
        { id: 's1', artist: 'Queen', title: 'Bohemian Rhapsody' },
        { id: 's2', artist: 'Journey', title: "Don't Stop Believin'" },
    ]
    const grouped = groupSongVersions(songs)
    assert.equal(grouped.length, 2)
    assert.equal(grouped[0].id, 's1')
    assert.equal(grouped[0].versions, undefined)
})

test('groupSongVersions collapses duplicates by canonical_id and preserves first-seen order', () => {
    const songs = [
        { song_id: 's1', canonical_id: 'can_1', artist: 'Queen', title: 'Bohemian Rhapsody', is_custom: true },
        { song_id: 's2', canonical_id: 'can_2', artist: 'Adele', title: 'Rolling in the Deep' },
        { song_id: 's3', canonical_id: 'can_1', artist: 'Queen', title: 'Bohemian Rhapsody', source: 'Sound Choice', disc: 'SC8101-02', is_custom: false },
        { song_id: 's4', canonical_id: 'can_1', artist: 'Queen', title: 'Bohemian Rhapsody', disc: 'SF042-01', is_custom: false },
    ]
    const grouped = groupSongVersions(songs)
    assert.equal(grouped.length, 2)
    assert.equal(grouped[0].title, 'Bohemian Rhapsody')
    assert.equal(grouped[0].song_id, 's3') // Prefers annotated non-custom track as primary
    assert.equal(grouped[0].versions.length, 3)
    assert.equal(grouped[1].song_id, 's2')
})

test('groupSongVersions falls back to normalized artist+title when canonical_id is absent', () => {
    const songs = [
        { id: 'a1', artist: 'Fleetwood Mac', title: 'Dreams' },
        { id: 'a2', artist: '  fleetwood   mac ', title: 'dreams ' },
    ]
    assert.equal(songGroupKey(songs[0]), songGroupKey(songs[1]))
    const grouped = groupSongVersions(songs)
    assert.equal(grouped.length, 1)
    assert.equal(grouped[0].versions.length, 2)
})

test('formatVersionLabel resolves publisher from source or catalog disc field', () => {
    assert.equal(formatVersionLabel({ provider: 'Sound Choice', disc_code: 'SC8101-02' }, 0), 'Sound Choice • SC8101-02')
    assert.equal(formatVersionLabel({ disc: 'SC8101-02' }, 0), 'Sound Choice • SC8101-02')
    assert.equal(formatVersionLabel({ disc: 'SF042-01' }, 1), 'Sunfly • SF042-01')
    assert.equal(formatVersionLabel({ disc_code: 'CB1234' }, 2), 'Chartbuster • CB1234')
    assert.equal(formatVersionLabel({ source: 'KaraFun' }, 0), 'KaraFun')
    assert.equal(formatVersionLabel({ source: 'KaraokePro', disc: 'KP-09' }, 0), 'KaraokePro • KP-09')
    assert.equal(formatVersionLabel({ disc: 'CUSTOM-01' }, 1), 'CUSTOM-01')
    assert.equal(formatVersionLabel({}, 2), 'Version 3')
})

test('describeVersion highlights title variants, artist/genre differences, and video sources across siblings', () => {
    const siblings = [
        {
            song_id: 'v1',
            canonical_id: 'can_dh',
            artist: '10cc',
            title: 'Dreadlock Holiday',
            disc: 'SF045-01',
            genre: 'Rock',
        },
        {
            song_id: 'v2',
            canonical_id: 'can_dh',
            artist: '10cc',
            title: 'Dreadlock Holiday (Without Backing Vocals)',
            source: 'KaraFun',
            disc: 'KFN-1234',
            genre: 'Rock',
        },
        {
            song_id: 'v3',
            canonical_id: 'can_dh',
            artist: '10cc',
            title: 'Dreadlock Holiday (Acoustic)',
            genre: 'Acoustic',
            video_url: 'https://www.youtube.com/watch?v=abc123',
        },
    ]

    const d1 = describeVersion(siblings[0], 0, siblings)
    assert.equal(d1.provider, 'Sunfly')
    assert.equal(d1.disc, 'SF045-01')
    assert.equal(d1.label, 'Sunfly')
    assert.deepEqual(d1.details, ['Rock'])

    const d2 = describeVersion(siblings[1], 1, siblings)
    assert.equal(d2.provider, 'KaraFun')
    assert.equal(d2.disc, 'KFN-1234')
    assert.equal(d2.label, 'KaraFun')
    assert.deepEqual(d2.details, ['Without Backing Vocals', 'Rock'])
    assert.equal(formatVersionLabel(siblings[1], 1, siblings), 'KaraFun • KFN-1234 • Without Backing Vocals • Rock')

    const d3 = describeVersion(siblings[2], 2, siblings)
    assert.equal(d3.provider, '')
    assert.equal(d3.label, 'Acoustic')
    assert.deepEqual(d3.details, ['YouTube'])
    assert.equal(formatVersionLabel(siblings[2], 2, siblings), 'Acoustic • YouTube')
})

test('describeVersion handles word boundaries, extra whitespace, and mixed bracket + text suffixes', () => {
    const wordBoundary = [
        { song_id: 'w1', canonical_id: 'c1', artist: 'A', title: 'Love Song' },
        { song_id: 'w2', canonical_id: 'c1', artist: 'A', title: 'Love Songs' },
    ]
    assert.equal(describeVersion(wordBoundary[1], 1, wordBoundary).label, 'Love Songs')

    const spaced = [
        { song_id: 's1', canonical_id: 'c2', artist: 'Beatles', title: 'Hey Jude' },
        { song_id: 's2', canonical_id: 'c2', artist: 'Beatles', title: 'Hey  Jude - Remix' },
    ]
    assert.equal(describeVersion(spaced[1], 1, spaced).label, 'Remix')

    const mixed = [
        { song_id: 'm1', canonical_id: 'c3', artist: 'Queen', title: 'Song (Acoustic)' },
        { song_id: 'm2', canonical_id: 'c3', artist: 'Queen', title: 'Song (Acoustic) - Duet' },
    ]
    assert.equal(describeVersion(mixed[0], 0, mixed).label, 'Acoustic')
    assert.equal(describeVersion(mixed[1], 1, mixed).label, 'Acoustic · Duet')
})

test('describeVersion reports hasMetadata=false when versions have no distinguishing fields', () => {
    const siblings = [
        { song_id: 'u1', artist: 'Queen', title: 'Radio Ga Ga' },
        { song_id: 'u2', artist: 'Queen', title: 'Radio Ga Ga' },
    ]
    const d1 = describeVersion(siblings[0], 0, siblings)
    assert.equal(d1.hasMetadata, false)
    assert.equal(d1.label, 'Version 1')
    assert.deepEqual(d1.details, [])
})
