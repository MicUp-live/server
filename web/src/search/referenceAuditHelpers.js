import { resolveProvider, extractDiscCode } from '../utils/karaokeProvider.js';
import { extractLeadingResidualDisc } from '../utils/canonicalMatcher.js';
import { normalizeDiscCode } from '../utils/songNormalize.js';

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

function withFuzzyCache(fuzzyCache, cacheKey, compute) {
  if (!fuzzyCache) return compute();
  if (fuzzyCache.has(cacheKey)) return fuzzyCache.get(cacheKey);
  const res = compute();
  fuzzyCache.set(cacheKey, res);
  return res;
}

function resolveDiscReason(hasCleanDiff, discWasExtracted, hasMetaDiff, normDisc) {
  if (hasCleanDiff || discWasExtracted) {
    return `Resolved from manufacturer disc code (${normDisc})`;
  }
  if (hasMetaDiff) {
    return 'Auto-filled genre/year metadata';
  }
  return null;
}

function resolveExactMatchReason(ctx, match, hasMetaDiff) {
  const artistDiff = match.artist !== ctx.originalArtist;
  const titleDiff = match.title !== ctx.originalTitle;
  if (artistDiff && titleDiff) return 'Standardized artist name and title';
  if (artistDiff) return `Standardized to "${match.artist}"`;
  if (titleDiff) return 'Standardized title';
  if (ctx.discWasExtracted) return `Detected manufacturer disc code (${ctx.detectedDisc})`;
  if (hasMetaDiff) return 'Auto-filled genre/year metadata';
  return null;
}

export function extractAuditContext(row) {
  const originalArtist = (row.artist || '').trim();
  const originalTitle = (row.title || '').trim();
  const effectiveArtist = /^unknown artist$/i.test(originalArtist) ? '' : originalArtist;
  const effectiveTitle = /^unknown title$/i.test(originalTitle) ? '' : originalTitle;
  const originalDisc = (row.disc || '').trim();
  const filenameStem = (row.location || '').split(/[\\/]/).pop()?.replace(/\.[A-Za-z0-9]{2,5}$/, '') || '';
  const detectedDisc = originalDisc
    || extractDiscCode([effectiveTitle, filenameStem, effectiveArtist].filter(Boolean).join(' '))
    || '';
  const discWasExtracted = !originalDisc && Boolean(detectedDisc);
  const explicitProvider = resolveProvider(detectedDisc, row.brand || row.source);

  return {
    originalArtist,
    originalTitle,
    effectiveArtist,
    effectiveTitle,
    originalDisc,
    filenameStem,
    detectedDisc,
    discWasExtracted,
    explicitProvider,
  };
}

export function tryDiscMatch(ctx, row, discMap, canonicalIndex) {
  if (!ctx.detectedDisc || !discMap?.discs) return null;

  const normDisc = normalizeDiscCode(ctx.detectedDisc);
  const discTrack = discMap.discs[normDisc];
  if (!discTrack) return null;

  const dictMatch = canonicalIndex ? canonicalIndex.lookupBaseExact(discTrack.artist, discTrack.title) : null;
  const resolvedGenre = discTrack.genre || dictMatch?.genre || '';
  const resolvedYear = discTrack.year || dictMatch?.year || null;

  const hasCleanDiff = discTrack.artist !== ctx.originalArtist || discTrack.title !== ctx.originalTitle;
  const hasMetaDiff = ctx.discWasExtracted || (!row.genre && resolvedGenre) || (!row.year && resolvedYear);
  const provider = ctx.explicitProvider || discTrack.source || resolveProvider(normDisc, '');

  return {
    status: (hasCleanDiff || hasMetaDiff) ? 'suggestion' : 'verified',
    canonical_id: discTrack.id || dictMatch?.id || null,
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
    change_reason: resolveDiscReason(hasCleanDiff, ctx.discWasExtracted, hasMetaDiff, normDisc),
  };
}

export function tryExactMatch(ctx, row, canonicalIndex) {
  const match = canonicalIndex.lookupBaseExact(ctx.effectiveArtist, ctx.effectiveTitle);
  if (!match) return null;

  const hasCleanDiff = match.artist !== ctx.originalArtist || match.title !== ctx.originalTitle;
  const hasMetaDiff = ctx.discWasExtracted || (!row.genre && match.genre) || (!row.year && match.year);
  const provider = ctx.explicitProvider || null;

  return {
    status: (hasCleanDiff || hasMetaDiff) ? 'suggestion' : 'verified',
    canonical_id: match.id,
    is_custom: false,
    confidence: 1.0,
    provider,
    disc_code: ctx.detectedDisc || null,
    provider_matched: Boolean(provider),
    suggested_artist: match.artist,
    suggested_title: match.title,
    genre: row.genre || match.genre || '',
    year: row.year || match.year || null,
    suggested_genre: (!row.genre && match.genre) ? match.genre : null,
    suggested_year: (!row.year && match.year) ? match.year : null,
    change_reason: resolveExactMatchReason(ctx, match, hasMetaDiff),
  };
}

