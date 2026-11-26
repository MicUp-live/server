package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

type mockS3Client struct {
	objects         map[string]time.Time
	deletedKeys     []string
	deletedPrefixes []string
	listErr         error
	pageSize        int
}

func newMockS3Client() *mockS3Client {
	return &mockS3Client{
		objects: make(map[string]time.Time),
	}
}

func (m *mockS3Client) ListObjectsV2(_ context.Context, input *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	prefix := aws.ToString(input.Prefix)
	delimiter := aws.ToString(input.Delimiter)

	out := &s3.ListObjectsV2Output{}
	prefixesSeen := make(map[string]bool)

	keys := make([]string, 0, len(m.objects))
	for k := range m.objects {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var allPrefixes []string
	var allObjects []types.Object

	for _, key := range keys {
		lastModified := m.objects[key]
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		if delimiter != "" {
			rest := strings.TrimPrefix(key, prefix)
			if idx := strings.Index(rest, delimiter); idx >= 0 {
				subPrefix := prefix + rest[:idx+1]
				if !prefixesSeen[subPrefix] {
					prefixesSeen[subPrefix] = true
					allPrefixes = append(allPrefixes, subPrefix)
				}
				continue
			}
		}
		allObjects = append(allObjects, types.Object{
			Key:          aws.String(key),
			LastModified: aws.Time(lastModified),
		})
	}

	if m.pageSize > 0 {
		offset := 0
		if input.ContinuationToken != nil {
			fmt.Sscanf(*input.ContinuationToken, "%d", &offset)
		}
		if len(allPrefixes) > 0 {
			end := offset + m.pageSize
			if end < len(allPrefixes) {
				out.IsTruncated = aws.Bool(true)
				out.NextContinuationToken = aws.String(fmt.Sprintf("%d", end))
				allPrefixes = allPrefixes[offset:end]
			} else {
				if offset < len(allPrefixes) {
					allPrefixes = allPrefixes[offset:]
				} else {
					allPrefixes = nil
				}
			}
		} else if len(allObjects) > 0 {
			end := offset + m.pageSize
			if end < len(allObjects) {
				out.IsTruncated = aws.Bool(true)
				out.NextContinuationToken = aws.String(fmt.Sprintf("%d", end))
				allObjects = allObjects[offset:end]
			} else {
				if offset < len(allObjects) {
					allObjects = allObjects[offset:]
				} else {
					allObjects = nil
				}
			}
		}
	}

	for _, p := range allPrefixes {
		out.CommonPrefixes = append(out.CommonPrefixes, types.CommonPrefix{
			Prefix: aws.String(p),
		})
	}
	out.Contents = allObjects
	return out, nil
}

func (m *mockS3Client) DeleteObject(_ context.Context, input *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	key := aws.ToString(input.Key)
	m.deletedKeys = append(m.deletedKeys, key)
	delete(m.objects, key)
	return &s3.DeleteObjectOutput{}, nil
}

func (m *mockS3Client) DeleteObjects(_ context.Context, input *s3.DeleteObjectsInput, _ ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
	if input.Delete != nil {
		for _, obj := range input.Delete.Objects {
			key := aws.ToString(obj.Key)
			m.deletedKeys = append(m.deletedKeys, key)
			delete(m.objects, key)
		}
	}
	return &s3.DeleteObjectsOutput{}, nil
}

type mockGCStore struct {
	books           []*models.Songbook
	versions        []*models.CatalogVersion
	jobs            []*models.ImportJob
	deletedVersions []string
	deletedJobs     []string
}

func (m *mockGCStore) ListSongbooks(_ context.Context) ([]*models.Songbook, error) {
	return m.books, nil
}
func (m *mockGCStore) ListCatalogVersions(_ context.Context) ([]*models.CatalogVersion, error) {
	return m.versions, nil
}
func (m *mockGCStore) ListImportJobs(_ context.Context) ([]*models.ImportJob, error) {
	return m.jobs, nil
}
func (m *mockGCStore) DeleteCatalogVersion(_ context.Context, id string, _ ...string) error {
	m.deletedVersions = append(m.deletedVersions, id)
	return nil
}
func (m *mockGCStore) DeleteImportJob(_ context.Context, id string) error {
	m.deletedJobs = append(m.deletedJobs, id)
	return nil
}
func (m *mockGCStore) PurgeCatalogVersion(ctx context.Context, id string, purgeAssets func(assetPrefix string) error, opts ...store.PurgeCatalogOptions) error {
	for _, v := range m.versions {
		if v.ID == id {
			if purgeAssets != nil && v.AssetPrefix != "" {
				if err := purgeAssets(strings.Trim(v.AssetPrefix, "/")); err != nil {
					return err
				}
			}
			break
		}
	}
	return m.DeleteCatalogVersion(ctx, id)
}
func extractCatalogIDFromPrefix(prefix string) string {
	trimmed := strings.Trim(prefix, "/")
	trimmed = strings.TrimPrefix(trimmed, "catalogs/")
	parts := strings.Split(trimmed, "/")
	if len(parts) > 1 && parts[0] == "epochs" {
		return parts[1]
	}
	return parts[0]
}

func (m *mockGCStore) PurgeOrphanCatalogPrefix(_ context.Context, prefix string, purgeAssets func(assetPrefix string) error, _ ...store.PurgeCatalogOptions) error {
	catalogID := extractCatalogIDFromPrefix(prefix)
	for _, b := range m.books {
		if b.CatalogVersionID == catalogID {
			return store.ErrCatalogVersionInUse
		}
	}
	for _, v := range m.versions {
		if v.ID == catalogID {
			if v.Status == models.CatalogVersionBuilding {
				return store.ErrCatalogDeletionConflict
			}
			existingPrefix := strings.Trim(v.AssetPrefix, "/")
			targetPrefix := strings.Trim(prefix, "/")
			isExact := existingPrefix != "" && existingPrefix == targetPrefix
			isDescendant := existingPrefix != "" && strings.HasPrefix(targetPrefix, existingPrefix+"/")
			isAncestor := targetPrefix != "" && strings.HasPrefix(existingPrefix, targetPrefix+"/")
			for _, b := range m.books {
				if b.CatalogVersionID == catalogID && (isExact || isDescendant || isAncestor) {
					return store.ErrCatalogVersionInUse
				}
			}
		}
	}
	if purgeAssets != nil {
		return purgeAssets(strings.Trim(prefix, "/"))
	}
	return nil
}

func TestExecuteGC_SafetyGuardZeroSongbooks(t *testing.T) {
	ctx := context.Background()
	s3c := newMockS3Client()
	store := &mockGCStore{
		books: []*models.Songbook{}, // 0 songbooks
		versions: []*models.CatalogVersion{
			{
				ID:          "v-old",
				Status:      models.CatalogVersionReady,
				AssetPrefix: "catalogs/v-old",
				CreatedAt:   time.Now().Add(-48 * time.Hour),
			},
		},
	}
	s3c.objects["catalogs/v-old/metadata.json"] = time.Now().Add(-48 * time.Hour)

	err := executeGC(ctx, store, s3c, "test-bucket", false, 24*time.Hour)
	if err != nil {
		t.Fatalf("executeGC: %v", err)
	}

	// Because books is 0 and versions > 0, safety guard must prevent deletion of unreferenced catalogs
	if len(store.deletedVersions) != 0 {
		t.Fatalf("expected 0 deleted versions due to safety guard, got %d", len(store.deletedVersions))
	}
}

func TestExecuteGC_OrphanPrefixGracePeriod(t *testing.T) {
	ctx := context.Background()
	s3c := newMockS3Client()

	now := time.Now().UTC()
	// Recent orphan prefix (created 10 minutes ago, within 24h grace period)
	s3c.objects["catalogs/v-recent-orphan/metadata.json"] = now.Add(-10 * time.Minute)
	// Old orphan prefix (created 48 hours ago, outside 24h grace period)
	s3c.objects["catalogs/v-old-orphan/metadata.json"] = now.Add(-48 * time.Hour)

	store := &mockGCStore{
		books: []*models.Songbook{
			{ID: "sb-1", CatalogVersionID: "v-active"},
		},
		versions: []*models.CatalogVersion{
			{ID: "v-active", Status: models.CatalogVersionReady, AssetPrefix: "catalogs/v-active", CreatedAt: now.Add(-72 * time.Hour)},
		},
	}
	s3c.objects["catalogs/v-active/metadata.json"] = now.Add(-72 * time.Hour)

	err := executeGC(ctx, store, s3c, "test-bucket", false, 24*time.Hour)
	if err != nil {
		t.Fatalf("executeGC: %v", err)
	}

	// Recent orphan must NOT be deleted
	if _, ok := s3c.objects["catalogs/v-recent-orphan/metadata.json"]; !ok {
		t.Fatal("recent orphan prefix was deleted despite being within grace period")
	}

	// Old orphan must be deleted
	if _, ok := s3c.objects["catalogs/v-old-orphan/metadata.json"]; ok {
		t.Fatal("old orphan prefix was not deleted outside grace period")
	}

	// Active catalog must NOT be deleted
	if _, ok := s3c.objects["catalogs/v-active/metadata.json"]; !ok {
		t.Fatal("active catalog prefix was deleted")
	}
}

func TestExecuteGC_OrphanImportGracePeriod(t *testing.T) {
	ctx := context.Background()
	s3c := newMockS3Client()

	now := time.Now().UTC()
	// Recent orphan import (uploaded 5 minutes ago)
	s3c.objects["imports/recent.json"] = now.Add(-5 * time.Minute)
	// Old orphan import (uploaded 48 hours ago)
	s3c.objects["imports/old.json"] = now.Add(-48 * time.Hour)

	store := &mockGCStore{
		books:    []*models.Songbook{{ID: "sb-1"}},
		versions: []*models.CatalogVersion{},
		jobs:     []*models.ImportJob{},
	}

	err := executeGC(ctx, store, s3c, "test-bucket", false, 24*time.Hour)
	if err != nil {
		t.Fatalf("executeGC: %v", err)
	}

	// Recent orphan import must NOT be deleted
	if _, ok := s3c.objects["imports/recent.json"]; !ok {
		t.Fatal("recent orphan import was deleted despite being within grace period")
	}

	// Old orphan import must be deleted
	if _, ok := s3c.objects["imports/old.json"]; ok {
		t.Fatal("old orphan import was not deleted outside grace period")
	}
}

func TestExecuteGC_InterruptedDeletingVersionRecovery(t *testing.T) {
	ctx := context.Background()
	s3c := newMockS3Client()
	now := time.Now().UTC()

	// Interrupted deletion with expired lease
	s3c.objects["catalogs/v-interrupted/search.json.gz"] = now.Add(-2 * time.Hour)
	// Active deletion with unexpired lease
	s3c.objects["catalogs/v-active-del/search.json.gz"] = now.Add(-5 * time.Minute)

	store := &mockGCStore{
		books: []*models.Songbook{{ID: "sb-1"}},
		versions: []*models.CatalogVersion{
			{
				ID:             "v-interrupted",
				Status:         models.CatalogVersionDeleting,
				AssetPrefix:    "catalogs/v-interrupted",
				LeaseExpiresAt: now.Add(-30 * time.Minute), // expired lease
				CreatedAt:      now.Add(-2 * time.Hour),
			},
			{
				ID:             "v-active-del",
				Status:         models.CatalogVersionDeleting,
				AssetPrefix:    "catalogs/v-active-del",
				LeaseExpiresAt: now.Add(10 * time.Minute), // active lease
				CreatedAt:      now.Add(-10 * time.Minute),
			},
		},
	}

	err := executeGC(ctx, store, s3c, "test-bucket", false, 24*time.Hour)
	if err != nil {
		t.Fatalf("executeGC: %v", err)
	}

	// Interrupted deletion must be resumed and deleted
	if len(store.deletedVersions) != 1 || store.deletedVersions[0] != "v-interrupted" {
		t.Fatalf("expected v-interrupted deleted, got %+v", store.deletedVersions)
	}
	if _, ok := s3c.objects["catalogs/v-interrupted/search.json.gz"]; ok {
		t.Fatal("expected v-interrupted assets deleted from S3")
	}

	// Active deletion must NOT be touched
	if _, ok := s3c.objects["catalogs/v-active-del/search.json.gz"]; !ok {
		t.Fatal("expected active deletion assets preserved")
	}
}

func TestExecuteGC_ReferencedLegacyCatalogRetainsLetterShards(t *testing.T) {
	ctx := context.Background()
	s3c := newMockS3Client()
	now := time.Now().UTC()

	// A live referenced legacy catalog with manifest, search, and letter shards
	legacyID := "v-legacy-live"
	legacyPrefix := "catalogs/" + legacyID
	s3c.objects[legacyPrefix+"/manifest.json"] = now.Add(-72 * time.Hour)
	s3c.objects[legacyPrefix+"/search.json.gz"] = now.Add(-72 * time.Hour)
	s3c.objects[legacyPrefix+"/letters/a.json"] = now.Add(-72 * time.Hour)
	s3c.objects[legacyPrefix+"/letters/b.json"] = now.Add(-72 * time.Hour)

	store := &mockGCStore{
		books: []*models.Songbook{
			{ID: "sb-1", CatalogVersionID: legacyID},
		},
		versions: []*models.CatalogVersion{
			{
				ID:          legacyID,
				Status:      models.CatalogVersionReady,
				AssetPrefix: legacyPrefix,
				CreatedAt:   now.Add(-72 * time.Hour),
			},
		},
	}

	err := executeGC(ctx, store, s3c, "test-bucket", false, 24*time.Hour)
	if err != nil {
		t.Fatalf("executeGC failed: %v", err)
	}

	// Layout-aware GC MUST retain manifest, search index, and letter shards for referenced legacy catalog!
	if _, ok := s3c.objects[legacyPrefix+"/manifest.json"]; !ok {
		t.Fatal("legacy manifest was purged!")
	}
	if _, ok := s3c.objects[legacyPrefix+"/search.json.gz"]; !ok {
		t.Fatal("legacy search index was purged!")
	}
	if _, ok := s3c.objects[legacyPrefix+"/letters/a.json"]; !ok {
		t.Fatal("legacy letter shard a.json was purged!")
	}
	if _, ok := s3c.objects[legacyPrefix+"/letters/b.json"]; !ok {
		t.Fatal("legacy letter shard b.json was purged!")
	}
	if len(s3c.deletedKeys) > 0 {
		t.Fatalf("expected 0 deletions for referenced legacy catalog, got: %+v", s3c.deletedKeys)
	}
}

func TestExecuteGC_MixedLegacyAndNewLayouts(t *testing.T) {
	ctx := context.Background()
	s3c := newMockS3Client()
	now := time.Now().UTC()

	// 1. Referenced legacy catalog (should be retained with all letter shards)
	legRefID := "v-leg-ref"
	legRefPrefix := "catalogs/" + legRefID
	s3c.objects[legRefPrefix+"/manifest.json"] = now.Add(-72 * time.Hour)
	s3c.objects[legRefPrefix+"/letters/a.json"] = now.Add(-72 * time.Hour)

	// 2. Orphan legacy catalog past grace (should be purged, including letters)
	legOrphanPrefix := "catalogs/v-leg-orphan"
	s3c.objects[legOrphanPrefix+"/manifest.json"] = now.Add(-48 * time.Hour)
	s3c.objects[legOrphanPrefix+"/letters/z.json"] = now.Add(-48 * time.Hour)

	// 3. Referenced new epoch catalog (should be retained)
	epochRefID := "v-epoch-ref"
	epochRefPrefix := "catalogs/epochs/" + epochRefID + "/ep-live"
	s3c.objects[epochRefPrefix+"/manifest.json"] = now.Add(-72 * time.Hour)
	s3c.objects[epochRefPrefix+"/search.json.gz"] = now.Add(-72 * time.Hour)

	// 4. Orphan new epoch catalog past grace (should be purged)
	epochOrphanPrefix := "catalogs/epochs/v-epoch-orphan/ep-dead"
	s3c.objects[epochOrphanPrefix+"/manifest.json"] = now.Add(-48 * time.Hour)
	s3c.objects[epochOrphanPrefix+"/search.json.gz"] = now.Add(-48 * time.Hour)

	// 5. Recent orphan new epoch catalog within grace (should be retained)
	epochRecentPrefix := "catalogs/epochs/v-epoch-recent/ep-young"
	s3c.objects[epochRecentPrefix+"/manifest.json"] = now.Add(-10 * time.Minute)

	store := &mockGCStore{
		books: []*models.Songbook{
			{ID: "sb-leg", CatalogVersionID: legRefID},
			{ID: "sb-epoch", CatalogVersionID: epochRefID},
		},
		versions: []*models.CatalogVersion{
			{
				ID:          legRefID,
				Status:      models.CatalogVersionReady,
				AssetPrefix: legRefPrefix,
				CreatedAt:   now.Add(-72 * time.Hour),
			},
			{
				ID:          epochRefID,
				Status:      models.CatalogVersionReady,
				AssetPrefix: epochRefPrefix,
				CreatedAt:   now.Add(-72 * time.Hour),
			},
		},
	}

	err := executeGC(ctx, store, s3c, "test-bucket", false, 24*time.Hour)
	if err != nil {
		t.Fatalf("executeGC: %v", err)
	}

	// 1. Referenced legacy retained
	if _, ok := s3c.objects[legRefPrefix+"/manifest.json"]; !ok {
		t.Fatal("referenced legacy manifest was deleted")
	}
	if _, ok := s3c.objects[legRefPrefix+"/letters/a.json"]; !ok {
		t.Fatal("referenced legacy letter shard was deleted")
	}

	// 2. Orphan legacy deleted
	if _, ok := s3c.objects[legOrphanPrefix+"/manifest.json"]; ok {
		t.Fatal("orphan legacy manifest was not deleted")
	}
	if _, ok := s3c.objects[legOrphanPrefix+"/letters/z.json"]; ok {
		t.Fatal("orphan legacy letter shard was not deleted")
	}

	// 3. Referenced new epoch retained
	if _, ok := s3c.objects[epochRefPrefix+"/manifest.json"]; !ok {
		t.Fatal("referenced epoch manifest was deleted")
	}

	// 4. Orphan new epoch deleted
	if _, ok := s3c.objects[epochOrphanPrefix+"/manifest.json"]; ok {
		t.Fatal("orphan epoch manifest was not deleted")
	}

	// 5. Recent orphan epoch within grace retained
	if _, ok := s3c.objects[epochRecentPrefix+"/manifest.json"]; !ok {
		t.Fatal("recent orphan epoch was deleted despite grace period")
	}
}

func TestListCatalogPrefixes_ListingFailure(t *testing.T) {
	ctx := context.Background()
	s3c := newMockS3Client()
	s3c.listErr = errors.New("simulated s3 access denied")

	_, err := listCatalogPrefixes(ctx, s3c, "test-bucket")
	if err == nil {
		t.Fatal("expected error from listCatalogPrefixes on listing failure, got nil")
	}
	if !strings.Contains(err.Error(), "simulated s3 access denied") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestListCatalogPrefixes_Pagination(t *testing.T) {
	ctx := context.Background()
	s3c := newMockS3Client()
	now := time.Now().UTC()

	// 3 legacy prefixes
	s3c.objects["catalogs/leg-1/manifest.json"] = now
	s3c.objects["catalogs/leg-2/manifest.json"] = now
	s3c.objects["catalogs/leg-3/manifest.json"] = now

	// 3 epoch prefixes under catalogs/epochs/
	s3c.objects["catalogs/epochs/cat-1/ep-a/manifest.json"] = now
	s3c.objects["catalogs/epochs/cat-2/ep-b/manifest.json"] = now
	s3c.objects["catalogs/epochs/cat-3/ep-c/manifest.json"] = now

	// Page size of 2 to force multiple pagination roundtrips at both top level and epoch levels
	s3c.pageSize = 2

	prefixes, err := listCatalogPrefixes(ctx, s3c, "test-bucket")
	if err != nil {
		t.Fatalf("listCatalogPrefixes paginated: %v", err)
	}

	prefixMap := make(map[string]bool)
	for _, p := range prefixes {
		prefixMap[p] = true
	}

	expected := []string{
		"catalogs/leg-1",
		"catalogs/leg-2",
		"catalogs/leg-3",
		"catalogs/epochs/cat-1/ep-a",
		"catalogs/epochs/cat-2/ep-b",
		"catalogs/epochs/cat-3/ep-c",
	}

	for _, exp := range expected {
		if !prefixMap[exp] {
			t.Errorf("missing expected prefix %s from paginated discovery result: %+v", exp, prefixes)
		}
	}
	if len(prefixes) != len(expected) {
		t.Errorf("got %d prefixes, want %d: %+v", len(prefixes), len(expected), prefixes)
	}
}

type mockOperatorStore struct {
	store.AuthStore
	store.PasskeyStore
	store.DeveloperKeyStore
	store.SongbookStore
	store.SongbookAccessStore
	store.EventStore
	store.RequestStore
	store.CatalogStore
	store.CatalogChangeStore
	store.ImportJobStore

	users map[string]*models.User
}

func (m *mockOperatorStore) ListSongbookGrantsByUser(_ context.Context, _ string) ([]*models.SongbookGrant, error) {
	return nil, nil
}

func (m *mockOperatorStore) ListDeletingUsers(_ context.Context) ([]*models.User, error) {
	var result []*models.User
	for _, u := range m.users {
		if u.Deleting {
			result = append(result, u)
		}
	}
	return result, nil
}

func (m *mockOperatorStore) GetUser(_ context.Context, id string) (*models.User, error) {
	if u, ok := m.users[id]; ok {
		return u, nil
	}
	return nil, os.ErrNotExist
}

func (m *mockOperatorStore) BeginAccountDeletion(_ context.Context, userID string, _ []string) (*models.User, error) {
	if u, ok := m.users[userID]; ok {
		u.Deleting = true
		if u.DeletionEpoch == "" {
			u.DeletionEpoch = "epoch-1"
		}
		return u, nil
	}
	return nil, os.ErrNotExist
}

func (m *mockOperatorStore) DeleteUser(_ context.Context, id string) error {
	delete(m.users, id)
	return nil
}

func (m *mockOperatorStore) DeleteAuthSessionsForUser(_ context.Context, _ string) error {
	return nil
}

func (m *mockOperatorStore) ListPasskeyCredentialsForUser(_ context.Context, _ string) ([]*models.PasskeyCredential, error) {
	return nil, nil
}

func (m *mockOperatorStore) ListDeveloperKeys(_ context.Context, _ string) ([]*models.DeveloperKey, error) {
	return nil, nil
}

func (m *mockOperatorStore) ListEventsByOwner(_ context.Context, _ string) ([]*models.Event, error) {
	return nil, nil
}

func (m *mockOperatorStore) ListSongbooksByOwner(_ context.Context, _ string) ([]*models.Songbook, error) {
	return nil, nil
}

func (m *mockOperatorStore) RecordAccountDeletionProgress(_ context.Context, _, _, _ string, _ []string) error {
	return nil
}

func TestExecuteOperatorAction(t *testing.T) {
	ctx := context.Background()
	s3c := newMockS3Client()
	st := &mockOperatorStore{
		users: map[string]*models.User{
			"user-1": {
				ID:                "user-1",
				Email:             "user1@example.com",
				Deleting:          true,
				DeletionStep:      "purge_catalog_version",
				DeletionError:     "S3 timeout",
				DeletionStartedAt: time.Now().Add(-10 * time.Minute),
			},
			"user-2": {
				ID:                "user-2",
				Email:             "user2@example.com",
				Deleting:          true,
				DeletionStep:      "delete_songbooks",
				DeletionStartedAt: time.Now().Add(-5 * time.Minute),
			},
			"active-user": {
				ID:       "active-user",
				Email:    "active@example.com",
				Deleting: false,
			},
		},
	}

	// 1. List deleting accounts
	err := ExecuteOperatorAction(ctx, st, s3c, "test-bucket", OperatorOptions{ListDeletingAccounts: true})
	if err != nil {
		t.Fatalf("ListDeletingAccounts: %v", err)
	}

	// 2. Recover single account
	err = ExecuteOperatorAction(ctx, st, s3c, "test-bucket", OperatorOptions{RecoverAccount: "user-1"})
	if err != nil {
		t.Fatalf("RecoverAccount: %v", err)
	}
	if _, ok := st.users["user-1"]; ok {
		t.Fatal("expected user-1 to be deleted")
	}

	// 3. Recover all deleting accounts
	err = ExecuteOperatorAction(ctx, st, s3c, "test-bucket", OperatorOptions{RecoverDeletingAccounts: true})
	if err != nil {
		t.Fatalf("RecoverDeletingAccounts: %v", err)
	}
	if _, ok := st.users["user-2"]; ok {
		t.Fatal("expected user-2 to be deleted")
	}
	if _, ok := st.users["active-user"]; !ok {
		t.Fatal("expected active-user to be preserved")
	}
}

func TestRunGC_SweepsOrphanPrivateLocations(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	s3c := newMockS3Client()
	s3c.objects["private/songbooks/sb-live/ver-live/locations.json.gz"] = now.Add(-48 * time.Hour)
	s3c.objects["private/songbooks/sb-live/ver-old/locations.json.gz"] = now.Add(-48 * time.Hour)
	s3c.objects["private/songbooks/sb-deleted/ver-x/locations.json.gz"] = now.Add(-48 * time.Hour)
	s3c.objects["private/songbooks/sb-recent/ver-y/locations.json.gz"] = now.Add(-5 * time.Minute)

	st := &mockGCStore{
		books: []*models.Songbook{
			{ID: "sb-live", CatalogVersionID: "ver-live"},
		},
	}

	if err := executeGC(ctx, st, s3c, "test-bucket", false, 24*time.Hour); err != nil {
		t.Fatalf("executeGC: %v", err)
	}
	if _, ok := s3c.objects["private/songbooks/sb-live/ver-live/locations.json.gz"]; !ok {
		t.Fatal("expected active private locations to be preserved")
	}
	if _, ok := s3c.objects["private/songbooks/sb-recent/ver-y/locations.json.gz"]; !ok {
		t.Fatal("expected within-grace private locations to be preserved")
	}
	if _, ok := s3c.objects["private/songbooks/sb-live/ver-old/locations.json.gz"]; ok {
		t.Fatal("expected superseded private locations to be swept")
	}
	if _, ok := s3c.objects["private/songbooks/sb-deleted/ver-x/locations.json.gz"]; ok {
		t.Fatal("expected deleted songbook private locations to be swept")
	}
}
