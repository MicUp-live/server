// Package models defines catalog version domain records.
package models

import "time"

// CatalogVersionStatus describes whether a content-addressed catalog can serve readers.
type CatalogVersionStatus string

const (
	CatalogVersionBuilding CatalogVersionStatus = "building"
	CatalogVersionReady    CatalogVersionStatus = "ready"
	CatalogVersionFailed   CatalogVersionStatus = "failed"
	CatalogVersionDeleting CatalogVersionStatus = "deleting"
)

// CatalogVersion represents one immutable, normalized catalog build. Its ID is
// the content hash so identical imports can share the same version.
type CatalogVersion struct {
	ID             string               `json:"id"`
	SchemaVersion  int                  `json:"schema_version"`
	Status         CatalogVersionStatus `json:"status"`
	SongCount      int                  `json:"song_count"`
	AssetPrefix    string               `json:"asset_prefix,omitempty"`
	FailureReason  string               `json:"failure_reason,omitempty"`
	CreatedAt      time.Time            `json:"created_at"`
	ReadyAt        time.Time            `json:"ready_at,omitempty"`
	LeaseToken     string               `json:"-"`
	LeaseExpiresAt time.Time            `json:"-"`
}
