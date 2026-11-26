package models

import (
	"time"
)

// RequestStatus represents the state of a singer song request in the queue.
type RequestStatus string

const (
	StatusPending   RequestStatus = "pending"   // Request submitted by singer, awaiting host action
	StatusAccepted  RequestStatus = "accepted"  // Host accepted into rotation
	StatusUpNext    RequestStatus = "up_next"   // Host marked singer as on deck / singing next
	StatusCompleted RequestStatus = "completed" // Song was performed
	StatusRejected  RequestStatus = "rejected"  // Host declined request
	StatusCancelled RequestStatus = "cancelled" // Singer withdrew request
)

// RoundRankMultiplier is the rank spacing per rotation round (10 Trillion).
// Fits comfortably within JavaScript's Number.MAX_SAFE_INTEGER (9e15) for up to 800 rounds,
// while accommodating Unix epoch millisecond timestamps without bit bleed.
const RoundRankMultiplier int64 = 10_000_000_000_000

// ResolveRound resolves a request's assigned rotation round from Round or Rank.
func ResolveRound(round int, rank int64) int {
	if round > 0 {
		return round
	}
	if rank >= RoundRankMultiplier {
		r := int(rank / RoundRankMultiplier)
		if r > 0 {
			return r
		}
	}
	// Fallback for legacy items written with old (round * 1T) + nowMilli formula
	if rank >= 2_000_000_000_000 && rank < RoundRankMultiplier {
		r := int((rank - 1_700_000_000_000) / 1_000_000_000_000)
		if r > 0 {
			return r
		}
	}
	return 1
}

// SongRequest represents a song requested by a singer at an event.
type SongRequest struct {
	ID            string        `json:"id"`
	EventID       string        `json:"event_id,omitempty"`
	SongbookID    string        `json:"songbook_id,omitempty"`
	SongID        string        `json:"song_id,omitempty"`
	IsWriteIn     bool          `json:"is_write_in,omitempty"`
	SongTitle     string        `json:"song_title"`
	SongArtist    string        `json:"song_artist"`
	SingerName    string        `json:"singer_name"`
	KeyChange     int           `json:"key_change"` // Pitch offset in semitones: -4 to +4
	Notes         string        `json:"notes,omitempty"`
	DeclineReason string        `json:"decline_reason,omitempty"`
	Status        RequestStatus `json:"status"`
	Rank          int64         `json:"rank"`                  // Relative order in rotation
	Round         int           `json:"round"`                 // Assigned rotation round (1, 2, 3...)
	SingerTurn    int           `json:"singer_turn,omitempty"` // Turn index for singer tonight (1st song, 2nd song, etc.)
	CancelToken   string        `json:"cancel_token,omitempty" dynamodbav:"-"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
	CompletedAt   *time.Time    `json:"completed_at,omitempty"`
	TTL           int64         `json:"-" dynamodbav:"ttl,omitempty"`
}

// PublicQueueItem represents sanitized queue visibility for singers / public screens.
type PublicQueueItem struct {
	ID         string        `json:"id"`
	SingerName string        `json:"singer_name"`
	SongTitle  string        `json:"song_title"`
	SongArtist string        `json:"song_artist"`
	IsWriteIn  bool          `json:"is_write_in,omitempty"`
	KeyChange  int           `json:"key_change"`
	Status     RequestStatus `json:"status"`
	Rank       int64         `json:"rank"`
	Round      int           `json:"round"`
	SingerTurn int           `json:"singer_turn,omitempty"`
	CreatedAt  time.Time     `json:"created_at"`
}

// RequestCreate contains the singer-submitted fields for requesting a song.
type RequestCreate struct {
	EventID    string `json:"event_id,omitempty"`
	SongID     string `json:"song_id,omitempty"`
	IsWriteIn  bool   `json:"is_write_in,omitempty"`
	SongTitle  string `json:"song_title"`
	SongArtist string `json:"song_artist"`
	SingerName string `json:"singer_name"`
	KeyChange  int    `json:"key_change"`
	Notes      string `json:"notes,omitempty"`
}

// RequestUpdate contains host-editable fields (status, rank, song details, notes).
type RequestUpdate struct {
	Status               *RequestStatus `json:"status,omitempty"`
	Rank                 *int64         `json:"rank,omitempty"`
	Round                *int           `json:"round,omitempty"`
	SongID               *string        `json:"song_id,omitempty"`
	SongTitle            *string        `json:"song_title,omitempty"`
	SongArtist           *string        `json:"song_artist,omitempty"`
	SingerName           *string        `json:"singer_name,omitempty"`
	KeyChange            *int           `json:"key_change,omitempty"`
	Notes                *string        `json:"notes,omitempty"`
	DeclineReason        *string        `json:"decline_reason,omitempty"`
	ExpectedQueueVersion *int64         `json:"expected_queue_version,omitempty"`
}
