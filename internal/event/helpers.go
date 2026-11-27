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
	applyTrimmedString(&event.Name, update.Name)
	applyTrimmedString(&event.VenueName, update.VenueName)
	applyTrimmedString(&event.TipURL, update.TipURL)
	applyTrimmedString(&event.Announcement, update.Announcement)
	applyKioskPINUpdate(event, update.KioskPIN)
	if update.CurrentRound != nil && *update.CurrentRound > 0 {
		event.CurrentRound = *update.CurrentRound
	}
	if update.SingerWeights != nil {
		event.SingerWeights = models.NormalizeSingerWeights(*update.SingerWeights)
	}
	applySubmissionDelayUpdate(event, update.SubmissionDelay)
	applyMaxActiveUpdate(event, update.MaxActivePerSinger)
	applyBoolIfPresent(&event.AutoAccept, update.AutoAccept)
	applyBoolIfPresent(&event.Recurring, update.Recurring)
	applyBoolIfPresent(&event.AllowKeyChange, update.AllowKeyChange)
	applyBoolIfPresent(&event.AllowWriteIns, update.AllowWriteIns)
	applyBoolIfPresent(&event.AllowSongReplacement, update.AllowSongReplacement)
}

func applyTrimmedString(dst, src *string) {
	if src != nil {
		*dst = strings.TrimSpace(*src)
	}
}

func applyBoolIfPresent(dst, src *bool) {
	if src != nil {
		*dst = *src
	}
}

func applyKioskPINUpdate(event *models.Event, rawPIN *string) {
	if rawPIN == nil {
		return
	}
	if pin := strings.TrimSpace(*rawPIN); pin != "" {
		event.KioskPIN = pin
	}
}

func applySubmissionDelayUpdate(event *models.Event, delay *int) {
	if delay == nil {
		return
	}
	event.SubmissionDelay = min(max(*delay, 0), 600)
}

func applyMaxActiveUpdate(event *models.Event, maxActive *int) {
	if maxActive == nil {
		return
	}
	event.MaxActivePerSinger = max(*maxActive, 0)
}
