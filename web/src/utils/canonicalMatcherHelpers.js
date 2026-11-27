import {
    normalizeTitle,
    boundedLevenshtein,
} from './songNormalize.js'

const NOISE_WORDS = new Set([
    'the', 'a', 'an', 'and', 'ft', 'feat', 'featuring', 'with', 'vs',
    'karaoke', 'cdg', 'mp3', 'zip', 'mp4', 'wav', 'm4a', 'kfn', 'wvocal',
])
const VARIANT_WORDS = new Set([
    'live', 'acoustic', 'remix', 'medley', 'duet', 'duo', 'multiplex',
    'spanish', 'french', 'german', 'italian', 'portuguese', 'reprise',
    'unplugged', 'parody', 'cover', 'tribute', 'remake', 'extended',
    'acapella', 'club', 'mashup', 'megamix', 'part', 'pt',
])
const GENERIC_DISC_EXACT = new Set(['mp3', 'mp4', 'm4a'])
const GENERIC_DISC_PREFIXES = new Set([
    'cd', 'disc', 'disk', 'track', 'trk', 'vol', 'side', 'part', 'pt', 'no', 'top',
])
const MAX_SEED_DF = 1500
const MAX_SEED_TOKENS = 8

export function trackRichness(t) {
    return (t.genre ? 2 : 0) + (t.year ? 1 : 0) + (/^the\s+/i.test(t.artist || '') ? 0.5 : 0)
}

function coreSignature(aTokens, tTokens) {
    return `${aTokens.join(' ')}:::${tTokens.join(' ')}`
}

function isDiscLikeToken(tok) {
    if (!/^[a-z]{1,6}\d{2,6}[a-z0-9]*$/i.test(tok)) return false
    const lower = tok.toLowerCase()
    if (GENERIC_DISC_EXACT.has(lower)) return false
    const match = /^([a-z]+)\d+$/.exec(lower)
    return !match || !GENERIC_DISC_PREFIXES.has(match[1])
}

function hasContiguousSpan(qTokens, targetTokens) {
    if (targetTokens.length === 0) return false
    if (targetTokens.length === 1) return qTokens.includes(targetTokens[0])
    const first = targetTokens[0]
    for (let i = 0; i < qTokens.length; i++) {
        if (qTokens[i] !== first) continue
        if (matchesSpanAt(qTokens, targetTokens, i)) return true
    }
    return false
}

function matchesSpanAt(qTokens, targetTokens, startIdx) {
    let qi = startIdx + 1
    let ti = 1
    let skipped = 0
    while (ti < targetTokens.length && qi < qTokens.length && skipped <= 2) {
        if (qTokens[qi] === targetTokens[ti]) ti++
        else skipped++
        qi++
    }
    return ti === targetTokens.length
}

function setIfRicher(map, key, track) {
    const existing = map.get(key)
    if (!existing || trackRichness(track) > trackRichness(existing)) {
        map.set(key, track)
    }
}

export function indexSecondaryMaps(index, origKey, track, normA, normT, deps) {
    const { stripLeadingThe, extractPrimaryArtist } = deps
    const expKey = `${normA}:::${normT}`
    if (expKey !== origKey) {
        setIfRicher(index.expandedMap, expKey, track)
    }

    const noTheKey = `${stripLeadingThe(normA)}:::${stripLeadingThe(normT)}`
    if (noTheKey !== expKey) {
        setIfRicher(index.noTheMap, noTheKey, track)
    }

    const primA = stripLeadingThe(extractPrimaryArtist(track.artist))
    if (primA && primA !== stripLeadingThe(normA) && normT) {
        setIfRicher(index.primaryArtistMap, `${primA}:::${normT}`, track)
    }
}

export function recordTrackTokens(tokenCounts, aTokens, tTokens) {
    if (aTokens.length === 0 || tTokens.length === 0) return
    const seen = new Set(aTokens)
    for (const tok of tTokens) seen.add(tok)
    for (const tok of seen) {
        tokenCounts.set(tok, (tokenCounts.get(tok) || 0) + 1)
    }
}

export function registerArtistTrack(index, normA, tid, stripLeadingThe) {
    const artistKey = stripLeadingThe(normA)
    if (!artistKey) return
    let aid = index.artistToId.get(artistKey)
    if (aid === undefined) {
        aid = index.artistNames.length
        index.artistToId.set(artistKey, aid)
        index.artistNames.push(artistKey)
        index.artistTrackIds.push([])
    }
    index.artistTrackIds[aid].push(tid)
}

function writeTrackPostings(index, writeCursor, tid) {
    const aTokens = index.artistCoreTokens[tid]
    const tTokens = index.titleCoreTokens[tid]
    if (aTokens.length === 0 || tTokens.length === 0) return
    const seen = new Set(aTokens)
    for (const tok of tTokens) seen.add(tok)
    for (const tok of seen) {
        const pos = writeCursor.get(tok)
        index.postingsBuf[pos] = tid
        writeCursor.set(tok, pos + 1)
    }
}

