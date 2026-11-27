import { BrandIcon } from '../components/BrandIcon'
import { Spinner } from '../components/Spinner'
import { QRCodeDisplay } from '../components/QRCodeDisplay'
import { formatWaitEstimate } from '../utils/waitTime.js'

function formatPitchLabel(keyChange) {
    return keyChange > 0 ? `+${keyChange}♯` : `${keyChange}♭`
}

export function StageStyles() {
    return (
        <style>{`
            @keyframes stageShiftShake {
                0% { transform: translate(0, 0) rotate(0deg); }
                15% { transform: translate(-6px, 3px) rotate(-0.5deg); }
                30% { transform: translate(6px, -3px) rotate(0.5deg); }
                45% { transform: translate(-4px, 2px) rotate(-0.3deg); }
                60% { transform: translate(4px, -2px) rotate(0.3deg); }
                75% { transform: translate(-2px, 1px) rotate(0deg); }
                100% { transform: translate(0, 0) rotate(0deg); }
            }
            @keyframes stagePulseGlow {
                0% { box-shadow: 0 0 50px rgba(255, 42, 95, 0.35); }
                50% { box-shadow: 0 0 95px rgba(255, 42, 95, 0.95), 0 0 40px rgba(245, 158, 11, 0.85); }
                100% { box-shadow: 0 0 50px rgba(255, 42, 95, 0.35); }
            }
            @keyframes stageBadgePop {
                0% { transform: scale(0.6); opacity: 0; }
                60% { transform: scale(1.1); opacity: 1; }
                100% { transform: scale(1); opacity: 1; }
            }
            @media (max-height: 780px) {
                .stage-view { padding: var(--space-3) var(--space-6) !important; }
                .stage-header { margin-bottom: var(--space-3) !important; padding-bottom: var(--space-2) !important; }
                .stage-grid { gap: var(--space-4) !important; }
                .stage-right-col { gap: var(--space-3) !important; }
                .stage-up-next-card { padding: var(--space-3) var(--space-4) !important; }
                .stage-side-qr { padding: var(--space-3) var(--space-4) !important; }
            }
        `}</style>
    )
}

export function StageHeader({ event, isShaking, queueLength, currentTime, onShowQr, onToggleFullscreen }) {
    const subtitle = event?.venue_name ? `${event.venue_name} — ${event.name}` : 'Live Stage Monitor'
    const queueLabel = queueLength > 0 ? `${queueLength} in line • ${formatWaitEstimate(queueLength)}` : 'Stage Open'

    return (
        <header class="stage-header" style={{
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'center',
            borderBottom: '1px solid rgba(255, 255, 255, 0.1)',
            paddingBottom: 'var(--space-4)',
            marginBottom: 'var(--space-8)',
        }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-3)' }}>
                <BrandIcon size={36} glowing />
                <div>
                    <div style={{ fontSize: 'var(--text-xl)', fontWeight: 'var(--font-bold)', fontFamily: 'var(--font-display)', letterSpacing: '-0.02em' }}>
                        MicUp<span style={{ color: 'var(--color-accent)' }}>.live</span>
                    </div>
                    <div style={{ fontSize: 'var(--text-sm)', color: 'var(--color-text-dim)' }}>
                        {subtitle}
                    </div>
                </div>
            </div>

            <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-6)' }}>
                {isShaking && (
                    <div style={{
                        background: 'linear-gradient(90deg, #ff2a5f 0%, #f59e0b 100%)',
                        color: '#fff',
                        padding: '6px 14px',
                        borderRadius: 'var(--radius-full)',
                        fontSize: 'var(--text-sm)',
                        fontWeight: 'var(--font-bold)',
                        letterSpacing: '0.06em',
                        textTransform: 'uppercase',
                        boxShadow: '0 0 25px rgba(255, 42, 95, 0.8)',
                        display: 'flex',
                        alignItems: 'center',
                        gap: '6px',
                        animation: 'stageBadgePop 0.3s ease-out',
                    }}>
                        <span>⚡</span>
                        <span>ROTATION UPDATE</span>
                    </div>
                )}
                <div style={{
                    display: 'inline-flex',
                    alignItems: 'center',
                    gap: '6px',
                    padding: '6px 14px',
                    borderRadius: 'var(--radius-full)',
                    background: 'rgba(255, 42, 95, 0.12)',
                    border: '1px solid var(--color-accent)',
                    color: '#fff',
                    fontSize: 'var(--text-sm)',
                    fontWeight: 'var(--font-bold)',
                    letterSpacing: '0.06em',
                    textTransform: 'uppercase',
                    boxShadow: '0 0 15px rgba(255, 42, 95, 0.25)',
                }}>
                    <span style={{ width: '8px', height: '8px', borderRadius: '50%', background: 'var(--color-accent)', boxShadow: '0 0 8px var(--color-accent)' }}></span>
                    <span>{queueLabel}</span>
                </div>
                <div style={{
                    fontSize: 'var(--text-3xl)',
                    fontFamily: 'var(--font-mono)',
                    fontWeight: 'var(--font-bold)',
                    color: 'var(--color-chorus)',
                    textShadow: '0 0 20px var(--color-chorus-glow)',
                }}>
                    {currentTime}
                </div>
                <button
                    type="button"
                    onClick={onShowQr}
                    class="btn btn-secondary btn-sm"
                    title="Enlarge QR Code for the Room"
                    style={{ fontSize: '1rem', padding: '6px 14px', display: 'inline-flex', alignItems: 'center', gap: '6px' }}
                >
                    <span>📱</span>
                    <span>Show QR</span>
                </button>
                <button
                    type="button"
                    onClick={onToggleFullscreen}
                    class="btn btn-secondary btn-sm"
                    title="Toggle Fullscreen"
                    style={{ fontSize: '1rem', padding: '6px 12px' }}
                >
                    ⛶ Fullscreen
                </button>
            </div>
        </header>
    )
}

