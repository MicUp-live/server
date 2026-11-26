import { useState, useEffect, useRef, useCallback } from 'preact/hooks'
import { route } from 'preact-router'
import { api } from '../../../api/client.js'
import { showToast } from '../../../components/toastState.js'
import { getShowStatusInfo } from '../../../utils/showStatus.js'

export function useKioskSession({ event, query, setQuery, setSearchResults, setArtistSearchResults, onEventStatusChange }) {
    // Request modal state
    const [selectedSong, setSelectedSong] = useState(null)
    const [singerName, setSingerName] = useState('')
    const [keyChange, setKeyChange] = useState(0)
    const [notes, setNotes] = useState('')
    const [submitting, setSubmitting] = useState(false)

    // Success splash screen state
    const [successSinger, setSuccessSinger] = useState(null)
    const [countdown, setCountdown] = useState(8)

    // Exit PIN modal
    const [showExitModal, setShowExitModal] = useState(false)
    const [pinInput, setPinInput] = useState('')
    const [pinError, setPinError] = useState('')
    const [verifyingPin, setVerifyingPin] = useState(false)

    const resetTimerRef = useRef(null)
    const countdownIntervalRef = useRef(null)

    const handleResetKiosk = useCallback(() => {
        setSuccessSinger(null)
        setSelectedSong(null)
        setSingerName('')
        setKeyChange(0)
        setNotes('')
        setQuery?.('')
        setSearchResults?.([])
        setArtistSearchResults?.([])
        if (countdownIntervalRef.current) clearInterval(countdownIntervalRef.current)
        if (resetTimerRef.current) clearTimeout(resetTimerRef.current)
    }, [setQuery, setSearchResults, setArtistSearchResults])

    // Auto-reset timer after success
    useEffect(() => {
        if (!successSinger) return

        setCountdown(8)
        countdownIntervalRef.current = setInterval(() => {
            setCountdown(prev => {
                if (prev <= 1) {
                    clearInterval(countdownIntervalRef.current)
                    handleResetKiosk()
                    return 0
                }
                return prev - 1
            })
        }, 1000)

        return () => {
            if (countdownIntervalRef.current) clearInterval(countdownIntervalRef.current)
        }
    }, [successSinger, handleResetKiosk])

    // 45-second user idle inactivity reset
    useEffect(() => {
        const isDirty = (query || '').trim() !== '' ||
            selectedSong !== null ||
            singerName.trim() !== '' ||
            notes.trim() !== '' ||
            showExitModal

        if (!isDirty) {
            if (resetTimerRef.current) clearTimeout(resetTimerRef.current)
            return
        }

        const resetIdleTimer = () => {
            if (resetTimerRef.current) clearTimeout(resetTimerRef.current)
            resetTimerRef.current = setTimeout(() => {
                handleResetKiosk()
                setShowExitModal(false)
                setPinInput('')
                setPinError('')
            }, 45000)
        }

        resetIdleTimer()

        const events = ['pointerdown', 'touchstart', 'keydown', 'scroll']
        events.forEach(evt => window.addEventListener(evt, resetIdleTimer, { passive: true }))

        return () => {
            if (resetTimerRef.current) clearTimeout(resetTimerRef.current)
            events.forEach(evt => window.removeEventListener(evt, resetIdleTimer))
        }
    }, [query, selectedSong, singerName, notes, showExitModal, handleResetKiosk])

    const handleSubmitRequest = async (e) => {
        e?.preventDefault()
        const statusInfo = getShowStatusInfo(event)
        if (statusInfo.isPaused || statusInfo.isEnded) {
            showToast(statusInfo.requestBlockedMessage, 'info')
            return
        }
        if (!singerName.trim()) {
            showToast('Please enter your name to sing!', 'error')
            return
        }
        if (!event?.id || !selectedSong) return

        setSubmitting(true)
        try {
            await api.post(`/events/${event.id}/requests`, {
                song_title: selectedSong.title || selectedSong.label,
                song_artist: selectedSong.artist,
                singer_name: singerName.trim(),
                key_change: event?.allow_key_change !== false ? keyChange : 0,
                notes: notes.trim(),
            })

            setSuccessSinger(singerName.trim())
            setSelectedSong(null)
        } catch (err) {
            const detail = err.data?.detail || err.message || 'Could not submit request'
            if (err.status === 410) {
                onEventStatusChange?.('ended')
                showToast(getShowStatusInfo('ended').requestBlockedMessage, 'info')
            } else if (err.status === 409 && /paused/i.test(detail)) {
                onEventStatusChange?.('paused')
                showToast(getShowStatusInfo('paused').requestBlockedMessage, 'info')
            } else {
                showToast(detail, 'error')
            }
        } finally {
            setSubmitting(false)
        }
    }

    const handleExitKiosk = async () => {
        if (!event?.id) return
        setVerifyingPin(true)
        setPinError('')
        try {
            await api.post(`/events/${event.id}/kiosk/verify-pin`, { pin: pinInput.trim() })
            setShowExitModal(false)
            route('/')
        } catch (err) {
            setPinError(err.data?.message || err.message || 'Incorrect PIN. Ask host for assistance.')
        } finally {
            setVerifyingPin(false)
        }
    }

    return {
        selectedSong,
        setSelectedSong,
        singerName,
        setSingerName,
        keyChange,
        setKeyChange,
        notes,
        setNotes,
        submitting,
        successSinger,
        countdown,
        showExitModal,
        setShowExitModal,
        pinInput,
        setPinInput,
        pinError,
        setPinError,
        verifyingPin,
        handleResetKiosk,
        handleSubmitRequest,
        handleExitKiosk,
    }
}
