import { useState, useEffect } from 'preact/hooks'
import { showToast } from '../components/Toast'
import { DEVELOPER_ENDPOINTS, CODE_SAMPLES } from './developerApiData'
import { UploadHeadersGuide } from './UploadHeadersGuide'
import './Docs.css'

const SECTIONS = [
    { id: 'quickstart', title: 'Quick Start', icon: '🚀', group: 'Getting Started' },
    { id: 'kj-guide', title: 'KJ Host & Live Queue', icon: '🎤', group: 'Host Operations' },
    { id: 'catalogs', title: 'Songbooks & Imports', icon: '📁', group: 'Host Operations' },
    { id: 'collaboration', title: 'Songbook Collaboration', icon: '👥', group: 'Host Operations' },
    { id: 'openkj', title: 'OpenKJ Integration', icon: '📡', group: 'Software Sync' },
    { id: 'virtualdj', title: 'VirtualDJ Import', icon: '🎛️', group: 'Software Sync' },
    { id: 'developer-api', title: 'Developer API & Keys', icon: '⚡', group: 'Software Sync' },
    { id: 'stage-kiosk', title: 'Stage Display & Kiosk', icon: '📺', group: 'Venue Hardware' },
    { id: 'singers', title: 'Singer Experience', icon: '🎵', group: 'Singer Features' },
    { id: 'faq', title: 'Troubleshooting & FAQ', icon: '❓', group: 'Support' },
]

