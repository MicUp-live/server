package models

import "time"

// ImportJobStatus describes the lifecycle of an asynchronous songbook import.
type ImportJobStatus string

const (
	ImportJobPending    ImportJobStatus = "pending"
	ImportJobQueued     ImportJobStatus = "queued"
	ImportJobProcessing ImportJobStatus = "processing"
	ImportJobCompleted  ImportJobStatus = "completed"
	ImportJobFailed     ImportJobStatus = "failed"
)

// ImportJob tracks durable Assistant Importer work independently of a browser session.
type ImportJob struct {
	ID                     string          `json:"id"`
	SongbookID             string          `json:"songbook_id"`
	ManifestKey            string          `json:"-"`
	Status                 ImportJobStatus `json:"status"`
	TotalSongs             int             `json:"total_songs"`
	ProcessedSongs         int             `json:"processed_songs"`
	PruneMissing           bool            `json:"prune_missing"`
	Generation             int64           `json:"generation,omitempty"`
	CatalogVersionID       string          `json:"catalog_version_id,omitempty"`
	DispatchReady          bool            `json:"-"`
	DispatchClaimToken     string          `json:"-"`
	DispatchClaimExpiresAt *time.Time      `json:"-"`
	DispatchAttempts       int             `json:"-"`
	DispatchedAt           *time.Time      `json:"-"`
	Error                  string          `json:"error,omitempty"`
	CreatedAt              time.Time       `json:"created_at"`
	UpdatedAt              time.Time       `json:"updated_at"`
	CompletedAt            time.Time       `json:"completed_at,omitempty"`
}
