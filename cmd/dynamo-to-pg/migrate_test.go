package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"

	"micup.live/server/internal/models"
	dynamostore "micup.live/server/internal/store/dynamo"
	pgstore "micup.live/server/internal/store/postgres"
)

func sha256Hex(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func sha256Bytes(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

func TestMigrateEndToEnd(t *testing.T) {
	dynamoEndpoint := os.Getenv("DYNAMO_ENDPOINT")
	databaseURL := os.Getenv("DATABASE_URL")
	if dynamoEndpoint == "" || databaseURL == "" {
		t.Skip("DYNAMO_ENDPOINT and DATABASE_URL must be set for e2e migration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "us-east-2"
	}
	prefix := fmt.Sprintf("migtest-%s-", uuid.NewString()[:8])

	ddb, err := newDynamoClient(ctx, region, dynamoEndpoint)
	if err != nil {
		t.Fatalf("newDynamoClient: %v", err)
	}
	dynStore, err := dynamostore.New(ctx, dynamostore.Config{
		Region:      region,
		Endpoint:    dynamoEndpoint,
		TablePrefix: prefix,
	})
	if err != nil {
		t.Fatalf("dynamostore.New: %v", err)
	}
	defer func() { _ = dynStore.Close() }()
	if err := dynStore.EnsureTables(ctx); err != nil {
		t.Fatalf("dynStore.EnsureTables: %v", err)
	}
	t.Cleanup(func() {
		cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanCancel()
		for _, suffix := range []string{"users", "identity-claims", "sessions", "songbooks", "event-requests", "events", "passkeys", "webauthn-sessions", "catalog-versions", "import-jobs", "catalog-changes", "developer-keys", "rate-limits", "idempotency", "songbook-grants", "songbook-invitations"} {
			_, _ = ddb.DeleteTable(cleanCtx, &dynamodb.DeleteTableInput{TableName: aws.String(prefix + suffix)})
		}
	})

	// Isolate this test in its own Postgres schema so parallel package tests
	// (e.g. internal/store/postgres) never collide on shared tables.
	schemaName := "mig_test_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	adminPG, err := pgstore.New(ctx, pgstore.Config{DSN: databaseURL})
	if err != nil {
		t.Fatalf("pgstore.New (admin): %v", err)
	}
	if _, err := adminPG.Pool().Exec(ctx, "CREATE SCHEMA "+schemaName); err != nil {
		_ = adminPG.Close()
		t.Fatalf("create test schema %s: %v", schemaName, err)
	}
	t.Cleanup(func() {
		cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanCancel()
		_, _ = adminPG.Pool().Exec(cleanCtx, "DROP SCHEMA IF EXISTS "+schemaName+" CASCADE")
		_ = adminPG.Close()
	})
	parsedDSN, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	q := parsedDSN.Query()
	q.Set("search_path", schemaName)
	parsedDSN.RawQuery = q.Encode()

	pg, err := pgstore.New(ctx, pgstore.Config{DSN: parsedDSN.String()})
	if err != nil {
		t.Fatalf("pgstore.New: %v", err)
	}
	defer func() { _ = pg.Close() }()
	if err := pg.EnsureSchema(ctx); err != nil {
		t.Fatalf("pg.EnsureSchema: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	seed := seedDynamoFixture(t, ctx, ddb, dynStore, prefix, now)

	quietLog := log.New(io.Discard, "", 0)

	// 1. Dry-run must scan & validate without writing to Postgres.
	// Use a deterministic migration clock slightly ahead of `now` so items seeded
	// with ttl=now+30s are expired to Migrate without racing DynamoDB Local's TTL reaper.
	migrateNow := now.Add(time.Minute)
	dryReport, err := Migrate(ctx, ddb, prefix, pg, Options{
		DryRun:        true,
		SkipEphemeral: true,
		EnsureSchema:  true,
		Now:           migrateNow,
		Logger:        quietLog,
	})
	if err != nil {
		t.Fatalf("dry-run Migrate: %v", err)
	}
	assertTableStats(t, dryReport, "users", 2, 2, 0)
	assertTableStats(t, dryReport, "songbook_invitations", 4, 3, 1) // 3 real + 1 pending# slot
	assertTableStats(t, dryReport, "event_requests", 5, 1, 4)       // 1 active + 1 singer slot + 1 restart slot + 1 expired + 1 orphaned

	var pgUsers int64
	if err := pg.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&pgUsers); err != nil {
		t.Fatalf("count users after dry-run: %v", err)
	}
	if pgUsers != 0 {
		t.Fatalf("expected 0 users in Postgres after dry-run, got %d", pgUsers)
	}

	// 2. Apply twice to verify idempotency.
	for run := 1; run <= 2; run++ {
		rep, err := Migrate(ctx, ddb, prefix, pg, Options{
			DryRun:        false,
			SkipEphemeral: true,
			EnsureSchema:  true,
			Now:           migrateNow,
			Logger:        quietLog,
		})
		if err != nil {
			t.Fatalf("apply Migrate run %d: %v", run, err)
		}
		wantCounts := map[string]int64{
			"users": 2, "auth_sessions": 1, "passkey_credentials": 1, "developer_keys": 1,
			"catalog_versions": 1, "songbooks": 3, "songbook_grants": 1, "songbook_invitations": 3,
			"catalog_changes": 2, "import_jobs": 1, "events": 2, "event_requests": 1,
			"idempotency_records": 1, "webauthn_sessions": 0, "rate_limits": 0,
		}
		for tbl, want := range wantCounts {
			assertVerifiedCount(t, rep, tbl, want)
		}
	}

	verifyPostgresRoundTrip(t, ctx, pg, seed)

	// 3. Re-run with --skip-ephemeral=false and confirm active ephemeral rows copy over.
	ephemRep, err := Migrate(ctx, ddb, prefix, pg, Options{
		DryRun:        false,
		SkipEphemeral: false,
		EnsureSchema:  false,
		Now:           now,
		Logger:        quietLog,
	})
	if err != nil {
		t.Fatalf("apply Migrate with skip-ephemeral=false: %v", err)
	}
	assertVerifiedCount(t, ephemRep, "webauthn_sessions", 1)
	assertVerifiedCount(t, ephemRep, "rate_limits", 1)
}

type fixtureIDs struct {
	hostID, collabID              string
	sessionToken                  string
	passkeyID, devRawKey          string
	catalogVerID                  string
	sbMainID, sbEmptySlugID       string
	sbOrphanCV                    string
	invPendingID, invCanceledID   string
	invAcceptedID                 string
	importJobID                   string
	activeEventID, detachedBookID string
	detachedEventID               string
	reqID                         string
	idemKey, idemHash             string
}

func seedDynamoFixture(t *testing.T, ctx context.Context, ddb *dynamodb.Client, ds *dynamostore.Store, prefix string, now time.Time) fixtureIDs {
	t.Helper()
	f := fixtureIDs{
		hostID:          uuid.NewString(),
		collabID:        uuid.NewString(),
		sessionToken:    "refresh-secret-1",
		passkeyID:       "cred-" + uuid.NewString(),
		devRawKey:       "micup_dev_" + uuid.NewString(),
		catalogVerID:    "cv-" + uuid.NewString(),
		sbMainID:        uuid.NewString(),
		sbEmptySlugID:   uuid.NewString(),
		sbOrphanCV:      uuid.NewString(),
		invPendingID:    uuid.NewString(),
		invCanceledID:   uuid.NewString(),
		invAcceptedID:   uuid.NewString(),
		importJobID:     uuid.NewString(),
		activeEventID:   uuid.NewString(),
		detachedBookID:  uuid.NewString(),
		detachedEventID: uuid.NewString(),
		reqID:           uuid.NewString(),
		idemKey:         "idem-" + uuid.NewString(),
		idemHash:        sha256Hex("req-payload"),
	}

	// Users (host has mixed-case/padded email + favorites).
	must(t, ds.CreateUser(ctx, &models.User{
		ID: f.hostID, Email: "  Host@Example.COM ", DisplayName: "DJ Host",
		PasswordHash: "pw-hash", AuthEpoch: "ep-1", IsHost: true, EmailVerified: true,
		CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-time.Hour),
	}), "CreateUser host")
	must(t, ds.SaveUserFavorites(ctx, f.hostID, &models.UserFavorites{
		Songs:   []models.FavoriteSong{{Artist: "Queen", Title: "Bohemian Rhapsody", PreferredKey: 1, AddedAt: now.UnixMilli()}},
		Artists: []models.FavoriteArtist{{Name: "Queen", AddedAt: now.UnixMilli()}},
	}), "SaveUserFavorites")
	must(t, ds.CreateUser(ctx, &models.User{
		ID: f.collabID, Email: "collab@example.com", DisplayName: "Collab",
		PasswordHash: "pw-hash-2", AuthEpoch: "ep-2", IsHost: true, EmailVerified: true,
	}), "CreateUser collab")

	// Sessions: 1 active, 1 expired, 1 orphaned.
	must(t, ds.CreateAuthSession(ctx, &models.AuthSession{
		TokenHash: sha256Bytes(f.sessionToken), UserID: f.hostID, AuthEpoch: "ep-1", ExpiresAt: now.Add(24 * time.Hour),
	}), "CreateAuthSession")
	putRawDynamo(t, ctx, ddb, prefix+"sessions", map[string]types.AttributeValue{
		"token_hash": &types.AttributeValueMemberS{Value: sha256Hex("expired-sess")},
		"user_id":    &types.AttributeValueMemberS{Value: f.hostID},
		"auth_epoch": &types.AttributeValueMemberS{Value: "ep-1"},
		"expires_at": &types.AttributeValueMemberS{Value: now.Add(-time.Minute).Format(time.RFC3339Nano)},
		"ttl":        &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", now.Add(-time.Minute).Unix())},
	})
	putRawDynamo(t, ctx, ddb, prefix+"sessions", map[string]types.AttributeValue{
		"token_hash": &types.AttributeValueMemberS{Value: sha256Hex("orphan-sess")},
		"user_id":    &types.AttributeValueMemberS{Value: "deleted-user-id"},
		"auth_epoch": &types.AttributeValueMemberS{Value: "ep-x"},
		"expires_at": &types.AttributeValueMemberS{Value: now.Add(time.Hour).Format(time.RFC3339Nano)},
		"ttl":        &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", now.Add(time.Hour).Unix())},
	})

	// Passkey, WebAuthn session, Developer key, Rate limit.
	must(t, ds.SavePasskeyCredential(ctx, &models.PasskeyCredential{
		ID: f.passkeyID, UserID: f.hostID, PublicKey: []byte("pk-bytes"),
		AttestationType: "none", AAGUID: []byte("1234567890123456"), SignCount: 3,
		Transports: []string{"internal"}, BackupEligible: true, UserPresent: true, FriendlyName: "MacBook",
	}), "SavePasskeyCredential")
	must(t, ds.SaveWebAuthnSession(ctx, &models.WebAuthnSession{
		SessionID: "wa-sess-1", UserID: f.hostID, Challenge: "ch-1",
		SessionData: []byte(`{"challenge":"ch-1"}`), ExpiresAt: now.Add(10 * time.Minute),
	}), "SaveWebAuthnSession")
	must(t, ds.CreateDeveloperKey(ctx, f.devRawKey, &models.DeveloperKey{
		OwnerID: f.hostID, Name: "OpenKJ", KeyPrefix: "micup_dev", Scopes: []string{"events:read", "queue:write"},
	}), "CreateDeveloperKey")
	_, err := ds.Allow(ctx, "login", "127.0.0.1", 10, time.Minute)
	must(t, err, "Allow rate limit")

	// Catalog version & songbooks (one with slug + last_import_generation, one empty slug, one orphaned catalog_version_id).
	must(t, ds.CreateCatalogVersion(ctx, &models.CatalogVersion{
		ID: f.catalogVerID, SchemaVersion: 1, Status: models.CatalogVersionBuilding,
	}), "CreateCatalogVersion")
	_, err = ds.MarkCatalogVersionReady(ctx, f.catalogVerID, "catalogs/"+f.catalogVerID, 42)
	must(t, err, "MarkCatalogVersionReady")
	must(t, ds.CreateSongbook(ctx, &models.Songbook{
		ID: f.sbMainID, OwnerID: f.hostID, PublicID: "pub-main", Slug: "  Main-Book ",
		VenueID: "v-1", CatalogVersionID: f.catalogVerID, Name: "Main Book", IsPublic: true, SongCount: 42,
	}), "CreateSongbook main")
	_, err = ds.NextCatalogGeneration(ctx, f.sbMainID)
	must(t, err, "NextCatalogGeneration 1")
	_, err = ds.NextCatalogGeneration(ctx, f.sbMainID)
	must(t, err, "NextCatalogGeneration 2")
	must(t, ds.CreateSongbook(ctx, &models.Songbook{
		ID: f.sbEmptySlugID, OwnerID: f.hostID, PublicID: "pub-empty", Slug: "", Name: "No Slug Book",
	}), "CreateSongbook empty slug")
	must(t, ds.CreateSongbook(ctx, &models.Songbook{
		ID: f.sbOrphanCV, OwnerID: f.hostID, PublicID: "pub-orphan-cv", CatalogVersionID: "missing-cv", Name: "Orphan CV Book",
	}), "CreateSongbook orphan cv")

	// Grants & Invitations: accepted (creates grant), pending (creates pending# slot), canceled (clears token_hash).
	accTok := sha256Hex("inv-acc-tok")
	must(t, ds.CreateSongbookInvitation(ctx, &models.SongbookInvitation{
		ID: f.invAcceptedID, SongbookID: f.sbMainID, OwnerID: f.hostID, RecipientEmail: "collab@example.com",
		CanEditCatalog: true, CanHostShows: true, TokenHash: accTok, ExpiresAt: now.Add(24 * time.Hour),
	}, nil), "CreateSongbookInvitation accepted")
	_, _, err = ds.AcceptSongbookInvitation(ctx, accTok, f.collabID, "collab@example.com", now)
	must(t, err, "AcceptSongbookInvitation")
	must(t, ds.CreateSongbookInvitation(ctx, &models.SongbookInvitation{
		ID: f.invCanceledID, SongbookID: f.sbMainID, OwnerID: f.hostID, RecipientEmail: "canceled@example.com",
		CanHostShows: true, TokenHash: sha256Hex("inv-cancel-tok"), ExpiresAt: now.Add(24 * time.Hour),
	}, nil), "CreateSongbookInvitation canceled")
	must(t, ds.CancelSongbookInvitation(ctx, f.sbMainID, f.invCanceledID), "CancelSongbookInvitation")
	must(t, ds.CreateSongbookInvitation(ctx, &models.SongbookInvitation{
		ID: f.invPendingID, SongbookID: f.sbMainID, OwnerID: f.hostID, RecipientEmail: "pending@example.com",
		CanEditCatalog: true, TokenHash: sha256Hex("inv-pending-tok"), ExpiresAt: now.Add(24 * time.Hour),
	}, nil), "CreateSongbookInvitation pending")

	// Catalog changes & Import job.
	must(t, ds.PutCatalogChange(ctx, &models.CatalogChange{
		SongbookID: f.sbMainID, SongID: "song-1",
		Song: &models.Song{ID: "song-1", Artist: "Adele", Title: "Hello", IsAvailable: true},
	}), "PutCatalogChange song")
	must(t, ds.PutCatalogChange(ctx, &models.CatalogChange{
		SongbookID: f.sbMainID, SongID: "__clear__", Clear: true, UpdatedAt: now.Add(-time.Minute),
	}), "PutCatalogChange clear")
	must(t, ds.CreateImportJob(ctx, &models.ImportJob{
		ID: f.importJobID, SongbookID: f.sbMainID, ManifestKey: "imports/m.json",
		Status: models.ImportJobQueued, TotalSongs: 10, PruneMissing: true, Generation: 2,
	}), "CreateImportJob")

	// Events: 1 active, 1 detached (via missing songbook_id -> detached), 1 expired TTL.
	must(t, ds.CreateEvent(ctx, &models.Event{
		ID: f.activeEventID, OwnerID: f.hostID, SongbookID: f.sbMainID, VenueName: "Bar",
		Name: "Live Night", JoinCode: "abcd", Status: models.EventStatusActive,
		MaxActivePerSinger: 1, AllowKeyChange: true, SingerWeights: map[string]int{"alice": 2},
	}), "CreateEvent active")
	must(t, ds.CreateEvent(ctx, &models.Event{
		ID: f.detachedEventID, OwnerID: f.hostID, SongbookID: f.detachedBookID, VenueName: "Bar",
		Name: "Detached Night", JoinCode: "wxyz", Status: models.EventStatusActive, AllowKeyChange: true,
	}), "CreateEvent detached")
	_, err = ds.DetachEventFromSongbook(ctx, f.detachedEventID, f.detachedBookID, "Old Book", 14*24*time.Hour)
	must(t, err, "DetachEventFromSongbook")
	putRawDynamo(t, ctx, ddb, prefix+"events", map[string]types.AttributeValue{
		"id": &types.AttributeValueMemberS{Value: "expired-ev"}, "owner_id": &types.AttributeValueMemberS{Value: f.hostID},
		"name": &types.AttributeValueMemberS{Value: "Old"}, "join_code": &types.AttributeValueMemberS{Value: "OLD1"},
		"status": &types.AttributeValueMemberS{Value: "ended"}, "created_at": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)},
		"updated_at": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)},
		"ttl":        &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", now.Add(30*time.Second).Unix())},
	})

	// Requests (CreateRequestAtVersion adds a singer slot# item) + restart# slot + expired + orphaned + idempotency.
	req := &models.SongRequest{
		ID: f.reqID, EventID: f.activeEventID, SongbookID: f.sbMainID, SongID: "song-1",
		SongTitle: "Hello", SongArtist: "Adele", SingerName: "Alice", Status: models.StatusPending, Round: 1, Rank: 100,
	}
	if err := ds.CreateRequestAtVersion(ctx, req, 1, nil, 1); err != nil {
		t.Fatalf("CreateRequestAtVersion: %v", err)
	}
	req.Status = models.StatusAccepted
	if _, err := ds.UpdateRequestWithIdempotency(ctx, req, nil, f.idemKey, f.hostID, "POST /accept", f.idemHash); err != nil {
		t.Fatalf("UpdateRequestWithIdempotency: %v", err)
	}
	putRawDynamo(t, ctx, ddb, prefix+"event-requests", map[string]types.AttributeValue{
		"event_id": &types.AttributeValueMemberS{Value: "slots#" + f.activeEventID},
		"id":       &types.AttributeValueMemberS{Value: "restart#1"},
	})
	putRawDynamo(t, ctx, ddb, prefix+"event-requests", map[string]types.AttributeValue{
		"event_id": &types.AttributeValueMemberS{Value: f.activeEventID},
		"id":       &types.AttributeValueMemberS{Value: "expired-req"},
		"ttl":      &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", now.Add(30*time.Second).Unix())},
	})
	putRawDynamo(t, ctx, ddb, prefix+"event-requests", map[string]types.AttributeValue{
		"event_id": &types.AttributeValueMemberS{Value: "missing-event"},
		"id":       &types.AttributeValueMemberS{Value: "orphan-req"},
	})
	putRawDynamo(t, ctx, ddb, prefix+"idempotency", map[string]types.AttributeValue{
		"idempotency_key": &types.AttributeValueMemberS{Value: "expired-idem"},
		"owner_id":        &types.AttributeValueMemberS{Value: f.hostID},
		"route":           &types.AttributeValueMemberS{Value: "r"},
		"entity_id":       &types.AttributeValueMemberS{Value: "e"},
		"request_hash":    &types.AttributeValueMemberS{Value: "h"},
		"status_code":     &types.AttributeValueMemberN{Value: "200"},
		"response_body":   &types.AttributeValueMemberS{Value: "{}"},
		"queue_version":   &types.AttributeValueMemberN{Value: "1"},
		"ttl":             &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", now.Add(30*time.Second).Unix())},
	})
	return f
}

