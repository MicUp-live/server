// Client wrapper for managing the referenceWorker lifecycle.

export function createReferenceWorker() {
    if (typeof window === 'undefined' || typeof Worker === 'undefined') {
        return null;
    }

    const worker = new Worker(new URL('../search/referenceWorker.js', import.meta.url), { type: 'module' });
    let nextId = 1;
    const pendingRequests = new Map();

    const send = (type, payload = {}) => new Promise((resolve, reject) => {
        const id = nextId++;
        pendingRequests.set(id, { resolve, reject });
        worker.postMessage({ type, id, ...payload });
    });

    worker.onmessage = (e) => {
        const { type, id, ...payload } = e.data;
        if (pendingRequests.has(id)) {
            const { resolve, reject } = pendingRequests.get(id);
            pendingRequests.delete(id);
            if (type === 'ERROR') {
                reject(new Error(payload.error || 'Reference worker error'));
            } else {
                resolve(payload);
            }
        }
    };

    worker.onerror = (err) => {
        for (const { reject } of pendingRequests.values()) {
            reject(err);
        }
        pendingRequests.clear();
    };

    return {
        init(options = {}) {
            return send('INIT', options);
        },
        auditRows(rows) {
            return send('AUDIT_ROWS', { rows });
        },
        auditSingle(index, row) {
            return send('AUDIT_SINGLE', { index, row });
        },
        parseFolderAndAudit(files, pattern) {
            return send('PARSE_FOLDER_AND_AUDIT', { files, pattern });
        },
        parseFileAndAudit(text, fileName) {
            return send('PARSE_FILE_AND_AUDIT', { text, fileName });
        },
        remapAndAudit(columnMapping) {
            return send('REMAP_AND_AUDIT', { columnMapping });
        },
        terminate() {
            worker.terminate();
            pendingRequests.clear();
        },
    };
}
