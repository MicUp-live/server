package catalog

import (
	"testing"

	"micup.live/server/internal/models"
)

func TestMergeSongs(t *testing.T) {
	t.Run("EmptyExisting", func(t *testing.T) {
		incoming := []*models.Song{
			{Artist: "Queen", Title: "Bohemian Rhapsody", IsAvailable: true},
		}
		merged := MergeSongs(nil, incoming)
		if len(merged) != 1 || merged[0].Title != "Bohemian Rhapsody" {
			t.Fatalf("unexpected merged result: %+v", merged)
		}
	})

	t.Run("EmptyIncoming", func(t *testing.T) {
		existing := []*models.Song{
			{Artist: "Queen", Title: "Bohemian Rhapsody", IsAvailable: true},
		}
		merged := MergeSongs(existing, nil)
		if len(merged) != 1 || merged[0].Title != "Bohemian Rhapsody" {
			t.Fatalf("unexpected merged result: %+v", merged)
		}
	})

	t.Run("PreservesExistingAndAppendsNew", func(t *testing.T) {
		existing := []*models.Song{
			{ID: "trk_1", Artist: "Queen", Title: "Bohemian Rhapsody", Disc: "SC01", IsAvailable: true},
			{ID: "trk_2", Artist: "The Beatles", Title: "Yesterday", Disc: "SC02", IsAvailable: true},
		}
		incoming := []*models.Song{
			{ID: "import_1", Artist: "ABBA", Title: "Dancing Queen", Disc: "SC03", IsAvailable: true},
		}
		merged := MergeSongs(existing, incoming)
		if len(merged) != 3 {
			t.Fatalf("expected 3 songs, got %d: %+v", len(merged), merged)
		}
		if merged[0].Title != "Bohemian Rhapsody" || merged[1].Title != "Yesterday" || merged[2].Title != "Dancing Queen" {
			t.Fatalf("unexpected order/content: %+v", merged)
		}
	})

	t.Run("UpdatesExistingByMetaKey", func(t *testing.T) {
		existing := []*models.Song{
			{ID: "trk_1", Artist: "Queen", Title: "Bohemian Rhapsody", Disc: "SC01", Genre: "Rock", IsAvailable: true},
			{ID: "trk_2", Artist: "The Beatles", Title: "Yesterday", Disc: "SC02", IsAvailable: true},
		}
		incoming := []*models.Song{
			// Match "queen", "bohemian rhapsody", "sc01" with case variation and new video_url
			{Artist: "QUEEN", Title: "bohemian rhapsody", Disc: "sc01", Genre: "Classic Rock", VideoURL: "https://video.example/queen", IsAvailable: true},
			{Artist: "ABBA", Title: "Waterloo", Disc: "SC03", IsAvailable: true},
		}
		merged := MergeSongs(existing, incoming)
		if len(merged) != 3 {
			t.Fatalf("expected 3 songs, got %d", len(merged))
		}
		// Index 0 should be updated with the incoming song
		if merged[0].Genre != "Classic Rock" || merged[0].VideoURL != "https://video.example/queen" {
			t.Fatalf("existing song was not updated: %+v", merged[0])
		}
		// Index 1 should be preserved
		if merged[1].Title != "Yesterday" {
			t.Fatalf("unrelated existing song was altered: %+v", merged[1])
		}
		// Index 2 should be the new song
		if merged[2].Title != "Waterloo" {
			t.Fatalf("new song was not appended: %+v", merged[2])
		}
	})

	t.Run("PreservesDistinctSourcesWithoutDisc", func(t *testing.T) {
		existing := []*models.Song{
			{Artist: "Queen", Title: "Bohemian Rhapsody", Source: "KaraFun", IsAvailable: true},
		}
		incoming := []*models.Song{
			{Artist: "Queen", Title: "Bohemian Rhapsody", Source: "Sunfly", IsAvailable: true},
		}
		merged := MergeSongs(existing, incoming)
		if len(merged) != 2 {
			t.Fatalf("expected 2 distinct publisher versions, got %d: %+v", len(merged), merged)
		}
		if merged[0].Source != "KaraFun" || merged[1].Source != "Sunfly" {
			t.Fatalf("unexpected merged sources: %+v", merged)
		}
	})

	t.Run("UpgradesLegacyDiscTrackWithSourceWithoutDuplicating", func(t *testing.T) {
		existing := []*models.Song{
			{ID: "trk_old", Artist: "Queen", Title: "Bohemian Rhapsody", Disc: "SC8101-02", Source: "", IsAvailable: true},
		}
		incoming := []*models.Song{
			{ID: "import-1", Artist: "Queen", Title: "Bohemian Rhapsody", Disc: "SC8101-02", Source: "Sound Choice", IsAvailable: true},
		}
		merged := MergeSongs(existing, incoming)
		if len(merged) != 1 {
			t.Fatalf("expected 1 upgraded track without duplicate, got %d: %+v", len(merged), merged)
		}
		if merged[0].Source != "Sound Choice" {
			t.Fatalf("expected upgraded source Sound Choice, got %+v", merged[0])
		}
	})

	t.Run("PreservesDistinctVideoURLsWhenDiscAndSourceEmpty", func(t *testing.T) {
		existing := []*models.Song{
			{Artist: "Queen", Title: "Bohemian Rhapsody", VideoURL: "https://youtu.be/v1", IsAvailable: true},
		}
		incoming := []*models.Song{
			{Artist: "Queen", Title: "Bohemian Rhapsody", VideoURL: "https://youtu.be/v2", IsAvailable: true},
		}
		merged := MergeSongs(existing, incoming)
		if len(merged) != 2 {
			t.Fatalf("expected 2 distinct video tracks, got %d: %+v", len(merged), merged)
		}
	})

	t.Run("UpdatesExistingByID", func(t *testing.T) {
		existing := []*models.Song{
			{ID: "song-123", Artist: "Adele", Title: "Hello", IsAvailable: true},
		}
		incoming := []*models.Song{
			{ID: "song-123", Artist: "Adele", Title: "Hello (Live)", VideoURL: "https://video.example/adele", IsAvailable: true},
		}
		merged := MergeSongs(existing, incoming)
		if len(merged) != 1 {
			t.Fatalf("expected 1 song, got %d", len(merged))
		}
		if merged[0].Title != "Hello (Live)" || merged[0].VideoURL != "https://video.example/adele" {
			t.Fatalf("song was not updated by ID: %+v", merged[0])
		}
	})
}
