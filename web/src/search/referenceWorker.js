// Web Worker for offline canonical reference lookup, local parsing, inverted column detection, and typo suggestions.
import { resolveProvider, extractDiscCode } from '../utils/karaokeProvider.js';
import { parseFolderFilesLocal } from '../utils/folderParser.js';
import { detectDelimiter, parseDelimitedText, detectSoftwareProfile, rowsToSongs } from '../utils/csvParser.js';
import { isVirtualDJDatabaseXML, parseVirtualDJDatabase } from '../utils/virtualDjParser.js';
import { CanonicalIndex, extractLeadingResidualDisc } from '../utils/canonicalMatcher.js';
import {
  removeDiacritics,
  uninvertArtist,
  uninvertTitle,
  ARTIST_ALIASES,
  stripNoiseTags,
  normalizeKey,
  normalizeArtist,
  normalizeTitle,
  compositeKey,
  normalizeDiscCode,
  boundedLevenshtein,
  levenshteinDistance,
} from '../utils/songNormalize.js';

export {
  resolveProvider,
  extractDiscCode,
  removeDiacritics,
  uninvertArtist,
  uninvertTitle,
  ARTIST_ALIASES,
  stripNoiseTags,
  normalizeKey,
  normalizeArtist,
  normalizeTitle,
  compositeKey,
  normalizeDiscCode,
  boundedLevenshtein,
  levenshteinDistance,
};

let dictionary = null;
let discMap = null;
let canonicalIndex = null; // Unified canonical index (exact + CSR inverted + title/artist typo)
let initPromise = null;
let referenceUnavailable = false;
let cachedRawRows = [];

export function setTestData(d, dm) {
  dictionary = d;
  discMap = dm;
  rebuildCanonicalIndex();
}

async function fetchGzipJSON(url) {
  const res = await fetch(url);
  if (!res.ok) throw new Error(`HTTP ${res.status} fetching ${url}`);

  try {
    const clone = res.clone();
    return await clone.json();
  } catch {
    // If transparent browser decompression was not performed, decode gzip stream
  }

  if (typeof DecompressionStream !== 'undefined') {
    const ds = new DecompressionStream('gzip');
    const stream = res.body.pipeThrough(ds);
    const text = await new Response(stream).text();
    return JSON.parse(text);
  }

  return await res.json();
}

export function detectInvertedColumns(rows) {
  if (!canonicalIndex || !rows || rows.length === 0) return false;

  const sample = rows.slice(0, 30).filter(r => (r.artist || r.title));
  if (sample.length < 5) return false;

  let normalMatches = 0;
  let invertedMatches = 0;

  for (const row of sample) {
    if (canonicalIndex.lookupBaseExact(row.artist, row.title)) normalMatches++;
    if (canonicalIndex.lookupBaseExact(row.title, row.artist)) invertedMatches++;
  }

  const invertRatio = invertedMatches / sample.length;
  return invertRatio >= 0.4 && invertedMatches > normalMatches * 1.5;
}

function buildWorkSuggestion(row, match, { provider, discCode, reason, confidence = 1.0 }) {
  return {
    status: 'suggestion',
    canonical_id: match.id,
    is_custom: false,
    confidence,
    provider: provider || null,
    disc_code: discCode || null,
    provider_matched: Boolean(provider),
    suggested_artist: match.artist,
    suggested_title: match.title,
    genre: row.genre || match.genre || '',
    year: row.year || match.year || null,
    suggested_genre: (!row.genre && match.genre) ? match.genre : null,
    suggested_year: (!row.year && match.year) ? match.year : null,
    change_reason: reason,
  };
}

