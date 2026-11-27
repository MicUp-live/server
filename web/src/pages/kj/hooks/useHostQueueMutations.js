import { useState, useCallback, useRef } from 'preact/hooks'
import { api } from '../../../api/client.js'
import { showToast } from '../../../components/Toast.jsx'

export function useHostQueueMutations({
    selectedEventId,
    currentEvent,
    setRequests,
    setEvents,
    refreshQueue,
}) {
    const [copiedId, setCopiedId] = useState(null)
    const [decliningInProgress, setDecliningInProgress] = useState(false)
    const [editingInProgress, setEditingInProgress] = useState(false)
    const [restarting, setRestarting] = useState(false)
    const [deletingEvent, setDeletingEvent] = useState(false)
    const [rotatingKey, setRotatingKey] = useState(false)
    const [updatingFeatures, setUpdatingFeatures] = useState(false)
    const [savingSettings, setSavingSettings] = useState(false)
    const [savingWeights, setSavingWeights] = useState(false)
    const deferringIdsRef = useRef(new Set())

    const editRequestDetails = useCallback(async (targetRequest, updates) => {
        if (!targetRequest || !selectedEventId) return false
        const singerName = updates.singer_name?.trim()
        const songTitle = updates.song_title?.trim()
        const songArtist = updates.song_artist?.trim()

        if (!singerName) {
            showToast('Singer name is required', 'error')
            return false
        }
        if (!songTitle) {
            showToast('Song title is required', 'error')
            return false
        }

        setEditingInProgress(true)
        try {
            const endpoint = `/events/${selectedEventId}/requests/${targetRequest.id}`
            const payload = {
                singer_name: singerName,
                song_title: songTitle,
                song_artist: songArtist || '',
                key_change: Number.isInteger(updates.key_change) ? updates.key_change : 0,
                notes: (updates.notes || '').trim(),
            }
            if (updates.song_id !== undefined) {
                payload.song_id = updates.song_id.trim()
            }
            const updated = await api.patch(endpoint, payload)
            setRequests(prev => prev.map(r => r.id === targetRequest.id ? updated : r))
            showToast(`Updated song request for ${updated.singer_name}`)
            refreshQueue?.(false)
            return true
        } catch {
            showToast('Failed to update song request details', 'error')
            return false
        } finally {
            setEditingInProgress(false)
        }
    }, [selectedEventId, setRequests, refreshQueue])

    const updateStatus = useCallback(async (request, newStatus, reasonOverride) => {
        if (!selectedEventId) return
        try {
            const endpoint = `/events/${selectedEventId}/requests/${request.id}`
            const payload = {
                status: newStatus,
                ...(reasonOverride ? { decline_reason: reasonOverride } : {}),
            }
            const updated = await api.patch(endpoint, payload)
            setRequests(prev => prev.map(r => r.id === request.id ? updated : r))
            const statusLabel = newStatus === 'up_next' ? 'Called to Stage' : newStatus.replace('_', ' ')
            showToast(`Marked ${request.singer_name} as ${statusLabel}`)
            refreshQueue?.(false)
            return updated
        } catch {
            showToast('Failed to update request status', 'error')
        }
    }, [selectedEventId, setRequests, refreshQueue])

    const confirmDecline = useCallback(async (targetRequest, reason) => {
        if (!targetRequest || !selectedEventId) return false
        const cleanReason = reason.trim()
        if (!cleanReason) {
            showToast('A decline reason is required', 'error')
            return false
        }
        setDecliningInProgress(true)
        try {
            const endpoint = `/events/${selectedEventId}/requests/${targetRequest.id}`
            const updated = await api.patch(endpoint, { status: 'rejected', decline_reason: cleanReason })
            setRequests(prev => prev.map(r => r.id === targetRequest.id ? updated : r))
            showToast(`Declined request from ${targetRequest.singer_name}`)
            refreshQueue?.(false)
            return true
        } catch {
            showToast('Failed to decline request', 'error')
            return false
        } finally {
            setDecliningInProgress(false)
        }
    }, [selectedEventId, setRequests, refreshQueue])

    const deleteRequest = useCallback(async (request) => {
        if (!selectedEventId) return
        if (!confirm(`Remove request from ${request.singer_name}?`)) return
        try {
            const endpoint = `/events/${selectedEventId}/requests/${request.id}`
            await api.delete(endpoint)
            setRequests(prev => prev.filter(r => r.id !== request.id))
            showToast('Request removed')
            refreshQueue?.(false)
        } catch {
            showToast('Failed to delete request', 'error')
        }
    }, [selectedEventId, setRequests, refreshQueue])

    const deferRequest = useCallback(async (request) => {
        if (!selectedEventId || !request || deferringIdsRef.current.has(request.id)) return
        deferringIdsRef.current.add(request.id)
        try {
            const headers = typeof crypto !== 'undefined' && crypto.randomUUID
                ? { 'Idempotency-Key': crypto.randomUUID() }
                : undefined
            const updated = await api.post(`/events/${selectedEventId}/requests/${request.id}/defer`, {}, headers)
            setRequests(prev => prev.map(r => r.id === request.id ? { ...r, ...updated } : r))
            showToast(`Deferred ${request.singer_name} down 3 spots`)
            refreshQueue?.(true)
            return updated
        } catch (err) {
            showToast(err?.data?.message || err?.message || 'Failed to defer request', 'error')
        } finally {
            deferringIdsRef.current.delete(request.id)
        }
    }, [selectedEventId, setRequests, refreshQueue])

    const moveRequest = useCallback(async (index, direction, group = null, activeRequests = []) => {
        if (!selectedEventId) return
        const activeList = [...activeRequests]
        let targetIndex = index + direction
        if (group && group.requests) {
            const inGroupIdx = group.requests.findIndex(r => r.id === activeList[index]?.id)
            if (inGroupIdx < 0) return
            const targetInGroupIdx = inGroupIdx + direction
            if (targetInGroupIdx < 0 || targetInGroupIdx >= group.requests.length) return
            const targetReq = group.requests[targetInGroupIdx]
            targetIndex = activeList.findIndex(r => r.id === targetReq.id)
            if (targetIndex < 0) return
        } else {
            if (targetIndex < 0 || targetIndex >= activeList.length) return
        }

        const temp = activeList[index]
        activeList[index] = activeList[targetIndex]
        activeList[targetIndex] = temp

        const orderedIds = activeList.map(r => r.id)
        try {
            const endpoint = `/events/${selectedEventId}/requests/reorder`
            await api.post(endpoint, { order: orderedIds })
            // Re-rank locally
            activeList.forEach((req, idx) => {
                req.rank = idx + 1
            })
            setRequests(prev => {
                const map = new Map(activeList.map(r => [r.id, r]))
                return prev.map(r => map.get(r.id) || r).sort((a, b) => {
                    const rA = a.round || 1
                    const rB = b.round || 1
                    if (rA !== rB) return rA - rB
                    return (a.rank || 0) - (b.rank || 0)
                })
            })
            showToast('Queue reordered')
            refreshQueue?.(false)
        } catch {
            showToast('Failed to reorder queue', 'error')
            refreshQueue?.(true)
        }
    }, [selectedEventId, setRequests, refreshQueue])

    const updateRequestRound = useCallback(async (request, newRound) => {
        if (!selectedEventId || !request) return
        try {
            const endpoint = `/events/${selectedEventId}/requests/${request.id}`
            const updated = await api.patch(endpoint, { round: newRound })
            setRequests(prev => prev.map(r => r.id === request.id ? { ...r, ...updated, round: newRound } : r))
            showToast(`Moved ${request.singer_name} to Round ${newRound}`)
            refreshQueue?.(false)
        } catch {
            showToast('Failed to change request round', 'error')
        }
    }, [selectedEventId, setRequests, refreshQueue])

    const updateEventStatus = useCallback(async (newStatus) => {
        if (!currentEvent) return
        try {
            const updated = await api.patch(`/events/${currentEvent.id}`, { status: newStatus })
            setEvents(prev => prev.map(e => e.id === currentEvent.id ? updated : e))
            showToast(`Show status updated: ${newStatus}`)
            refreshQueue?.(true)
        } catch {
            showToast('Failed to update show status', 'error')
        }
    }, [currentEvent, setEvents, refreshQueue])

    const restartShow = useCallback(async (clearQueue) => {
        if (!currentEvent) return false
        setRestarting(true)
        try {
            const updated = await api.post(`/events/${currentEvent.id}/restart`, { clear_queue: clearQueue })
            setEvents(prev => prev.map(e => e.id === currentEvent.id ? updated : e))
            if (clearQueue) {
                setRequests([])
            }
            showToast(`Show "${currentEvent.name}" restarted and ready for singers!`)
            refreshQueue?.(true)
            return true
        } catch (err) {
            showToast(err.message || 'Failed to restart show', 'error')
            return false
        } finally {
            setRestarting(false)
        }
    }, [currentEvent, setEvents, setRequests, refreshQueue])

    const deleteEvent = useCallback(async () => {
        if (!currentEvent) return false
        if (!confirm(`Are you sure you want to permanently delete "${currentEvent.name}"? This cannot be undone.`)) {
            return false
        }
        setDeletingEvent(true)
        try {
            await api.delete(`/events/${currentEvent.id}`)
            setEvents(prev => prev.filter(e => e.id !== currentEvent.id))
            setRequests([])
            showToast(`Deleted "${currentEvent.name}"`)
            return true
        } catch (err) {
            showToast(err.message || 'Failed to delete event', 'error')
            return false
        } finally {
            setDeletingEvent(false)
        }
    }, [currentEvent, setEvents, setRequests])

    const rotateOpenKJKey = useCallback(async () => {
        if (!currentEvent) return
        setRotatingKey(true)
        try {
            const res = await api.post(`/events/${currentEvent.id}/openkj-key/rotate`)
            setEvents(prev => prev.map(e => e.id === currentEvent.id ? { ...e, openkj_key: res.openkj_key } : e))
            showToast('Rotated OpenKJ Venue Key!')
        } catch (err) {
            showToast(err.message || 'Failed to rotate OpenKJ key', 'error')
        } finally {
            setRotatingKey(false)
        }
    }, [currentEvent, setEvents])

    const saveAnnouncement = useCallback(async (text) => {
        if (!currentEvent) return false
        setUpdatingFeatures(true)
        try {
            const cleanText = text.trim()
            await api.patch(`/events/${currentEvent.id}`, { announcement: cleanText })
            setEvents(prev => prev.map(ev => ev.id === currentEvent.id ? { ...ev, announcement: cleanText } : ev))
            showToast(cleanText ? 'Shout-Out announcement broadcast live! 📢' : 'Announcement cleared')
            return true
        } catch {
            showToast('Failed to broadcast announcement', 'error')
            return false
        } finally {
            setUpdatingFeatures(false)
        }
    }, [currentEvent, setEvents])

    const saveQueueSettings = useCallback(async (settingsPayload) => {
        if (!currentEvent) return false
        setSavingSettings(true)
        try {
            const updated = await api.patch(`/events/${currentEvent.id}`, settingsPayload)
            setEvents(prev => prev.map(e => e.id === currentEvent.id ? updated : e))
            showToast('Queue settings updated!')
            refreshQueue?.(true)
            return true
        } catch (err) {
            showToast(err.message || 'Failed to save show settings', 'error')
            return false
        } finally {
            setSavingSettings(false)
        }
    }, [currentEvent, setEvents, refreshQueue])

    const saveSingerWeights = useCallback(async (singerWeights) => {
        if (!currentEvent) return false
        setSavingWeights(true)
        try {
            const updated = await api.patch(`/events/${currentEvent.id}`, { singer_weights: singerWeights })
            setEvents(prev => prev.map(e => e.id === currentEvent.id ? updated : e))
            const count = Object.keys(updated?.singer_weights || {}).length
            showToast(count > 0 ? `Saved private weights for ${count} singer${count === 1 ? '' : 's'}` : 'Cleared private singer weights')
            return true
        } catch (err) {
            showToast(err.message || 'Failed to save singer weights', 'error')
            return false
        } finally {
            setSavingWeights(false)
        }
    }, [currentEvent, setEvents])

    const advanceRound = useCallback(async (currentRound) => {
        if (!currentEvent) return
        const nextRound = currentRound + 1
        try {
            const updated = await api.patch(`/events/${currentEvent.id}`, { current_round: nextRound })
            setEvents(prev => prev.map(e => e.id === currentEvent.id ? updated : e))
            showToast(`Advanced to Round ${nextRound}! 🔄`)
            refreshQueue?.(true)
        } catch {
            showToast('Failed to advance round', 'error')
        }
    }, [currentEvent, setEvents, refreshQueue])

    const copySongInfo = useCallback(async (request) => {
        const text = request.song_artist 
            ? `${request.song_artist} - ${request.song_title}` 
            : request.song_title
        try {
            await navigator.clipboard.writeText(text)
            setCopiedId(request.id)
            setTimeout(() => setCopiedId(null), 2000)
            showToast(`Copied to clipboard: "${text}"`)
        } catch {
            showToast('Failed to copy', 'error')
        }
    }, [])

    const copySingerLink = useCallback(async (joinCode) => {
        const origin = typeof window !== 'undefined' ? window.location.origin : 'https://micup.live'
        const link = `${origin}/e/${joinCode}`
        try {
            await navigator.clipboard.writeText(link)
            showToast(`Singer link copied: ${link}`)
        } catch {
            showToast(`Join URL: ${link}`)
        }
    }, [])

    return {
        copiedId,
        decliningInProgress,
        editingInProgress,
        restarting,
        deletingEvent,
        rotatingKey,
        updatingFeatures,
        savingSettings,
        savingWeights,
        editRequestDetails,
        updateStatus,
        confirmDecline,
        deleteRequest,
        deferRequest,
        moveRequest,
        updateRequestRound,
        updateEventStatus,
        restartShow,
        deleteEvent,
        rotateOpenKJKey,
        saveAnnouncement,
        saveQueueSettings,
        saveSingerWeights,
        advanceRound,
        copySongInfo,
        copySingerLink,
    }
}
