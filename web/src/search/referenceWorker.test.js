import test from 'node:test';
import assert from 'node:assert/strict';

import {
  removeDiacritics,
  uninvertArtist,
  stripNoiseTags,
  normalizeKey,
  normalizeArtist,
  resolveProvider,
  compositeKey,
  normalizeDiscCode,
  levenshteinDistance,
  auditSong,
  setTestData,
} from './referenceWorker.js';
import { parseDelimitedText, guessColumnMapping, rowsToSongs } from '../utils/csvParser.js';
import { parseVirtualDJDatabase } from '../utils/virtualDjParser.js';

test('removeDiacritics strips accents correctly', () => {
  assert.equal(removeDiacritics('Beyoncé'), 'Beyonce');
  assert.equal(removeDiacritics('Mötley Crüe'), 'Motley Crue');
  assert.equal(removeDiacritics('Björk'), 'Bjork');
});

test('uninvertArtist handles inverted names', () => {
  assert.equal(uninvertArtist('Beatles, The'), 'The Beatles');
  assert.equal(uninvertArtist('Presley, Elvis'), 'Elvis Presley');
  assert.equal(uninvertArtist('Queen'), 'Queen');
  assert.equal(uninvertArtist('Earth, Wind & Fire'), 'Earth, Wind & Fire');
});

test('stripNoiseTags removes karaoke noise', () => {
  assert.equal(stripNoiseTags('Bohemian Rhapsody (Karaoke Version)'), 'Bohemian Rhapsody');
  assert.equal(stripNoiseTags('Sweet Caroline [SC8101]'), 'Sweet Caroline');
  assert.equal(stripNoiseTags('Dreams (Remastered)'), 'Dreams');
  assert.equal(stripNoiseTags('Dreadlock Holiday (Without Backing Vocals)'), 'Dreadlock Holiday');
  assert.equal(stripNoiseTags('Dreadlock Holiday (Two Semitones Down)'), 'Dreadlock Holiday');
  assert.equal(stripNoiseTags('Livin\' on a Prayer (With Backing Vocals)'), 'Livin\' on a Prayer');
  assert.equal(stripNoiseTags('Hotel California (Acoustic)'), 'Hotel California (Acoustic)');
});

test('normalizeKey expands conjunctions and cleans punctuation', () => {
  assert.equal(normalizeKey('Simon & Garfunkel'), 'simon and garfunkel');
  assert.equal(normalizeKey('Florence + The Machine'), 'florence and the machine');
  assert.equal(normalizeKey('Rock & Roll'), 'rock and roll');
});

test('normalizeArtist handles MusicBrainz aliases and stylizations', () => {
  assert.equal(normalizeArtist('Beatles, The'), 'the beatles');
  assert.equal(normalizeArtist('Simon & Garfunkel'), 'simon and garfunkel');
  assert.equal(normalizeArtist('Simon and Garfunkel'), 'simon and garfunkel');
  assert.equal(normalizeArtist('P!nk'), 'pink');
  assert.equal(normalizeArtist('Pink'), 'pink');
  assert.equal(normalizeArtist('Ke$ha'), 'kesha');
  assert.equal(normalizeArtist('Kesha'), 'kesha');
  assert.equal(normalizeArtist('CCR'), 'creedence clearwater revival');
  assert.equal(normalizeArtist('Creedence Clearwater Revival'), 'creedence clearwater revival');
  assert.equal(normalizeArtist('ELO'), 'electric light orchestra');
  assert.equal(normalizeArtist('Electric Light Orchestra'), 'electric light orchestra');
  assert.equal(normalizeArtist("Guns N' Roses"), 'guns and roses');
  assert.equal(normalizeArtist('Guns and Roses'), 'guns and roses');
  assert.equal(normalizeArtist('Guns & Roses'), 'guns and roses');
  assert.equal(normalizeArtist('Snoop Doggy Dogg'), 'snoop dogg');
  assert.equal(normalizeArtist('Hall & Oates'), 'hall and oates');
  assert.equal(normalizeArtist('Daryl Hall & John Oates'), 'hall and oates');
});

