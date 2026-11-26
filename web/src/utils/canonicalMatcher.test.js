import test from 'node:test'
import assert from 'node:assert/strict'
import { CanonicalIndex, extractLeadingResidualDisc } from './canonicalMatcher.js'

const SAMPLE_DICT = {
    'backstreet boys:::10 000 promises': { id: 'can_bsb', artist: 'Backstreet Boys', title: '10,000 Promises', genre: 'Pop', year: 1997 },
    'the smashing pumpkins:::33': { id: 'can_sp', artist: 'The Smashing Pumpkins', title: '33', genre: 'Rock', year: 1995 },
    'truth hurts and rakim:::addictive': { id: 'can_th', artist: 'Truth Hurts & Rakim', title: 'Addictive', genre: 'R&B', year: 2002 },
    'brooke hogan:::about us': { id: 'can_bh', artist: 'Brooke Hogan', title: 'About Us', genre: 'Pop', year: 2006 },
    'mumford and sons:::believe': { id: 'can_ms', artist: 'Mumford & Sons', title: 'Believe', genre: 'Indie', year: 2015 },
    'ronnie dunn:::bleed red': { id: 'can_rd', artist: 'Ronnie Dunn', title: 'Bleed Red', genre: 'Country', year: 2011 },
    'the 411:::dumb': { id: 'can_411', artist: 'The 411', title: 'Dumb', genre: 'R&B', year: 2004 },
    'chris janson:::til a woman comes along': { id: 'can_cj', artist: 'Chris Janson', title: "'Til A Woman Comes Along", genre: 'Country', year: 2010 },
    'dwight yoakam:::ain t that lonely yet': { id: 'can_dy', artist: 'Dwight Yoakam', title: "Ain't That Lonely Yet", genre: 'Country', year: 1993 },
    'u2:::one': { id: 'can_u2', artist: 'U2', title: 'One', genre: 'Rock', year: 1991 },
    'queen:::bohemian rhapsody': { id: 'can_q', artist: 'Queen', title: 'Bohemian Rhapsody', genre: 'Rock', year: 1975 },
    'leonard cohen:::hallelujah': { id: 'can_lc', artist: 'Leonard Cohen', title: 'Hallelujah', genre: 'Folk', year: 1984 },
    'guns and roses:::sweet child o mine': { id: 'can_gnr', artist: "Guns N' Roses", title: "Sweet Child O' Mine", genre: 'Rock', year: 1987 },
    'ed sheeran:::perfect': { id: 'can_ed', artist: 'Ed Sheeran', title: 'Perfect', genre: 'Pop', year: 2017 },
    'hair:::hair': { id: 'can_hair', artist: 'Hair', title: 'Hair', genre: 'Musical', year: 1968 },
    'the the:::this is the day': { id: 'can_tt', artist: 'The The', title: 'This Is The Day', genre: 'Post-Punk', year: 1983 },
    '3 doors down:::be like that': { id: 'can_3dd1', artist: '3 Doors Down', title: 'Be Like That', genre: 'Rock', year: 2000 },
    '3 doors down:::be like that radio version': { id: 'can_3dd2', artist: '3 Doors Down', title: 'Be Like That (Radio Version)', genre: 'Rock', year: 2000 },
}

test('CanonicalIndex Layer 1 resolves leading "The" and explicit feat. collaborations without collapsing & bands', () => {
    const idx = new CanonicalIndex(SAMPLE_DICT)

    assert.equal(idx.lookupSecondaryExact('Backstreet Boys, The', '10,000 Promises')?.track.id, 'can_bsb')
    assert.equal(idx.lookupSecondaryExact('Smashing Pumpkins', '33')?.track.id, 'can_sp')
    assert.equal(idx.lookupSecondaryExact('BROOKE HOGAN FT.PAUL WALL', 'ABOUT US')?.track.id, 'can_bh')

    // Must NOT collapse "&" band names or distinct "&" duets onto solo primary words
    assert.equal(idx.lookupSecondaryExact('Guns', "Sweet Child O' Mine"), null)
    assert.equal(idx.lookupSecondaryExact('Ed Sheeran & Andrea Bocelli', 'Perfect'), null)
})

