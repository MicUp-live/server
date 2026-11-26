import { Spinner } from '../../Spinner.jsx'

export function ImportProgressStage({ importProgress }) {
    const percent = importProgress && importProgress.total > 0
        ? Math.round((importProgress.current / importProgress.total) * 100)
        : 0

    return (
        <div class="assistant-progress-stage">
            <Spinner label={importProgress
                ? `Syncing with cloud: ${importProgress.current.toLocaleString()} / ${importProgress.total.toLocaleString()} songs (${percent}%)...`
                : 'Syncing with cloud...'}
            />
            {importProgress && importProgress.total > 0 && (
                <div
                    class="assistant-progress-track"
                    role="progressbar"
                    aria-label="Import progress"
                    aria-valuemin={0}
                    aria-valuemax={100}
                    aria-valuenow={percent}
                >
                    <div
                        class="assistant-progress-fill"
                        style={{ width: `${percent}%` }}
                    />
                </div>
            )}
        </div>
    )
}