test('resolveProvider identifies manufacturer from disc code or brand', () => {
  assert.equal(resolveProvider('SC8101-02'), 'Sound Choice');
  assert.equal(resolveProvider('sc8101-02'), 'Sound Choice');
  assert.equal(resolveProvider('SF045-12'), 'Sunfly');
  assert.equal(resolveProvider('CB5001-14'), 'Chartbuster');
  assert.equal(resolveProvider('SYB1001-05'), 'Sunfly Sybersound');
  assert.equal(resolveProvider('PHM0101-01'), 'Pop Hits Monthly');
  assert.equal(resolveProvider('KFN123'), 'KaraFun');
  assert.equal(resolveProvider('ZM012-01'), 'Zoom Karaoke');
  assert.equal(resolveProvider('', 'Zoom Karaoke'), 'Zoom Karaoke');
  assert.equal(resolveProvider('', 'Sound Choice'), 'Sound Choice');
  assert.equal(resolveProvider('101', 'Sunfly'), 'Sunfly');
  assert.equal(resolveProvider('CUSTOM-01'), null);
  assert.equal(resolveProvider(''), null);
});

test('compositeKey normalizes, un-inverts, and resolves aliases', () => {
  assert.equal(compositeKey('Beatles, The', 'Hey Jude (Karaoke Version)'), 'the beatles:::hey jude');
  assert.equal(compositeKey('The Beatles', 'Hey Jude'), 'the beatles:::hey jude');
  assert.equal(compositeKey('CCR', 'Bad Moon Rising'), 'creedence clearwater revival:::bad moon rising');
  assert.equal(compositeKey('Creedence Clearwater Revival', 'Bad Moon Rising'), 'creedence clearwater revival:::bad moon rising');
  assert.equal(compositeKey('P!nk', 'Raise Your Glass'), 'pink:::raise your glass');
  assert.equal(compositeKey('Pink', 'Raise Your Glass'), 'pink:::raise your glass');
});

test('normalizeDiscCode standardizes disc-track format', () => {
  assert.equal(normalizeDiscCode('sc8101-2'), 'SC8101-02');
  assert.equal(normalizeDiscCode('sf045-12'), 'SF045-12');
  assert.equal(normalizeDiscCode('syb1001_5'), 'SYB1001-05');
});

test('levenshteinDistance computes accurate edit distance', () => {
  assert.equal(levenshteinDistance('def leppard', 'def leopard'), 1);
  assert.equal(levenshteinDistance('queen', 'queen'), 0);
  assert.equal(levenshteinDistance('abba', 'abba1'), 1);
  assert.equal(levenshteinDistance('kitten', 'sitting'), 3);
});

test('auditSong enforces Strict Provider Guard on custom and fallback tracks', () => {
  // Generic track without disc code or brand: provider must be null
  const result1 = auditSong({ artist: 'Local Band', title: 'Song 1' });
  assert.equal(result1.status, 'custom');
  assert.equal(result1.is_custom, true);
  assert.equal(result1.canonical_id, null);
  assert.equal(result1.provider, null);
  assert.equal(result1.provider_matched, false);

  // Track with recognizable disc code: provider resolved
  const result2 = auditSong({ artist: 'Local Band', title: 'Song 2', disc: 'SC8101-02' });
  assert.equal(result2.provider, 'Sound Choice');
  assert.equal(result2.provider_matched, true);
});

test('auditSong enriches a disc-code match from the canonical dictionary', () => {
  setTestData(
    {
      tracks: {
        'tanya tucker:::some kind of trouble': {
          id: 'work-1',
          artist: 'Tanya Tucker',
          title: 'Some Kind of Trouble',
          genre: 'Country,Rock',
          year: 1992,
        },
      },
    },
    {
      discs: {
        'SC8101-02': {
          artist: 'Tanya Tucker',
          title: 'Some Kind of Trouble',
          source: 'Sound Choice',
        },
      },
    },
  );

  const result = auditSong({
    artist: 'Tanya Tucker',
    title: 'Some Kind of Trouble',
    disc: 'sc8101-2',
  });

  assert.equal(result.status, 'suggestion');
  assert.equal(result.canonical_id, 'work-1');
  assert.equal(result.suggested_genre, 'Country,Rock');
  assert.equal(result.suggested_year, 1992);
  assert.equal(result.genre, 'Country,Rock');
  assert.equal(result.year, 1992);

  setTestData(null, null);
});