function NowSingingContent({ currentSinger }) {
    return (
        <div>
            <h1 style={{
                margin: '0 0 var(--space-2)',
                fontSize: '3rem',
                fontFamily: 'var(--font-display)',
                fontWeight: '800',
                color: '#fff',
                lineHeight: 1.1,
                textShadow: '0 0 30px rgba(255, 255, 255, 0.2)',
            }}>
                {currentSinger.singer_name}
            </h1>
            <div style={{
                fontSize: 'var(--text-2xl)',
                color: 'var(--color-chorus)',
                fontWeight: 'var(--font-semibold)',
                marginTop: 'var(--space-2)',
            }}>
                {currentSinger.song_title}
            </div>
            {currentSinger.song_artist && (
                <div style={{ fontSize: 'var(--text-lg)', color: 'var(--color-text-muted)', marginTop: '4px' }}>
                    {currentSinger.song_artist}
                </div>
            )}

            {currentSinger.key_change !== 0 && (
                <div style={{
                    display: 'inline-flex',
                    alignItems: 'center',
                    gap: '6px',
                    marginTop: 'var(--space-4)',
                    background: 'rgba(245, 158, 11, 0.2)',
                    color: 'var(--color-chorus)',
                    border: '1px solid var(--color-chorus)',
                    padding: '4px 14px',
                    borderRadius: 'var(--radius-full)',
                    fontSize: 'var(--text-sm)',
                    fontFamily: 'var(--font-mono)',
                    fontWeight: 'var(--font-bold)',
                    boxShadow: '0 0 14px var(--color-chorus-glow)',
                }}>
                    <span>Pitch:</span>
                    <span>{formatPitchLabel(currentSinger.key_change)}</span>
                </div>
            )}
        </div>
    )
}

function StageOpenContent({ joinUrl, joinCode, showQrModal, onToggleEnlarge }) {
    return (
        <div style={{
            display: 'flex',
            alignItems: 'center',
            gap: 'var(--space-6)',
            padding: 'var(--space-4) 0',
            flexWrap: 'wrap',
        }}>
            <div style={{ flex: '1 1 240px' }}>
                <h2 style={{
                    margin: '0 0 var(--space-2)',
                    fontSize: '2.4rem',
                    fontFamily: 'var(--font-display)',
                    fontWeight: '800',
                    color: '#fff',
                    lineHeight: 1.15,
                }}>
                    The Stage is Open!
                </h2>
                <div style={{
                    fontSize: 'var(--text-lg)',
                    color: 'var(--color-chorus)',
                    fontWeight: 'var(--font-semibold)',
                    marginBottom: 'var(--space-3)',
                }}>
                    Take the mic & become tonight's superstar.
                </div>
                <div style={{ color: 'var(--color-text-muted)', fontSize: 'var(--text-sm)' }}>
                    Point your camera at the QR code to browse the catalog and submit your song request.
                </div>
            </div>
            <div style={{ flexShrink: 0 }}>
                <QRCodeDisplay
                    url={joinUrl}
                    size={240}
                    showLogo={true}
                    showCode={true}
                    joinCode={joinCode}
                    title="Scan To Sing"
                    subtitle=""
                    allowEnlarge={true}
                    enlarged={showQrModal}
                    onToggleEnlarge={onToggleEnlarge}
                />
            </div>
        </div>
    )
}

