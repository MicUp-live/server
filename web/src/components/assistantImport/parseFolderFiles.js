import { api } from '../../api/client.js'

const MAX_FILES_PER_REQUEST = 1_000
// Leave room for Lambda's proxy envelope and the larger parsed response.
const MAX_REQUEST_BYTES = 512 * 1024

export async function parseFolderFiles(songbookId, fileList, pattern, isCurrent) {
    const path = `/songbooks/${songbookId}/parse-files`
    const encoder = new TextEncoder()
    const baseBytes = encoder.encode(JSON.stringify({ files: [], pattern })).length
    const results = []
    let files = []
    let bytes = baseBytes

    const parseBatch = async () => {
        const data = await api.post(path, { files, pattern })
        if (!Array.isArray(data?.results) || data.results.length !== files.length) {
            throw new Error('Incomplete folder parse response')
        }
        for (const result of data.results) results.push(result)
        files = []
        bytes = baseBytes
    }

    for (const { name, path: filePath, size } of fileList) {
        const file = { name, path: filePath || name, size }
        const fileBytes = encoder.encode(JSON.stringify(file)).length
        if (baseBytes + fileBytes > MAX_REQUEST_BYTES) {
            throw new Error('A file path is too long to parse')
        }
        if (files.length && (files.length === MAX_FILES_PER_REQUEST || bytes + fileBytes + 1 > MAX_REQUEST_BYTES)) {
            await parseBatch()
            if (!isCurrent()) return null
        }
        bytes += fileBytes + (files.length ? 1 : 0)
        files.push(file)
    }
    if (files.length) await parseBatch()
    return isCurrent() ? results : null
}
