package catalog

import (
	"strings"

	"micup.live/server/internal/models"
)

// MergeSongs combines existing catalog songs with incoming manifest songs when pruneMissing is false.
// Incoming songs update matching existing songs; unmatched existing songs are preserved, and new incoming songs are appended.
func MergeSongs(existing []*models.Song, incoming []*models.Song) []*models.Song {
	if len(existing) == 0 {
		return incoming
	}
	if len(incoming) == 0 {
		return existing
	}

	makeMetaKey := func(s *models.Song) string {
		if s == nil {
			return ""
		}
		artist := strings.ToLower(strings.TrimSpace(s.Artist))
		title := strings.ToLower(strings.TrimSpace(s.Title))
		disc := strings.ToLower(strings.TrimSpace(s.Disc))
		source := strings.ToLower(strings.TrimSpace(s.Source))
		video := ""
		if disc == "" && source == "" {
			video = strings.TrimSpace(s.VideoURL)
		}
		return artist + "\x00" + title + "\x00" + disc + "\x00" + source + "\x00" + video
	}

	makeDiscKey := func(s *models.Song) string {
		if s == nil {
			return ""
		}
		disc := strings.ToLower(strings.TrimSpace(s.Disc))
		if disc == "" {
			return ""
		}
		return strings.ToLower(strings.TrimSpace(s.Artist)) + "\x00" +
			strings.ToLower(strings.TrimSpace(s.Title)) + "\x00" + disc
	}

	result := make([]*models.Song, len(existing))
	copy(result, existing)

	idIndex := make(map[string]int, len(existing))
	metaIndex := make(map[string]int, len(existing))
	discIndex := make(map[string][]int, len(existing))

	for i, s := range existing {
		if s == nil {
			continue
		}
		if s.ID != "" {
			idIndex[s.ID] = i
		}
		mk := makeMetaKey(s)
		if mk != "\x00\x00\x00\x00" {
			metaIndex[mk] = i
		}
		if dk := makeDiscKey(s); dk != "" {
			discIndex[dk] = append(discIndex[dk], i)
		}
	}

	preserveLocation := func(existing, incoming *models.Song) *models.Song {
		if strings.TrimSpace(incoming.Location) == "" && strings.TrimSpace(existing.Location) != "" {
			cp := *incoming
			cp.Location = existing.Location
			return &cp
		}
		return incoming
	}

	for _, s := range incoming {
		if s == nil {
			continue
		}
		// Match by explicit song ID if present.
		if s.ID != "" {
			if idx, ok := idIndex[s.ID]; ok {
				result[idx] = preserveLocation(result[idx], s)
				continue
			}
		}
		// Match by normalized (Artist, Title, Disc, Source[, VideoURL]).
		mk := makeMetaKey(s)
		if mk != "\x00\x00\x00\x00" {
			if idx, ok := metaIndex[mk]; ok {
				result[idx] = preserveLocation(result[idx], s)
				continue
			}
		}
		// Upgrade/match legacy disc-coded entries when one side lacks Source so
		// additive re-imports enrich rather than duplicate existing tracks.
		if dk := makeDiscKey(s); dk != "" {
			incomingSource := strings.TrimSpace(s.Source)
			matchedIdx := -1
			for _, candidateIdx := range discIndex[dk] {
				existingSource := strings.TrimSpace(result[candidateIdx].Source)
				if existingSource == "" || incomingSource == "" {
					matchedIdx = candidateIdx
					break
				}
			}
			if matchedIdx >= 0 {
				merged := preserveLocation(result[matchedIdx], s)
				if incomingSource == "" && strings.TrimSpace(result[matchedIdx].Source) != "" {
					cp := *merged
					cp.Source = result[matchedIdx].Source
					merged = &cp
				}
				result[matchedIdx] = merged
				if mk != "\x00\x00\x00\x00" {
					metaIndex[mk] = matchedIdx
				}
				continue
			}
		}
		// New song: append and register in lookup maps.
		idx := len(result)
		result = append(result, s)
		if s.ID != "" {
			idIndex[s.ID] = idx
		}
		if mk != "\x00\x00\x00\x00" {
			metaIndex[mk] = idx
		}
		if dk := makeDiscKey(s); dk != "" {
			discIndex[dk] = append(discIndex[dk], idx)
		}
	}

	return result
}
