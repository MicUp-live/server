// Web Worker for offline canonical reference lookup, local parsing, inverted column detection, and typo suggestions.
export { resolveProvider, extractDiscCode } from '../utils/karaokeProvider.js';
import { parseFolderFilesLocal } from '../utils/folderParser.js';
import { detectDelimiter, parseDelimitedText, detectSoftwareProfile, rowsToSongs } from '../utils/csvParser.js';
import { isVirtualDJDatabaseXML, parseVirtualDJDatabase } from '../utils/virtualDjParser.js';
import { CanonicalIndex } from '../utils/canonicalMatcher.js';
import {
  extractAuditContext,
  tryDiscMatch,
  tryExactMatch,
  trySecondaryExactMatch,
  tryShiftedFieldsMatch,
  tryReversedMatch,
  tryRawTokenMatch,
  tryTypoMatch,
  buildCustomTrack,
} from './referenceAuditHelpers.js';
export {
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

export function auditSong(row, fuzzyCache = null) {
  const ctx = extractAuditContext(row);

  const discResult = tryDiscMatch(ctx, row, discMap, canonicalIndex);
  if (discResult) return discResult;

  if (canonicalIndex) {
    const canonicalResult =
      tryExactMatch(ctx, row, canonicalIndex)
      || trySecondaryExactMatch(ctx, row, canonicalIndex)
      || tryShiftedFieldsMatch(ctx, row, canonicalIndex)
      || tryReversedMatch(ctx, row, canonicalIndex)
      || tryRawTokenMatch(ctx, row, canonicalIndex, fuzzyCache)
      || tryTypoMatch(ctx, row, canonicalIndex, fuzzyCache);
    if (canonicalResult) return canonicalResult;
  }

  return buildCustomTrack(ctx, row);
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

async function ensureInitialized() {
  if (initPromise) await initPromise;
}

async function handleInit(payload) {
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
  return {
    type: 'INIT_DONE',
    totalTracks: dictionary?.total_tracks || 0,
    totalDiscs: discMap?.total_discs || 0,
    reference_unavailable: referenceUnavailable,
  };
}

async function handleAuditRows(payload) {
  await ensureInitialized();
  const { rows = [] } = payload;
  return { type: 'AUDIT_RESULTS', ...auditRowsBatch(rows) };
}

async function handleAuditSingle(payload) {
  await ensureInitialized();
  const { index, row } = payload;
  const audit = auditSong(row);
  return {
    type: 'AUDIT_SINGLE_RESULT',
    index,
    result: { index, ...audit },
  };
}

async function handleParseFolderAndAudit(payload) {
  await ensureInitialized();
  const { files = [], pattern = 'auto' } = payload;
  cachedRawRows = [];
  const lookup = canonicalIndex ? {
    hasTrack: (key) => Boolean(dictionary.tracks[key]),
    hasArtist: (normArtist) => canonicalIndex.hasNormArtist(normArtist),
  } : null;
  const songs = parseFolderFilesLocal(files, pattern, lookup);
  return {
    type: 'PARSE_AND_AUDIT_RESULTS',
    songs,
    ...auditRowsBatch(songs),
  };
}

async function handleParseFileAndAudit(payload) {
  await ensureInitialized();
  const { text = '', fileName = '' } = payload;

  if (isVirtualDJDatabaseXML(text, fileName)) {
    const vdj = parseVirtualDJDatabase(text);
    cachedRawRows = vdj.rows;
    return {
      type: 'PARSE_AND_AUDIT_RESULTS',
      songs: vdj.songs,
      headers: vdj.headerRow,
      columnMapping: vdj.mapping,
      softwareProfile: { id: 'virtualdj_xml', name: 'VirtualDJ (database.xml)' },
      isVirtualDJ: true,
      ...auditRowsBatch(vdj.songs),
    };
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
  return {
    type: 'PARSE_AND_AUDIT_RESULTS',
    songs,
    headers: headerRow,
    columnMapping: mapping,
    softwareProfile: { id: profile.id, name: profile.name },
    ...auditRowsBatch(songs),
  };
}

async function handleRemapAndAudit(payload) {
  await ensureInitialized();
  const { columnMapping } = payload;
  const songs = rowsToSongs(cachedRawRows, columnMapping, true);
  return {
    type: 'PARSE_AND_AUDIT_RESULTS',
    songs,
    columnMapping,
    ...auditRowsBatch(songs),
  };
}

const MESSAGE_HANDLERS = {
  INIT: handleInit,
  AUDIT_ROWS: handleAuditRows,
  AUDIT_SINGLE: handleAuditSingle,
  PARSE_FOLDER_AND_AUDIT: handleParseFolderAndAudit,
  PARSE_FILE_AND_AUDIT: handleParseFileAndAudit,
  REMAP_AND_AUDIT: handleRemapAndAudit,
};

if (typeof self !== 'undefined') {
  self.onmessage = async (e) => {
    const { type, id, ...payload } = e.data;
    try {
      const handler = MESSAGE_HANDLERS[type];
      if (!handler) {
        throw new Error(`Unknown referenceWorker action: ${type}`);
      }
      self.postMessage({ id, ...(await handler(payload)) });
    } catch (err) {
      self.postMessage({
        type: 'ERROR',
        id,
        error: err.message || String(err),
      });
    }
  };
}
