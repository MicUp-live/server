package event

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"strings"

	"micup.live/server/internal/models"
)

// GenerateJoinCode generates a random 4-letter join code excluding confusing characters.
func GenerateJoinCode() string {
	const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ" // Exclude I, O to avoid confusion
	bytes := make([]byte, 4)
	if _, err := rand.Read(bytes); err != nil {
		return "KARA"
	}
	for i, b := range bytes {
		bytes[i] = chars[int(b)%len(chars)]
	}
	return string(bytes)
}

// GenerateKioskPIN generates a 4-digit PIN.
func GenerateKioskPIN() string {
	const digits = "0123456789"
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "1234"
	}
	for i := range b {
		b[i] = digits[int(b[i])%len(digits)]
	}
	return string(b)
}

// HashKioskPIN computes SHA-256 hex string of the PIN.
func HashKioskPIN(pin string) string {
	sum := sha256.Sum256([]byte(pin))
	return fmt.Sprintf("%x", sum[:])
}

func applyEventSettingsUpdate(event *models.Event, update models.EventUpdate) {
	if update.Name != nil {
		event.Name = strings.TrimSpace(*update.Name)
	}
	if update.VenueName != nil {
		event.VenueName = strings.TrimSpace(*update.VenueName)
	}
	if update.TipURL != nil {
		event.TipURL = strings.TrimSpace(*update.TipURL)
	}
	if update.Announcement != nil {
		event.Announcement = strings.TrimSpace(*update.Announcement)
	}
	if update.KioskPIN != nil {
		pin := strings.TrimSpace(*update.KioskPIN)
		if pin != "" {
			event.KioskPIN = pin
		}
	}
	if update.CurrentRound != nil && *update.CurrentRound > 0 {
		event.CurrentRound = *update.CurrentRound
	}
	if update.SingerWeights != nil {
		event.SingerWeights = models.NormalizeSingerWeights(*update.SingerWeights)
	}
	if update.AutoAccept != nil {
		event.AutoAccept = *update.AutoAccept
	}
	if update.SubmissionDelay != nil {
		delay := *update.SubmissionDelay
		if delay < 0 {
			delay = 0
		} else if delay > 600 {
			delay = 600
		}
		event.SubmissionDelay = delay
	}
	if update.MaxActivePerSinger != nil {
		maxActive := *update.MaxActivePerSinger
		if maxActive < 0 {
			maxActive = 0
		}
		event.MaxActivePerSinger = maxActive
	}
	if update.Recurring != nil {
		event.Recurring = *update.Recurring
	}
	if update.AllowKeyChange != nil {
		event.AllowKeyChange = *update.AllowKeyChange
	}
	if update.AllowWriteIns != nil {
		event.AllowWriteIns = *update.AllowWriteIns
	}
}
