package models

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	// MinSingerWeight is the lowest host-controlled priority adjustment for fair_priority rotation.
	MinSingerWeight = -10
	// MaxSingerWeight is the highest host-controlled priority adjustment for fair_priority rotation.
	MaxSingerWeight = 10
)

// ClampSingerWeight bounds a host-controlled singer weight to [MinSingerWeight, MaxSingerWeight].
func ClampSingerWeight(weight int) int {
	if weight < MinSingerWeight {
		return MinSingerWeight
	}
	if weight > MaxSingerWeight {
		return MaxSingerWeight
	}
	return weight
}

// ValidateSingerWeights rejects conflicting weights for keys that normalize to
// the same singer name (e.g. "Alice": 3 and " alice ": -4).
func ValidateSingerWeights(weights map[string]int) error {
	if len(weights) == 0 {
		return nil
	}
	rawNames := make([]string, 0, len(weights))
	for rawName := range weights {
		rawNames = append(rawNames, rawName)
	}
	sort.Strings(rawNames)

	seen := make(map[string]int, len(weights))
	for _, rawName := range rawNames {
		name := strings.ToLower(strings.TrimSpace(rawName))
		if name == "" {
			continue
		}
		clamped := ClampSingerWeight(weights[rawName])
		if prev, exists := seen[name]; exists && prev != clamped {
			return fmt.Errorf("conflicting singer_weights for %q: %w", name, os.ErrInvalid)
		}
		seen[name] = clamped
	}
	return nil
}