test('CanonicalIndex Layer 2 matches raw clutter/unknown-disc strings and rescues disc codes', () => {
    const idx = new CanonicalIndex(SAMPLE_DICT)

    const mumford = idx.matchRawTokens('Mumford & - Sons - Believe(MP3+CDG - Karaoke)')
    assert.equal(mumford?.track.id, 'can_ms')
    assert.equal(mumford?.rescuedDisc, '')

    const phn = idx.matchRawTokens('PHN1104 - 01 - Ronnie Dunn - Bleed Red')
    assert.equal(phn?.track.id, 'can_rd')
    assert.equal(phn?.rescuedDisc, 'PHN1104-01')

    const ezh = idx.matchRawTokens('EZH039-05 - 411 - Dumb')
    assert.equal(ezh?.track.id, 'can_411')
    assert.equal(ezh?.rescuedDisc, 'EZH039-05')

    // Rejects generic media/track prefixes as disc codes (including hyphenated variants)
    assert.equal(extractLeadingResidualDisc('MP3 - Queen - Bohemian Rhapsody'), '')
    assert.equal(extractLeadingResidualDisc('CD1 - 05 - Queen - Bohemian Rhapsody'), '')
    assert.equal(extractLeadingResidualDisc('CD01-05 - Queen - Bohemian Rhapsody'), '')
    assert.equal(extractLeadingResidualDisc('DISC1 - Queen - Bohemian Rhapsody'), '')
    assert.equal(extractLeadingResidualDisc('TRACK01 - Queen - Bohemian Rhapsody'), '')
    assert.equal(extractLeadingResidualDisc('TRACK01-02 - Queen - Bohemian Rhapsody'), '')

    // Empty index safely returns null without throwing
    assert.equal(new CanonicalIndex().lookupSecondaryExact('Queen', 'Bohemian Rhapsody'), null)
})

test('CanonicalIndex Layer 2 rejects short-track, self-titled, stopword-only, variant, and cover false positives', () => {
    const idx = new CanonicalIndex(SAMPLE_DICT)

    assert.equal(idx.matchRawTokens('Tribute Band - U2 Style - One Day'), null)
    assert.equal(idx.matchRawTokens('U2 - One Mix'), null)
    assert.equal(idx.matchRawTokens('Queen - Bohemian Rhapsody (Live)'), null)
    assert.equal(idx.matchRawTokens('Queen - Bohemian Rhapsody (Pt2)'), null)
    assert.equal(idx.matchRawTokens('Queen - Bohemian Rhapsody (Cover)'), null)
    assert.equal(idx.matchRawTokens('Local Singer - Hallelujah'), null)
    assert.equal(idx.matchRawTokens('The Hair'), null)
    assert.equal(idx.matchRawTokens('Scott - This Is The Day'), null)
})

test('CanonicalIndex Layer 3 corrects 1-2 edit artist typos including duplicate radio-edit canonical entries', () => {
    const idx = new CanonicalIndex(SAMPLE_DICT)

    assert.equal(idx.matchArtistTypo('Jansen, Chris', "'Til A Woman Comes Along")?.track.id, 'can_cj')
    assert.equal(idx.matchArtistTypo('Yoakum, Dwight', "Ain't That Lonely Yet")?.track.id, 'can_dy')
    assert.equal(idx.matchArtistTypo('3 Door Down', 'Be Like That')?.track.id, 'can_3dd1')
    assert.equal(idx.matchArtistTypo('Smashing Pumpkin', '33')?.track.id, 'can_sp')

    // Title typo must break ties on duplicate canonical titles (leading "The" artist or radio-edit variants) via trackRichness
    const dupes = {
        ...SAMPLE_DICT,
        'beatles:::hey jude': { id: 'can_b1', artist: 'Beatles', title: 'Hey Jude' },
        'the beatles:::hey jude': { id: 'can_b2', artist: 'The Beatles', title: 'Hey Jude', genre: 'Rock', year: 1968 },
    }
    const dupeIdx = new CanonicalIndex(dupes)
    assert.equal(dupeIdx.matchTitleTypo('The Beatles', 'Hey Jud')?.track.id, 'can_b2')
    assert.equal(dupeIdx.matchTitleTypo('3 Doors Down', 'Be Lik That')?.track.id, 'can_3dd1')
})
