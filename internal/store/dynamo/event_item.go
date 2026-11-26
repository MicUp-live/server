package dynamo

import (
	"time"

	"micup.live/server/internal/models"
)

type eventItem struct {
	ID                   string         `dynamodbav:"id"`
	OwnerID              string         `dynamodbav:"owner_id"`
	SongbookID           string         `dynamodbav:"songbook_id"`
	DetachedSongbookName string         `dynamodbav:"detached_songbook_name,omitempty"`
	VenueName            string         `dynamodbav:"venue_name"`
	Name                 string         `dynamodbav:"name"`
	JoinCode             string         `dynamodbav:"join_code"`
	Status               string         `dynamodbav:"status"`
	Recurring            bool           `dynamodbav:"recurring,omitempty"`
	TipURL               string         `dynamodbav:"tip_url,omitempty"`
	Announcement         string         `dynamodbav:"announcement,omitempty"`
	KioskPIN             string         `dynamodbav:"kiosk_pin,omitempty"`
	RotationMode         string         `dynamodbav:"rotation_mode,omitempty"`
	CurrentRound         int            `dynamodbav:"current_round"`
	AutoAccept           bool           `dynamodbav:"auto_accept"`
	SubmissionDelay      int            `dynamodbav:"submission_delay,omitempty"`
	MaxActivePerSinger   *int           `dynamodbav:"max_active_per_singer,omitempty"`
	AllowKeyChange       *bool          `dynamodbav:"allow_key_change,omitempty"`
	AllowWriteIns        bool           `dynamodbav:"allow_write_ins,omitempty"`
	SingerWeights        map[string]int `dynamodbav:"singer_weights,omitempty"`

	QueueVersion     int64  `dynamodbav:"queue_version"`
	MetadataVersion  int64  `dynamodbav:"metadata_version,omitempty"`
	Restarting       bool   `dynamodbav:"restart_in_progress,omitempty"`
	Detaching        bool   `dynamodbav:"detach_in_progress,omitempty"`
	PendingDetachTTL int64  `dynamodbav:"pending_detach_ttl,omitempty"`
	Deleting         bool   `dynamodbav:"deleting,omitempty"`
	OpenKJKey        string `dynamodbav:"openkj_key,omitempty"`
	CreatedAt        string `dynamodbav:"created_at"`
	CreatedKey       string `dynamodbav:"created_key"`
	UpdatedAt        string `dynamodbav:"updated_at"`
	EndedAt          string `dynamodbav:"ended_at,omitempty"`
	TTL              int64  `dynamodbav:"ttl,omitempty"`
}

func modelEvent(item eventItem) (*models.Event, error) {
	created, err := parseTime(item.CreatedAt)
	if err != nil {
		return nil, err
	}
	updated, err := parseTime(item.UpdatedAt)
	if err != nil {
		return nil, err
	}
	var ended *time.Time
	if item.EndedAt != "" {
		t, err := parseTime(item.EndedAt)
		if err == nil {
			ended = &t
		}
	}

	rotationMode := item.RotationMode
	if rotationMode == "" {
		rotationMode = models.RotationModeRoundRobin
	}
	autoAccept := item.AutoAccept
	if item.RotationMode == "" && !item.AutoAccept {
		// Legacy records created before auto_accept field default to true
		autoAccept = true
	}
	submissionDelay := item.SubmissionDelay
	if submissionDelay == 0 && item.RotationMode == "" {
		submissionDelay = 60
	}
	maxActive := 1
	if item.MaxActivePerSinger != nil {
		maxActive = *item.MaxActivePerSinger
		if maxActive < 0 {
			maxActive = 0
		}
	}
	allowKeyChange := true
	if item.AllowKeyChange != nil {
		allowKeyChange = *item.AllowKeyChange
	}

	currentRound := item.CurrentRound
	if currentRound <= 0 {
		currentRound = 1
	}

	return &models.Event{
		ID:                   item.ID,
		OwnerID:              item.OwnerID,
		SongbookID:           item.SongbookID,
		DetachedSongbookName: item.DetachedSongbookName,
		VenueName:            item.VenueName,
		Name:                 item.Name,
		JoinCode:             item.JoinCode,
		Status:               models.EventStatus(item.Status),
		Recurring:            item.Recurring,
		TipURL:               item.TipURL,
		Announcement:         item.Announcement,
		KioskPIN:             item.KioskPIN,
		RotationMode:         rotationMode,
		CurrentRound:         currentRound,
		AutoAccept:           autoAccept,
		SubmissionDelay:      submissionDelay,
		MaxActivePerSinger:   maxActive,
		AllowKeyChange:       allowKeyChange,
		AllowWriteIns:        item.AllowWriteIns,
		SingerWeights:        models.NormalizeSingerWeights(item.SingerWeights),
		QueueVersion:         item.QueueVersion,
		MetadataVersion:      item.MetadataVersion,
		Restarting:           item.Restarting,
		Detaching:            item.Detaching,
		PendingDetachTTL:     item.PendingDetachTTL,
		Deleting:             item.Deleting,
		OpenKJKey:            item.OpenKJKey,
		TTL:                  item.TTL,
		CreatedAt:            created,
		UpdatedAt:            updated,
		EndedAt:              ended,
	}, nil
}
