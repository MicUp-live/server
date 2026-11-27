package catalog

import (
	"strings"

	"micup.live/server/internal/models"
)

const emptyMetaKey = "\x00\x00\x00\x00"

type songMergeIndex struct {
	result    []*models.Song
	idIndex   map[string]int
	metaIndex map[string]int
	discIndex map[string][]int
}

func newSongMergeIndex(existing []*models.Song) *songMergeIndex {
	result := make([]*models.Song, len(existing))
	copy(result, existing)

	idx := &songMergeIndex{
		result:    result,
		idIndex:   make(map[string]int, len(existing)),
		metaIndex: make(map[string]int, len(existing)),
		discIndex: make(map[string][]int, len(existing)),
	}
	for i, s := range existing {
		idx.register(i, s)
	}
	return idx
}

func (m *songMergeIndex) register(idx int, s *models.Song) {
	if s == nil {
		return
	}
	if s.ID != "" {
		m.idIndex[s.ID] = idx
	}
	if mk := songMetaKey(s); mk != emptyMetaKey {
		m.metaIndex[mk] = idx
	}
	if dk := songDiscKey(s); dk != "" {
		m.discIndex[dk] = append(m.discIndex[dk], idx)
	}
}

func (m *songMergeIndex) merge(s *models.Song) {
	if s == nil {
		return
	}
	if m.matchByID(s) {
		return
	}
	mk := songMetaKey(s)
	if m.matchByMeta(mk, s) {
		return
	}
	if m.matchByDisc(mk, s) {
		return
	}
	idx := len(m.result)
	m.result = append(m.result, s)
	m.register(idx, s)
}

func (m *songMergeIndex) matchByID(s *models.Song) bool {
	if s.ID == "" {
		return false
	}
	idx, ok := m.idIndex[s.ID]
	if !ok {
		return false
	}
	m.result[idx] = preserveSongLocation(m.result[idx], s)
	return true
}

func (m *songMergeIndex) matchByMeta(mk string, s *models.Song) bool {
	if mk == emptyMetaKey {
		return false
	}
	idx, ok := m.metaIndex[mk]
	if !ok {
		return false
	}
	m.result[idx] = preserveSongLocation(m.result[idx], s)
	return true
}

func (m *songMergeIndex) matchByDisc(mk string, s *models.Song) bool {
	dk := songDiscKey(s)
	if dk == "" {
		return false
	}
	incomingSource := strings.TrimSpace(s.Source)
	matchedIdx := m.findDiscMatch(dk, incomingSource)
	if matchedIdx < 0 {
		return false
	}
	merged := preserveSongLocation(m.result[matchedIdx], s)
	if incomingSource == "" && strings.TrimSpace(m.result[matchedIdx].Source) != "" {
		cp := *merged
		cp.Source = m.result[matchedIdx].Source
		merged = &cp
	}
	m.result[matchedIdx] = merged
	if mk != emptyMetaKey {
		m.metaIndex[mk] = matchedIdx
	}
	return true
}

func (m *songMergeIndex) findDiscMatch(dk, incomingSource string) int {
	for _, candidateIdx := range m.discIndex[dk] {
		existingSource := strings.TrimSpace(m.result[candidateIdx].Source)
		if existingSource == "" || incomingSource == "" {
			return candidateIdx
		}
	}
	return -1
}

func songMetaKey(s *models.Song) string {
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

func songDiscKey(s *models.Song) string {
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

func preserveSongLocation(existing, incoming *models.Song) *models.Song {
	if strings.TrimSpace(incoming.Location) == "" && strings.TrimSpace(existing.Location) != "" {
		cp := *incoming
		cp.Location = existing.Location
		return &cp
	}
	return incoming
}

// MergeSongs combines existing catalog songs with incoming manifest songs when pruneMissing is false.
// Incoming songs update matching existing songs; unmatched existing songs are preserved, and new incoming songs are appended.
func MergeSongs(existing []*models.Song, incoming []*models.Song) []*models.Song {
	if len(existing) == 0 {
		return incoming
	}
	if len(incoming) == 0 {
		return existing
	}

	idx := newSongMergeIndex(existing)
	for _, s := range incoming {
		idx.merge(s)
	}
	return idx.result
}
