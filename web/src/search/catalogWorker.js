import MiniSearch from 'minisearch';

let allTracks = [];
let searchIndex = null;
let artistSearchIndex = null;
let artistGroups = [];
let artistMap = new Map();
let artistSongsMap = new Map();
let trackMap = new Map();
let canonicalTrackMap = new Map();

export function normalizeString(str) {
  return (str || '').trim().toLowerCase().replace(/\s+/g, ' ');
}

export function cleanLeadingArticle(str) {
  return (str || '').replace(/^(the|a|an)\s+/i, '');
}

export function songKey(artist, title) {
  return `${normalizeString(artist)}:::${normalizeString(title)}`;
}

export function computeSongMatchTier(song, query) {
  const q = normalizeString(query);
  if (!q) return 9;

  const title = normalizeString(song.title || song.label || '');
  const artist = normalizeString(song.artist || '');
  const artistNoArticle = cleanLeadingArticle(artist);
  const artistAndTitle = `${artist} ${title}`;
  const titleAndArtist = `${title} ${artist}`;

  // Tier 0: Exact match on Title, or exact multi-word combined artist+title
  if (title === q || artistAndTitle === q || titleAndArtist === q) return 0;

  // Tier 1: Exact match on Artist (e.g. "Queen", "Jelly Roll", "Adele")
  if (artist === q || artistNoArticle === q) return 1;

  // Tier 2: Title or Artist starts with query (prefix match, e.g. "jell", "bohem")
  if (title.startsWith(q) || artistAndTitle.startsWith(q)) return 2;
  if (artist.startsWith(q) || artistNoArticle.startsWith(q)) return 2;

  // Tier 3: Word in Title or Artist starts with query (word-boundary prefix)
  const titleWords = title.split(/[\s\-_\/]+/);
  const artistWords = artist.split(/[\s\-_\/]+/);
  if (titleWords.some((w) => w.startsWith(q))) return 3;
  if (artistWords.some((w) => w.startsWith(q))) return 3;

  // Tier 4: Substring match anywhere in title or artist
  if (title.includes(q)) return 4;
  if (artist.includes(q)) return 4;

  // Tier 5: Other / fuzzy matches
  return 5;
}

export function rankSongResults(results, query) {
  return results
    .map((result, index) => ({ result, index, tier: computeSongMatchTier(result, query) }))
    .sort((a, b) => {
      if (a.tier !== b.tier) return a.tier - b.tier;
      // Preserve MiniSearch's relevance ordering for equally strong matches within a tier.
      return a.index - b.index;
    })
    .map(({ result }) => result);
}

export function computeArtistMatchTier(artistItem, query) {
  const q = normalizeString(query);
  if (!q) return 9;

  const norm = normalizeString(artistItem.norm || artistItem.artist || '');
  const normNoArticle = cleanLeadingArticle(norm);

  // Tier 0: Exact artist match
  if (norm === q || normNoArticle === q) return 0;

  // Tier 1: Starts with query
  if (norm.startsWith(q) || normNoArticle.startsWith(q)) return 1;

  // Tier 2: Word boundary prefix
  const words = norm.split(/[\s\-_\/]+/);
  if (words.some((w) => w.startsWith(q))) return 2;

  // Tier 3: Substring
  if (norm.includes(q)) return 3;

  // Tier 4: Other / fuzzy
  return 4;
}

export function rankArtistResults(results, query) {
  return results
    .map((result, index) => ({ result, index, tier: computeArtistMatchTier(result, query) }))
    .sort((a, b) => {
      if (a.tier !== b.tier) return a.tier - b.tier;
      return a.index - b.index;
    })
    .map(({ result }) => result);
}

export const trackSearchOptions = {
  boost: { artist: 2, title: 1.5 },
  prefix: true,
  fuzzy: (term) => (term.length >= 5 ? 0.2 : false),
  weights: { prefix: 1.0, fuzzy: 0.15 },
};

export const artistSearchOptions = {
  boost: { artist: 2 },
  prefix: true,
  fuzzy: (term) => (term.length >= 5 ? 0.2 : false),
  weights: { prefix: 1.0, fuzzy: 0.15 },
};