test('auditSong preserves imported metadata while suggesting only a missing year', () => {
  setTestData(
    {
      tracks: {
        'garth brooks:::friends in low places': {
          id: 'work-2',
          artist: 'Garth Brooks',
          title: 'Friends in Low Places',
          genre: 'Country',
          year: 1990,
        },
      },
    },
    null,
  );

  const result = auditSong({
    artist: 'Garth Brooks',
    title: 'Friends in Low Places',
    genre: 'Country & Western',
  });

  assert.equal(result.status, 'suggestion');
  assert.equal(result.genre, 'Country & Western');
  assert.equal(result.suggested_genre, null);
  assert.equal(result.suggested_year, 1990);

  setTestData(null, null);
});

test('auditSong extracts manufacturer disc code from filename/location before matching', () => {
  setTestData(
    {
      tracks: {
        'queen:::bohemian rhapsody': {
          id: 'can_bohemian',
          artist: 'Queen',
          title: 'Bohemian Rhapsody',
          genre: 'Rock',
          year: 1975,
        },
      },
    },
    {
      discs: {
        'SC8101-02': {
          id: 'can_bohemian',
          artist: 'Queen',
          title: 'Bohemian Rhapsody',
          disc_code: 'SC8101-02',
          source: 'Sound Choice',
        },
      },
    }
  );

  const audited = auditSong({
    artist: 'SC8101-02',
    title: 'Queen',
    location: 'C:\\Karaoke\\SC8101-02 - Queen - Bohemian Rhapsody.zip',
  });
  assert.equal(audited.status, 'suggestion');
  assert.equal(audited.disc_code, 'SC8101-02');
  assert.equal(audited.provider, 'Sound Choice');
  assert.equal(audited.suggested_artist, 'Queen');
  assert.equal(audited.suggested_title, 'Bohemian Rhapsody');
});

import { boundedLevenshtein } from './referenceWorker.js';

test('boundedLevenshtein rejects length gaps > maxDist and matches within bound', () => {
  assert.equal(boundedLevenshtein('queen', 'queen', 2), 0);
  assert.equal(boundedLevenshtein('def leppard', 'def leopard', 2), 1);
  assert.equal(boundedLevenshtein('bohemian rhapsody', 'bohemien rhapsdy', 2), 2);
  assert.equal(boundedLevenshtein('kitten', 'sitting', 2), 3);
  assert.equal(boundedLevenshtein('short', 'way too long title', 2), 3);
});

test('auditSong recovers trailing title articles, radio versions, artist aliases, shifted fields, and per-row reversals', () => {
  setTestData(
    {
      tracks: {
        'ed sheeran:::the a team': { id: 'can_ateam', artist: 'Ed Sheeran', title: 'The A Team', genre: 'Pop', year: 2011 },
        'sugababes:::about you now': { id: 'can_ayn', artist: 'Sugababes', title: 'About You Now', genre: 'Pop', year: 2007 },
        'matchbox twenty:::3am': { id: 'can_3am', artist: 'Matchbox Twenty', title: '3AM', genre: 'Rock', year: 1996 },
        'bon jovi:::you want to make a memory': { id: 'can_mem', artist: 'Bon Jovi', title: '(You Want To) Make A Memory', genre: 'Rock', year: 2007 },
        'a perfect circle:::imagine': { id: 'can_img', artist: 'A Perfect Circle', title: 'Imagine', genre: 'Rock', year: 2004 },
        'rem:::losing my religion': { id: 'can_rem', artist: 'REM', title: 'Losing My Religion', genre: 'Rock', year: 1991 },
        'creedence clearwater revival:::travelin band radio edit': { id: 'can_ccr', artist: 'Creedence Clearwater Revival', title: 'Travelin Band (Radio Edit)', genre: 'Rock', year: 1970 },
      },
    },
    { discs: {} }
  );

  // Base alias + preserved radio edit qualifier in dictionary key
  const ccrRadio = auditSong({ artist: 'CCR', title: 'Travelin Band (Radio Edit)' });
  assert.equal(ccrRadio.status, 'suggestion');
  assert.equal(ccrRadio.canonical_id, 'can_ccr');
  assert.equal(ccrRadio.suggested_artist, 'Creedence Clearwater Revival');

  // Base artist key + stripped radio edit qualifier
  const remRadio = auditSong({ artist: 'REM', title: 'Losing My Religion (Radio Edit)' });
  assert.equal(remRadio.status, 'suggestion');
  assert.equal(remRadio.canonical_id, 'can_rem');
  assert.equal(remRadio.suggested_title, 'Losing My Religion');

  // Trailing title article + inverted artist
  const aTeam = auditSong({ artist: 'Sheeran, Ed', title: 'A Team, The' });
  assert.equal(aTeam.status, 'suggestion');
  assert.equal(aTeam.canonical_id, 'can_ateam');
  assert.equal(aTeam.suggested_title, 'The A Team');

  // Radio version suffix
  const radio = auditSong({ artist: 'Sugababes', title: 'About You Now (Radio Version)' });
  assert.equal(radio.status, 'suggestion');
  assert.equal(radio.canonical_id, 'can_ayn');

  // Artist alias
  const alias = auditSong({ artist: 'Matchbox 20', title: '3AM' });
  assert.equal(alias.status, 'suggestion');
  assert.equal(alias.canonical_id, 'can_3am');
  assert.equal(alias.suggested_artist, 'Matchbox Twenty');

  // Shifted disc/artist with empty title
  const shifted = auditSong({ disc: 'Bon Jovi', artist: '(You Want To) Make A Memory', title: '' });
  assert.equal(shifted.status, 'suggestion');
  assert.equal(shifted.canonical_id, 'can_mem');
  assert.equal(shifted.clear_disc, true);
  assert.equal(shifted.suggested_artist, 'Bon Jovi');

  // Per-row reversed artist/title
  const reversed = auditSong({ artist: 'IMAGINE', title: 'A Perfect Circle' });
  assert.equal(reversed.status, 'suggestion');
  assert.equal(reversed.canonical_id, 'can_img');
  assert.equal(reversed.suggested_artist, 'A Perfect Circle');
  assert.equal(reversed.suggested_title, 'Imagine');

  // Per-row reversed artist/title using baseCompositeKey fallback
  const reversedBase = auditSong({ artist: 'Losing My Religion', title: 'REM' });
  assert.equal(reversedBase.status, 'suggestion');
  assert.equal(reversedBase.canonical_id, 'can_rem');
  assert.equal(reversedBase.suggested_artist, 'REM');
  assert.equal(reversedBase.suggested_title, 'Losing My Religion');
});

