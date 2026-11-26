export function ImportSourcePicker({
    importMode = 'folder',
    onSelectMode,
    folderInputRef,
    spreadsheetInputRef,
    onFolderSelect,
    onFolderDrop,
    onSpreadsheetFile,
    onCancel,
}) {
    const handleDropzoneKeyDown = (e, ref) => {
        if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault()
            ref.current?.click()
        }
    }

    return (
        <div>
            {/* Segmented Mode Switcher */}
            <div class="assistant-mode-tabs" role="group" aria-label="Import source type">
                <button
                    type="button"
                    aria-pressed={importMode === 'folder'}
                    class={`assistant-mode-tab ${importMode === 'folder' ? 'assistant-mode-tab--active' : ''}`}
                    onClick={() => onSelectMode('folder')}
                >
                    <span aria-hidden="true">📁</span>
                    <span>Folder Scan</span>
                </button>
                <button
                    type="button"
                    aria-pressed={importMode === 'spreadsheet'}
                    class={`assistant-mode-tab ${importMode === 'spreadsheet' ? 'assistant-mode-tab--active' : ''}`}
                    onClick={() => onSelectMode('spreadsheet')}
                >
                    <span aria-hidden="true">📊</span>
                    <span>File Upload</span>
                </button>
            </div>

            {importMode === 'folder' ? (
                <div>
                    <div
                        class="assistant-dropzone"
                        role="button"
                        tabIndex={0}
                        onDragOver={(e) => e.preventDefault()}
                        onDrop={onFolderDrop}
                        onClick={() => folderInputRef.current?.click()}
                        onKeyDown={(e) => handleDropzoneKeyDown(e, folderInputRef)}
                    >
                        <span class="assistant-dropzone-icon" aria-hidden="true">📂</span>
                        <p class="assistant-dropzone-title">Select karaoke folder</p>
                        <p class="assistant-dropzone-hint">
                            Drop a folder here or <span class="assistant-dropzone-link">browse your device</span>
                        </p>
                    </div>
                    <input
                        type="file"
                        ref={folderInputRef}
                        webkitdirectory
                        directory
                        multiple
                        onChange={onFolderSelect}
                        style={{ display: 'none' }}
                    />

                    <div class="assistant-trust-strip">
                        <span aria-hidden="true">🛡️</span>
                        <span>
                            <strong>100% private &amp; local:</strong> reads file names only. Audio files never leave your device.
                        </span>
                    </div>

                    <details class="assistant-disclosure">
                        <summary>How does local folder scanning work?</summary>
                        <div class="assistant-disclosure-body">
                            <p>
                                MicUp only scans <strong>file names</strong> (e.g. <em>Artist - Title.mp3</em>) to build your catalog index.
                            </p>
                            <p>
                                Your browser may show a standard prompt asking to &ldquo;upload files&rdquo; &mdash; clicking <strong>Upload</strong> only grants permission to read file names. No audio or video data is ever uploaded.
                            </p>
                        </div>
                    </details>
                </div>
            ) : (
                <div>
                    <div
                        class="assistant-dropzone"
                        role="button"
                        tabIndex={0}
                        onDragOver={(e) => e.preventDefault()}
                        onDrop={(e) => {
                            e.preventDefault()
                            if (e.dataTransfer.files?.[0]) onSpreadsheetFile(e.dataTransfer.files[0])
                        }}
                        onClick={() => spreadsheetInputRef.current?.click()}
                        onKeyDown={(e) => handleDropzoneKeyDown(e, spreadsheetInputRef)}
                    >
                        <span class="assistant-dropzone-icon" aria-hidden="true">📊</span>
                        <p class="assistant-dropzone-title">Choose catalog file</p>
                        <p class="assistant-dropzone-hint">
                            Drop a file here or <span class="assistant-dropzone-link">browse (.csv, .tsv, .txt, .xml)</span>
                        </p>
                        <p class="assistant-dropzone-formats">
                            Delimited spreadsheets (CSV, TSV, TXT) or VirtualDJ database.xml
                        </p>
                    </div>
                    <input
                        type="file"
                        ref={spreadsheetInputRef}
                        accept=".tsv,.csv,.txt,.sbdb,.xml"
                        onChange={(e) => {
                            if (e.target.files?.[0]) onSpreadsheetFile(e.target.files[0])
                        }}
                        style={{ display: 'none' }}
                    />

                    <details class="assistant-disclosure">
                        <summary>Where to find VirtualDJ database.xml</summary>
                        <div class="assistant-disclosure-body">
                            <p>
                                <strong>Windows:</strong> <code>Documents\VirtualDJ\database.xml</code>
                            </p>
                            <p>
                                <strong>Mac:</strong> <code>~/Library/Application Support/VirtualDJ/database.xml</code>
                            </p>
                        </div>
                    </details>
                </div>
            )}

            <div class="assistant-source-footer">
                <button type="button" class="btn btn-secondary" onClick={onCancel}>
                    Back to Songbooks
                </button>
            </div>
        </div>
    )
}