export function Docs({ section }) {
    const initialSection = section && SECTIONS.some((s) => s.id === section) ? section : 'quickstart'
    const [activeSection, setActiveSection] = useState(initialSection)
    const [searchQuery, setSearchQuery] = useState('')
    const [copiedId, setCopiedId] = useState(null)
    const [codeLang, setCodeLang] = useState('curl')

    useEffect(() => {
        // Sync with :section path param or window hash if present
        const hash = typeof window !== 'undefined' ? window.location.hash.replace('#', '') : ''
        const target = (section && SECTIONS.some((s) => s.id === section))
            ? section
            : (hash && SECTIONS.some((s) => s.id === hash) ? hash : '')
        if (target) {
            setActiveSection(target)
            const el = document.getElementById(target)
            if (el) {
                el.scrollIntoView({ behavior: 'smooth' })
            }
        }
    }, [section])

    const handleSelectSection = (id) => {
        setActiveSection(id)
        if (typeof window !== 'undefined') {
            window.history.replaceState(null, '', `/docs/${id}`)
        }
        const el = document.getElementById(id)
        if (el) {
            el.scrollIntoView({ behavior: 'smooth' })
        }
    }

    const copyToClipboard = (text, id) => {
        if (navigator.clipboard) {
            navigator.clipboard.writeText(text).then(() => {
                setCopiedId(id)
                showToast('Copied to clipboard! 📋')
                setTimeout(() => setCopiedId(null), 2000)
            })
        }
    }

    const filteredSections = searchQuery.trim()
        ? SECTIONS.filter(
            (s) =>
                s.title.toLowerCase().includes(searchQuery.toLowerCase()) ||
                s.group.toLowerCase().includes(searchQuery.toLowerCase())
        )
        : SECTIONS

    const groups = [...new Set(filteredSections.map((s) => s.group))]

    return (
        <div class="docs-container">
            {/* Sidebar */}
            <aside class="docs-sidebar">
                <div class="docs-search-box">
                    <span class="docs-search-icon">🔍</span>
                    <input
                        type="text"
                        class="docs-search-input"
                        placeholder="Search documentation..."
                        value={searchQuery}
                        onInput={(e) => setSearchQuery(e.target.value)}
                        aria-label="Search docs"
                    />
                </div>

                <nav class="docs-nav">
                    {groups.map((grp) => (
                        <div key={grp} class="docs-nav-group">
                            <div class="docs-nav-heading">{grp}</div>
                            {filteredSections
                                .filter((s) => s.group === grp)
                                .map((section) => (
                                    <button
                                        key={section.id}
                                        type="button"
                                        class={`docs-nav-item ${activeSection === section.id ? 'active' : ''}`}
                                        onClick={() => handleSelectSection(section.id)}
                                    >
                                        <span class="docs-nav-item-icon">{section.icon}</span>
                                        <span>{section.title}</span>
                                    </button>
                                ))}
                        </div>
                    ))}
                </nav>
            </aside>

            {/* Main Content Area */}
            <div class="docs-main">
                <div class="docs-header-badge">
                    <span>📖</span>
                    <span>MicUp.live Documentation & Field Manual</span>
                </div>
                <h1 class="docs-title">MicUp.live Documentation</h1>
                <p class="docs-lead">
                    Everything you need to master MicUp: catalog imports, live stage rotation, OpenKJ sync, TV stage displays, and zero-app singer requests.
                </p>

                {/* Section 1: Quick Start */}
                <section id="quickstart" class="docs-section">
                    <h2 class="docs-section-title">
                        <span>🚀</span>
                        <span>Quick Start Guide</span>
                    </h2>
                    <p>
                        MicUp.live turns messy karaoke binders and spreadsheets into an instant, searchable mobile songbook that singers access directly on their phones.
                    </p>

                    <div class="docs-grid">
                        <div class="docs-card">
                            <span class="docs-card-icon">🎤</span>
                            <div class="docs-card-title">For Karaoke Hosts (KJs)</div>
                            <p class="docs-card-desc">
                                Create an account with a Biometric Passkey or password, upload your song library from <code>.csv</code>, <code>.tsv</code>, VirtualDJ <code>database.xml</code>, or a local folder, and launch your show with a custom Show Code.
                            </p>
                        </div>
                        <div class="docs-card">
                            <span class="docs-card-icon">📱</span>
                            <div class="docs-card-title">For Singers (Zero-App)</div>
                            <p class="docs-card-desc">
                                Singers scan the TV screen QR code or enter the Show Code (e.g. <code>OMALLEY</code>). No app download and no login wall are required to search and submit requests.
                            </p>
                        </div>
                    </div>

                    <div class="docs-steps">
                        <div class="docs-step-card">
                            <div class="docs-step-number">1</div>
                            <div class="docs-step-body">
                                <h4>Create Your Host Account</h4>
                                <p>Head to <a href="/login" style={{ color: 'var(--color-accent)' }}>Sign In / Register</a>. MicUp supports 1-tap Biometric Passkeys (Face ID, Touch ID, Windows Hello) and standard email/password authentication.</p>
                            </div>
                        </div>
                        <div class="docs-step-card">
                            <div class="docs-step-number">2</div>
                            <div class="docs-step-body">
                                <h4>Import Your Songbook Catalog</h4>
                                <p>Navigate to <strong>Songbooks</strong> and drag &amp; drop your catalog file (<code>.csv</code>, <code>.tsv</code>, <code>.txt</code>, VirtualDJ <code>database.xml</code>, or a local karaoke folder). Your catalog is deduplicated and indexed in seconds.</p>
                            </div>
                        </div>
                        <div class="docs-step-card">
                            <div class="docs-step-number">3</div>
                            <div class="docs-step-body">
                                <h4>Launch Show &amp; TV Stage Display</h4>
                                <p>From <strong>Songbooks</strong>, click <strong>"Start Show"</strong> on your library (or open <strong>Live Queue</strong> and click <strong>"Create Show"</strong>). The <strong>"Create a Karaoke Show"</strong> modal opens with your songbook preselected. Set your show title, venue, and memorable Show Code (e.g. <code>THEPUB</code>), pick your rotation style, and click <strong>"🚀 Launch Show"</strong> to enter the live queue. From there, launch the full-screen TV Stage Display on the venue screen.</p>
                            </div>
                        </div>
                    </div>

                    <div class="docs-callout tip">
                        <span class="docs-callout-icon">💡</span>
                        <div>
                            <strong>Singer Repertoire Memory:</strong> When singers star favorites or pick pitch transposition (e.g. <code>-1♭</code>), their preferences are saved in their browser's local storage. They persist across MicUp shows on the same device and browser as long as browser storage remains available, with no account creation required.
                        </div>
                    </div>
                </section>

                {/* Section 2: KJ Host & Live Queue */}
                <section id="kj-guide" class="docs-section">
                    <h2 class="docs-section-title">
                        <span>🎤</span>
                        <span>KJ Host Operations & Live Queue</span>
                    </h2>
                    <p>
                        The Live Queue Command Center (<code>/kj/queue</code>) gives you complete stage control during a live show:
                    </p>

                    <div class="docs-grid">
                        <div class="docs-card">
                            <span class="docs-card-icon">⚡</span>
                            <div class="docs-card-title">Call to Stage &amp; Stage Promotion</div>
                            <p class="docs-card-desc">
                                Promote singers to the stage with one click. When singers keep the songbook visible and connected in their browser, MicUp displays real-time stage badges, with browser notifications delivered if supported and permitted.
                            </p>

                        </div>
                        <div class="docs-card">
                            <span class="docs-card-icon">🔀</span>
                            <div class="docs-card-title">Bump & Rotation Reordering</div>
                            <p class="docs-card-desc">
                                Fairness matters. Use the <strong>Bump Up</strong> and <strong>Bump Down</strong> buttons to effortlessly adjust rotation order without losing track of waiting times.
                            </p>
                        </div>
                        <div class="docs-card">
                            <span class="docs-card-icon">🎼</span>
                            <div class="docs-card-title">Pitch Transpose Badges</div>
                            <p class="docs-card-desc">
                                Singer key requests (from <code>-3♭</code> to <code>+3♯</code>) appear highlighted in high-contrast amber tags so you can set your player's pitch slider before starting the track.
                            </p>
                        </div>
                        <div class="docs-card">
                            <span class="docs-card-icon">📜</span>
                            <div class="docs-card-title">Performance History & Sung Logs</div>
                            <p class="docs-card-desc">
                                Marking songs as sung logs them to the show's "Songs Sung Tonight" history, allowing singers to browse what's been performed with 1-click re-requests.
                            </p>
                        </div>
                        <div class="docs-card">
                            <span class="docs-card-icon">✍️</span>
                            <div class="docs-card-title">Optional Write-In Requests</div>
                            <p class="docs-card-desc">
                                Enable <strong>Allow Write-In Requests</strong> in Show Settings to let singers request songs missing from your catalog, complete with a <code>Write-In</code> badge and 1-click YouTube search link in your queue.
                            </p>
                        </div>
                    </div>

                    <div class="docs-callout note">
                        <span class="docs-callout-icon">ℹ️</span>
                        <div>
                            <strong>Pause Requests:</strong> Need a breather or reaching last call? Toggle <strong>"Pause Requests"</strong> in the queue header. Singers will still be able to browse songs and star favorites, but new request submissions are paused.
                        </div>
                    </div>

                    <h3>Queue Rotation Modes</h3>
                    <p>
                        Choose how MicUp orders singer requests in your Show Settings or when creating a new show:
                    </p>
                    <div class="docs-grid">
                        <div class="docs-card">
                            <span class="docs-card-icon">⚖️</span>
                            <div class="docs-card-title">Fair Priority (Smart Weave)</div>
                            <p class="docs-card-desc">
                                Least-recently-sung fair interleaving. Places incoming requests by active cycle and how recently each singer performed—so latecomers weave in quickly without starving regulars who have been waiting—while protecting the top 3 stage positions (On Stage, On Deck, In the Hole). Hosts can also set private VIP boost or cooldown weights (never visible to singers or on the stage TV).
                            </p>
                        </div>
                        <div class="docs-card">
                            <span class="docs-card-icon">🔄</span>
                            <div class="docs-card-title">Classic Round Robin</div>
                            <p class="docs-card-desc">
                                Distinct round tiers. When singers submit multiple tracks, songs stack cleanly into Round 1, Round 2, and Round 3. Advance rounds with one click or move singers between rounds as the night unfolds.
                            </p>
                        </div>
                        <div class="docs-card">
                            <span class="docs-card-icon">⏱️</span>
                            <div class="docs-card-title">First-Come, First-Served (FCFS)</div>
                            <p class="docs-card-desc">
                                Pure chronological order. Songs are added to the end of the line in the order received. You can adjust queue order at any time using Bump Up and Bump Down controls.
                            </p>
                        </div>
                    </div>
                </section>

                {/* Section 3: Songbooks & Imports */}
                <section id="catalogs" class="docs-section">
                    <h2 class="docs-section-title">
                        <span>📁</span>
                        <span>Songbook Catalogs & Software Imports</span>
                    </h2>
                    <p>
                        MicUp eliminates manual catalog entry with automatic software profile detection and intelligent deduplication:
                    </p>

                    <h3>Supported File Types &amp; Import Methods</h3>
                    <div class="docs-grid">
                        <div class="docs-card">
                            <div class="docs-card-title">CSV Spreadsheets (.csv)</div>
                            <p class="docs-card-desc">Comma-separated files exported from spreadsheets or karaoke managers. Common headers like <code>Artist</code>, <code>Title</code>, <code>Disc</code>, <code>Source</code>, <code>Genre</code>, and <code>Video URL</code> are auto-mapped.</p>
                        </div>
                        <div class="docs-card">
                            <div class="docs-card-title">TSV &amp; Delimited Text (.tsv / .txt)</div>
                            <p class="docs-card-desc">Tab-, semicolon-, or pipe-separated text catalogs with automatic delimiter detection and customizable column mapping.</p>
                        </div>
                        <div class="docs-card">
                            <div class="docs-card-title">VirtualDJ XML (database.xml)</div>
                            <p class="docs-card-desc">Direct import of VirtualDJ&apos;s <code>database.xml</code>. Automatically filters hybrid DJ/KJ libraries to karaoke tracks (<code>Flag=&quot;32&quot;</code>, <code>.cdg</code>, or karaoke genre).</p>
                        </div>
                        <div class="docs-card">
                            <div class="docs-card-title">Local Folder Scan</div>
                            <p class="docs-card-desc">Scans local karaoke folder file names directly in the browser using configurable naming patterns; audio and video files are never uploaded.</p>
                        </div>
                    </div>

                    <UploadHeadersGuide />

                    <h3>Additive Imports vs. Replacement Catalogs</h3>
                    <p>
                        MicUp distinguishes between additive imports and replacement catalogs:
                    </p>
                    <ul>
                        <li>
                            <strong>Additive Imports:</strong> Uploading new spreadsheet rows or files merges incoming tracks into your existing songbook, keeping previously imported tracks and custom entries part of the searchable catalog.
                        </li>
                        <li>
                            <strong>Replacement Catalogs:</strong> Uploading a full replacement export publishes only the tracks contained in the new file as the active searchable catalog. Omitted tracks are excluded from future catalog search without altering songs already submitted in active queues, performance histories, or singer favorites.
                        </li>
                        <li>
                            <strong>Deduplication:</strong> MicUp normalizes track names, cleans noisy metadata tags, and matches manufacturer disc codes while preventing duplicate entries within the published catalog.
                        </li>
                    </ul>

                    <div class="docs-callout tip">
                        <span class="docs-callout-icon">💡</span>
                        <div>
                            <strong>Client-Side Search &amp; Background Imports:</strong> MicUp search is powered by <code>MiniSearch</code> running in an in-browser Web Worker. Once the catalog downloads, search queries run locally on the singer's device without server round-trips for each keystroke. When uploading new catalogs, once the upload completes and is accepted by the server, catalog processing and indexing run independently in the background.
                        </div>
                    </div>
                </section>

                {/* Section 3b: Songbook Collaboration */}
                <section id="collaboration" class="docs-section">
                    <h2 class="docs-section-title">
                        <span>👥</span>
                        <span>Songbook Collaboration</span>
                    </h2>
                    <p>
                        Share a single master songbook (private or public) across a team of KJs without sharing passwords or mixing up live show queues. Every songbook keeps one permanent owner, and every show belongs to the host who started it.
                    </p>
                    <div class="docs-grid">
                        <div class="docs-card">
                            <div class="docs-card-title">Edit catalog</div>
                            <p class="docs-card-desc">
                                Allows a collaborator to view managed song data, add/edit/remove tracks, run imports and syncs, and use catalog-wide tools. Does not allow renaming, changing visibility, managing access, or deleting the book.
                            </p>
                        </div>
                        <div class="docs-card">
                            <div class="docs-card-title">Host shows</div>
                            <p class="docs-card-desc">
                                Allows a collaborator to start and run their own independent shows from the book (even when private) with their own join codes, queues, and OpenKJ keys. Hosting-only collaborators cannot modify the catalog.
                            </p>
                        </div>
                    </div>
                    <ul>
                        <li><strong>Email invitations:</strong> Open <strong>Manage Access</strong> on any owned songbook to send a single-use, 7-day acceptance link. Recipients must sign in with a verified Host account matching the invited email.</li>
                        <li><strong>Revocation:</strong> Removing <em>Edit catalog</em> blocks new edits immediately (an import already accepted for background processing can finish). Removing <em>Host shows</em> blocks new shows, reassignments, and restarting ended shows while allowing any currently active or paused show to finish.</li>
                        <li><strong>Owner-controlled deletion:</strong> Collaborators can never block an owner from deleting a songbook. If another host has shows linked to the book, the owner sees a confirmation prompt before ending and detaching those shows—preserving each host&apos;s event and queue history until they pick a replacement songbook.</li>
                    </ul>
                </section>

                {/* Section 4: OpenKJ Software Integration */}
                <section id="openkj" class="docs-section">
                    <h2 class="docs-section-title">
                        <span>📡</span>
                        <span>OpenKJ Software Integration</span>
                    </h2>
                    <p>
                        If you run the popular open-source <strong>OpenKJ</strong> karaoke hosting software, MicUp.live provides a native, protocol-compatible drop-in remote request backend!
                    </p>

                    <div class="docs-steps">
                        <div class="docs-step-card">
                            <div class="docs-step-number">1</div>
                            <div class="docs-step-body">
                                <h4>Find Your Venue Key in Show Settings</h4>
                                <p>Open your show at <code>/kj/queue</code>. Click <strong>Show Settings</strong> (⚙️) to find your private <strong>Venue Key</strong> under the <strong>OpenKJ Desktop Software Sync</strong> card.</p>
                            </div>
                        </div>
                        <div class="docs-step-card">
                            <div class="docs-step-number">2</div>
                            <div class="docs-step-body">
                                <h4>Configure OpenKJ Remote Service</h4>
                                <p>In the OpenKJ desktop application, open <strong>Settings</strong> &gt; <strong>Remote Request Service</strong>. Choose custom server and enter the MicUp endpoint:</p>
                                <div class="docs-code-block">
                                    <div class="docs-code-header">
                                        <span>OpenKJ Remote Endpoint</span>
                                        <button
                                            type="button"
                                            class="docs-code-copy-btn"
                                            onClick={() => copyToClipboard('https://micup.live/api/v1/openkj', 'openkj-url')}
                                        >
                                            {copiedId === 'openkj-url' ? '✓ Copied' : 'Copy'}
                                        </button>
                                    </div>
                                    <div class="docs-code">https://micup.live/api/v1/openkj</div>
                                </div>
                            </div>
                        </div>
                        <div class="docs-step-card">
                            <div class="docs-step-number">3</div>
                            <div class="docs-step-body">
                                <h4>Enter Venue Key &amp; Connect</h4>
                                <p>Click <strong>Copy Key</strong> in Show Settings and paste it into the <strong>Venue ID / Key</strong> field in OpenKJ. From OpenKJ, you can upload your song database to MicUp, retrieve incoming singer requests into your desktop queue, and toggle whether the show is accepting requests. (Note: Catalog synchronization is one-way from OpenKJ to MicUp; songs added or edited in MicUp are not pushed back into OpenKJ's local database.) If a key is ever compromised, click <strong>Rotate</strong> in Show Settings to generate a new key and revoke the old one immediately.</p>
                            </div>
                        </div>
                    </div>

                    <div class="docs-callout tip">
                        <span class="docs-callout-icon">🔒</span>
                        <div>
                            <strong>Security &amp; Isolation:</strong> MicUp requires private, revocable Integration Keys (<code>okj_&lt;event_id&gt;_&lt;secret&gt;</code>) for all OpenKJ remote requests. Public Show Codes and public event identifiers cannot be used to modify your queue, clear song databases, or change host settings, keeping administrative controls isolated from public participants.
                        </div>
                    </div>
                </section>

                {/* Section 4b: VirtualDJ Import */}
                <section id="virtualdj" class="docs-section">
                    <h2 class="docs-section-title">
                        <span>🎛️</span>
                        <span>VirtualDJ (database.xml) Import</span>
                    </h2>
                    <p>
                        MicUp parses <strong>VirtualDJ&apos;s</strong> native <code>database.xml</code> directly in your browser — no CSV export or desktop plugin required.
                    </p>

                    <div class="docs-steps">
                        <div class="docs-step-card">
                            <div class="docs-step-number">1</div>
                            <div class="docs-step-body">
                                <h4>Locate Your <code>database.xml</code></h4>
                                <p>
                                    On <strong>Windows</strong>, look in <code>Documents\VirtualDJ\database.xml</code>. On <strong>macOS</strong>, look in <code>~/Library/Application Support/VirtualDJ/database.xml</code>. If your karaoke tracks live on an external USB drive, check the <code>VirtualDJ\database.xml</code> file on that drive as well.
                                </p>
                            </div>
                        </div>
                        <div class="docs-step-card">
                            <div class="docs-step-number">2</div>
                            <div class="docs-step-body">
                                <h4>Drop It Into Songbooks</h4>
                                <p>
                                    Open <strong>Songbooks → Import / Sync → File Upload</strong> and drag in your <code>database.xml</code>. MicUp reads artist, title, genre, optional label, and file path locally without uploading any audio/video files.
                                </p>
                            </div>
                        </div>
                    </div>

                    <div class="docs-callout tip">
                        <span class="docs-callout-icon">💡</span>
                        <div>
                            <strong>Hybrid DJ + Karaoke Libraries:</strong> When your <code>database.xml</code> includes both DJ music and karaoke, MicUp automatically filters to karaoke tracks marked with VirtualDJ&apos;s karaoke flag (<code>Flag=&quot;32&quot;</code>), <code>.cdg</code>/<code>.kar</code>/<code>.kfn</code> extensions, or a <code>Karaoke</code> genre. If none are explicitly tagged, it falls back to <code>.zip</code> tracks.
                        </div>
                    </div>
                </section>

                {/* Section: Developer API & Personal Access Tokens */}
                <section id="developer-api" class="docs-section">
                    <h2 class="docs-section-title">
                        <span>⚡</span>
                        <span>Developer REST API & Integrations</span>
                    </h2>
                    <p>
                        The MicUp Developer API enables third-party playback applications, desktop hosters, custom automation scripts, and venue hardware to intake live singer requests, update playback deck statuses, and manage show states programmatically.
                    </p>

                    {/* Swagger & OpenAPI Downloads Banner */}
                    <div class="docs-swagger-card">
                        <div class="docs-swagger-info">
                            <h4>
                                <span>📜</span>
                                <span>Interactive OpenAPI & Swagger Documentation</span>
                            </h4>
                            <p>Test live endpoints interactively in your browser or import our official OpenAPI 3.0 specification into Postman, Insomnia, or code generators.</p>
                        </div>
                        <div class="docs-swagger-actions">
                            <a href="/swagger.html" target="_blank" rel="noopener noreferrer" class="btn btn-primary btn-sm">
                                🚀 Open Swagger UI
                            </a>
                            <a href="/openapi.json" download="openapi.json" class="btn btn-secondary btn-sm">
                                ⬇️ openapi.json
                            </a>
                            <a href="/openapi.yaml" download="openapi.yaml" class="btn btn-secondary btn-sm">
                                ⬇️ openapi.yaml
                            </a>
                        </div>
                    </div>

                    <h3>1. Creating & Managing Developer API Keys</h3>
                    <div class="docs-steps">
                        <div class="docs-step-card">
                            <div class="docs-step-number">1</div>
                            <div class="docs-step-body">
                                <h4>Open Account Settings</h4>
                                <p>Click your profile avatar in the top-right header and select ⚡ <strong>Developer API Keys</strong> under Account Settings.</p>
                            </div>
                        </div>
                        <div class="docs-step-card">
                            <div class="docs-step-number">2</div>
                            <div class="docs-step-body">
                                <h4>Generate Personal Access Token</h4>
                                <p>Enter a descriptive label for your key (e.g. <code>Stage Playback Deck</code> or <code>Booth Controller</code>) and click <strong>Generate Key</strong>.</p>
                            </div>
                        </div>
                        <div class="docs-step-card">
                            <div class="docs-step-number">3</div>
                            <div class="docs-step-body">
                                <h4>Copy Raw Secret & Store Securely</h4>
                                <p>Your token starts with <code>mk_live_</code> followed by 32 hex characters. The raw secret is shown <strong>only once</strong> upon creation and is never stored in plaintext. Copy it immediately into your desktop player or script configuration.</p>
                            </div>
                        </div>
                    </div>

                    <h3>2. Authentication</h3>
                    <p>
                        Authenticate all HTTP requests by including your token in the standard <code>Authorization</code> header:
                    </p>
                    <div class="docs-code-block">
                        <div class="docs-code-header">
                            <span>HTTP Header</span>
                            <button
                                type="button"
                                class="docs-code-copy-btn"
                                onClick={() => copyToClipboard('Authorization: Bearer mk_live_YOUR_KEY_HERE', 'auth-header')}
                            >
                                {copiedId === 'auth-header' ? 'Copied!' : 'Copy'}
                            </button>
                        </div>
                        <div class="docs-code">Authorization: Bearer mk_live_YOUR_KEY_HERE</div>
                    </div>

                    <h3>3. High-Efficiency Conditional Polling (<code>?since=v</code>)</h3>
                    <p>
                        To eliminate unnecessary bandwidth and compute overhead during live shows, desktop clients poll using a monotonic version counter:
                    </p>
                    <ul>
                        <li>
                            <strong>Host Queue Intake:</strong> Poll <code>GET /api/v1/events/{'{id}'}/requests?since={'{queue_version}'}</code> (requires <code>requests:read</code> scope) every 3 to 5 seconds to receive all 6 request statuses (<code>pending</code>, <code>accepted</code>, <code>up_next</code>, <code>completed</code>, <code>rejected</code>, <code>cancelled</code>) and singer notes for your host playback deck.
                        </li>
                        <li>
                            <strong>Public Stage Rotation:</strong> Poll <code>GET /api/v1/events/{'{id}'}/queue?since={'{queue_version}'}</code> (public, no auth) to display active rotation tracks (<code>accepted</code> and <code>up_next</code> only; private fields like notes and decline reasons omitted).
                        </li>
                        <li>
                            <strong>Monotonic 204 Responses:</strong> When the queue is unchanged (<code>since == queue_version</code>), MicUp immediately returns <strong><code>204 No Content</code></strong> with <strong>0 payload bytes</strong> and the current <code>X-Queue-Version</code> header.
                        </li>
                        <li>
                            <strong>Atomic Concurrency:</strong> Send <code>expected_queue_version</code> on <code>PATCH /api/v1/events/{'{id}'}/requests/{'{rid}'}</code> or reorders to commit mutations and version increments together. Stale preconditions return <strong><code>409 Conflict</code></strong> without applying changes.
                        </li>
                    </ul>

                    <h3>4. Code Integration Examples</h3>
                    <div class="docs-tabs-header">
                        <button
                            type="button"
                            class={`docs-tab-btn ${codeLang === 'curl' ? 'active' : ''}`}
                            onClick={() => setCodeLang('curl')}
                        >
                            cURL (CLI)
                        </button>
                        <button
                            type="button"
                            class={`docs-tab-btn ${codeLang === 'python' ? 'active' : ''}`}
                            onClick={() => setCodeLang('python')}
                        >
                            Python 3
                        </button>
                        <button
                            type="button"
                            class={`docs-tab-btn ${codeLang === 'csharp' ? 'active' : ''}`}
                            onClick={() => setCodeLang('csharp')}
                        >
                            C# (.NET)
                        </button>
                    </div>

                    <div class="docs-code-block">
                        <div class="docs-code-header">
                            <span>
                                {codeLang === 'curl' && 'Bash / cURL Integration'}
                                {codeLang === 'python' && 'Python 3 Polling Loop'}
                                {codeLang === 'csharp' && 'C# / .NET Desktop Client Service'}
                            </span>
                            <button
                                type="button"
                                class="docs-code-copy-btn"
                                onClick={() => copyToClipboard(CODE_SAMPLES[codeLang], `code-${codeLang}`)}
                            >
                                {copiedId === `code-${codeLang}` ? 'Copied!' : 'Copy'}
                            </button>
                        </div>
                        <div class="docs-code">{CODE_SAMPLES[codeLang]}</div>
                    </div>


                    <h3>5. Core REST Endpoints</h3>
                    <div class="docs-endpoint-list">
                        {DEVELOPER_ENDPOINTS.map((ep) => (
                            <div key={`${ep.method}-${ep.path}`} class="docs-endpoint-row">
                                <span class={`method-badge ${ep.method.toLowerCase()}`}>{ep.method}</span>
                                <span class="docs-endpoint-path">{ep.path}</span>
                                <span class="docs-endpoint-desc">{ep.desc}</span>
                            </div>
                        ))}
                    </div>
                </section>

                {/* Section 5: Stage Display & Kiosk */}
                <section id="stage-kiosk" class="docs-section">
                    <h2 class="docs-section-title">
                        <span>📺</span>
                        <span>TV Stage Display & Kiosk Mode</span>
                    </h2>
                    <p>
                        Turn any secondary monitor, bar television, or tablet into a professional stage display.
                    </p>

                    <h3>TV Stage Display (<code>/stage/event/:id</code>)</h3>
                    <ul>
                        <li><strong>Live High-Contrast QR Code:</strong> Large on-screen QR code allows singers in the crowd to scan the songbook from across the room.</li>
                        <li><strong>Live Stage Ticker:</strong> Highlights who is currently singing, who is on deck, and who is up next.</li>
                        <li><strong>Stage Shift Motion &amp; Glow:</strong> Delivers high-energy visual feedback (card shake and neon bloom) whenever the queue updates or a singer is promoted, catching the room's attention with zero PA audio conflicts.</li>
                        <li><strong>Designed for Secondary Screens:</strong> High-contrast layout optimized for venue TVs, projectors, or secondary monitors. (Configure your operating system or device settings to prevent screen sleep during shows.)</li>
                    </ul>

                    <h3>Walk-Up Kiosk Mode (<code>/kiosk/event/:id</code>)</h3>
                    <ul>
                        <li><strong>Touchscreen Optimized:</strong> Designed for an iPad or tablet placed at the host desk for patrons who prefer not to use their personal phones.</li>
                        <li><strong>Host PIN Security:</strong> Exiting kiosk mode to access show management or navigation requires entering the host PIN (verified securely by the server).</li>
                        <li><strong>Inactivity Reset:</strong> Automatically clears searches, singer names, and open modals after 45 seconds of inactivity to protect singer privacy between walk-up patrons.</li>
                    </ul>
                </section>

                {/* Section 6: Singer Experience */}
                <section id="singers" class="docs-section">
                    <h2 class="docs-section-title">
                        <span>🎵</span>
                        <span>Singer Experience & Repertoire Memory</span>
                    </h2>
                    <p>
                        MicUp is designed with a <strong>Zero-Friction, Zero-App Wall</strong> philosophy for singers:
                    </p>

                    <div class="docs-grid">
                        <div class="docs-card">
                            <span class="docs-card-icon">⚡</span>
                            <div class="docs-card-title">Instant Mobile Load</div>
                            <p class="docs-card-desc">Fast page loads via global CDN edge caching. No downloading app store bundles or waiting for SMS verification codes.</p>
                        </div>
                        <div class="docs-card">
                            <span class="docs-card-icon">⭐</span>
                            <div class="docs-card-title">Universal Favorites</div>
                            <p class="docs-card-desc">Starring a song or artist saves it to that device's browser storage. When attending another MicUp show in the same browser, favorites remain highlighted without needing an account.</p>
                        </div>
                        <div class="docs-card">
                            <span class="docs-card-icon">🎹</span>
                            <div class="docs-card-title">Pitch Transposition Memory</div>
                            <p class="docs-card-desc">Need Bohemian Rhapsody in <code>-2♭</code>? The singer selects their key preference once, and MicUp remembers that setting in their browser for future requests of that song.</p>
                        </div>
                        <div class="docs-card">
                            <span class="docs-card-icon">🔔</span>
                            <div class="docs-card-title">Live Stage Alerts</div>
                            <p class="docs-card-desc">On-screen stage status alerts update while the songbook page is visible and connected, with browser notifications delivered if supported and permitted.</p>
                        </div>
                    </div>
                </section>

                {/* Section 7: Troubleshooting & FAQ */}
                <section id="faq" class="docs-section">
                    <h2 class="docs-section-title">
                        <span>❓</span>
                        <span>Troubleshooting & Frequently Asked Questions</span>
                    </h2>

                    <div class="docs-grid">
                        <div class="docs-card">
                            <div class="docs-card-title">What if the venue Wi-Fi is spotty?</div>
                            <p class="docs-card-desc">
                                MicUp downloads the published song catalog to the browser and indexes it locally using <code>MiniSearch</code>. Singers can continue searching and browsing even when Wi-Fi fluctuates. Submitting requests, receiving queue updates, checking host acceptance, and cancelling requests require an active connection; once reconnected, the browser automatically retries and catches up with the latest queue state.
                            </p>
                        </div>
                        <div class="docs-card">
                            <div class="docs-card-title">How do I reset my account password?</div>
                            <p class="docs-card-desc">
                                Go to the <a href="/login" style={{ color: 'var(--color-accent)' }}>Sign In</a> page and click <strong>"Forgot password?"</strong>. Enter your account email to receive a secure, 1-hour reset link.
                            </p>
                        </div>
                        <div class="docs-card">
                            <div class="docs-card-title">How does the TV Stage alert the room without sound?</div>
                            <p class="docs-card-desc">
                                In loud venue environments, audio chimes are easily drowned out or clash with the music PA. MicUp uses dynamic "Stage Shift" shake animations and neon pulse banners to catch crowd attention visually without requiring audio permissions.
                            </p>
                        </div>
                        <div class="docs-card">
                            <div class="docs-card-title">Will VirtualDJ import my regular DJ music?</div>
                            <p class="docs-card-desc">
                                If your <code>database.xml</code> contains karaoke markers (<code>Flag=&quot;32&quot;</code>, <code>.cdg</code>/<code>.kar</code>/<code>.kfn</code> files, or <code>Karaoke</code> genre), MicUp automatically filters out non-karaoke DJ tracks during import.
                            </p>
                        </div>
                        <div class="docs-card">
                            <div class="docs-card-title">Can singers delete another person's request?</div>
                            <p class="docs-card-desc">
                                No. Each submitted request is issued a private cancellation token stored in that browser on the device. Presenting that token authorizes the singer to cancel the request, and the authenticated KJ host can also manage or remove requests from the live queue.
                            </p>
                        </div>
                    </div>

                    <div style={{
                        marginTop: 'var(--space-6)',
                        padding: 'var(--space-6)',
                        background: 'linear-gradient(135deg, rgba(88, 101, 242, 0.12) 0%, rgba(20, 20, 30, 0.95) 100%)',
                        border: '1px solid rgba(88, 101, 242, 0.35)',
                        borderRadius: 'var(--radius-xl)',
                        display: 'flex',
                        flexDirection: 'column',
                        gap: 'var(--space-3)',
                    }}>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-3)' }}>
                            <span style={{ fontSize: '1.75rem' }}>💬</span>
                            <div>
                                <div style={{ fontWeight: 'var(--font-bold)', fontSize: 'var(--text-lg)', color: 'var(--color-text)' }}>
                                    Need Help or Have Feature Feedback?
                                </div>
                                <div style={{ fontSize: 'var(--text-sm)', color: 'var(--color-text-muted)', marginTop: '2px' }}>
                                    Join the official MicUp.live Discord community to get fast host support, report bugs, or request features directly from the team.
                                </div>
                            </div>
                        </div>
                        <div>
                            <a
                                href="https://discord.gg/ZUPwCTWXSx"
                                target="_blank"
                                rel="noopener noreferrer"
                                class="btn btn-primary btn-sm"
                                style={{
                                    display: 'inline-flex',
                                    alignItems: 'center',
                                    gap: '6px',
                                    background: '#5865F2',
                                    borderColor: '#5865F2',
                                    color: '#ffffff',
                                }}
                            >
                                <span>Join Discord Support</span>
                                <span aria-hidden="true">↗</span>
                            </a>
                        </div>
                    </div>

                    <div class="docs-actions">
                        <a href="/login" class="btn btn-primary btn-md">
                            🚀 Launch MicUp Host Dashboard
                        </a>
                        <a href="/" class="btn btn-secondary btn-md">
                            🏠 Back to MicUp Home
                        </a>
                    </div>
                </section>
            </div>
        </div>
    )
}
