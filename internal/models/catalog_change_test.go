package models

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestApplyCatalogChanges_RespectsClearTimestampRegardlessOfOrder(t *testing.T) {
	t0 := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	t1 := t0.Add(1 * time.Minute)
	t2 := t0.Add(2 * time.Minute)
	t3 := t0.Add(3 * time.Minute)

	base := []*Song{
		{ID: "base-1", Artist: "Base Artist", Title: "Base Song"},
	}

	// Place Clear AFTER an older edit and BEFORE/AFTER a newer edit to prove slice order cannot change the outcome.
	orders := [][]*CatalogChange{
		{
			{SongID: "old-edit", Song: &Song{ID: "old-edit", Title: "Before Clear"}, UpdatedAt: t1},
			{SongID: "new-add", Song: &Song{ID: "new-add", Title: "After Clear"}, UpdatedAt: t3},
			{SongID: "__clear__", Clear: true, UpdatedAt: t2},
		},
		{
			{SongID: "__clear__", Clear: true, UpdatedAt: t2},
			{SongID: "new-add", Song: &Song{ID: "new-add", Title: "After Clear"}, UpdatedAt: t3},
			{SongID: "old-edit", Song: &Song{ID: "old-edit", Title: "Before Clear"}, UpdatedAt: t1},
		},
	}

	for i, changes := range orders {
		got := ApplyCatalogChanges(base, changes)
		if len(got) != 1 {
			t.Fatalf("order %d: expected 1 surviving song, got %d (%+v)", i, len(got), got)
		}
		if got["new-add"] == nil || got["new-add"].Title != "After Clear" {
			t.Fatalf("order %d: expected new-add to survive, got %+v", i, got)
		}
		if _, exists := got["old-edit"]; exists {
			t.Fatalf("order %d: stale pre-clear edit resurrected", i)
		}
		if _, exists := got["base-1"]; exists {
			t.Fatalf("order %d: base song survived clear", i)
		}

		// Verify ResolveCatalogChange agrees on every ID.
		if song, resolved := ResolveCatalogChange(changes, "new-add"); !resolved || song == nil || song.ID != "new-add" {
			t.Fatalf("order %d: ResolveCatalogChange(new-add) = (%v, %v)", i, song, resolved)
		}
		if song, resolved := ResolveCatalogChange(changes, "old-edit"); !resolved || song != nil {
			t.Fatalf("order %d: ResolveCatalogChange(old-edit) = (%v, %v), want (nil, true)", i, song, resolved)
		}
		if song, resolved := ResolveCatalogChange(changes, "base-1"); !resolved || song != nil {
			t.Fatalf("order %d: ResolveCatalogChange(base-1) = (%v, %v), want (nil, true)", i, song, resolved)
		}
	}
}

func TestApplyCatalogChanges_ZeroTimestampClearAndDelete(t *testing.T) {
	base := []*Song{
		{ID: "s-1", Title: "One"},
		{ID: "s-2", Title: "Two"},
	}
	changes := []*CatalogChange{
		{SongID: "s-3", Song: &Song{ID: "s-3", Title: "Three"}},
		{SongID: "__clear__", Clear: true},
	}
	got := ApplyCatalogChanges(base, changes)
	if len(got) != 1 || got["s-3"] == nil {
		t.Fatalf("expected only s-3 after zero-timestamp clear, got %+v", got)
	}
}

func TestInternalLeaseAndDispatchFieldsOmittedFromJSON(t *testing.T) {
	now := time.Now().UTC()
	job := ImportJob{
		ID:                     "job-1",
		SongbookID:             "sb-1",
		ManifestKey:            "imports/sb-1/job-1.json",
		Status:                 ImportJobQueued,
		DispatchReady:          true,
		DispatchClaimToken:     "secret-claim-token",
		DispatchClaimExpiresAt: &now,
		DispatchAttempts:       3,
		DispatchedAt:           &now,
	}
	rawJob, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("marshal import job: %v", err)
	}
	for _, forbidden := range []string{
		"dispatch_ready",
		"dispatch_claim_token",
		"dispatch_claim_expires_at",
		"dispatch_attempts",
		"dispatched_at",
		"secret-claim-token",
	} {
		if strings.Contains(string(rawJob), forbidden) {
			t.Fatalf("ImportJob JSON leaked internal field %q: %s", forbidden, string(rawJob))
		}
	}

	version := CatalogVersion{
		ID:             "ver-1",
		SchemaVersion:  1,
		Status:         CatalogVersionBuilding,
		LeaseToken:     "secret-lease-token",
		LeaseExpiresAt: now,
	}
	rawVersion, err := json.Marshal(version)
	if err != nil {
		t.Fatalf("marshal catalog version: %v", err)
	}
	for _, forbidden := range []string{
		"lease_token",
		"lease_expires_at",
		"secret-lease-token",
	} {
		if strings.Contains(string(rawVersion), forbidden) {
			t.Fatalf("CatalogVersion JSON leaked internal field %q: %s", forbidden, string(rawVersion))
		}
	}
}
