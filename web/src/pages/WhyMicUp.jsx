import { useEffect } from 'preact/hooks'
import { BrandIcon } from '../components/BrandIcon'
import './WhyMicUp.css'

const CAPABILITIES = [
    {
        number: '01',
        eyebrow: 'Your catalog',
        title: 'A songbook that stays in sync',
        intro: 'MicUp turns the library you already own into a fast, searchable songbook without making you maintain a second catalog by hand.',
        features: [
            'Import folders and exports from VirtualDJ (database.xml), PCDJ Karaoki, SongbookDB, OpenKJ, MTU Hoster, CSV, and TSV.',
            'Catch duplicate tracks, swapped artist and title columns, common typos, and noisy karaoke tags before publishing.',
            'Match manufacturer disc codes while keeping custom and unmatched tracks exactly as you entered them.',
            'Choose additive imports to merge new songs into your catalog, or replacement imports to publish an updated library while preserving active queues and singer history.',
            'Search libraries of 50,000+ tracks directly in the browser once loaded, with duplicate versions collapsed into clean song cards.',
        ],
    },
    {
        number: '02',
        eyebrow: 'Your show',
        title: 'The room, under control',
        intro: 'Songbooks are reusable. Shows are separate. Run recurring nights at different venues without duplicating your library or mixing up their queues.',
        features: [
            'Launch a show with a memorable code and share it by link or QR code.',
            'Choose Fair Priority, Classic Round Robin, or First-Come, First-Served rotation, with full manual bump and reordering controls.',
            'Review requests in staging or send them directly into the live queue.',
            'Reorder singers, call someone to the stage, mark them up next, complete a turn, or record a decline reason.',
            'Pause and resume requests, set submission cooldowns, cap active songs per singer, control key-change requests, and optionally accept write-in requests with 1-click YouTube lookup.',
            'Share a songbook with fellow verified hosts using separate Edit catalog and Host shows permissions so multiple KJs can run independent shows from one private or public library while the owner stays in control.',
            'Restart recurring events with a clean queue while preserving the show setup.',
        ],
    },
    {
        number: '03',
        eyebrow: 'Your singers',
        title: 'No download. No account wall.',
        intro: 'A singer scans, searches, and requests. MicUp keeps useful preferences on their own device, so the first interaction is not a registration form.',
        features: [
            'Browse by song, artist, or favorites with fuzzy search and smooth large-list scrolling.',
            'Remember favorite songs, favorite artists, and preferred pitch changes across MicUp venues in the same browser, with no account required.',
            'Track queue position with in-app alerts and optional browser notifications when on deck or called to the stage while the page is visible and connected.',
            'Request a screen wake lock on supported mobile browsers to help keep the queue visible during the show.',
            'Withdraw a request securely, review songs sung tonight, and request a favorite again in one tap.',
        ],
    },
    {
        number: '04',
        eyebrow: 'Your venue',
        title: 'Every screen has a job',
        intro: 'The host console does not have to double as the customer display. MicUp gives each device a view designed for where it lives in the room.',
        features: [
            'A fullscreen TV Stage view with a large QR code, now-playing and up-next states, and ticker announcements.',
            'A PIN-protected walk-up kiosk for an iPad or tablet, with an automatic inactivity reset between singers.',
            'A private KJ command center for requests, rotation, event settings, and show history.',
            'A mobile songbook that keeps local file paths and private host metadata out of public responses.',
        ],
    },
]

const DETAILS = [
    {
        title: 'Works with your playback setup',
        body: 'MicUp is the catalog, request, and live-stage layer—not another player you have to migrate into. Connect OpenKJ privately to upload your catalog and pull requests, or use the documented REST API with your own tools and scripts.',
        link: '/docs#openkj',
        linkLabel: 'See OpenKJ setup',
    },
    {
        title: 'Built for unreliable venue conditions',
        body: 'Songbook search stays fast once loaded even when venue Wi-Fi fluctuates. Live screens reconnect and catch up when a device wakes, while large catalog imports process reliably in the background once uploaded and accepted.',
        link: '/docs#faq',
        linkLabel: 'Read the venue FAQ',
    },
    {
        title: 'Private where it matters',
        body: 'Host accounts support passkeys or password sign-in. Connections can be revoked, every queue stays isolated to its show, and private media locations never appear in the public songbook.',
        link: '/docs#developer-api',
        linkLabel: 'Explore the developer API',
    },
]