func verifyPostgresRoundTrip(t *testing.T, ctx context.Context, pg *pgstore.Store, f fixtureIDs) {
	t.Helper()
	u, err := pg.GetUser(ctx, f.hostID)
	if err != nil || u.Email != "host@example.com" {
		t.Fatalf("GetUser: u=%v err=%v", u, err)
	}
	if uByEmail, err := pg.GetUserByEmail(ctx, "HOST@example.com"); err != nil || uByEmail.ID != f.hostID {
		t.Fatalf("GetUserByEmail: u=%v err=%v", uByEmail, err)
	}
	favs, err := pg.GetUserFavorites(ctx, f.hostID)
	if err != nil || len(favs.Songs) != 1 || favs.Songs[0].Title != "Bohemian Rhapsody" || len(favs.Artists) != 1 || favs.Artists[0].Name != "Queen" {
		t.Fatalf("GetUserFavorites: favs=%+v err=%v", favs, err)
	}
	sessUser, err := pg.ConsumeAuthSession(ctx, sha256Bytes(f.sessionToken))
	if err != nil || sessUser.ID != f.hostID {
		t.Fatalf("ConsumeAuthSession: user=%v err=%v", sessUser, err)
	}
	if _, err := pg.GetPasskeyCredential(ctx, f.passkeyID); err != nil {
		t.Fatalf("GetPasskeyCredential: %v", err)
	}
	if dk, err := pg.GetDeveloperKey(ctx, f.devRawKey); err != nil || dk.OwnerID != f.hostID {
		t.Fatalf("GetDeveloperKey: dk=%v err=%v", dk, err)
	}
	if cv, err := pg.GetCatalogVersion(ctx, f.catalogVerID); err != nil || cv.SongCount != 42 {
		t.Fatalf("GetCatalogVersion: cv=%v err=%v", cv, err)
	}
	sb, err := pg.GetSongbook(ctx, f.sbMainID)
	if err != nil || sb.Slug != "main-book" || sb.CatalogVersionID != f.catalogVerID {
		t.Fatalf("GetSongbook main: sb=%+v err=%v", sb, err)
	}
	if nextGen, err := pg.NextCatalogGeneration(ctx, f.sbMainID); err != nil || nextGen != 3 {
		t.Fatalf("NextCatalogGeneration = %d, %v (want 3)", nextGen, err)
	}
	if sbEmpty, err := pg.GetSongbook(ctx, f.sbEmptySlugID); err != nil || sbEmpty.Slug != "" {
		t.Fatalf("GetSongbook empty slug: sb=%+v err=%v", sbEmpty, err)
	}
	if sbOrphan, err := pg.GetSongbook(ctx, f.sbOrphanCV); err != nil || sbOrphan.CatalogVersionID != "" {
		t.Fatalf("GetSongbook orphan cv: sb=%+v err=%v", sbOrphan, err)
	}
	if g, err := pg.GetSongbookGrant(ctx, f.sbMainID, f.collabID); err != nil || !g.CanEditCatalog {
		t.Fatalf("GetSongbookGrant: g=%+v err=%v", g, err)
	}
	if sbBySlug, err := pg.GetSongbookBySlug(ctx, "main-book"); err != nil || sbBySlug.ID != f.sbMainID {
		t.Fatalf("GetSongbookBySlug: sb=%+v err=%v", sbBySlug, err)
	}
	if invP, err := pg.GetSongbookInvitation(ctx, f.invPendingID); err != nil || invP.Status != models.InvitationStatusPending {
		t.Fatalf("GetSongbookInvitation pending: inv=%+v err=%v", invP, err)
	}
	if invTok, err := pg.GetSongbookInvitationByTokenHash(ctx, sha256Hex("inv-pending-tok")); err != nil || invTok.ID != f.invPendingID {
		t.Fatalf("GetSongbookInvitationByTokenHash: inv=%+v err=%v", invTok, err)
	}
	if invC, err := pg.GetSongbookInvitation(ctx, f.invCanceledID); err != nil || invC.TokenHash != "" || invC.Status != models.InvitationStatusCanceled {
		t.Fatalf("GetSongbookInvitation canceled: inv=%+v err=%v", invC, err)
	}
	if changes, err := pg.ListCatalogChanges(ctx, f.sbMainID); err != nil || len(changes) != 2 {
		t.Fatalf("ListCatalogChanges: len=%d err=%v", len(changes), err)
	}
	if job, err := pg.GetImportJob(ctx, f.importJobID); err != nil || job.Generation != 2 {
		t.Fatalf("GetImportJob: job=%+v err=%v", job, err)
	}
	if ev, err := pg.GetEvent(ctx, f.activeEventID); err != nil || ev.JoinCode != "ABCD" || ev.SingerWeights["alice"] != 2 {
		t.Fatalf("GetEvent active: ev=%+v err=%v", ev, err)
	}
	if dev, err := pg.GetEvent(ctx, f.detachedEventID); err != nil || dev.SongbookID != "" || dev.DetachedSongbookName != "Old Book" || dev.Status != models.EventStatusEnded || dev.TTL == 0 || dev.EndedAt == nil {
		t.Fatalf("GetEvent detached: ev=%+v err=%v", dev, err)
	}
	if reqs, err := pg.ListRequestsByEvent(ctx, f.activeEventID); err != nil || len(reqs) != 1 || reqs[0].ID != f.reqID || reqs[0].Status != models.StatusAccepted {
		t.Fatalf("ListRequestsByEvent: reqs=%+v err=%v", reqs, err)
	}
	if res, ok, err := pg.GetIdempotencyResult(ctx, f.idemKey, f.hostID, "POST /accept", f.reqID, f.idemHash); err != nil || !ok || res == nil {
		t.Fatalf("GetIdempotencyResult: ok=%v res=%+v err=%v", ok, res, err)
	}
	if _, err := pg.GetEvent(ctx, "expired-ev"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected expired event to be omitted, got err=%v", err)
	}
}