export function buildPostingsBuffer(index, tokenCounts, n) {
    let totalPostings = 0
    for (const [tok, count] of tokenCounts.entries()) {
        index.tokenOffsets.set(tok, totalPostings)
        index.tokenLengths.set(tok, count)
        totalPostings += count
    }
    index.postingsBuf = new Uint32Array(totalPostings)
    const writeCursor = new Map(index.tokenOffsets)

    for (let tid = 0; tid < n; tid++) {
        writeTrackPostings(index, writeCursor, tid)
    }
}

function addArtistDeleteEntry(artistDeleteIndex, key, aid) {
    const cur = artistDeleteIndex.get(key)
    if (cur === undefined) {
        artistDeleteIndex.set(key, aid)
        return
    }
    if (typeof cur === 'number') {
        if (cur !== aid) artistDeleteIndex.set(key, [cur, aid])
        return
    }
    if (cur.length < 24 && !cur.includes(aid)) {
        cur.push(aid)
    }
}

export function buildArtistDeleteIndex(artistNames, artistDeleteIndex) {
    for (let aid = 0; aid < artistNames.length; aid++) {
        const compact = artistNames[aid].replace(/\s+/g, '')
        if (compact.length < 4 || compact.length > 32) continue
        addArtistDeleteEntry(artistDeleteIndex, compact, aid)
        for (let i = 0; i < compact.length; i++) {
            addArtistDeleteEntry(artistDeleteIndex, compact.slice(0, i) + compact.slice(i + 1), aid)
        }
    }
}

export function countQueryTokens(qTokens) {
    const counts = new Map()
    for (const tok of qTokens) {
        counts.set(tok, (counts.get(tok) || 0) + 1)
    }
    return counts
}

export function collectSeedTokens(qUnique, tokenLengths) {
    const seedable = []
    for (const tok of qUnique) {
        const df = tokenLengths.get(tok)
        if (df !== undefined && df > 0 && df <= MAX_SEED_DF) {
            seedable.push({ tok, df })
        }
    }
    seedable.sort((a, b) => a.df - b.df)
    return seedable
}

export function collectCandidateTids(index, seedable) {
    index.querySeq++
    if (index.querySeq === 0xffffffff) {
        index.stamp.fill(0)
        index.querySeq = 1
    }
    const seq = index.querySeq
    let touchedCount = 0

    const seedLimit = Math.min(seedable.length, MAX_SEED_TOKENS)
    for (let s = 0; s < seedLimit; s++) {
        const tok = seedable[s].tok
        const offset = index.tokenOffsets.get(tok)
        const end = offset + index.tokenLengths.get(tok)
        for (let p = offset; p < end; p++) {
            const tid = index.postingsBuf[p]
            if (index.stamp[tid] !== seq) {
                index.stamp[tid] = seq
                index.touched[touchedCount++] = tid
            }
        }
    }
    return touchedCount
}

function hasAllTokens(tokens, tokenSet) {
    for (const tok of tokens) {
        if (!tokenSet.has(tok)) return false
    }
    return true
}

function countResidualTokens(qUnique, matchedSet) {
    let rVar = 0
    let rExtra = 0
    for (const tok of qUnique) {
        if (matchedSet.has(tok)) continue
        if (NOISE_WORDS.has(tok) || isDiscLikeToken(tok) || /^\d{1,2}$/.test(tok) || /^\d{5,8}$/.test(tok)) {
            continue
        }
        if (VARIANT_WORDS.has(tok)) rVar++
        else rExtra++
    }
    return { rVar, rExtra }
}

function computeIdfSum(matchedSet, tokenLengths, totalDocs) {
    let idfSum = 0
    for (const tok of matchedSet) {
        const df = tokenLengths.get(tok) || 1
        idfSum += Math.log(1 + totalDocs / df)
    }
    return idfSum
}

