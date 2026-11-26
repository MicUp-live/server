package dynamo

import (
	"fmt"
	"time"

	"micup.live/server/internal/models"
)

type requestItem struct {
	ID            string `dynamodbav:"id"`
	EventID       string `dynamodbav:"event_id,omitempty"`
	SongbookID    string `dynamodbav:"songbook_id,omitempty"`
	SongID        string `dynamodbav:"song_id,omitempty"`
	IsWriteIn     bool   `dynamodbav:"is_write_in,omitempty"`
	SongTitle     string `dynamodbav:"song_title"`
	SongArtist    string `dynamodbav:"song_artist"`
	SingerName    string `dynamodbav:"singer_name"`
	KeyChange     int    `dynamodbav:"key_change"`
	Notes         string `dynamodbav:"notes,omitempty"`
	DeclineReason string `dynamodbav:"decline_reason,omitempty"`
	Status        string `dynamodbav:"status"`
	Rank          int64  `dynamodbav:"rank"`
	Round         int    `dynamodbav:"round"`
	SingerTurn    int    `dynamodbav:"singer_turn,omitempty"`
	CreatedAt     string `dynamodbav:"created_at"`
	CreatedKey    string `dynamodbav:"created_key"`
	UpdatedAt     string `dynamodbav:"updated_at"`
	CompletedAt   string `dynamodbav:"completed_at,omitempty"`
	TTL           int64  `dynamodbav:"ttl,omitempty"`
}

func buildRequestItem(req *models.SongRequest, partition, updatedAt string) requestItem {
	created := timestamp(req.CreatedAt)
	item := requestItem{
		ID:            req.ID,
		EventID:       partition,
		SongbookID:    req.SongbookID,
		SongID:        req.SongID,
		IsWriteIn:     req.IsWriteIn,
		SongTitle:     req.SongTitle,
		SongArtist:    req.SongArtist,
		SingerName:    req.SingerName,
		KeyChange:     req.KeyChange,
		Notes:         req.Notes,
		DeclineReason: req.DeclineReason,
		Status:        string(req.Status),
		Rank:          req.Rank,
		Round:         req.Round,
		SingerTurn:    req.SingerTurn,
		CreatedAt:     created,
		CreatedKey:    fmt.Sprintf("%s#%s", created, req.ID),
		UpdatedAt:     updatedAt,
		TTL:           req.TTL,
	}
	if req.CompletedAt != nil && !req.CompletedAt.IsZero() {
		item.CompletedAt = timestamp(*req.CompletedAt)
	}
	return item
}

func modelRequest(item requestItem) (*models.SongRequest, error) {
	created, err := parseTime(item.CreatedAt)
	if err != nil {
		return nil, err
	}
	updated, err := parseTime(item.UpdatedAt)
	if err != nil {
		return nil, err
	}
	var completedAt *time.Time
	if item.CompletedAt != "" {
		t, err := parseTime(item.CompletedAt)
		if err == nil {
			completedAt = &t
		}
	}
	return &models.SongRequest{
		ID:            item.ID,
		EventID:       item.EventID,
		SongbookID:    item.SongbookID,
		SongID:        item.SongID,
		IsWriteIn:     item.IsWriteIn,
		SongTitle:     item.SongTitle,
		SongArtist:    item.SongArtist,
		SingerName:    item.SingerName,
		KeyChange:     item.KeyChange,
		Notes:         item.Notes,
		DeclineReason: item.DeclineReason,
		Status:        models.RequestStatus(item.Status),
		Rank:          item.Rank,
		Round:         item.Round,
		SingerTurn:    item.SingerTurn,
		CreatedAt:     created,
		UpdatedAt:     updated,
		CompletedAt:   completedAt,
		TTL:           item.TTL,
	}, nil
}
