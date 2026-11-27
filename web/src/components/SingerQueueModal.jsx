import { useState, useEffect } from 'preact/hooks'
import { useSingerQueue } from './singerQueue/hooks/useSingerQueue.js'
import { SingerQueueHeader } from './singerQueue/components/SingerQueueHeader.jsx'
import { SingerScreenNameBanner } from './singerQueue/components/SingerScreenNameBanner.jsx'
import { SingerDeclinedRequests } from './singerQueue/components/SingerDeclinedRequests.jsx'
import { SingerActiveRequests } from './singerQueue/components/SingerActiveRequests.jsx'
import { ReplaceSongModal } from './singerQueue/components/ReplaceSongModal.jsx'
import { SingerSungRequests } from './singerQueue/components/SingerSungRequests.jsx'
import { SingerPublicRotation } from './singerQueue/components/SingerPublicRotation.jsx'
import { EditScreenNameModal } from './EditScreenNameModal.jsx'
import { ShowStatusBanner } from './ShowStatusBanner.jsx'
import { getShowStatusInfo } from '../utils/showStatus.js'

export function SingerQueueModal({
    songbookId,
    songbookName,
    eventId,
    eventName,
    eventStatus,
    venueName,
    rotationMode,
    autoAccept,
    allowSongReplacement = false,
    allowKeyChange = true,
    allowWriteIns = false,
    isOpen,
    onClose,
    onEventJoined,
    onEventStatusChange,
    onReRequest,
}) {
    const [replaceTarget, setReplaceTarget] = useState(null)
    useEffect(() => {
        if (!isOpen) setReplaceTarget(null)
    }, [isOpen])
    const {
        screenName,
        setScreenNameState,
        showScreenNameModal,
        setShowScreenNameModal,
        queue,
        loading,
        currentRound,
        eventStatus: liveEventStatus,
        allowSongReplacement: liveAllowSongReplacement,
        noEvent,
        joinCodeInput,
        setJoinCodeInput,
        joining,
        alertsEnabled,
        toggleAlerts,
        myRequests,
        setMyRequests,
        activeRequests,
        completedRequests,
        declinedRequests,
        myUpNext,
        dismissDeclinedRequest,
        cancelMyRequest,
        replaceMyRequestSong,
        handleJoin,
    } = useSingerQueue({ songbookId, eventId, eventStatus, allowSongReplacement, isOpen, onClose, onEventJoined, onEventStatusChange })

    if (!isOpen) return null

    const effectiveStatus = liveEventStatus || eventStatus
    const statusInfo = getShowStatusInfo(effectiveStatus)
    const displayTitle = venueName ? `${venueName} — ${eventName || 'Show'}` : (eventName || songbookName || 'Live Queue')

    return (
        <dialog
            open
            class="modal-overlay"
            aria-modal="true"
            aria-labelledby="singer-queue-title"
            onCancel={(e) => {
                e.preventDefault()
                onClose()
            }}
            style={{
                border: 'none',
                margin: 0,
                maxWidth: 'none',
                maxHeight: 'none',
                width: '100%',
                height: '100%',
            }}
        >
            <div class="modal-sheet modal-sheet-wide">
                <SingerQueueHeader
                    displayTitle={displayTitle}
                    eventStatus={effectiveStatus}
                    alertsEnabled={alertsEnabled}
                    onToggleAlerts={toggleAlerts}
                    onClose={onClose}
                    myUpNext={myUpNext}
                />

                {/* Body */}
                <div style={{ flex: 1, overflowY: 'auto', padding: 'var(--space-4)' }}>
                    <ShowStatusBanner status={effectiveStatus} compact />

                    <SingerScreenNameBanner
                        screenName={screenName}
                        onOpenEdit={() => setShowScreenNameModal(true)}
                    />

                    <SingerDeclinedRequests
                        declinedRequests={declinedRequests}
                        onDismiss={dismissDeclinedRequest}
                    />

                    <SingerActiveRequests
                        activeRequests={activeRequests}
                        queue={queue}
                        currentRound={currentRound}
                        eventStatus={effectiveStatus}
                        allowSongReplacement={liveAllowSongReplacement}
                        onReplaceSong={(req) => setReplaceTarget(req)}
                        onCancelRequest={cancelMyRequest}
                    />

                    <SingerSungRequests
                        completedRequests={completedRequests}
                        eventStatus={effectiveStatus}
                        onRequestsChange={() => setMyRequests([...myRequests])}
                        onReRequest={onReRequest}
                    />

                    <SingerPublicRotation
                        loading={loading}
                        noEvent={noEvent}
                        queue={queue}
                        currentRound={currentRound}
                        eventStatus={effectiveStatus}
                        joinCodeInput={joinCodeInput}
                        onJoinCodeInput={setJoinCodeInput}
                        onJoin={handleJoin}
                        joining={joining}
                    />
                </div>

                {/* Footer */}
                <div style={{
                    padding: 'var(--space-3) var(--space-5)',
                    borderTop: '1px solid var(--color-border)',
                    background: 'var(--color-surface-alt)',
                    display: 'flex',
                    justifyContent: 'space-between',
                    alignItems: 'center',
                }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--space-3)' }}>
                        <span style={{ fontSize: 'var(--text-xs)', color: 'var(--color-text-dim)' }}>
                            {statusInfo.queueFooterMessage}
                        </span>
                    </div>
                    <button type="button" class="btn btn-secondary btn-sm" onClick={onClose}>
                        Close
                    </button>
                </div>
            </div>

            <EditScreenNameModal
                isOpen={showScreenNameModal}
                onClose={() => setShowScreenNameModal(false)}
                onUpdated={(newName) => setScreenNameState(newName)}
            />

            <ReplaceSongModal
                request={replaceTarget}
                allowKeyChange={allowKeyChange}
                allowWriteIns={allowWriteIns}
                onClose={() => setReplaceTarget(null)}
                onSubmit={replaceMyRequestSong}
            />
        </dialog>
    )
}