test('auditSong uses CanonicalIndex layers across both folder and file-upload rows while preserving strict provider guard', () => {
  setTestData(
    {
      tracks: {
        'ronnie dunn:::bleed red': { id: 'can_rd', artist: 'Ronnie Dunn', title: 'Bleed Red', genre: 'Country', year: 2011, source: 'Party Tyme' },
        'chris janson:::til a woman comes along': { id: 'can_cj', artist: 'Chris Janson', title: "'Til A Woman Comes Along", genre: 'Country', year: 2010, source: 'KaraFun' },
        'the smashing pumpkins:::33': { id: 'can_sp', artist: 'The Smashing Pumpkins', title: '33', genre: 'Rock', year: 1995, source: 'Zoom Karaoke' },
      },
    },
    { discs: {} }
  );

  // Unknown disc prefix stuck in artist (folder or spreadsheet): rescues disc_code, NEVER guesses provider from dictionary source
  const phn = auditSong({ artist: 'PHN1104', title: '01 - Ronnie Dunn - Bleed Red' });
  assert.equal(phn.status, 'suggestion');
  assert.equal(phn.canonical_id, 'can_rd');
  assert.equal(phn.suggested_artist, 'Ronnie Dunn');
  assert.equal(phn.suggested_title, 'Bleed Red');
  assert.equal(phn.disc_code, 'PHN1104-01');
  assert.equal(phn.provider, null);
  assert.equal(phn.provider_matched, false);

  // File upload (VirtualDJ/CSV) with missing artist/title falls back to basename of location
  const vdjRow = auditSong({ artist: '', title: '', location: 'C:\\Karaoke\\Country\\PHN1104 - 01 - Ronnie Dunn - Bleed Red.zip' });
  assert.equal(vdjRow.status, 'suggestion');
  assert.equal(vdjRow.canonical_id, 'can_rd');
  assert.equal(vdjRow.disc_code, 'PHN1104-01');
  assert.equal(vdjRow.provider, null);

  // Leading "The" mismatch
  const sp = auditSong({ artist: 'Smashing Pumpkins', title: '33' });
  assert.equal(sp.status, 'suggestion');
  assert.equal(sp.canonical_id, 'can_sp');
  assert.equal(sp.provider, null);

  // 1-edit artist typo recovery
  const typo = auditSong({ artist: 'Jansen, Chris', title: "'Til A Woman Comes Along" });
  assert.equal(typo.status, 'suggestion');
  assert.equal(typo.canonical_id, 'can_cj');
  assert.equal(typo.suggested_artist, 'Chris Janson');
  assert.equal(typo.provider, null);

  setTestData(null, null);
});

