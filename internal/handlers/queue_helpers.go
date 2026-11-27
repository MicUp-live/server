package handlers

import (
	"errors"
	"strings"

	"micup.live/server/internal/models"
)

func normalizeUpdateStatus(field **models.RequestStatus) error {
	if *field == nil {
		return nil
	}
	status := **field
	switch status {
	case models.StatusPending, models.StatusAccepted, models.StatusUpNext, models.StatusCompleted, models.StatusRejected, models.StatusCancelled:
	case "playing":
		status = models.StatusUpNext
	case "declined":
		status = models.StatusRejected
	default:
		return errors.New("invalid status")
	}
	*field = &status
	return nil
}

func trimRequiredString(field **string, emptyMsg string) error {
	if *field == nil {
		return nil
	}
	trimmed := strings.TrimSpace(**field)
	if trimmed == "" {
		return errors.New(emptyMsg)
	}
	*field = &trimmed
	return nil
}

func trimOptionalString(field **string) {
	if *field == nil {
		return
	}
	trimmed := strings.TrimSpace(**field)
	*field = &trimmed
}

func clampKeyChange(field **int) {
	if *field == nil {
		return
	}
	keyChange := **field
	if keyChange < -6 {
		keyChange = -6
	} else if keyChange > 6 {
		keyChange = 6
	}
	*field = &keyChange
}
