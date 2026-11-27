export function getStageOrigin() {
    return typeof window !== 'undefined' ? window.location.origin : 'https://micup.live'
}

export function buildJoinUrl(origin, joinCode, eventId, id) {
    if (joinCode) return `${origin}/e/${joinCode}`
    if (eventId) return `${origin}/e/${eventId}`
    if (id) return `${origin}/book/${id}`
    return `${origin}`
}

export function buildRotationKey(queue) {
    const current = queue.find(q => q.status === 'up_next') || queue[0] || null
    const currentId = current?.id || 'none'
    const topIds = queue.slice(0, 3).map(q => q.id).join(',')
    return `${currentId}:${queue.length}:${topIds}`
}

export function mapStageEvent(data) {
    return {
        id: data.event_id,
        name: data.event_name,
        venue_name: data.venue_name,
        join_code: data.join_code,
        status: data.event_status,
        tip_url: data.tip_url,
        announcement: data.announcement,
    }
}

export function clearShakeTimeout(shakeTimeoutRef) {
    if (shakeTimeoutRef.current) {
        clearTimeout(shakeTimeoutRef.current)
        shakeTimeoutRef.current = null
    }
}

export function updateRotationShake(prevRotationRef, shakeTimeoutRef, queue, setIsShaking) {
    const newRotationKey = buildRotationKey(queue)
    const prevKey = prevRotationRef.current
    prevRotationRef.current = newRotationKey
    if (prevKey === null || prevKey === newRotationKey) return

    setIsShaking(true)
    clearShakeTimeout(shakeTimeoutRef)
    shakeTimeoutRef.current = setTimeout(() => setIsShaking(false), 900)
}

export function selectStageSingers(queue) {
    const currentSinger = queue.find(q => q.status === 'up_next') || queue[0] || null
    const upcomingQueue = currentSinger ? queue.filter(q => q.id !== currentSinger.id) : queue
    return { currentSinger, upcomingQueue }
}

export function toggleStageFullscreen() {
    if (!document.fullscreenElement) {
        document.documentElement.requestFullscreen().catch(() => {})
        return
    }
    document.exitFullscreen().catch(() => {})
}