test('auditSong isolates shared batch fuzzyCache entries across different locations and handles real CSV/VDJ upload outputs', () => {
  setTestData(
    {
      tracks: {
        'queen:::bohemian rhapsody': { id: 'can_qr', artist: 'Queen', title: 'Bohemian Rhapsody', genre: 'Rock', year: 1975, source: 'Party Tyme' },
        'journey:::faithfully': { id: 'can_jf', artist: 'Journey', title: 'Faithfully', genre: 'Rock', year: 1983, source: 'Sound Choice' },
        'ronnie dunn:::bleed red': { id: 'can_rd', artist: 'Ronnie Dunn', title: 'Bleed Red', genre: 'Country', year: 2011, source: 'Party Tyme' },
        'lou bega:::mambo no 5': { id: 'can_lb', artist: 'Lou Bega', title: 'Mambo No. 5', genre: 'Pop', year: 1999, source: 'Zoom Karaoke' },
      },
    },
    { discs: {} }
  );

  // Shared batch fuzzyCache must NOT poison distinct rows that share placeholder artist/title
  const batchCache = new Map();
  const first = auditSong(
    { artist: 'Unknown Artist', title: 'Track 1', location: 'C:\\Karaoke\\Queen - Bohemian Rhapsody.zip' },
    batchCache
  );
  const second = auditSong(
    { artist: 'Unknown Artist', title: 'Track 1', location: 'C:\\Karaoke\\Journey - Faithfully.zip' },
    batchCache
  );
  assert.equal(first.status, 'suggestion');
  assert.equal(first.canonical_id, 'can_qr');
  assert.equal(second.status, 'suggestion');
  assert.equal(second.canonical_id, 'can_jf');

  // Extensionless filenameStem must not truncate titles containing dots like "Mambo No. 5"
  const dotTitle = auditSong(
    { artist: 'Unknown Artist', title: 'Unknown Title', location: 'C:\\Karaoke\\Lou Bega - Mambo No. 5' },
    batchCache
  );
  assert.equal(dotTitle.status, 'suggestion');
  assert.equal(dotTitle.canonical_id, 'can_lb');

  // Real CSV upload pipeline: blank Artist -> rowsToSongs() injects 'Unknown Artist' -> auditSong() recovers from location
  const csvText = [
    'Artist,Title,Path',
    ',Track 01,C:\\Karaoke\\Queen - Bohemian Rhapsody.zip',
    ',Track 02,C:\\Karaoke\\Journey - Faithfully.zip',
  ].join('\n');
  const parsedCsv = parseDelimitedText(csvText);
  const mapping = guessColumnMapping(parsedCsv[0]);
  const csvSongs = rowsToSongs(parsedCsv, mapping, true);
  assert.equal(csvSongs.length, 2);
  assert.equal(csvSongs[0].artist, 'Unknown Artist');
  const csvAudited = csvSongs.map((song) => auditSong(song, batchCache));
  assert.equal(csvAudited[0].canonical_id, 'can_qr');
  assert.equal(csvAudited[0].provider, null);
  assert.equal(csvAudited[1].canonical_id, 'can_jf');
  assert.equal(csvAudited[1].provider, null);

  // Real VirtualDJ XML pipeline: untagged <Song FilePath="..."> -> parseVirtualDJDatabase() -> auditSong() preserves full PHN1104-01
  const vdjXml = `<?xml version="1.0" encoding="UTF-8"?>
<VirtualDJ_Database Version="8.5">
  <Song FilePath="C:\\Karaoke\\Country\\PHN1104 - 01 - Ronnie Dunn - Bleed Red.zip" />
</VirtualDJ_Database>`;
  const vdj = parseVirtualDJDatabase(vdjXml);
  assert.equal(vdj.songs.length, 1);
  const vdjAudit = auditSong(vdj.songs[0], batchCache);
  assert.equal(vdjAudit.status, 'suggestion');
  assert.equal(vdjAudit.canonical_id, 'can_rd');
  assert.equal(vdjAudit.suggested_artist, 'Ronnie Dunn');
  assert.equal(vdjAudit.suggested_title, 'Bleed Red');
  assert.equal(vdjAudit.disc_code, 'PHN1104-01');
  assert.equal(vdjAudit.provider, null);

  setTestData(null, null);
});
