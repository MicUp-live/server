package models

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

type Songbook struct {
	ID                string               `json:"id"`
	OwnerID           string               `json:"-"`
	OwnerDisplayName  string               `json:"owner_display_name,omitempty"`
	PublicID          string               `json:"public_id"`
	Slug              string               `json:"slug,omitempty"`
	VenueID           string               `json:"venue_id"`
	CatalogVersionID  string               `json:"catalog_version_id,omitempty"`
	Name              string               `json:"name"`
	IsPublic          bool                 `json:"is_public"`
	SongCount         int                  `json:"song_count"`
	CatalogGeneration int64                `json:"catalog_generation,omitempty"`
	Deleting          bool                 `json:"-" dynamodbav:"deleting,omitempty"`
	DeletionConfirmed bool                 `json:"-" dynamodbav:"deletion_confirmed,omitempty"`
	Permissions       *SongbookPermissions `json:"permissions,omitempty"`
	CreatedAt         time.Time            `json:"created_at"`
	UpdatedAt         time.Time            `json:"updated_at"`
}

type Song struct {
	ID          string                 `json:"song_id"`
	SongbookID  string                 `json:"songbook_id,omitempty"`
	Artist      string                 `json:"artist"`
	Title       string                 `json:"title"`
	Label       string                 `json:"label,omitempty"`
	SearchText  string                 `json:"search_text,omitempty"`
	Genre       string                 `json:"genre,omitempty"`
	Disc        string                 `json:"disc,omitempty"`
	Source      string                 `json:"source,omitempty"`
	Location    string                 `json:"location,omitempty"`
	VideoURL    string                 `json:"video_url,omitempty"`
	Data        map[string]interface{} `json:"data,omitempty"`
	IsAvailable bool                   `json:"is_available"`
	Fingerprint string                 `json:"fingerprint,omitempty"`
	CanonicalID string                 `json:"canonical_id,omitempty"`
	IsCustom    bool                   `json:"is_custom,omitempty"`
	CreatedAt   time.Time              `json:"created_at,omitempty"`
	UpdatedAt   time.Time              `json:"updated_at,omitempty"`
}

type SongCreate struct {
	Artist      string `json:"artist"`
	Title       string `json:"title"`
	Genre       string `json:"genre"`
	Disc        string `json:"disc"`
	Source      string `json:"source"`
	Location    string `json:"location"`
	VideoURL    string `json:"video_url"`
	IsAvailable bool   `json:"is_available"`
	Fingerprint string `json:"fingerprint"`
}

// SongUpdate contains fields that may be changed independently.
type SongUpdate struct {
	Artist      *string `json:"artist"`
	Title       *string `json:"title"`
	Genre       *string `json:"genre"`
	Disc        *string `json:"disc"`
	Source      *string `json:"source"`
	Location    *string `json:"location"`
	VideoURL    *string `json:"video_url"`
	IsAvailable *bool   `json:"is_available"`
	Fingerprint *string `json:"fingerprint"`
}

type SongbookCreate struct {
	VenueID  string `json:"venue_id"`
	Name     string `json:"name"`
	Slug     string `json:"slug,omitempty"`
	IsPublic bool   `json:"is_public"`
}

// SongbookUpdate contains the owner-editable songbook fields.
type SongbookUpdate struct {
	Name     *string `json:"name"`
	Slug     *string `json:"slug,omitempty"`
	IsPublic *bool   `json:"is_public"`
}

var slugRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]{3,32}$`)

var reservedSlugs = map[string]bool{
	"api":       true,
	"auth":      true,
	"admin":     true,
	"docs":      true,
	"e":         true,
	"book":      true,
	"stage":     true,
	"kiosk":     true,
	"join":      true,
	"kj":        true,
	"new":       true,
	"edit":      true,
	"swagger":   true,
	"reference": true,
	"public":    true,
	"terms":     true,
	"privacy":   true,
}

func NormalizeSlug(slug string) string {
	return strings.ToLower(strings.TrimSpace(slug))
}

func ValidateSlug(slug string) error {
	trimmed := strings.TrimSpace(slug)
	if trimmed == "" {
		return nil
	}
	if len(trimmed) < 3 || len(trimmed) > 32 {
		return fmt.Errorf("slug must be between 3 and 32 characters")
	}
	if !slugRegex.MatchString(trimmed) {
		return fmt.Errorf("slug must contain only letters, numbers, hyphens, and underscores")
	}
	if reservedSlugs[strings.ToLower(trimmed)] {
		return fmt.Errorf("slug %q is a reserved system path", trimmed)
	}
	return nil
}

func BuildLabel(artist, title string) string {
	if artist != "" && title != "" {
		return artist + " — " + title // em dash
	}
	if artist != "" {
		return artist
	}
	return title
}

func BuildSearchText(artist, title, disc, label, location string) string {
	var parts []string
	for _, p := range []string{artist, title, disc, label, location} {
		if strings.TrimSpace(p) != "" {
			parts = append(parts, p)
		}
	}
	return strings.ToLower(strings.Join(parts, " "))
}
