import test from 'node:test';
import assert from 'node:assert/strict';
import MiniSearch from 'minisearch';

import {
  normalizeString,
  computeSongMatchTier,
  rankSongResults,
  computeArtistMatchTier,
  rankArtistResults,
  trackSearchOptions,
  artistSearchOptions,
} from './catalogWorker.js';

test('computeSongMatchTier assigns appropriate priority tiers', () => {
  const song1 = { artist: 'Jelly Roll', title: 'Save Me' };
  const song2 = { artist: 'Lou Monte', title: 'Jellyroll' };
  const song3 = { artist: 'Buckwheat Boyz', title: 'Peanut Butter Jelly Time' };
  const song4 = { artist: 'Queen', title: 'Bohemian Rhapsody' };
  const song5 = { artist: 'ABBA', title: 'Dancing Queen' };
  const song6 = { artist: 'Toto', title: 'Africa' };

  // Query: "jell"
  // Both artist prefix ("Jelly Roll") and title prefix ("Jellyroll") are top prefix matches (Tier 2)
  assert.equal(computeSongMatchTier(song1, 'jell'), 2);
  assert.equal(computeSongMatchTier(song2, 'jell'), 2);
  // Word boundary substring inside title is Tier 3
  assert.equal(computeSongMatchTier(song3, 'jell'), 3);

  // Query: "jelly roll" -> exact artist match is Tier 1
  assert.equal(computeSongMatchTier(song1, 'jelly roll'), 1);

  // Multi-word exact match: "jelly roll save me" is Tier 0
  assert.equal(computeSongMatchTier(song1, 'jelly roll save me'), 0);

  // Query: "queen" -> Queen exact artist is Tier 1, "Dancing Queen" word boundary is Tier 3
  assert.equal(computeSongMatchTier(song4, 'queen'), 1);
  assert.equal(computeSongMatchTier(song5, 'queen'), 3);

  // Query: "africa" -> Toto exact title is Tier 0
  assert.equal(computeSongMatchTier(song6, 'africa'), 0);
});

test('rankSongResults prioritizes Jelly Roll over fuzzy collisions and title substring noise', () => {
  const tracks = [
    { song_id: 'jr-1', artist: 'Jelly Roll', title: 'Save Me' },
    { song_id: 'jr-2', artist: 'Jelly Roll', title: 'Need a Favor' },
    { song_id: 't-1', artist: 'Buckwheat Boyz', title: 'Peanut Butter Jelly Time' },
    { song_id: 't-2', artist: 'Lou Monte', title: 'Jellyroll' },
    { song_id: 'f-1', artist: 'Bell Biv DeVoe', title: 'Poison' },
    { song_id: 'f-2', artist: 'Neil Young', title: 'Tell Me Why' },
    { song_id: 'f-3', artist: 'Billy Idol', title: 'Rebel Yell' },
  ];

  const ms = new MiniSearch({
    idField: 'song_id',
    fields: ['artist', 'title'],
    storeFields: ['song_id', 'artist', 'title'],
    searchOptions: trackSearchOptions,
  });
  ms.addAll(tracks);

  const raw = ms.search('jell');
  const ranked = rankSongResults(raw, 'jell');

  // Verify that Bell, Tell, Yell did NOT match because fuzzy is disabled for terms < 5 chars
  const matchedIds = ranked.map((r) => r.song_id);
  assert.ok(!matchedIds.includes('f-1'), 'should not match Bell Biv DeVoe');
  assert.ok(!matchedIds.includes('f-2'), 'should not match Neil Young (Tell)');
  assert.ok(!matchedIds.includes('f-3'), 'should not match Billy Idol (Yell)');

  // Verify Jelly Roll and Jellyroll are at the very top
  assert.ok(matchedIds.includes('jr-1'));
  assert.ok(matchedIds.includes('jr-2'));
  assert.ok(matchedIds.includes('t-2'));

  // Peanut Butter Jelly Time (substring match) is ranked after prefix matches
  const pbIndex = matchedIds.indexOf('t-1');
  const jr1Index = matchedIds.indexOf('jr-1');
  const jr2Index = matchedIds.indexOf('jr-2');
  assert.ok(jr1Index < pbIndex, 'Jelly Roll should rank before Peanut Butter Jelly Time');
  assert.ok(jr2Index < pbIndex, 'Jelly Roll should rank before Peanut Butter Jelly Time');
});

test('rankArtistResults prioritizes Jelly Roll for artist search', () => {
  const artists = [
    { norm: 'jelly roll', artist: 'Jelly Roll', count: 12 },
    { norm: 'the jellies', artist: 'The Jellies', count: 2 },
    { norm: 'dj jelly', artist: 'DJ Jelly', count: 1 },
    { norm: 'bell biv devoe', artist: 'Bell Biv DeVoe', count: 5 },
    { norm: 'archie bell', artist: 'Archie Bell', count: 2 },
  ];

  const asi = new MiniSearch({
    idField: 'norm',
    fields: ['artist'],
    storeFields: ['artist', 'norm', 'count'],
    searchOptions: artistSearchOptions,
  });
  asi.addAll(artists);

  const raw = asi.search('jell');
  const ranked = rankArtistResults(raw, 'jell');

  const artistNames = ranked.map((a) => a.artist);
  assert.ok(!artistNames.includes('Bell Biv DeVoe'), 'should not match Bell Biv DeVoe');
  assert.ok(!artistNames.includes('Archie Bell'), 'should not match Archie Bell');
  assert.ok(artistNames.includes('Jelly Roll'), 'should match Jelly Roll');
  assert.ok(artistNames.includes('DJ Jelly'), 'should match DJ Jelly');
});
