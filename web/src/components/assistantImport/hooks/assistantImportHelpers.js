import {
    detectDelimiter,
    parseDelimitedText,
    detectSoftwareProfile,
    rowsToSongs,
} from '../../../utils/csvParser.js'
import { isVirtualDJDatabaseXML, parseVirtualDJDatabase } from '../../../utils/virtualDjParser.js'

/**
 * Parses spreadsheet or VirtualDJ XML text locally when the Web Worker is unavailable.
 */
export function parseSpreadsheetTextLocal(text, fileName) {
    if (isVirtualDJDatabaseXML(text, fileName)) {
        const parsed = parseVirtualDJDatabase(text)
        return {
            ...parsed,
            isVirtualDJ: true,
            softwareProfile: { id: 'virtualdj_xml', name: 'VirtualDJ (database.xml)' },
        }
    }
    if (fileName.toLowerCase().endsWith('.xml')) {
        throw new Error('Unsupported XML file. Please select a VirtualDJ database.xml file.')
    }
    const delim = detectDelimiter(text)
    const rows = parseDelimitedText(text, delim)
    if (rows.length === 0) throw new Error('The selected file appears to be empty')
    const profile = detectSoftwareProfile(rows[0], delim)
    const mapping = profile.columnMapping(rows[0])
    return {
        rows,
        headerRow: rows[0],
        mapping,
        songs: rowsToSongs(rows, mapping, true),
        isVirtualDJ: false,
        softwareProfile: null,
    }
}

/**
 * Builds the toast notification message after loading a spreadsheet or VirtualDJ file.
 */
export function getSpreadsheetLoadedMessage(fileName, songCount, isVirtualDJ) {
    if (isVirtualDJ) {
        return `Loaded ${songCount} karaoke songs from VirtualDJ database.xml`
    }
    return `Loaded ${songCount} songs from ${fileName}`
}

/**
 * Remaps spreadsheet columns via the reference worker and updates hook state if still current.
 */
export async function remapColumnsWithWorker({
    worker,
    mapping,
    parseSequence,
    auditSequence,
    setAuditing,
    setResults,
    setExcluded,
    applyAuditPayload,
    showToast,
    successMessage,
}) {
    const seq = ++parseSequence.current
    const auditSeq = ++auditSequence.current
    setAuditing(true)
    try {
        const out = await worker.remapAndAudit(mapping)
        if (seq !== parseSequence.current || auditSeq !== auditSequence.current) return
        setResults(out.songs)
        setExcluded(new Set())
        applyAuditPayload(out)
        if (successMessage) showToast(successMessage)
    } catch (err) {
        if (seq === parseSequence.current) showToast(err.message || 'Failed to remap columns', 'error')
    } finally {
        if (auditSeq === auditSequence.current) setAuditing(false)
    }
}
