import { useState, useEffect, useRef, useCallback } from 'preact/hooks'
import { useQueuePolling } from '../hooks/useQueuePolling.js'
import {
    StageAnnouncementBanner,
    StageBody,
    StageHeader,
    StageStyles,
} from './StageSections.jsx'
import {
    buildJoinUrl,
    clearShakeTimeout,
    getStageOrigin,
    mapStageEvent,
    selectStageSingers,
    toggleStageFullscreen,
    updateRotationShake,
} from './stageHelpers.js'

export function Stage({ id }) {
    const [queue, setQueue] = useState([])
    const [event, setEvent] = useState(null)
    const [currentTime, setCurrentTime] = useState('')
    const [showQrModal, setShowQrModal] = useState(false)
    const [isShaking, setIsShaking] = useState(false)
    const [hasLoaded, setHasLoaded] = useState(false)
    const prevRotationRef = useRef(null)
    const shakeTimeoutRef = useRef(null)

    useEffect(() => {
        setHasLoaded(false)
    }, [id])

    // Live clock
    useEffect(() => {
        const updateClock = () => {
            const now = new Date()
            setCurrentTime(now.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }))
        }
        updateClock()
        const interval = setInterval(updateClock, 1000)
        return () => clearInterval(interval)
    }, [])

    const handleQueueData = useCallback((data) => {
        if (!data) return

        const newQueue = data.queue || []
        updateRotationShake(prevRotationRef, shakeTimeoutRef, newQueue, setIsShaking)

        setQueue(newQueue)
        setEvent(mapStageEvent(data))
        setHasLoaded(true)
    }, [])

    const { loading } = useQueuePolling({
        endpoint: id ? `/events/${id}/queue` : null,
        interval: 5000,
        onData: handleQueueData,
        onError: (err) => {
            console.warn('Stage poll error', err)
            setHasLoaded(true)
        },
    })

    useEffect(() => () => clearShakeTimeout(shakeTimeoutRef), [])

    const { currentSinger, upcomingQueue } = selectStageSingers(queue)
    const joinCode = event?.join_code || ''
    const joinUrl = buildJoinUrl(getStageOrigin(), joinCode, event?.id, id)

    return (
        <div class="stage-view" style={{
            minHeight: '100vh',
            background: 'radial-gradient(ellipse at 50% 10%, #1c1328 0%, #0b0b10 70%)',
            color: 'var(--color-text)',
            display: 'flex',
            flexDirection: 'column',
            padding: 'var(--space-6) var(--space-8)',
            boxSizing: 'border-box',
            fontFamily: 'var(--font-sans)',
        }}>
            <StageStyles />

            <StageHeader
                event={event}
                isShaking={isShaking}
                queueLength={queue.length}
                currentTime={currentTime}
                onShowQr={() => setShowQrModal(true)}
                onToggleFullscreen={toggleStageFullscreen}
            />

            <StageBody
                isLoading={!hasLoaded && loading}
                currentSinger={currentSinger}
                upcomingQueue={upcomingQueue}
                isShaking={isShaking}
                joinUrl={joinUrl}
                joinCode={joinCode}
                showQrModal={showQrModal}
                onToggleEnlarge={setShowQrModal}
            />

            <StageAnnouncementBanner announcement={event?.announcement} />
        </div>
    )
}