export function auditSong(row, fuzzyCache = null) {
  const originalArtist = (row.artist || '').trim();
  const originalTitle = (row.title || '').trim();
  const effectiveArtist = /^unknown artist$/i.test(originalArtist) ? '' : originalArtist;
  const effectiveTitle = /^unknown title$/i.test(originalTitle) ? '' : originalTitle;
  const originalDisc = (row.disc || '').trim();
  const filenameStem = (row.location || '').split(/[\\/]/).pop()?.replace(/\.[A-Za-z0-9]{2,5}$/, '') || '';
  const detectedDisc = originalDisc || extractDiscCode([effectiveTitle, filenameStem, effectiveArtist].filter(Boolean).join(' ')) || '';
  const discWasExtracted = !originalDisc && Boolean(detectedDisc);
  const explicitProvider = resolveProvider(detectedDisc, row.brand || row.source);

  // 1. Disc code exact resolution (from explicit disc column or extracted from filename/fields)
  if (detectedDisc && discMap && discMap.discs) {
    const normDisc = normalizeDiscCode(detectedDisc);
    const discTrack = discMap.discs[normDisc];
    if (discTrack) {
      const dictMatch = canonicalIndex ? canonicalIndex.lookupBaseExact(discTrack.artist, discTrack.title) : null;

      const resolvedGenre = discTrack.genre || (dictMatch && dictMatch.genre) || '';
      const resolvedYear = discTrack.year || (dictMatch && dictMatch.year) || null;

      const hasCleanDiff = discTrack.artist !== originalArtist || discTrack.title !== originalTitle;
      const hasMetaDiff = discWasExtracted || (!row.genre && resolvedGenre) || (!row.year && resolvedYear);
      const provider = explicitProvider || discTrack.source || resolveProvider(normDisc, '');

      let reason = null;
      if (hasCleanDiff || discWasExtracted) {
        reason = `Resolved from manufacturer disc code (${normDisc})`;
      } else if (hasMetaDiff) {
        reason = 'Auto-filled genre/year metadata';
      }

      return {
        status: (hasCleanDiff || hasMetaDiff) ? 'suggestion' : 'verified',
        canonical_id: discTrack.id || (dictMatch && dictMatch.id) || null,
        is_custom: false,
        confidence: 1.0,
        provider: provider || null,
        disc_code: normDisc,
        provider_matched: Boolean(provider),
        suggested_artist: discTrack.artist,
        suggested_title: discTrack.title,
        genre: row.genre || resolvedGenre,
        year: row.year || resolvedYear,
        suggested_genre: (!row.genre && resolvedGenre) ? resolvedGenre : null,
        suggested_year: (!row.year && resolvedYear) ? resolvedYear : null,
        change_reason: reason,
      };
    }
  }

  // 2. Exact normalized match via CanonicalIndex (base dictionary key first, then secondary article/collab)
  const exactMatch = canonicalIndex ? canonicalIndex.lookupBaseExact(effectiveArtist, effectiveTitle) : null;

  if (exactMatch) {
    const match = exactMatch;
    const hasCleanDiff = match.artist !== originalArtist || match.title !== originalTitle;
    const hasMetaDiff = discWasExtracted || (!row.genre && match.genre) || (!row.year && match.year);

    let reason = null;
    if (match.artist !== originalArtist && match.title !== originalTitle) {
      reason = 'Standardized artist name and title';
    } else if (match.artist !== originalArtist) {
      reason = `Standardized to "${match.artist}"`;
    } else if (match.title !== originalTitle) {
      reason = 'Standardized title';
    } else if (discWasExtracted) {
      reason = `Detected manufacturer disc code (${detectedDisc})`;
    } else if (hasMetaDiff) {
      reason = 'Auto-filled genre/year metadata';
    }

    const provider = explicitProvider || null;

    return {
      status: (hasCleanDiff || hasMetaDiff) ? 'suggestion' : 'verified',
      canonical_id: match.id,
      is_custom: false,
      confidence: 1.0,
      provider,
      disc_code: detectedDisc || null,
      provider_matched: Boolean(provider),
      suggested_artist: match.artist,
      suggested_title: match.title,
      genre: row.genre || match.genre || '',
      year: row.year || match.year || null,
      suggested_genre: (!row.genre && match.genre) ? match.genre : null,
      suggested_year: (!row.year && match.year) ? match.year : null,
      change_reason: reason,
    };
  }

  // 2b. Layer 1 secondary exact lookups (expanded key, leading-"The", primary collaboration artist)
  if (canonicalIndex && effectiveArtist && effectiveTitle) {
    const sec = canonicalIndex.lookupSecondaryExact(effectiveArtist, effectiveTitle);
    if (sec) {
      return buildWorkSuggestion(row, sec.track, {
        provider: explicitProvider || null,
        discCode: detectedDisc || null,
        confidence: sec.confidence,
        reason: sec.reason,
      });
    }
  }

  // 2c. Shifted fields recovery (title empty because artist landed in disc)
  if (canonicalIndex && !effectiveTitle && originalDisc && effectiveArtist) {
    const shiftedMatch = canonicalIndex.lookupBaseExact(originalDisc, effectiveArtist)
      || canonicalIndex.lookupBaseExact(effectiveArtist, originalDisc);
    if (shiftedMatch) {
      return {
        ...buildWorkSuggestion(row, shiftedMatch, {
          provider: resolveProvider('', row.brand || row.source),
          discCode: null,
          reason: 'Repaired shifted disc/artist columns',
        }),
        clear_disc: true,
      };
    }
  }

  // 2d. Per-row reversed artist/title recovery (handles mixed-layout folders/spreadsheets)
  if (canonicalIndex && effectiveArtist && effectiveTitle) {
    const reversedMatch = canonicalIndex.lookupBaseExact(effectiveTitle, effectiveArtist);
    if (reversedMatch) {
      return buildWorkSuggestion(row, reversedMatch, {
        provider: explicitProvider || null,
        discCode: detectedDisc || null,
        reason: 'Swapped reversed artist and title',
      });
    }
  }

  // 2e. Layer 2: Canonical-first CSR inverted-index match on raw row/filename tokens (folder + CSV/XML uploads)
  if (canonicalIndex) {
    const lookupRaw = (query) => {
      if (!query) return null;
      const cacheKey = fuzzyCache ? `raw:::${query}` : '';
      if (fuzzyCache && fuzzyCache.has(cacheKey)) {
        return fuzzyCache.get(cacheKey);
      }
      const res = canonicalIndex.matchRawTokens(query);
      if (fuzzyCache) fuzzyCache.set(cacheKey, res);
      return res;
    };

    const combinedFields = [effectiveArtist, effectiveTitle].filter(Boolean).join(' - ').trim();
    let rawMatch = combinedFields ? lookupRaw(combinedFields) : null;
    if (!rawMatch && filenameStem && filenameStem !== combinedFields) {
      rawMatch = lookupRaw(filenameStem);
    }
    if (rawMatch) {
      const stemDisc = (!detectedDisc && filenameStem) ? extractLeadingResidualDisc(filenameStem, rawMatch.matchedSet) : '';
      const finalDisc = detectedDisc || stemDisc || rawMatch.rescuedDisc || null;
      const finalProvider = explicitProvider || (finalDisc ? resolveProvider(finalDisc, row.brand || row.source) : null);
      return buildWorkSuggestion(row, rawMatch.track, {
        provider: finalProvider || null,
        discCode: finalDisc,
        confidence: rawMatch.confidence,
        reason: rawMatch.reason,
      });
    }
  }

  // 3. Layer 3: Bounded title typo (exact artist) and artist 1-delete SymSpell typo (exact title)
  if (canonicalIndex && effectiveArtist && effectiveTitle) {
    const typoBaseKey = fuzzyCache ? `${effectiveArtist.toLowerCase()}:::${effectiveTitle.toLowerCase()}` : '';
    const titleCacheKey = fuzzyCache ? `ttypo:::${typoBaseKey}` : '';
    let titleMatch;
    if (fuzzyCache && fuzzyCache.has(titleCacheKey)) {
      titleMatch = fuzzyCache.get(titleCacheKey);
    } else {
      titleMatch = canonicalIndex.matchTitleTypo(effectiveArtist, effectiveTitle);
      if (fuzzyCache) fuzzyCache.set(titleCacheKey, titleMatch);
    }
    if (titleMatch) {
      return buildWorkSuggestion(row, titleMatch.track, {
        provider: explicitProvider || null,
        discCode: detectedDisc || null,
        confidence: titleMatch.confidence,
        reason: titleMatch.reason,
      });
    }

    const artistCacheKey = fuzzyCache ? `atypo:::${typoBaseKey}` : '';
    let artistMatch;
    if (fuzzyCache && fuzzyCache.has(artistCacheKey)) {
      artistMatch = fuzzyCache.get(artistCacheKey);
    } else {
      artistMatch = canonicalIndex.matchArtistTypo(effectiveArtist, effectiveTitle);
      if (fuzzyCache) fuzzyCache.set(artistCacheKey, artistMatch);
    }
    if (artistMatch) {
      return buildWorkSuggestion(row, artistMatch.track, {
        provider: explicitProvider || null,
        discCode: detectedDisc || null,
        confidence: artistMatch.confidence,
        reason: artistMatch.reason,
      });
    }
  }

  // 4. Custom Track (No confident match)
  return {
    status: 'custom',
    canonical_id: null,
    is_custom: true,
    confidence: 0,
    provider: explicitProvider || null,
    disc_code: detectedDisc || null,
    provider_matched: Boolean(explicitProvider),
    suggested_artist: originalArtist,
    suggested_title: originalTitle,
    genre: row.genre || '',
    year: row.year || null,
    change_reason: null,
  };
}