export function trySecondaryExactMatch(ctx, row, canonicalIndex) {
  if (!ctx.effectiveArtist || !ctx.effectiveTitle) return null;
  const sec = canonicalIndex.lookupSecondaryExact(ctx.effectiveArtist, ctx.effectiveTitle);
  if (!sec) return null;
  return buildWorkSuggestion(row, sec.track, {
    provider: ctx.explicitProvider || null,
    discCode: ctx.detectedDisc || null,
    confidence: sec.confidence,
    reason: sec.reason,
  });
}

export function tryShiftedFieldsMatch(ctx, row, canonicalIndex) {
  if (ctx.effectiveTitle || !ctx.originalDisc || !ctx.effectiveArtist) return null;
  const shiftedMatch = canonicalIndex.lookupBaseExact(ctx.originalDisc, ctx.effectiveArtist)
    || canonicalIndex.lookupBaseExact(ctx.effectiveArtist, ctx.originalDisc);
  if (!shiftedMatch) return null;
  return {
    ...buildWorkSuggestion(row, shiftedMatch, {
      provider: resolveProvider('', row.brand || row.source),
      discCode: null,
      reason: 'Repaired shifted disc/artist columns',
    }),
    clear_disc: true,
  };
}

export function tryReversedMatch(ctx, row, canonicalIndex) {
  if (!ctx.effectiveArtist || !ctx.effectiveTitle) return null;
  const reversedMatch = canonicalIndex.lookupBaseExact(ctx.effectiveTitle, ctx.effectiveArtist);
  if (!reversedMatch) return null;
  return buildWorkSuggestion(row, reversedMatch, {
    provider: ctx.explicitProvider || null,
    discCode: ctx.detectedDisc || null,
    reason: 'Swapped reversed artist and title',
  });
}

export function tryRawTokenMatch(ctx, row, canonicalIndex, fuzzyCache) {
  const combinedFields = [ctx.effectiveArtist, ctx.effectiveTitle].filter(Boolean).join(' - ').trim();
  let rawMatch = combinedFields
    ? withFuzzyCache(fuzzyCache, `raw:::${combinedFields}`, () => canonicalIndex.matchRawTokens(combinedFields))
    : null;

  if (!rawMatch && ctx.filenameStem && ctx.filenameStem !== combinedFields) {
    rawMatch = withFuzzyCache(
      fuzzyCache,
      `raw:::${ctx.filenameStem}`,
      () => canonicalIndex.matchRawTokens(ctx.filenameStem),
    );
  }
  if (!rawMatch) return null;

  const stemDisc = (!ctx.detectedDisc && ctx.filenameStem)
    ? extractLeadingResidualDisc(ctx.filenameStem, rawMatch.matchedSet)
    : '';
  const finalDisc = ctx.detectedDisc || stemDisc || rawMatch.rescuedDisc || null;
  const finalProvider = ctx.explicitProvider || (finalDisc ? resolveProvider(finalDisc, row.brand || row.source) : null);

  return buildWorkSuggestion(row, rawMatch.track, {
    provider: finalProvider || null,
    discCode: finalDisc,
    confidence: rawMatch.confidence,
    reason: rawMatch.reason,
  });
}

export function tryTypoMatch(ctx, row, canonicalIndex, fuzzyCache) {
  if (!ctx.effectiveArtist || !ctx.effectiveTitle) return null;
  const typoBaseKey = `${ctx.effectiveArtist.toLowerCase()}:::${ctx.effectiveTitle.toLowerCase()}`;

  const titleMatch = withFuzzyCache(
    fuzzyCache,
    `ttypo:::${typoBaseKey}`,
    () => canonicalIndex.matchTitleTypo(ctx.effectiveArtist, ctx.effectiveTitle),
  );
  if (titleMatch) {
    return buildWorkSuggestion(row, titleMatch.track, {
      provider: ctx.explicitProvider || null,
      discCode: ctx.detectedDisc || null,
      confidence: titleMatch.confidence,
      reason: titleMatch.reason,
    });
  }

  const artistMatch = withFuzzyCache(
    fuzzyCache,
    `atypo:::${typoBaseKey}`,
    () => canonicalIndex.matchArtistTypo(ctx.effectiveArtist, ctx.effectiveTitle),
  );
  if (artistMatch) {
    return buildWorkSuggestion(row, artistMatch.track, {
      provider: ctx.explicitProvider || null,
      discCode: ctx.detectedDisc || null,
      confidence: artistMatch.confidence,
      reason: artistMatch.reason,
    });
  }

  return null;
}

export function buildCustomTrack(ctx, row) {
  return {
    status: 'custom',
    canonical_id: null,
    is_custom: true,
    confidence: 0,
    provider: ctx.explicitProvider || null,
    disc_code: ctx.detectedDisc || null,
    provider_matched: Boolean(ctx.explicitProvider),
    suggested_artist: ctx.originalArtist,
    suggested_title: ctx.originalTitle,
    genre: row.genre || '',
    year: row.year || null,
    change_reason: null,
  };
}
