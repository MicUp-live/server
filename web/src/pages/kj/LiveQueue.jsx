import { useState, useRef, useMemo, useCallback } from 'preact/hooks'
import { api } from '../../api/client.js'
import { Spinner } from '../../components/Spinner.jsx'
import { showToast } from '../../components/Toast.jsx'
import { useQueuePolling } from '../../hooks/useQueuePolling.js'
import { useHostQueueMutations } from './hooks/useHostQueueMutations.js'
import { useDirectQueueHandoff } from './hooks/useStartShowHandoff.js'
import { QueueHeader } from './components/QueueHeader.jsx'
import { QueueStats } from './components/QueueStats.jsx'
import { RequestList } from './components/RequestList.jsx'
import { CreateShowModal } from './components/CreateShowModal.jsx'
import { ShowSettingsModal } from './components/ShowSettingsModal.jsx'
import { AnnouncementModal } from './components/AnnouncementModal.jsx'
import { RestartShowModal } from './components/RestartShowModal.jsx'
import { DeclineRequestModal } from './components/DeclineRequestModal.jsx'
import { EditRequestModal } from './components/EditRequestModal.jsx'
import { SingerWeightsModal } from './components/SingerWeightsModal.jsx'
import './kj.css'

// Synthesize a clean 2-tone notification chime using Web Audio API
function playChime() {
    try {
        const AudioContext = window.AudioContext || window.webkitAudioContext
        if (!AudioContext) return
        const ctx = new AudioContext()
        const now = ctx.currentTime

        const osc1 = ctx.createOscillator()
        const gain1 = ctx.createGain()
        osc1.type = 'sine'
        osc1.frequency.setValueAtTime(587.33, now) // D5
        gain1.gain.setValueAtTime(0.15, now)
        gain1.gain.exponentialRampToValueAtTime(0.001, now + 0.3)
        osc1.connect(gain1)
        gain1.connect(ctx.destination)
        osc1.start(now)
        osc1.stop(now + 0.3)

        const osc2 = ctx.createOscillator()
        const gain2 = ctx.createGain()
        osc2.type = 'sine'
        osc2.frequency.setValueAtTime(880, now + 0.12) // A5
        gain2.gain.setValueAtTime(0.2, now + 0.12)
        gain2.gain.exponentialRampToValueAtTime(0.001, now + 0.45)
        osc2.connect(gain2)
        gain2.connect(ctx.destination)
        osc2.start(now + 0.12)
        osc2.stop(now + 0.45)
    } catch {
        // AudioContext restricted or unsupported
    }
}

export function LiveQueue({ id }) {
    const {
        events,
        setEvents,
        songbooks,
        selectedEventId,
        setSelectedEventId,
        initialCreateSongbookId,
        setInitialCreateSongbookId,
        showCreateModal,
        setShowCreateModal,
        loading,
        loadError,
        loadHostData,
    } = useDirectQueueHandoff({ targetId: id })

    const [soundEnabled, setSoundEnabled] = useState(true)
    const [tab, setTab] = useState('active') // 'staging' | 'active' | 'history'
    const [creatingShow, setCreatingShow] = useState(false)
    const restoreSwitcherFocus = useRef(false)
    const showSwitcherRef = useCallback(node => {
        if (node && restoreSwitcherFocus.current) {
            restoreSwitcherFocus.current = false
            node.focus()
        }
    }, [])

    const currentEvent = events.find(e => e.id === selectedEventId)
    const currentSongbook = songbooks.find(sb => sb.id === currentEvent?.songbook_id)
    const openCreateModal = () => setShowCreateModal(true)

    const handleCreateShow = async (formData) => {
        setCreatingShow(true)
        try {
            const payload = {
                name: formData.name.trim(),
                venue_name: formData.venue_name.trim(),
                join_code: formData.join_code.trim().toUpperCase(),
                songbook_id: formData.songbook_id,
                recurring: !!formData.recurring,
                rotation_mode: formData.rotation_mode || 'round_robin',
                auto_accept: formData.auto_accept !== undefined ? formData.auto_accept : false,
                submission_delay: parseInt(formData.submission_delay, 10) || 0,
                max_active_per_singer: Math.max(0, parseInt(formData.max_active_per_singer, 10) || 0),
                allow_key_change: formData.allow_key_change !== undefined ? formData.allow_key_change : true,
                allow_write_ins: !!formData.allow_write_ins,
                ...(formData.kiosk_pin?.trim() ? { kiosk_pin: formData.kiosk_pin.trim() } : {}),
            }
            const created = await api.post('/events', payload)
            showToast(`Show created! Singers can join with code: ${created.join_code}`)
            setEvents(prev => [created, ...prev])
            setSelectedEventId(created.id)
            setShowCreateModal(false)
        } catch (err) {
            showToast(err.message || 'Failed to create show', 'error')
        } finally {
            setCreatingShow(false)
        }
    }

    return (
        <div class="kj-page-shell">
            {/* Keep the queue, poller, and dialogs tied to the selected show. */}
            <SelectedShowQueue
                key={selectedEventId}
                selectedEventId={selectedEventId}
                currentEvent={currentEvent}
                currentSongbook={currentSongbook}
                songbooks={songbooks}
                events={events}
                setEvents={setEvents}
                onSelectEventId={(eventId, restoreFocus = false) => {
                    if (eventId === selectedEventId) return
                    restoreSwitcherFocus.current = restoreFocus
                    setSelectedEventId(eventId)
                }}
                onDeletedEvent={eventId => setSelectedEventId(prev => prev === eventId ? '' : prev)}
                showSwitcherRef={showSwitcherRef}
                onOpenCreateModal={openCreateModal}
                loading={loading}
                loadError={loadError}
                loadHostData={loadHostData}
                tab={tab}
                setTab={setTab}
                soundEnabled={soundEnabled}
                onToggleSound={() => setSoundEnabled(prev => !prev)}
            />
            <CreateShowModal
                isOpen={showCreateModal}
                onClose={() => {
                    setShowCreateModal(false)
                    setInitialCreateSongbookId('')
                }}
                songbooks={songbooks}
                onCreateShow={handleCreateShow}
                creatingShow={creatingShow}
                initialSongbookId={initialCreateSongbookId || currentSongbook?.id || songbooks[0]?.id || ''}
            />
        </div>
    )
}