func must(t *testing.T, err error, label string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", label, err)
	}
}

func putRawDynamo(t *testing.T, ctx context.Context, ddb *dynamodb.Client, table string, item map[string]types.AttributeValue) {
	t.Helper()
	if _, err := ddb.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(table), Item: item}); err != nil {
		t.Fatalf("PutItem %s: %v", table, err)
	}
}

func assertTableStats(t *testing.T, r *Report, table string, scanned, migrated, skipped int) {
	t.Helper()
	for _, ts := range r.Tables {
		if ts.Table == table {
			if ts.Scanned != scanned || ts.Migrated != migrated || ts.Skipped != skipped {
				t.Fatalf("table %s stats = %+v, want scanned=%d migrated=%d skipped=%d", table, ts, scanned, migrated, skipped)
			}
			return
		}
	}
	t.Fatalf("table %s missing from report", table)
}

func assertVerifiedCount(t *testing.T, r *Report, table string, want int64) {
	t.Helper()
	for _, ts := range r.Tables {
		if ts.Table == table {
			if ts.VerifiedCount != want {
				t.Fatalf("table %s VerifiedCount = %d, want %d (stats=%+v)", table, ts.VerifiedCount, want, ts)
			}
			return
		}
	}
	t.Fatalf("table %s missing from report", table)
}