function auditRowsBatch(rows) {
  const isInverted = detectInvertedColumns(rows);
  const fuzzyCache = new Map();

  let verifiedCount = 0;
  let suggestionCount = 0;
  let customCount = 0;

  const audited = new Array(rows.length);
  for (let idx = 0; idx < rows.length; idx++) {
    const audit = auditSong(rows[idx], fuzzyCache);
    if (audit.status === 'verified') verifiedCount++;
    else if (audit.status === 'suggestion') suggestionCount++;
    else customCount++;

    audited[idx] = { index: idx, ...audit };
  }

  return {
    inverted_columns_detected: isInverted,
    reference_unavailable: referenceUnavailable,
    summary: {
      total: rows.length,
      verified: verifiedCount,
      suggestions: suggestionCount,
      custom: customCount,
    },
    results: audited,
  };
}

function rebuildCanonicalIndex() {
  canonicalIndex = dictionary?.tracks ? new CanonicalIndex(dictionary.tracks) : null;
}

if (typeof self !== 'undefined') {
  self.onmessage = async (e) => {
    const { type, id, ...payload } = e.data;

    try {
      switch (type) {
        case 'INIT': {
          const { dictionaryUrl = '/reference/v1/dictionary.json.gz', discMapUrl = '/reference/v1/disc_map.json.gz' } = payload;

          initPromise = Promise.all([
            fetchGzipJSON(dictionaryUrl).catch(() => null),
            fetchGzipJSON(discMapUrl).catch(() => null),
          ]).then(([dictData, discData]) => {
            dictionary = dictData;
            discMap = discData;
            referenceUnavailable = !dictData && !discData;
            rebuildCanonicalIndex();
          });

          await initPromise;
          self.postMessage({
            type: 'INIT_DONE',
            id,
            totalTracks: dictionary?.total_tracks || 0,
            totalDiscs: discMap?.total_discs || 0,
            reference_unavailable: referenceUnavailable,
          });
          break;
        }

        case 'AUDIT_ROWS': {
          if (initPromise) await initPromise;
          const { rows = [] } = payload;
          const batch = auditRowsBatch(rows);
          self.postMessage({ type: 'AUDIT_RESULTS', id, ...batch });
          break;
        }

        case 'AUDIT_SINGLE': {
          if (initPromise) await initPromise;
          const { index, row } = payload;
          const audit = auditSong(row);
          self.postMessage({
            type: 'AUDIT_SINGLE_RESULT',
            id,
            index,
            result: { index, ...audit },
          });
          break;
        }

        case 'PARSE_FOLDER_AND_AUDIT': {
          if (initPromise) await initPromise;
          const { files = [], pattern = 'auto' } = payload;
          cachedRawRows = [];
          const lookup = canonicalIndex ? {
            hasTrack: (key) => Boolean(dictionary.tracks[key]),
            hasArtist: (normArtist) => canonicalIndex.hasNormArtist(normArtist),
          } : null;
          const songs = parseFolderFilesLocal(files, pattern, lookup);
          const batch = auditRowsBatch(songs);
          self.postMessage({
            type: 'PARSE_AND_AUDIT_RESULTS',
            id,
            songs,
            ...batch,
          });
          break;
        }

        case 'PARSE_FILE_AND_AUDIT': {
          if (initPromise) await initPromise;
          const { text = '', fileName = '' } = payload;
          if (isVirtualDJDatabaseXML(text, fileName)) {
            const vdj = parseVirtualDJDatabase(text);
            cachedRawRows = vdj.rows;
            const batch = auditRowsBatch(vdj.songs);
            self.postMessage({
              type: 'PARSE_AND_AUDIT_RESULTS',
              id,
              songs: vdj.songs,
              headers: vdj.headerRow,
              columnMapping: vdj.mapping,
              softwareProfile: { id: 'virtualdj_xml', name: 'VirtualDJ (database.xml)' },
              isVirtualDJ: true,
              ...batch,
            });
            break;
          }
          if (fileName.toLowerCase().endsWith('.xml')) {
            throw new Error('Unsupported XML file. Please select a VirtualDJ database.xml file.');
          }
          const delim = detectDelimiter(text);
          const rows = parseDelimitedText(text, delim);
          if (rows.length === 0) {
            throw new Error('The selected file appears to be empty');
          }
          cachedRawRows = rows;
          const headerRow = rows[0];
          const profile = detectSoftwareProfile(headerRow, delim);
          const mapping = profile.columnMapping(headerRow);
          const songs = rowsToSongs(rows, mapping, true);
          const batch = auditRowsBatch(songs);
          self.postMessage({
            type: 'PARSE_AND_AUDIT_RESULTS',
            id,
            songs,
            headers: headerRow,
            columnMapping: mapping,
            softwareProfile: { id: profile.id, name: profile.name },
            ...batch,
          });
          break;
        }

        case 'REMAP_AND_AUDIT': {
          if (initPromise) await initPromise;
          const { columnMapping } = payload;
          const songs = rowsToSongs(cachedRawRows, columnMapping, true);
          const batch = auditRowsBatch(songs);
          self.postMessage({
            type: 'PARSE_AND_AUDIT_RESULTS',
            id,
            songs,
            columnMapping,
            ...batch,
          });
          break;
        }

        default:
          throw new Error(`Unknown referenceWorker action: ${type}`);
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