const EMPTY_REQUESTS = []

function SelectedShowQueue({
    selectedEventId, currentEvent, currentSongbook, songbooks, events, setEvents, onSelectEventId,
    onDeletedEvent, showSwitcherRef, onOpenCreateModal, loading, loadError, loadHostData,
    tab, setTab, soundEnabled, onToggleSound,
}) {
    const [queue, setRequests] = useState(null)
    const [queueError, setQueueError] = useState(false)
    const [showSettingsModal, setShowSettingsModal] = useState(false)
    const [showAnnouncementModal, setShowAnnouncementModal] = useState(false)
    const [showRestartModal, setShowRestartModal] = useState(false)
    const [declineModalTarget, setDeclineModalTarget] = useState(null)
    const [editModalTarget, setEditModalTarget] = useState(null)
    const [weightsModal, setWeightsModal] = useState({ open: false, singer: '' })

    const previousReqIds = useRef(null)

    const currentRound = currentEvent?.current_round || 1
    const requests = queue ?? EMPTY_REQUESTS

    const handleQueueData = useCallback((data) => {
        if (!data) return
        if (Number.isInteger(data.current_round)) {
            setEvents(prev => prev.map(e => (e.id === selectedEventId && e.current_round !== data.current_round) ? { ...e, current_round: data.current_round } : e))
        }
        const list = data.requests || []

        const previous = previousReqIds.current
        if (soundEnabled && previous && list.some(r => r.status === 'pending' && !previous.has(r.id))) {
            playChime()
            showToast('New singer request arrived!', 'info')
        }
        previousReqIds.current = new Set(list.map(r => r.id))

        setRequests(list)
        setQueueError(false)
    }, [selectedEventId, setEvents, soundEnabled])

    const { refresh: refreshQueue } = useQueuePolling({
        endpoint: selectedEventId ? `/events/${selectedEventId}/requests` : null,
        enabled: !!selectedEventId,
        interval: 3500,
        onData: handleQueueData,
        onError: () => {
            if (!queue) setQueueError(true)
        },
    })

    const mutations = useHostQueueMutations({
        selectedEventId,
        currentEvent,
        setRequests,
        setEvents,
        refreshQueue,
    })

    const handleUpdateStatus = useCallback((request, newStatus, reasonOverride) => {
        if (newStatus === 'rejected' && !reasonOverride) {
            setDeclineModalTarget(request)
            return
        }
        mutations.updateStatus(request, newStatus, reasonOverride)
    }, [mutations])

    const singerPerformanceCounts = useMemo(() => {
        const counts = {}
        for (const r of requests) {
            if (r.status === 'completed' && r.singer_name) {
                const normalized = r.singer_name.trim().toLowerCase()
                counts[normalized] = (counts[normalized] || 0) + 1
            }
        }
        return counts
    }, [requests])

    const stagingRequests = useMemo(() => requests.filter(r => r.status === 'pending'), [requests])
    const activeRequests = useMemo(() => requests.filter(r => r.status === 'accepted' || r.status === 'up_next'), [requests])
    const historyRequests = useMemo(() => requests.filter(r => r.status === 'completed' || r.status === 'rejected' || r.status === 'cancelled'), [requests])
    const uniqueSingersCount = useMemo(() => new Set(
        requests
            .filter(r => (r.status === 'accepted' || r.status === 'up_next' || r.status === 'completed') && r.singer_name?.trim())
            .map(r => r.singer_name.trim().toLowerCase())
    ).size, [requests])

    return (
        <>
            <QueueHeader
                currentEvent={currentEvent}
                currentSongbook={currentSongbook}
                currentRound={currentRound}
                events={events}
                selectedEventId={selectedEventId}
                onSelectEventId={onSelectEventId}
                showSwitcherRef={showSwitcherRef}
                onOpenCreateModal={onOpenCreateModal}
                onCopySingerLink={mutations.copySingerLink}
                onUpdateEventStatus={mutations.updateEventStatus}
                onOpenRestartModal={() => setShowRestartModal(true)}
                onOpenAnnouncementModal={() => setShowAnnouncementModal(true)}
                onOpenSettingsModal={() => setShowSettingsModal(true)}
                soundEnabled={soundEnabled}
                onToggleSound={onToggleSound}
            />

            {loadError && (
                <div class="card" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--space-4)', padding: 'var(--space-3) var(--space-4)', borderColor: 'rgba(239, 68, 68, 0.4)', background: 'rgba(239, 68, 68, 0.1)', color: '#fca5a5' }} role="alert">
                    <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-2)' }}>
                        <span>⚠️</span>
                        <span>{loadError}</span>
                    </div>
                    <button type="button" class="btn btn-secondary btn-sm" onClick={loadHostData} disabled={loading}>
                        {loading ? 'Retrying...' : 'Retry'}
                    </button>
                </div>
            )}

            {currentEvent && queue && (
                <QueueStats
                    currentEvent={currentEvent}
                    activeCount={activeRequests.length}
                    completedCount={requests.filter(r => r.status === 'completed').length}
                    totalCount={requests.length}
                    uniqueSingersCount={uniqueSingersCount}
                    onOpenWeightsModal={(singer = '') => setWeightsModal({ open: true, singer })}
                />
            )}

            {currentEvent && !queue && !loadError && !queueError && (
                <Spinner label="Loading queue..." role="status" />
            )}

            {currentEvent && !queue && !loadError && queueError && (
                <div class="card" role="alert">
                    <p>Could not load the show queue.</p>
                    <button
                        type="button"
                        class="btn btn-secondary btn-sm"
                        onClick={() => {
                            setQueueError(false)
                            refreshQueue()
                        }}
                    >
                        Retry queue
                    </button>
                </div>
            )}

            {(!currentEvent || queue || loadError) && (
                <RequestList
                    tab={tab}
                    setTab={setTab}
                    loading={loading}
                    loadError={loadError}
                    onRetry={loadHostData}
                    currentEvent={currentEvent}
                    currentRound={currentRound}
                    stagingRequests={stagingRequests}
                    activeRequests={activeRequests}
                    historyRequests={historyRequests}
                    singerPerformanceCounts={singerPerformanceCounts}
                    copiedId={mutations.copiedId}
                    onMoveRequest={(index, direction, group) => mutations.moveRequest(index, direction, group, activeRequests)}
                    onUpdateRequestRound={mutations.updateRequestRound}
                    onUpdateStatus={handleUpdateStatus}
                    onDeleteRequest={mutations.deleteRequest}
                    onCopySongInfo={mutations.copySongInfo}
                    onEditRequest={(req) => setEditModalTarget(req)}
                    onOpenWeightsModal={(singer = '') => setWeightsModal({ open: true, singer })}
                    onAdvanceRound={() => mutations.advanceRound(currentRound)}
                    onLaunchShow={onOpenCreateModal}
                    onSelectEventId={onSelectEventId}
                    events={events}
                />
            )}

            <ShowSettingsModal
                isOpen={showSettingsModal}
                onClose={() => setShowSettingsModal(false)}
                currentEvent={currentEvent}
                onSaveSettings={mutations.saveQueueSettings}
                savingSettings={mutations.savingSettings}
                onRotateOpenKJKey={mutations.rotateOpenKJKey}
                rotatingKey={mutations.rotatingKey}
                onEndShow={() => mutations.updateEventStatus('ended')}
                onDeleteEvent={async () => {
                    const ok = await mutations.deleteEvent()
                    if (ok) {
                        setShowSettingsModal(false)
                        onDeletedEvent(currentEvent.id)
                    }
                }}
                deletingEvent={mutations.deletingEvent}
            />

            <AnnouncementModal
                isOpen={showAnnouncementModal}
                onClose={() => setShowAnnouncementModal(false)}
                currentEvent={currentEvent}
                onSaveAnnouncement={mutations.saveAnnouncement}
                updatingFeatures={mutations.updatingFeatures}
            />

            <RestartShowModal
                isOpen={showRestartModal}
                onClose={() => setShowRestartModal(false)}
                currentEvent={currentEvent}
                songbooks={songbooks}
                onRestartShow={mutations.restartShow}
                restarting={mutations.restarting}
            />

            <DeclineRequestModal
                targetRequest={declineModalTarget}
                onClose={() => setDeclineModalTarget(null)}
                onConfirmDecline={mutations.confirmDecline}
                decliningInProgress={mutations.decliningInProgress}
            />

            <EditRequestModal
                targetRequest={editModalTarget}
                onClose={() => setEditModalTarget(null)}
                onConfirmEdit={mutations.editRequestDetails}
                editingInProgress={mutations.editingInProgress}
            />

            <SingerWeightsModal
                isOpen={weightsModal.open}
                onClose={() => setWeightsModal({ open: false, singer: '' })}
                currentEvent={currentEvent}
                requests={requests}
                initialSinger={weightsModal.singer}
                onSaveSingerWeights={mutations.saveSingerWeights}
                savingWeights={mutations.savingWeights}
            />
        </>
    )
}