function StageNowSingingCard({ currentSinger, isShaking, joinUrl, joinCode, showQrModal, onToggleEnlarge }) {
    return (
        <div style={{
            background: 'linear-gradient(135deg, rgba(255, 42, 95, 0.18) 0%, rgba(19, 19, 28, 0.9) 100%)',
            border: '2px solid var(--color-accent)',
            borderRadius: 'var(--radius-xl)',
            padding: 'var(--space-8)',
            boxShadow: '0 0 50px rgba(255, 42, 95, 0.35)',
            position: 'relative',
            overflow: 'hidden',
            animation: isShaking ? 'stageShiftShake 0.7s cubic-bezier(.36,.07,.19,.97) both, stagePulseGlow 0.8s ease-out' : 'none',
        }}>
            {isShaking && (
                <div style={{
                    position: 'absolute',
                    top: 'var(--space-4)',
                    right: 'var(--space-4)',
                    background: 'linear-gradient(90deg, #ff2a5f 0%, #f59e0b 100%)',
                    color: '#fff',
                    padding: '6px 14px',
                    borderRadius: 'var(--radius-full)',
                    fontSize: 'var(--text-xs)',
                    fontWeight: '900',
                    letterSpacing: '0.1em',
                    textTransform: 'uppercase',
                    boxShadow: '0 0 25px rgba(255, 42, 95, 0.8)',
                    display: 'flex',
                    alignItems: 'center',
                    gap: '6px',
                    animation: 'stageBadgePop 0.4s ease-out',
                    zIndex: 10,
                }}>
                    <span>⚡</span>
                    <span>STAGE SHIFT</span>
                </div>
            )}
            <div style={{
                fontSize: 'var(--text-sm)',
                fontWeight: 'var(--font-bold)',
                color: 'var(--color-accent)',
                textTransform: 'uppercase',
                letterSpacing: '0.12em',
                display: 'flex',
                alignItems: 'center',
                gap: 'var(--space-2)',
                marginBottom: 'var(--space-4)',
            }}>
                <span style={{ width: '10px', height: '10px', borderRadius: '50%', background: 'var(--color-accent)', display: 'inline-block', boxShadow: '0 0 12px var(--color-accent)' }}></span>
                <span>NOW ON STAGE</span>
            </div>

            {currentSinger ? (
                <NowSingingContent currentSinger={currentSinger} />
            ) : (
                <StageOpenContent
                    joinUrl={joinUrl}
                    joinCode={joinCode}
                    showQrModal={showQrModal}
                    onToggleEnlarge={onToggleEnlarge}
                />
            )}
        </div>
    )
}

function StageOnDeckCard({ singer }) {
    if (!singer) return null
    const artistSuffix = singer.song_artist ? `— ${singer.song_artist}` : ''

    return (
        <div style={{
            background: 'rgba(19, 19, 28, 0.8)',
            border: '1px solid var(--color-chorus)',
            borderRadius: 'var(--radius-xl)',
            padding: 'var(--space-6)',
            boxShadow: '0 0 30px rgba(245, 158, 11, 0.15)',
        }}>
            <div style={{
                fontSize: 'var(--text-xs)',
                fontWeight: 'var(--font-bold)',
                color: 'var(--color-chorus)',
                textTransform: 'uppercase',
                letterSpacing: '0.1em',
                marginBottom: 'var(--space-2)',
            }}>
                ON DECK (UP NEXT)
            </div>
            <div style={{
                fontSize: 'var(--text-2xl)',
                fontWeight: 'var(--font-bold)',
                color: 'var(--color-text)',
            }}>
                {singer.singer_name}
            </div>
            <div style={{ fontSize: 'var(--text-base)', color: 'var(--color-text-muted)', marginTop: '2px' }}>
                {singer.song_title} {artistSuffix}
            </div>
        </div>
    )
}

function StageRotationList({ upcomingQueue }) {
    return (
        <div class="stage-up-next-card" style={{
            background: 'var(--color-surface)',
            border: '1px solid var(--color-border)',
            borderRadius: 'var(--radius-xl)',
            padding: 'var(--space-6)',
        }}>
            <div style={{
                fontSize: 'var(--text-sm)',
                fontWeight: 'var(--font-bold)',
                color: 'var(--color-text-muted)',
                textTransform: 'uppercase',
                letterSpacing: '0.08em',
                marginBottom: 'var(--space-4)',
            }}>
                Coming Up In Rotation
            </div>

            {upcomingQueue.length <= 1 ? (
                <div style={{ color: 'var(--color-text-dim)', fontSize: 'var(--text-sm)', padding: 'var(--space-4) 0' }}>
                    No other singers in line yet.
                </div>
            ) : (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-3)' }}>
                    {upcomingQueue.slice(1, 6).map((item, idx) => (
                        <div
                            key={item.id}
                            style={{
                                display: 'flex',
                                alignItems: 'center',
                                gap: 'var(--space-3)',
                                padding: 'var(--space-3)',
                                background: 'var(--color-surface-alt)',
                                borderRadius: 'var(--radius-lg)',
                                border: '1px solid var(--color-border)',
                            }}
                        >
                            <div style={{
                                width: '28px',
                                height: '28px',
                                borderRadius: '50%',
                                background: 'var(--color-surface)',
                                color: 'var(--color-text-muted)',
                                fontWeight: 'bold',
                                fontSize: 'var(--text-xs)',
                                display: 'flex',
                                alignItems: 'center',
                                justifyContent: 'center',
                                flexShrink: 0,
                            }}>
                                {idx + 3}
                            </div>
                            <div style={{ flex: 1, minWidth: 0 }}>
                                <div style={{ fontWeight: 'var(--font-bold)', color: 'var(--color-text)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                                    {item.singer_name}
                                </div>
                                <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-dim)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                                    {item.song_title}
                                </div>
                            </div>
                        </div>
                    ))}
                </div>
            )}
        </div>
    )
}

