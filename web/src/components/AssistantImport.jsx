import { Spinner } from './Spinner.jsx'
import { useAssistantImport } from './assistantImport/hooks/useAssistantImport.js'
import { ImportSourcePicker } from './assistantImport/components/ImportSourcePicker.jsx'
import { ImportAuditView } from './assistantImport/components/ImportAuditView.jsx'
import { ImportProgressStage } from './assistantImport/components/ImportProgressStage.jsx'
import './AssistantImport.css'

export function AssistantImport({ songbookId, onComplete, onCancel }) {
    const {
        importMode,
        setImportMode,
        scanning,
        parsing,
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
        handlePatternChange,
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
        updateMapping,
        handleSwapColumns,
        handleApplyAllSuggestions,
        handleApplySingleSuggestion,
        toggleExcluded,
        handleResetSource,
        handleImport,
    } = useAssistantImport({ songbookId, onComplete })

    return (
        <div class="card assistant-import-card">
            <div class="assistant-import-header">
                <h3 class="assistant-import-title">Assistant Importer</h3>
                <p class="assistant-import-subtitle">
                    Import your karaoke library from local folders or software exports.
                </p>
            </div>

            {existingJob && (
                <div class="card assistant-job-banner">
                    <strong>Import already in progress</strong>
                    <div class="assistant-job-meta">
                        {existingJob.status === 'queued' ? 'Waiting for worker…' : 'Processing in the background…'} {existingJob.processed_songs || 0} / {existingJob.total_songs} songs
                    </div>
                </div>
            )}

            {!results && !scanning && !parsing && (
                <ImportSourcePicker
                    importMode={importMode}
                    onSelectMode={setImportMode}
                    folderInputRef={folderInputRef}
                    spreadsheetInputRef={spreadsheetInputRef}
                    onFolderSelect={handleFolderSelect}
                    onFolderDrop={handleFolderDrop}
                    onSpreadsheetFile={handleSpreadsheetFile}
                    onCancel={onCancel}
                />
            )}

            {!results && (scanning || parsing) && (
                <>
                    <Spinner label={scanning ? 'Scanning files...' : 'Parsing files...'} />
                    <div class="assistant-source-footer">
                        <button type="button" class="btn btn-secondary" onClick={onCancel}>
                            Back to Songbooks
                        </button>
                    </div>
                </>
            )}

            {results && !importing && (
                <ImportAuditView
                    importMode={importMode}
                    results={results}
                    excluded={excluded}
                    visibleResultCount={visibleResultCount}
                    onSetVisibleResultCount={setVisibleResultCount}
                    onToggleExcluded={toggleExcluded}
                    onSetExcluded={setExcluded}
                    pruneMissing={pruneMissing}
                    onSetPruneMissing={setPruneMissing}
                    pattern={pattern}
                    onPatternChange={handlePatternChange}
                    parsing={parsing}
                    spreadsheetFile={spreadsheetFile}
                    headers={headers}
                    softwareProfile={softwareProfile}
                    columnMapping={columnMapping}
                    showMappingEditor={showMappingEditor}
                    onToggleMappingEditor={() => setShowMappingEditor(!showMappingEditor)}
                    onUpdateMapping={updateMapping}
                    invertedDetected={invertedDetected}
                    onSwapColumns={handleSwapColumns}
                    auditing={auditing}
                    auditSummary={auditSummary}
                    auditResults={auditResults}
                    onApplyAllSuggestions={handleApplyAllSuggestions}
                    onApplySingleSuggestion={handleApplySingleSuggestion}
                    onResetSource={handleResetSource}
                    onImport={handleImport}
                />
            )}

            {importing && (
                <ImportProgressStage importProgress={importProgress} />
            )}
        </div>
    )
}
