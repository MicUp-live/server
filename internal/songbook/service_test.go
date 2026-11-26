package songbook

import (
	"context"
	"errors"
	"os"
	"testing"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

type fakeSongbookStore struct {
	store.SongbookStore
	books map[string]*models.Songbook
}

func newFakeSongbookStore() *fakeSongbookStore {
	return &fakeSongbookStore{books: make(map[string]*models.Songbook)}
}

func (s *fakeSongbookStore) CreateSongbook(_ context.Context, sb *models.Songbook) error {
	s.books[sb.ID] = sb
	return nil
}

func (s *fakeSongbookStore) GetSongbook(_ context.Context, id string) (*models.Songbook, error) {
	sb, ok := s.books[id]
	if !ok {
		return nil, errors.New("not found")
	}
	cp := *sb
	return &cp, nil
}

func (s *fakeSongbookStore) GetPublicSongbook(_ context.Context, publicID string) (*models.Songbook, error) {
	for _, sb := range s.books {
		if sb.PublicID == publicID {
			cp := *sb
			return &cp, nil
		}
	}
	return nil, errors.New("not found")
}

func (s *fakeSongbookStore) GetSongbookBySlug(_ context.Context, slug string) (*models.Songbook, error) {
	for _, sb := range s.books {
		if sb.Slug == slug {
			cp := *sb
			return &cp, nil
		}
	}
	return nil, errors.New("not found")
}

func (s *fakeSongbookStore) ListSongbooks(_ context.Context) ([]*models.Songbook, error) {
	list := make([]*models.Songbook, 0, len(s.books))
	for _, sb := range s.books {
		cp := *sb
		list = append(list, &cp)
	}
	return list, nil
}

func (s *fakeSongbookStore) ListSongbooksByOwner(_ context.Context, ownerID string) ([]*models.Songbook, error) {
	list := make([]*models.Songbook, 0, len(s.books))
	for _, sb := range s.books {
		if sb.OwnerID == ownerID {
			cp := *sb
			list = append(list, &cp)
		}
	}
	return list, nil
}

func (s *fakeSongbookStore) NextCatalogGeneration(_ context.Context, _ string) (int64, error) {
	return 1, nil
}

func (s *fakeSongbookStore) BeginSongbookDeletion(_ context.Context, id string) (*models.Songbook, error) {
	if sb, ok := s.books[id]; ok {
		sb.Deleting = true
		cp := *sb
		return &cp, nil
	}
	return &models.Songbook{ID: id, Deleting: true}, nil
}

func (s *fakeSongbookStore) UpdateSongbook(_ context.Context, sb *models.Songbook) error {
	if _, ok := s.books[sb.ID]; !ok {
		return errors.New("not found")
	}
	s.books[sb.ID] = sb
	return nil
}

func (s *fakeSongbookStore) DeleteSongbook(_ context.Context, id string) error {
	if _, ok := s.books[id]; !ok {
		return errors.New("not found")
	}
	delete(s.books, id)
	return nil
}

func (f *fakeSongbookStore) ConfirmSongbookDeletion(_ context.Context, _ string) error { return nil }
func (f *fakeSongbookStore) AbortSongbookDeletion(_ context.Context, _ string) error   { return nil }

type fakeEventStore struct {
	store.EventStore
	events map[string][]*models.Event
}

func (e *fakeEventStore) CreateEvent(_ context.Context, _ *models.Event) error { return nil }
func (e *fakeEventStore) GetEvent(_ context.Context, _ string) (*models.Event, error) {
	return nil, errors.New("not found")
}
func (e *fakeEventStore) GetEventByJoinCode(_ context.Context, _ string) (*models.Event, error) {
	return nil, errors.New("not found")
}
func (e *fakeEventStore) ListEventsByOwner(_ context.Context, _ string) ([]*models.Event, error) {
	return nil, nil
}
func (e *fakeEventStore) ListEventsBySongbook(_ context.Context, songbookID string) ([]*models.Event, error) {
	return e.events[songbookID], nil
}
func (e *fakeEventStore) UpdateEvent(_ context.Context, _ *models.Event) error { return nil }
func (e *fakeEventStore) UpdateEventWithVersion(_ context.Context, _ *models.Event, _ bool) error {
	return nil
}
func (e *fakeEventStore) DeleteEvent(_ context.Context, _ string) error        { return nil }
func (e *fakeEventStore) BeginEventDeletion(_ context.Context, _ string) error { return nil }

func TestSongbookService_CreateAndGet(t *testing.T) {
	sbStore := newFakeSongbookStore()
	evStore := &fakeEventStore{events: make(map[string][]*models.Event)}
	svc := NewService(sbStore, evStore)

	ctx := context.Background()

	// 1. Create with slug
	created, err := svc.CreateSongbook(ctx, "user-1", models.SongbookCreate{
		Name:     "Test Book",
		Slug:     "test-book",
		IsPublic: true,
	})
	if err != nil {
		t.Fatalf("unexpected error creating songbook: %v", err)
	}
	if created.ID == "" || created.PublicID == "" || created.OwnerID != "user-1" {
		t.Fatalf("unexpected created songbook fields: %+v", created)
	}

	// 2. Duplicate slug should fail with ErrSlugTaken
	_, err = svc.CreateSongbook(ctx, "user-2", models.SongbookCreate{
		Name: "Another Book",
		Slug: "test-book",
	})
	if !errors.Is(err, ErrSlugTaken) {
		t.Fatalf("expected ErrSlugTaken, got %v", err)
	}

	// 3. Invalid slug should fail
	_, err = svc.CreateSongbook(ctx, "user-3", models.SongbookCreate{
		Name: "Invalid Slug",
		Slug: "INVALID SLUG!",
	})
	if !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("expected os.ErrInvalid, got %v", err)
	}

	// 4. Get created
	got, err := svc.GetSongbook(ctx, created.ID)
	if err != nil {
		t.Fatalf("unexpected error getting songbook: %v", err)
	}
	if got.Name != "Test Book" {
		t.Fatalf("expected 'Test Book', got %s", got.Name)
	}
}

func TestSongbookService_DeleteBlockedByEvents(t *testing.T) {
	sbStore := newFakeSongbookStore()
	evStore := &fakeEventStore{events: make(map[string][]*models.Event)}
	svc := NewService(sbStore, evStore)

	ctx := context.Background()
	created, _ := svc.CreateSongbook(ctx, "user-1", models.SongbookCreate{Name: "Event Book"})

	// Attach an event
	evStore.events[created.ID] = []*models.Event{
		{ID: "ev-1", SongbookID: created.ID, Name: "Friday Karaoke"},
	}

	err := svc.DeleteSongbook(ctx, created.ID)
	var eventsErr *EventsAssociatedError
	if !errors.As(err, &eventsErr) {
		t.Fatalf("expected EventsAssociatedError, got %v", err)
	}
	if len(eventsErr.EventNames) != 1 || eventsErr.EventNames[0] != `"Friday Karaoke"` {
		t.Fatalf("unexpected event names in error: %v", eventsErr.EventNames)
	}

	// Clear events and delete successfully
	evStore.events[created.ID] = nil
	if err := svc.DeleteSongbook(ctx, created.ID); err != nil {
		t.Fatalf("expected successful delete, got %v", err)
	}
}