function scoreRawCandidate(index, tid, queryCtx) {
    const { qTokens, qUnique, qSet, qTokenCounts, totalDocs } = queryCtx
    const aLen = index.artistCoreLen[tid]
    const tLen = index.titleCoreLen[tid]
    if (aLen === 0 || tLen === 0 || aLen > qUnique.length || tLen > qUnique.length) return null

    const aTokens = index.artistCoreTokens[tid]
    const tTokens = index.titleCoreTokens[tid]
    if (!hasAllTokens(aTokens, qSet) || !hasAllTokens(tTokens, qSet)) return null

    const matchedSet = new Set(aTokens)
    for (const tok of tTokens) matchedSet.add(tok)

    // Self-titled 1-word guard: if artist and title share a single token (e.g. "Hair - Hair"),
    // require that token to appear at least twice in qTokens.
    if (matchedSet.size < 2 && (qTokenCounts.get(aTokens[0]) || 0) < 2) return null

    const { rVar, rExtra } = countResidualTokens(qUnique, matchedSet)
    const totalCore = aLen + tLen
    if (rVar > 0 || (totalCore <= 3 && rExtra > 0) || rExtra > 1) return null

    const idfSum = computeIdfSum(matchedSet, index.tokenLengths, totalDocs)
    const artistContig = hasContiguousSpan(qTokens, aTokens)
    const titleContig = hasContiguousSpan(qTokens, tTokens)
    const spanBonus = (artistContig ? 1.0 : 0) + (titleContig ? 1.0 : 0)
    const track = index.tracks[tid]
    const score = idfSum + (matchedSet.size * 0.6) + spanBonus - (rExtra * 2.2) + (trackRichness(track) * 0.05)

    return {
        track,
        sig: coreSignature(aTokens, tTokens),
        rExtra,
        totalCore,
        contiguous: artistContig && titleContig,
        matchedSet,
        score,
    }
}

function recordScoredCandidate(state, cand) {
    if (cand.score > state.bestScore) {
        if (state.best && state.best.sig !== cand.sig) {
            state.secondScore = state.bestScore
            state.secondSig = state.best.sig
        }
        state.bestScore = cand.score
        state.best = cand
    } else if (state.best && cand.sig !== state.best.sig && cand.score > state.secondScore) {
        state.secondScore = cand.score
        state.secondSig = cand.sig
    }
}

export function pickBestRawCandidate(index, touchedCount, queryCtx) {
    const state = {
        best: null,
        bestScore: -Infinity,
        secondScore: -Infinity,
        secondSig: '',
    }

    for (let i = 0; i < touchedCount; i++) {
        const cand = scoreRawCandidate(index, index.touched[i], queryCtx)
        if (cand) recordScoredCandidate(state, cand)
    }

    if (!state.best) return null
    if (state.secondSig && (state.bestScore - state.secondScore) < 0.25) return null
    return state.best
}

function addDeleteCandidates(artistDeleteIndex, key, candAids) {
    const val = artistDeleteIndex.get(key)
    if (val === undefined) return
    if (typeof val === 'number') {
        candAids.add(val)
        return
    }
    for (const aid of val) candAids.add(aid)
}

export function collectArtistCandidates(artistDeleteIndex, compact) {
    const candAids = new Set()
    addDeleteCandidates(artistDeleteIndex, compact, candAids)
    for (let i = 0; i < compact.length; i++) {
        addDeleteCandidates(artistDeleteIndex, compact.slice(0, i) + compact.slice(i + 1), candAids)
    }
    return candAids
}

function findRichestTrackWithTitle(tracks, tids, normT) {
    let richest = null
    for (const tid of tids) {
        const t = tracks[tid]
        if (normalizeTitle(t.title) !== normT) continue
        if (!richest || trackRichness(t) > trackRichness(richest)) {
            richest = t
        }
    }
    return richest
}

export function findBestArtistTypoMatch(index, candAids, compact, normT) {
    const maxArtistDist = compact.length <= 6 ? 1 : 2
    let bestTrack = null
    let bestAid = -1
    let bestDist = maxArtistDist + 1
    let tied = false

    for (const aid of candAids) {
        const candCompact = index.artistNames[aid].replace(/\s+/g, '')
        const dist = boundedLevenshtein(compact, candCompact, maxArtistDist)
        if (dist === 0 || dist > maxArtistDist) continue

        const richestForAid = findRichestTrackWithTitle(index.tracks, index.artistTrackIds[aid], normT)
        if (!richestForAid) continue

        if (dist < bestDist) {
            bestDist = dist
            bestTrack = richestForAid
            bestAid = aid
            tied = false
        } else if (dist === bestDist && bestAid !== aid) {
            tied = true
        }
    }

    if (!bestTrack || tied) return null
    return { bestTrack, bestDist }
}

export function findBestTitleTypoMatch(tracks, tids, normT) {
    let bestDist = 3
    let bestTrack = null
    let bestNormTitle = ''
    let tied = false

    for (const tid of tids) {
        const t = tracks[tid]
        const candNormTitle = normalizeTitle(t.title)
        const dist = boundedLevenshtein(normT, candNormTitle, 2)
        if (dist === 0 || dist > 2 || dist > bestDist) continue

        if (dist < bestDist) {
            bestDist = dist
            bestTrack = t
            bestNormTitle = candNormTitle
            tied = false
        } else if (candNormTitle !== bestNormTitle) {
            tied = true
        } else if (trackRichness(t) > trackRichness(bestTrack)) {
            bestTrack = t
        }
    }

    if (tied || bestDist > 2 || !bestTrack) return null
    return { bestTrack, bestDist }
}