export function WhyMicUp() {
    useEffect(() => {
        const previousTitle = document.title
        document.title = 'Why MicUp.live? | Karaoke songbooks and live show control'
        return () => {
            document.title = previousTitle
        }
    }, [])

    return (
        <div class="why-page">
            <section class="why-hero" aria-labelledby="why-title">
                <div class="why-hero-mark" aria-hidden="true">
                    <BrandIcon size={56} glowing />
                </div>
                <p class="why-kicker">Why MicUp.live?</p>
                <h1 id="why-title">Your library. Your rotation.<br />A better night for everyone.</h1>
                <p class="why-hero-copy">
                    MicUp is a professional songbook and live-show platform for karaoke hosts who already
                    own their music and care about running a fair, polished room.
                </p>
                <div class="why-hero-actions">
                    <a class="btn btn-primary" href="/login">Set up a host account</a>
                    <a class="why-text-link" href="/docs">Read the field manual <span aria-hidden="true">→</span></a>
                </div>
            </section>

            <section class="why-position" aria-label="What makes MicUp different">
                <p class="why-position-lead">More than a request form</p>
                <p>
                    Plenty of tools can collect a singer's name and song. MicUp handles the work around that
                    moment: cleaning and publishing the catalog, protecting a fair rotation, keeping singers
                    informed, coordinating the screens in the venue, and connecting the show to the software
                    on the KJ's laptop.
                </p>
            </section>

            <div class="why-capabilities">
                {CAPABILITIES.map((capability) => (
                    <section class="why-capability" key={capability.number}>
                        <div class="why-capability-heading">
                            <span class="why-number" aria-hidden="true">{capability.number}</span>
                            <div>
                                <p class="why-eyebrow">{capability.eyebrow}</p>
                                <h2>{capability.title}</h2>
                                <p class="why-capability-intro">{capability.intro}</p>
                            </div>
                        </div>
                        <ul class="why-feature-list">
                            {capability.features.map((feature) => (
                                <li key={feature}>{feature}</li>
                            ))}
                        </ul>
                    </section>
                ))}
            </div>

            <section class="why-details" aria-labelledby="why-foundation-title">
                <div class="why-details-heading">
                    <p class="why-eyebrow">Under the surface</p>
                    <h2 id="why-foundation-title">Made for the way working KJs operate</h2>
                </div>
                <div class="why-detail-grid">
                    {DETAILS.map((detail) => (
                        <article class="why-detail" key={detail.title}>
                            <h3>{detail.title}</h3>
                            <p>{detail.body}</p>
                            <a href={detail.link}>{detail.linkLabel} <span aria-hidden="true">→</span></a>
                        </article>
                    ))}
                </div>
            </section>

            <section class="why-honest" aria-labelledby="why-is-title">
                <div>
                    <p class="why-eyebrow">One clear boundary</p>
                    <h2 id="why-is-title">MicUp runs the show around the song.</h2>
                </div>
                <p>
                    It does not replace your karaoke playback engine or supply licensed backing tracks.
                    Keep the player and library you trust; use MicUp to publish that library, receive requests,
                    manage the room, and give singers a consistent experience.
                </p>
            </section>

            <section class="why-closing" aria-labelledby="why-closing-title">
                <p class="why-kicker">Ready when the room is</p>
                <h2 id="why-closing-title">The mic is hot. The room is ready.</h2>
                <div class="why-hero-actions">
                    <a class="btn btn-primary" href="/login">Get started</a>
                    <a class="btn btn-secondary" href="/docs">Browse documentation</a>
                </div>
            </section>
        </div>
    )
}