// NormalizeSingerWeights returns a sanitized copy of singer weights keyed by
// lowercased, trimmed singer name with values clamped to [-10, +10].
// Empty keys and zero weights (the default) are omitted; returns nil if empty.
// Keys are processed in sorted order so any colliding aliases resolve deterministically.
func NormalizeSingerWeights(weights map[string]int) map[string]int {
	if len(weights) == 0 {
		return nil
	}
	rawNames := make([]string, 0, len(weights))
	for rawName := range weights {
		rawNames = append(rawNames, rawName)
	}
	sort.Strings(rawNames)

	normalized := make(map[string]int, len(weights))
	for _, rawName := range rawNames {
		name := strings.ToLower(strings.TrimSpace(rawName))
		if name == "" {
			continue
		}
		clamped := ClampSingerWeight(weights[rawName])
		if clamped == 0 {
			delete(normalized, name)
			continue
		}
		normalized[name] = clamped
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

// EventStatus defines the live lifecycle of a karaoke show.
type EventStatus string

const (
	EventStatusActive EventStatus = "active" // Live and accepting singer requests
	EventStatusPaused EventStatus = "paused" // Show is ongoing, but requests are temporarily paused/capped
	EventStatusEnded  EventStatus = "ended"  // Show finished and archived
)

const (
	RotationModeRoundRobin   = "round_robin"   // Fair rounds per singer
	RotationModeFairPriority = "fair_priority" // Turn-weighted smart weave with stage lock
	RotationModeFCFS         = "fcfs"          // Chronological first-come first-served
	RotationModeManual       = "manual"        // Staging inbox; host manually reviews and places
)

// Event represents a distinct karaoke night or gig session tied to a songbook catalog.
type Event struct {
	ID                   string         `json:"id"`
	OwnerID              string         `json:"-"`
	SongbookID           string         `json:"songbook_id"`
	DetachedSongbookName string         `json:"detached_songbook_name,omitempty"`
	VenueName            string         `json:"venue_name"`
	Name                 string         `json:"name"`
	JoinCode             string         `json:"join_code"` // e.g. "OMALLEY" or "MIC42"
	Status               EventStatus    `json:"status"`
	Recurring            bool           `json:"recurring"`                 // Recurring show (e.g. weekly venue residency)
	TipURL               string         `json:"tip_url,omitempty"`         // e.g. Venmo / Cash App / PayPal link
	Announcement         string         `json:"announcement,omitempty"`    // Live shout out ticker message
	KioskPIN             string         `json:"kiosk_pin,omitempty"`       // 4-digit PIN for exiting kiosk mode
	RotationMode         string         `json:"rotation_mode"`             // "round_robin", "fcfs", "manual"
	AutoAccept           bool           `json:"auto_accept"`               // whether incoming requests auto-accept into rotation
	SubmissionDelay      int            `json:"submission_delay"`          // cooldown delay in seconds between singer submissions (burst protection)
	MaxActivePerSinger   int            `json:"max_active_per_singer"`     // max active requests per singer in rotation (0 = unlimited)
	AllowKeyChange       bool           `json:"allow_key_change"`          // whether singers can request pitch/key changes (-3 to +3)
	AllowWriteIns        bool           `json:"allow_write_ins,omitempty"` // whether singers can submit custom write-in requests not in catalog
	CurrentRound         int            `json:"current_round"`             // active show rotation round (1, 2, 3...)
	SingerWeights        map[string]int `json:"singer_weights,omitempty"`  // private host-only priority adjustments [-10, +10], keyed by normalized singer name

	QueueVersion     int64      `json:"queue_version"`
	MetadataVersion  int64      `json:"-"` // Fences snapshot writes, including private-key changes without a queue bump.
	Restarting       bool       `json:"-"` // durable admission barrier while a queue is cleared
	Detaching        bool       `json:"-"` // durable barrier while songbook deletion finishes event request retention
	PendingDetachTTL int64      `json:"-"` // exact retention deadline preserved while ttl is temporarily removed during detach
	Deleting         bool       `json:"-" dynamodbav:"deleting,omitempty"`
	OpenKJKey        string     `json:"openkj_key,omitempty"`
	TTL              int64      `json:"-" dynamodbav:"ttl,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	EndedAt          *time.Time `json:"ended_at,omitempty"`
}

// GenerateOpenKJKey creates a secure, revocable integration key for OpenKJ.
func GenerateOpenKJKey(eventID string) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("okj_%s_%x", eventID, time.Now().UnixNano())
	}
	return fmt.Sprintf("okj_%s_%s", eventID, hex.EncodeToString(b))
}

// PublicEvent provides sanitized event details for singers, kiosks, and stage displays.
type PublicEvent struct {
	ID                 string      `json:"id"`
	SongbookID         string      `json:"songbook_id"`
	PublicSongbookID   string      `json:"public_songbook_id"`
	SongbookName       string      `json:"songbook_name"`
	VenueName          string      `json:"venue_name"`
	Name               string      `json:"name"`
	JoinCode           string      `json:"join_code"`
	Status             EventStatus `json:"status"`
	Recurring          bool        `json:"recurring"`
	TipURL             string      `json:"tip_url,omitempty"`
	Announcement       string      `json:"announcement,omitempty"`
	RotationMode       string      `json:"rotation_mode"`
	AutoAccept         bool        `json:"auto_accept"`
	SubmissionDelay    int         `json:"submission_delay"`
	MaxActivePerSinger int         `json:"max_active_per_singer,omitempty"`
	AllowKeyChange     bool        `json:"allow_key_change"`
	AllowWriteIns      bool        `json:"allow_write_ins,omitempty"`
	CurrentRound       int         `json:"current_round"`
	SongCount          int         `json:"song_count"`
	CreatedAt          time.Time   `json:"created_at"`
}

// EventCreate contains parameters for starting a new show.
type EventCreate struct {
	SongbookID         string `json:"songbook_id"`
	Name               string `json:"name"`
	VenueName          string `json:"venue_name"`
	JoinCode           string `json:"join_code,omitempty"`
	Recurring          bool   `json:"recurring,omitempty"`
	TipURL             string `json:"tip_url,omitempty"`
	Announcement       string `json:"announcement,omitempty"`
	KioskPIN           string `json:"kiosk_pin,omitempty"`
	RotationMode       string `json:"rotation_mode,omitempty"`
	AutoAccept         *bool  `json:"auto_accept,omitempty"`
	SubmissionDelay    *int   `json:"submission_delay,omitempty"`
	MaxActivePerSinger *int   `json:"max_active_per_singer,omitempty"`
	AllowKeyChange     *bool  `json:"allow_key_change,omitempty"`
	AllowWriteIns      *bool  `json:"allow_write_ins,omitempty"`
	CurrentRound       *int   `json:"current_round,omitempty"`
}

// EventUpdate allows hosts to change event state, venue, tip link, announcement, or PIN.
type EventUpdate struct {
	Name               *string         `json:"name,omitempty"`
	VenueName          *string         `json:"venue_name,omitempty"`
	Status             *EventStatus    `json:"status,omitempty"`
	Recurring          *bool           `json:"recurring,omitempty"`
	SongbookID         *string         `json:"songbook_id,omitempty"`
	TipURL             *string         `json:"tip_url,omitempty"`
	Announcement       *string         `json:"announcement,omitempty"`
	KioskPIN           *string         `json:"kiosk_pin,omitempty"`
	RotationMode       *string         `json:"rotation_mode,omitempty"`
	AutoAccept         *bool           `json:"auto_accept,omitempty"`
	SubmissionDelay    *int            `json:"submission_delay,omitempty"`
	MaxActivePerSinger *int            `json:"max_active_per_singer,omitempty"`
	AllowKeyChange     *bool           `json:"allow_key_change,omitempty"`
	AllowWriteIns      *bool           `json:"allow_write_ins,omitempty"`
	CurrentRound       *int            `json:"current_round,omitempty"`
	SingerWeights      *map[string]int `json:"singer_weights,omitempty"`
}