function StageSideQrCard({ visible, joinUrl, joinCode, showQrModal, onToggleEnlarge }) {
    if (!visible) return null

    return (
        <div class="stage-side-qr" style={{
            background: 'linear-gradient(135deg, rgba(245, 158, 11, 0.08) 0%, rgba(19, 19, 28, 0.95) 100%)',
            border: '1px solid rgba(245, 158, 11, 0.3)',
            borderRadius: 'var(--radius-xl)',
            padding: 'var(--space-6)',
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'center',
            textAlign: 'center',
            boxShadow: '0 0 30px rgba(0, 0, 0, 0.5)',
        }}>
            <QRCodeDisplay
                url={joinUrl}
                size={240}
                showLogo={true}
                showCode={true}
                joinCode={joinCode}
                title="Scan To Sing"
                subtitle="Point camera to browse & request songs from your phone"
                allowEnlarge={true}
                enlarged={showQrModal}
                onToggleEnlarge={onToggleEnlarge}
            />
        </div>
    )
}

export function StageBody({
    isLoading,
    currentSinger,
    upcomingQueue,
    isShaking,
    joinUrl,
    joinCode,
    showQrModal,
    onToggleEnlarge,
}) {
    if (isLoading) {
        return (
            <div style={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                <Spinner size="lg" label="Connecting to stage queue..." />
            </div>
        )
    }

    return (
        <div class="stage-grid" style={{
            flex: 1,
            display: 'grid',
            gridTemplateColumns: '1.2fr 0.8fr',
            gap: 'var(--space-8)',
            alignItems: 'start',
        }}>
            {/* Left Hero Column: On Stage & On Deck */}
            <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-6)' }}>
                <StageNowSingingCard
                    currentSinger={currentSinger}
                    isShaking={isShaking}
                    joinUrl={joinUrl}
                    joinCode={joinCode}
                    showQrModal={showQrModal}
                    onToggleEnlarge={onToggleEnlarge}
                />
                <StageOnDeckCard singer={upcomingQueue[0]} />
            </div>

            {/* Right Column: Rotation & Audience QR Callout */}
            <div class="stage-right-col" style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-6)' }}>
                <StageRotationList upcomingQueue={upcomingQueue} />
                <StageSideQrCard
                    visible={Boolean(currentSinger)}
                    joinUrl={joinUrl}
                    joinCode={joinCode}
                    showQrModal={showQrModal}
                    onToggleEnlarge={onToggleEnlarge}
                />
            </div>
        </div>
    )
}

export function StageAnnouncementBanner({ announcement }) {
    if (!announcement) return null

    return (
        <div style={{
            marginTop: 'var(--space-6)',
            background: 'linear-gradient(90deg, rgba(255, 42, 95, 0.3) 0%, rgba(245, 158, 11, 0.3) 100%)',
            border: '2px solid var(--color-accent)',
            borderRadius: 'var(--radius-xl)',
            padding: 'var(--space-4) var(--space-6)',
            display: 'flex',
            alignItems: 'center',
            gap: 'var(--space-4)',
            boxShadow: '0 0 30px rgba(255, 42, 95, 0.3)',
        }}>
            <span style={{ fontSize: '2rem' }}>📢</span>
            <div style={{ flex: 1 }}>
                <div style={{ fontSize: 'var(--text-xs)', color: 'var(--color-chorus)', fontWeight: 'var(--font-bold)', textTransform: 'uppercase', letterSpacing: '0.1em' }}>
                    Host Announcement
                </div>
                <div style={{ fontSize: 'var(--text-xl)', fontWeight: 'var(--font-bold)', color: '#fff', marginTop: '2px' }}>
                    {announcement}
                </div>
            </div>
        </div>
    )
}
