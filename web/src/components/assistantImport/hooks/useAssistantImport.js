import { useState, useRef, useEffect, useCallback } from 'preact/hooks'
import { api } from '../../../api/client.js'
import { showToast } from '../../../components/toastState.js'
import { createReferenceWorker } from '../../../api/referenceClient.js'
import { rowsToSongs } from '../../../utils/csvParser.js'
import { parseFolderFilesLocal } from '../../../utils/folderParser.js'
import {
    parseSpreadsheetTextLocal,
    getSpreadsheetLoadedMessage,
    remapColumnsWithWorker,
} from './assistantImportHelpers.js'

export const RESULTS_PAGE_SIZE = 100

export function useAssistantImport({ songbookId, onComplete }) {
    const [importMode, setImportMode] = useState('folder') // 'folder' | 'spreadsheet'
    const [scanning, setScanning] = useState(false)
    const [parsing, setParsing] = useState(false)
    const [files, setFiles] = useState([])
    const [results, setResults] = useState(null)
    const [excluded, setExcluded] = useState(new Set())
    const [visibleResultCount, setVisibleResultCount] = useState(RESULTS_PAGE_SIZE)
    const [pruneMissing, setPruneMissing] = useState(false)
    const [importing, setImporting] = useState(false)
    const [importProgress, setImportProgress] = useState(null)
    const [pattern, setPattern] = useState('auto')
    const [totalSongs, setTotalSongs] = useState(0)
    const [existingJob, setExistingJob] = useState(null)

    // Canonical Reference Audit state
    const refWorkerRef = useRef(null)
    const [auditing, setAuditing] = useState(false)
    const [auditSummary, setAuditSummary] = useState(null)
    const [auditResults, setAuditResults] = useState(null)
    const [invertedDetected, setInvertedDetected] = useState(false)

    // Spreadsheet state
    const [spreadsheetFile, setSpreadsheetFile] = useState(null)
    const [rawRows, setRawRows] = useState([])
    const [headers, setHeaders] = useState([])
    const [softwareProfile, setSoftwareProfile] = useState(null)
    const [columnMapping, setColumnMapping] = useState(null)
    const [showMappingEditor, setShowMappingEditor] = useState(false)

    const folderInputRef = useRef(null)
    const spreadsheetInputRef = useRef(null)
    const parseSequence = useRef(0)

    useEffect(() => {
        api.get(`/songbooks/${songbookId}`).then(book => {
            setTotalSongs(book?.song_count || 0)
        }).catch(() => {})
    }, [songbookId])

    useEffect(() => {
        api.get(`/songbooks/${songbookId}/import-jobs/latest`).then(job => {
            if (job && (job.status === 'queued' || job.status === 'processing')) setExistingJob(job)
        }).catch(() => {})
    }, [songbookId])

    useEffect(() => () => { parseSequence.current += 1 }, [])

    useEffect(() => {
        const worker = createReferenceWorker()
        if (worker) {
            refWorkerRef.current = worker
            worker.init().catch(err => {
                console.error('Failed to init reference worker:', err)
            })
        }
        return () => {
            refWorkerRef.current?.terminate()
            refWorkerRef.current = null
        }
    }, [])

    const auditSequence = useRef(0)

    const applyAuditPayload = useCallback((audit) => {
        if (!audit) return
        setInvertedDetected(Boolean(audit.inverted_columns_detected))
        setAuditSummary(audit.summary || null)
        setAuditResults(audit.results || [])
        if (audit.reference_unavailable) {
            showToast('Reference catalog unavailable — tracks will import as custom', 'info')
        }
    }, [])

    const runAudit = useCallback(async (songs) => {
        const seq = ++auditSequence.current
        if (!refWorkerRef.current || !songs || songs.length === 0) {
            setAuditSummary(null)
            setAuditResults(null)
            setInvertedDetected(false)
            return
        }
        setAuditing(true)
        try {
            const audit = await refWorkerRef.current.auditRows(songs)
            if (seq !== auditSequence.current) return
            applyAuditPayload(audit)
        } catch (err) {
            if (seq === auditSequence.current) console.error('Reference audit failed:', err)
        } finally {
            if (seq === auditSequence.current) setAuditing(false)
        }
    }, [applyAuditPayload])

    const handleSwapColumns = useCallback(async () => {
        if (importMode === 'spreadsheet' && columnMapping) {
            const swappedMapping = {
                ...columnMapping,
                artistIndex: columnMapping.titleIndex,
                titleIndex: columnMapping.artistIndex,
            }
            setColumnMapping(swappedMapping)
            setInvertedDetected(false)
            if (refWorkerRef.current) {
                await remapColumnsWithWorker({
                    worker: refWorkerRef.current,
                    mapping: swappedMapping,
                    parseSequence,
                    auditSequence,
                    setAuditing,
                    setResults,
                    setExcluded,
                    applyAuditPayload,
                    showToast,
                    successMessage: 'Swapped Artist and Title columns',
                })
                return
            }
            if (rawRows.length > 0) {
                const songs = rowsToSongs(rawRows, swappedMapping, true)
                setResults(songs)
                setExcluded(new Set())
                runAudit(songs)
                showToast('Swapped Artist and Title columns')
            }
            return
        }
        if (!results || results.length === 0) return
        const swapped = results.map(r => ({
            ...r,
            artist: r.title,
            title: r.artist,
        }))
        setResults(swapped)
        setInvertedDetected(false)
        runAudit(swapped)
        showToast('Swapped Artist and Title columns')
    }, [importMode, columnMapping, rawRows, results, runAudit, applyAuditPayload])

    const handleApplyAllSuggestions = useCallback(() => {
        if (!results || !auditResults) return
        let appliedCount = 0
        const updated = results.map((r, idx) => {
            const audit = auditResults[idx]
            if (audit && audit.status === 'suggestion') {
                appliedCount++
                return {
                    ...r,
                    artist: audit.suggested_artist || r.artist,
                    title: audit.suggested_title || r.title,
                    genre: audit.genre || r.genre,
                    year: audit.year || r.year,
                    canonical_id: audit.canonical_id,
                    is_custom: false,
                    source: r.source || audit.provider || '',
                    disc: audit.clear_disc ? '' : (audit.disc_code || r.disc || ''),
                }
            }
            return r
        })
        setResults(updated)
        runAudit(updated)
        showToast(`Applied ${appliedCount} catalog suggestions!`)
    }, [results, auditResults, runAudit])

    const handleApplySingleSuggestion = useCallback(async (index) => {
        if (!results || !auditResults) return
        const audit = auditResults[index]
        if (!audit || audit.status !== 'suggestion') return
        const updatedRow = {
            ...results[index],
            artist: audit.suggested_artist || results[index].artist,
            title: audit.suggested_title || results[index].title,
            genre: audit.genre || results[index].genre,
            year: audit.year || results[index].year,
            canonical_id: audit.canonical_id,
            is_custom: false,
            source: results[index].source || audit.provider || '',
            disc: audit.clear_disc ? '' : (audit.disc_code || results[index].disc || ''),
        }
        const updated = [...results]
        updated[index] = updatedRow
        setResults(updated)
        showToast(`Applied suggestion for "${updatedRow.title}"`)

        if (refWorkerRef.current) {
            const seq = auditSequence.current
            try {
                const res = await refWorkerRef.current.auditSingle(index, updatedRow)
                if (seq !== auditSequence.current || !res?.result) return
                const prevStatus = audit.status
                const nextStatus = res.result.status
                setAuditResults(prev => {
                    if (!prev || index >= prev.length) return prev
                    const next = [...prev]
                    next[index] = res.result
                    return next
                })
                if (prevStatus !== nextStatus) {
                    setAuditSummary(prev => {
                        if (!prev) return prev
                        const next = { ...prev }
                        if (prevStatus === 'suggestion') next.suggestions = Math.max(0, next.suggestions - 1)
                        else if (prevStatus === 'verified') next.verified = Math.max(0, next.verified - 1)
                        else next.custom = Math.max(0, next.custom - 1)
                        if (nextStatus === 'suggestion') next.suggestions += 1
                        else if (nextStatus === 'verified') next.verified += 1
                        else next.custom += 1
                        return next
                    })
                }
            } catch (err) {
                console.error('Single-row re-audit failed:', err)
            }
        }
    }, [results, auditResults])

    const parseFiles = useCallback(async (fileList, p) => {
        const sequence = ++parseSequence.current
        const auditSeq = ++auditSequence.current
        setParsing(true)
        setAuditing(true)
        try {
            if (refWorkerRef.current) {
                const out = await refWorkerRef.current.parseFolderAndAudit(fileList, p)
                if (sequence !== parseSequence.current) return
                setResults(out.songs)
                setExcluded(new Set())
                setVisibleResultCount(RESULTS_PAGE_SIZE)
                applyAuditPayload(out)
            } else {
                const parsedSongs = parseFolderFilesLocal(fileList, p)
                if (sequence !== parseSequence.current) return
                setResults(parsedSongs)
                setExcluded(new Set())
                setVisibleResultCount(RESULTS_PAGE_SIZE)
            }
        } catch {
            if (sequence !== parseSequence.current) return
            showToast('Failed to parse files', 'error')
            setResults(null)
            setExcluded(new Set())
            setVisibleResultCount(RESULTS_PAGE_SIZE)
            setAuditSummary(null)
            setAuditResults(null)
        } finally {
            if (sequence === parseSequence.current) setParsing(false)
            if (auditSeq === auditSequence.current) setAuditing(false)
        }
    }, [applyAuditPayload])

    const handlePatternChange = useCallback((nextPattern) => {
        setPattern(nextPattern)
        if (files.length > 0) {
            parseFiles(files, nextPattern)
        }
    }, [files, parseFiles])

    const handleFolderSelect = useCallback(async (e) => {
        const inputFiles = Array.from(e.target.files || [])
        if (inputFiles.length === 0) return
        setScanning(true)
        const fileList = []
        for (const file of inputFiles) {
            if (file.name.match(/\.(mp3|wav|mp4|m4a|zip|cdg)$/i)) {
                fileList.push({
                    name: file.name,
                    path: file.webkitRelativePath || file.name,
                    size: file.size,
                })
            }
        }
        setFiles(fileList)
        setScanning(false)
        await parseFiles(fileList, pattern)
    }, [pattern, parseFiles])

    const handleFolderDrop = useCallback(async (e) => {
        e.preventDefault()
        const items = e.dataTransfer.items
        if (!items) return

        const sequence = ++parseSequence.current
        const isCurrent = () => sequence === parseSequence.current
        setScanning(true)
        const fileList = []

        try {
            const roots = Array.from(items, item => item.webkitGetAsEntry()).filter(Boolean)
            const traverse = async (item) => {
                if (!isCurrent()) return
                if (item.isFile) {
                    const file = await new Promise((resolve, reject) => item.file(resolve, reject))
                    if (file.name.match(/\.(mp3|wav|mp4|m4a|zip|cdg)$/i)) {
                        fileList.push({
                            name: file.name,
                            path: item.fullPath,
                            size: file.size,
                        })
                    }
                } else if (item.isDirectory) {
                    const reader = item.createReader()
                    while (isCurrent()) {
                        const entries = await new Promise((resolve, reject) => reader.readEntries(resolve, reject))
                        if (entries.length === 0) break
                        for (const entry of entries) {
                            await traverse(entry)
                        }
                    }
                }
            }

            for (const entry of roots) await traverse(entry)
        } catch (err) {
            if (isCurrent()) showToast(err.message || 'Failed to scan folder', 'error')
            return
        } finally {
            if (isCurrent()) setScanning(false)
        }
        if (!isCurrent()) return
        setFiles(fileList)
        await parseFiles(fileList, pattern)
    }, [pattern, parseFiles])

    const applySpreadsheetResults = useCallback(({
        rawRows: nextRawRows,
        headers: nextHeaders,
        softwareProfile: nextProfile,
        columnMapping: nextMapping,
        songs,
        isVirtualDJ,
        toastMessage,
        auditPayload,
    }) => {
        if (isVirtualDJ && (!songs || songs.length === 0)) {
            showToast('No karaoke tracks found in VirtualDJ database.xml', 'error')
            return
        }
        setRawRows(nextRawRows)
        setHeaders(nextHeaders)
        setSoftwareProfile(nextProfile)
        setColumnMapping(nextMapping)
        setResults(songs)
        setExcluded(new Set())
        setVisibleResultCount(RESULTS_PAGE_SIZE)
        if (auditPayload) applyAuditPayload(auditPayload)
        showToast(toastMessage)
    }, [applyAuditPayload])

    const handleSpreadsheetFile = useCallback(async (file) => {
        if (!file) return
        const seq = ++parseSequence.current
        const auditSeq = ++auditSequence.current
        setParsing(true)
        setAuditing(true)
        setSpreadsheetFile(file.name)

        try {
            const text = await file.text()
            if (seq !== parseSequence.current) return
            if (refWorkerRef.current) {
                const out = await refWorkerRef.current.parseFileAndAudit(text, file.name)
                if (seq !== parseSequence.current) return
                const songs = out.songs || []
                applySpreadsheetResults({
                    rawRows: [],
                    headers: out.headers || [],
                    softwareProfile: out.softwareProfile || null,
                    columnMapping: out.columnMapping || null,
                    songs,
                    isVirtualDJ: out.isVirtualDJ,
                    toastMessage: getSpreadsheetLoadedMessage(file.name, songs.length, out.isVirtualDJ),
                    auditPayload: out,
                })
                return
            }
            // Fallback for environments without Web Workers
            const parsed = parseSpreadsheetTextLocal(text, file.name)
            applySpreadsheetResults({
                rawRows: parsed.rows,
                headers: parsed.headerRow,
                softwareProfile: parsed.softwareProfile,
                columnMapping: parsed.mapping,
                songs: parsed.songs,
                isVirtualDJ: parsed.isVirtualDJ,
                toastMessage: `Loaded ${parsed.songs.length} songs from ${file.name}`,
                auditPayload: null,
            })
        } catch (err) {
            if (seq !== parseSequence.current) return
            showToast(err.message || 'Failed to parse file', 'error')
            setResults(null)
            setAuditSummary(null)
            setAuditResults(null)
        } finally {
            if (seq === parseSequence.current) setParsing(false)
            if (auditSeq === auditSequence.current) setAuditing(false)
        }
    }, [applySpreadsheetResults])

    const updateMapping = useCallback(async (field, colIndex) => {
        if (!columnMapping) return
        const next = { ...columnMapping, [field]: Number.parseInt(colIndex, 10) }
        setColumnMapping(next)
        if (refWorkerRef.current) {
            const seq = ++parseSequence.current
            const auditSeq = ++auditSequence.current
            setAuditing(true)
            try {
                const out = await refWorkerRef.current.remapAndAudit(next)
                if (seq !== parseSequence.current || auditSeq !== auditSequence.current) return
                setResults(out.songs || [])
                setExcluded(new Set())
                applyAuditPayload(out)
            } catch (err) {
                if (seq === parseSequence.current) showToast(err.message || 'Failed to remap columns', 'error')
            } finally {
                if (auditSeq === auditSequence.current) setAuditing(false)
            }
        } else if (rawRows.length > 0) {
            setResults(rowsToSongs(rawRows, next, true))
            setExcluded(new Set())
        }
    }, [columnMapping, rawRows, applyAuditPayload])

    const toggleExcluded = useCallback((index) => {
        setExcluded(current => {
            const next = new Set(current)
            if (next.has(index)) next.delete(index)
            else next.add(index)
            return next
        })
    }, [])

    const handleResetSource = useCallback(() => {
        parseSequence.current += 1
        auditSequence.current += 1
        setAuditing(false)
        setParsing(false)
        setResults(null)
        setSpreadsheetFile(null)
        setAuditSummary(null)
        setAuditResults(null)
        setInvertedDetected(false)
        setExcluded(new Set())
    }, [])

    const handleImport = useCallback(async () => {
        if (!results) return
        const includedResults = results.flatMap((result, index) =>
            excluded.has(index) ? [] : [{ result, audit: auditResults?.[index] }]
        )
        if (pruneMissing && totalSongs > 0 && includedResults.length < totalSongs * 0.5) {
            if (!confirm(`Warning: You are importing ${includedResults.length} songs, but your catalog currently has ${totalSongs} songs. The missing ${totalSongs - includedResults.length} songs will be marked as "Unavailable" (Soft-Pruned). Are you sure you want to proceed?`)) {
                return
            }
        }
        setImporting(true)
        setImportProgress({ current: 0, total: includedResults.length })
        try {
            const songs = includedResults.map(({ result: r, audit }) => {
                return {
                    ...r,
                    songbook_id: songbookId,
                    canonical_id: r.canonical_id || audit?.canonical_id || '',
                    is_custom: r.is_custom !== undefined ? r.is_custom : (audit ? audit.is_custom : true),
                    source: r.source || audit?.provider || '',
                    disc: r.disc || audit?.disc_code || '',
                    is_available: true
                }
            })

            const manifest = JSON.stringify({ songs, prune_missing: pruneMissing })
            const upload = await api.post(`/songbooks/${songbookId}/import-uploads`, { total_songs: songs.length, size: new Blob([manifest]).size, prune_missing: pruneMissing })
            const uploadResponse = await fetch(upload.upload_url, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: manifest })
            if (!uploadResponse.ok) throw new Error('Manifest upload failed')
            const job = await api.post(`/songbooks/${songbookId}/import-jobs/${upload.job.id}/complete`)
            let status = job
            while (status?.status !== 'completed') {
                if (status?.status === 'failed') throw new Error(status.error || 'Import failed')
                setImportProgress({ current: status?.processed_songs || 0, total: songs.length })
                await new Promise(resolve => setTimeout(resolve, 1500))
                status = await api.get(`/songbooks/${songbookId}/import-jobs/${job.id}`)
            }
            setImportProgress({ current: songs.length, total: songs.length })

            showToast(`Successfully synced ${songs.length} songs`)
            onComplete?.()
        } catch (err) {
            showToast(err.message || 'Import failed', 'error')
        } finally {
            setImporting(false)
            setImportProgress(null)
        }
    }, [results, excluded, pruneMissing, totalSongs, auditResults, songbookId, onComplete])

    return {
        importMode,
        setImportMode,
        scanning,
        parsing,
        files,
        results,
        excluded,
        setExcluded,
        visibleResultCount,
        setVisibleResultCount,
        pruneMissing,
        setPruneMissing,
        importing,
        importProgress,
        pattern,
        setPattern,
        handlePatternChange,
        totalSongs,
        existingJob,
        auditing,
        auditSummary,
        auditResults,
        invertedDetected,
        spreadsheetFile,
        headers,
        softwareProfile,
        columnMapping,
        showMappingEditor,
        setShowMappingEditor,
        folderInputRef,
        spreadsheetInputRef,
        handleFolderSelect,
        handleFolderDrop,
        handleSpreadsheetFile,
        parseFiles,
        updateMapping,
        handleSwapColumns,
        handleApplyAllSuggestions,
        handleApplySingleSuggestion,
        toggleExcluded,
        handleResetSource,
        handleImport,
    }
}
