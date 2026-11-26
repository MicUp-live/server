/**
 * Shared regex source for recognized karaoke manufacturer disc/track codes.
 * Supports standard codes (SC8101-02), letter-suffixed discs (MM6380B-01),
 * and multipart disc-track codes (CBEP457-3-08).
 */
export const KNOWN_DISC_CODE_SOURCE =
    '(?:(?:CBEP|CBE|CB|SCTP|NSC)\\d{1,6}[A-Z]?(?:[-_]\\d{1,3}){0,2}|(?:(?:VP|RSQ)?SC(?:VP|PC|GPX|RSS)?|CRK|VPS|SAV|SDK|SYB|SF|PHM|THM|TH|MM|PC|PS|DK|PT|PH|PY|LG|ZM|KV)\\d{1,6}[A-Z0-9]*(?:[-_]\\d{1,3})?|(?:SCDEMO|SCFPI)[-_]\\d{1,3}|KFN[-_]?\\d{2,6})'

const DISC_PROVIDER_RULES = [
    [/^((?:N|VP|RSQ)?SC(?:TP|VP|PC|GPX|RSS)?|CRK|VPS|SAV|SDK)\d|^(SCDEMO|SCFPI)[-_]\d/, 'Sound Choice'],
    [/^SYB\d/, 'Sunfly Sybersound'],
    [/^SF\d/, 'Sunfly'],
    [/^(CBEP|CBE|CB)\d/, 'Chartbuster'],
    [/^PHM\d/, 'Pop Hits Monthly'],
    [/^THM?\d/, 'Top Hits Monthly'],
    [/^MM\d/, 'Music Maestro'],
    [/^P[CS]\d/, 'Pocket Songs'],
    [/^DK\d/, 'DKKaraoke'],
    [/^P[THY]\d/, 'Party Tyme'],
    [/^LG\d/, 'Legends'],
    [/^KFN[-_]?\d/, 'KaraFun'],
    [/^ZM\d/, 'Zoom Karaoke'],
    [/^KV\d/, 'Karaoke Version'],
]

function matchNamedProvider(text) {
    if (!text) return null
    if (text.includes('SOUND CHOICE')) return 'Sound Choice'
    if (text.includes('SUNFLY SYBERSOUND')) return 'Sunfly Sybersound'
    if (text.includes('SUNFLY')) return 'Sunfly'
    if (text.includes('CHARTBUSTER')) return 'Chartbuster'
    if (text.includes('POP HITS MONTHLY')) return 'Pop Hits Monthly'
    if (text.includes('TOP HITS MONTHLY')) return 'Top Hits Monthly'
    if (text.includes('MUSIC MAESTRO')) return 'Music Maestro'
    if (text.includes('POCKET SONGS')) return 'Pocket Songs'
    if (text.includes('DKKARAOKE')) return 'DKKaraoke'
    if (text.includes('PARTY TYME')) return 'Party Tyme'
    if (text.includes('LEGENDS')) return 'Legends'
    if (text.includes('KARAFUN')) return 'KaraFun'
    if (text.includes('KARAOKE VERSION')) return 'Karaoke Version'
    if (text.includes('ZOOM')) return 'Zoom Karaoke'
    return null
}

/**
 * Identifies known karaoke backing-track publishers/manufacturers from
 * explicit brand/source tags first, then authoritative disc-code prefixes.
 * Never guesses a publisher from song title or canonical ID alone.
 */
export function resolveProvider(discCode, brandTag) {
    const brand = (brandTag || '').trim().toUpperCase()
    const fromBrand = matchNamedProvider(brand)
    if (fromBrand) return fromBrand

    const code = (discCode || '').trim().toUpperCase()
    if (!code) return null

    const fromCodeName = matchNamedProvider(code)
    if (fromCodeName) return fromCodeName

    for (const [pattern, provider] of DISC_PROVIDER_RULES) {
        if (pattern.test(code)) return provider
    }
    return null
}

const EMBEDDED_DISC_CODE_RE = new RegExp(
    `(?:^|[\\s\\-_(\\[/\\\\])(${KNOWN_DISC_CODE_SOURCE})(?=$|[\\s\\-_)\\]/\\\\.,])`,
    'i'
)

/**
 * Extracts a recognized karaoke manufacturer disc code from a filename or
 * concatenated metadata string (e.g. "[SC8101-02]", "CBEP457-3-08 - Queen - ...").
 * Returns null if no authoritative manufacturer disc code is present.
 */
export function extractDiscCode(text) {
    if (!text) return null
    const match = String(text).match(EMBEDDED_DISC_CODE_RE)
    return match ? match[1].toUpperCase() : null
}

/**
 * Returns the publisher/provider display name for a catalog track.
 * Explicit provider/brand/source always takes precedence over disc-code inference.
 */
export function resolveTrackProvider(song) {
    if (!song) return ''
    const explicit = (song.provider || song.brand || song.source || '').trim()
    if (explicit) {
        return resolveProvider('', explicit) || explicit
    }
    const disc = (song.disc || song.disc_code || song.disc_id || '').trim()
    return resolveProvider(disc, '') || ''
}