if (typeof self !== 'undefined') {
  self.onmessage = async (e) => {
    const { type, id, ...payload } = e.data;

    try {
      switch (type) {
        case 'INIT': {
          const { searchUrl } = payload;
          const res = await fetch(searchUrl);
          if (!res.ok) {
            throw new Error(`Failed to load catalog search asset: HTTP ${res.status}`);
          }

          let data;
          const contentType = res.headers.get('content-type') || '';
          if (contentType.includes('json')) {
            data = await res.json();
          } else if (typeof DecompressionStream !== 'undefined') {
            const ds = new DecompressionStream('gzip');
            const stream = res.body.pipeThrough(ds);
            const text = await new Response(stream).text();
            data = JSON.parse(text);
          } else {
            data = await res.json();
          }

          allTracks = data.songs || [];

          // Build track MiniSearch index
          searchIndex = new MiniSearch({
            idField: 'song_id',
            fields: ['artist', 'title'],
            storeFields: ['song_id', 'artist', 'title', 'label', 'genre', 'disc', 'source', 'video_url', 'canonical_id', 'is_custom'],
            searchOptions: trackSearchOptions,
          });

          await searchIndex.addAllAsync(allTracks, { chunkSize: 2000 });

          // Build artist aggregations and track maps
          artistMap = new Map();
          artistSongsMap = new Map();
          trackMap = new Map();
          canonicalTrackMap = new Map();

          for (const track of allTracks) {
            const artist = (track.artist || 'Unknown Artist').trim();
            const norm = normalizeString(artist);
            if (!artistMap.has(norm)) {
              artistMap.set(norm, { artist, norm, count: 0 });
              artistSongsMap.set(norm, []);
            }
            artistMap.get(norm).count++;
            artistSongsMap.get(norm).push(track);

            const key = songKey(track.artist, track.title || track.label);
            trackMap.set(key, track);
            if (track.song_id) {
              trackMap.set(track.song_id, track);
            }
            if (track.canonical_id && !canonicalTrackMap.has(track.canonical_id)) {
              canonicalTrackMap.set(track.canonical_id, track);
            }
          }

          artistGroups = Array.from(artistMap.values()).sort((a, b) =>
            a.artist.localeCompare(b.artist, undefined, { sensitivity: 'base' })
          );

          // Build artist MiniSearch index
          artistSearchIndex = new MiniSearch({
            idField: 'norm',
            fields: ['artist'],
            storeFields: ['artist', 'norm', 'count'],
            searchOptions: artistSearchOptions,
          });

          await artistSearchIndex.addAllAsync(artistGroups, { chunkSize: 1000 });

          self.postMessage({
            type: 'INIT_DONE',
            id,
            total: allTracks.length,
            artists: artistGroups,
          });
          break;
        }

        case 'SEARCH': {
          const { query, limit = 200 } = payload;
          if (!query || !query.trim() || !searchIndex) {
            self.postMessage({ type: 'SEARCH_RESULTS', id, results: [] });
            return;
          }
          const results = rankSongResults(searchIndex.search(query), query);
          self.postMessage({
            type: 'SEARCH_RESULTS',
            id,
            results: results.slice(0, limit),
          });
          break;
        }

        case 'SEARCH_ARTISTS': {
          const { query, limit = 100 } = payload;
          if (!query || !query.trim() || !artistSearchIndex) {
            self.postMessage({ type: 'SEARCH_ARTISTS_RESULTS', id, results: [] });
            return;
          }
          const rawArtistMatches = artistSearchIndex.search(query);
          const artistMatches = rankArtistResults(rawArtistMatches, query);
          const seenNorms = new Set(artistMatches.map((a) => a.norm));
          const combined = [...artistMatches];

          if (searchIndex && combined.length < limit) {
            const songMatches = rankSongResults(searchIndex.search(query), query);
            for (const song of songMatches) {
              const norm = normalizeString(song.artist);
              if (norm && !seenNorms.has(norm)) {
                seenNorms.add(norm);
                const grp = artistMap.get(norm);
                if (grp) {
                  combined.push({
                    id: grp.norm,
                    norm: grp.norm,
                    artist: grp.artist,
                    count: grp.count,
                    matchedBySong: true,
                  });
                }
                if (combined.length >= limit) break;
              }
            }
          }

          self.postMessage({
            type: 'SEARCH_ARTISTS_RESULTS',
            id,
            results: combined.slice(0, limit),
          });
          break;
        }

        case 'GET_ARTIST_SONGS': {
          const { artistNorm } = payload;
          const songs = artistSongsMap.get(artistNorm) || [];
          self.postMessage({
            type: 'GET_ARTIST_SONGS_RESULTS',
            id,
            songs,
          });
          break;
        }

        case 'RESOLVE_FAVORITES': {
          const { favorites = [] } = payload;
          const inBookTracks = [];
          const offBookFavorites = [];

          for (const fav of favorites) {
            const key = songKey(fav.artist, fav.title);
            let track = fav.song_id ? trackMap.get(fav.song_id) : null;
            if (!track) {
              track = trackMap.get(key);
            }
            if (!track && fav.canonical_id) {
              track = canonicalTrackMap.get(fav.canonical_id);
            }
            if (track) {
              inBookTracks.push(track);
            } else {
              offBookFavorites.push(fav);
            }
          }

          self.postMessage({
            type: 'RESOLVE_FAVORITES_RESULTS',
            id,
            inBookTracks,
            offBookFavorites,
          });
          break;
        }

        default:
          throw new Error(`Unknown worker action: ${type}`);
      }
    } catch (err) {
      self.postMessage({
        type: 'ERROR',
        id,
        error: err.message || String(err),
      });
    }
  };
}
