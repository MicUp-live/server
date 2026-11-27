// Package catalog coordinates immutable catalog build and publication state.
package catalog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

// ErrBuildInProgress reports that an identical catalog is already being built.
var ErrBuildInProgress = errors.New("catalog build already in progress")

const (
	defaultBuildLeaseDuration = 10 * time.Minute
	defaultPollInterval       = 250 * time.Millisecond
)

// Builder compiles, uploads, and safely publishes catalog versions.
type Builder struct {
	Catalogs        store.CatalogStore
	Assets          AssetStore
	Options         Options
	LeaseDuration   time.Duration
	PollInterval    time.Duration
	MaxWaitDuration time.Duration
}

// BuildAndPublish compiles available songs and changes a songbook pointer only
// after all immutable assets are stored and the version is ready.
func (b Builder) BuildAndPublish(ctx context.Context, songbookID string, songs []*models.Song, opts ...store.PublicationOptions) (*models.CatalogVersion, error) {
	if b.Catalogs == nil || b.Assets == nil {
		return nil, fmt.Errorf("build catalog: catalog and asset stores are required")
	}
	compiled, err := Compile(songs, b.Options)
	if err != nil {
		return nil, err
	}

	leaseDuration := b.LeaseDuration
	if leaseDuration <= 0 {
		leaseDuration = defaultBuildLeaseDuration
	}

	version := &models.CatalogVersion{
		ID:             compiled.CatalogID,
		SchemaVersion:  b.Options.SchemaVersion,
		LeaseExpiresAt: time.Now().UTC().Add(leaseDuration),
	}
	if err := b.Catalogs.CreateCatalogVersion(ctx, version); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		return b.handleExistingVersion(ctx, songbookID, compiled, leaseDuration, opts...)
	}

	return b.uploadAndPublish(ctx, songbookID, compiled, version.LeaseToken, opts...)
}

func (b Builder) handleExistingVersion(ctx context.Context, songbookID string, compiled *Compilation, leaseDuration time.Duration, opts ...store.PublicationOptions) (*models.CatalogVersion, error) {
	existing, err := b.Catalogs.GetCatalogVersion(ctx, compiled.CatalogID)
	if err != nil {
		return nil, err
	}

	switch existing.Status {
	case models.CatalogVersionReady:
		return b.publishReadyVersion(ctx, songbookID, existing, compiled, opts...)
	case models.CatalogVersionFailed, models.CatalogVersionBuilding, models.CatalogVersionDeleting:
		if b.isReclaimable(existing, leaseDuration) {
			if ver, handled, reclaimErr := b.tryReclaimAndPublish(ctx, songbookID, compiled, leaseDuration, opts...); handled {
				return ver, reclaimErr
			}
		}
	default:
		return nil, fmt.Errorf("build catalog: existing version %s is %s", existing.ID, existing.Status)
	}

	if b.MaxWaitDuration > 0 {
		return b.waitForReadyOrReclaim(ctx, songbookID, compiled, leaseDuration, opts...)
	}

	current, getErr := b.Catalogs.GetCatalogVersion(ctx, compiled.CatalogID)
	if getErr == nil && current.Status == models.CatalogVersionReady {
		return b.publishReadyVersion(ctx, songbookID, current, compiled, opts...)
	}

	return nil, existingVersionError(compiled.CatalogID, existing.Status)
}

func (b Builder) isReclaimable(version *models.CatalogVersion, leaseDuration time.Duration) bool {
	return version.Status == models.CatalogVersionFailed ||
		((version.Status == models.CatalogVersionBuilding || version.Status == models.CatalogVersionDeleting) && b.isAbandoned(version, leaseDuration))
}

func (b Builder) tryReclaimAndPublish(ctx context.Context, songbookID string, compiled *Compilation, leaseDuration time.Duration, opts ...store.PublicationOptions) (*models.CatalogVersion, bool, error) {
	reclaimed, err := b.Catalogs.ReclaimCatalogVersion(ctx, compiled.CatalogID, leaseDuration)
	if err == nil {
		ver, pubErr := b.uploadAndPublish(ctx, songbookID, compiled, reclaimed.LeaseToken, opts...)
		return ver, true, pubErr
	}
	if !errors.Is(err, store.ErrReclaimConflict) {
		return nil, true, err
	}
	return nil, false, nil
}

func existingVersionError(catalogID string, status models.CatalogVersionStatus) error {
	if status == models.CatalogVersionBuilding {
		return fmt.Errorf("%w: %s", ErrBuildInProgress, catalogID)
	}
	return fmt.Errorf("build catalog: existing version %s is %s", catalogID, status)
}

func (b Builder) isAbandoned(version *models.CatalogVersion, leaseDuration time.Duration) bool {
	now := time.Now().UTC()
	if !version.LeaseExpiresAt.IsZero() {
		return now.After(version.LeaseExpiresAt)
	}
	if !version.CreatedAt.IsZero() {
		return now.Sub(version.CreatedAt) > leaseDuration
	}
	return false
}

func (b Builder) waitForReadyOrReclaim(ctx context.Context, songbookID string, compiled *Compilation, leaseDuration time.Duration, opts ...store.PublicationOptions) (*models.CatalogVersion, error) {
	pollInterval := b.PollInterval
	if pollInterval <= 0 {
		pollInterval = defaultPollInterval
	}

	deadline := time.Now().Add(b.MaxWaitDuration)
	deadlineTimer := time.NewTimer(b.MaxWaitDuration)
	defer deadlineTimer.Stop()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadlineTimer.C:
			return b.handleDeadline(ctx, songbookID, compiled, leaseDuration, opts...)
		case <-ticker.C:
			if ver, done, err := b.handlePollTick(ctx, songbookID, compiled, leaseDuration, deadline, opts...); done {
				return ver, err
			}
		}
	}
}

func (b Builder) createMissingVersion(ctx context.Context, songbookID string, compiled *Compilation, leaseDuration time.Duration, getErr error, opts ...store.PublicationOptions) (*models.CatalogVersion, error) {
	if errors.Is(getErr, os.ErrNotExist) {
		version := &models.CatalogVersion{
			ID:             compiled.CatalogID,
			SchemaVersion:  b.Options.SchemaVersion,
			LeaseExpiresAt: time.Now().UTC().Add(leaseDuration),
		}
		if createErr := b.Catalogs.CreateCatalogVersion(ctx, version); createErr == nil {
			return b.uploadAndPublish(ctx, songbookID, compiled, version.LeaseToken, opts...)
		}
	}
	return nil, getErr
}

func (b Builder) handleDeadline(ctx context.Context, songbookID string, compiled *Compilation, leaseDuration time.Duration, opts ...store.PublicationOptions) (*models.CatalogVersion, error) {
	current, err := b.Catalogs.GetCatalogVersion(ctx, compiled.CatalogID)
	if err != nil {
		return b.createMissingVersion(ctx, songbookID, compiled, leaseDuration, err, opts...)
	}
	if current.Status == models.CatalogVersionReady {
		return b.publishReadyVersion(ctx, songbookID, current, compiled, opts...)
	}
	return nil, existingVersionError(compiled.CatalogID, current.Status)
}

func (b Builder) handlePollTick(ctx context.Context, songbookID string, compiled *Compilation, leaseDuration time.Duration, deadline time.Time, opts ...store.PublicationOptions) (*models.CatalogVersion, bool, error) {
	current, err := b.Catalogs.GetCatalogVersion(ctx, compiled.CatalogID)
	if err != nil {
		ver, createErr := b.createMissingVersion(ctx, songbookID, compiled, leaseDuration, err, opts...)
		return ver, true, createErr
	}

	switch current.Status {
	case models.CatalogVersionReady:
		ver, pubErr := b.publishReadyVersion(ctx, songbookID, current, compiled, opts...)
		return ver, true, pubErr
	case models.CatalogVersionFailed, models.CatalogVersionBuilding, models.CatalogVersionDeleting:
		if b.isReclaimable(current, leaseDuration) {
			if ver, handled, reclaimErr := b.tryReclaimAndPublish(ctx, songbookID, compiled, leaseDuration, opts...); handled {
				return ver, true, reclaimErr
			}
		}
	default:
		return nil, true, fmt.Errorf("build catalog: unexpected status %s for version %s", current.Status, current.ID)
	}

	if time.Now().After(deadline) {
		return nil, true, existingVersionError(current.ID, current.Status)
	}
	return nil, false, nil
}

func (b Builder) putPrivateLocations(ctx context.Context, songbookID string, compiled *Compilation) error {
	if compiled == nil || compiled.LocationsAsset == nil || songbookID == "" || compiled.CatalogID == "" {
		return nil
	}
	return b.Assets.Put(ctx, "private/songbooks/"+songbookID+"/"+compiled.CatalogID, []Asset{*compiled.LocationsAsset})
}

func (b Builder) publishReadyVersion(ctx context.Context, songbookID string, version *models.CatalogVersion, compiled *Compilation, opts ...store.PublicationOptions) (*models.CatalogVersion, error) {
	if err := b.putPrivateLocations(ctx, songbookID, compiled); err != nil {
		return nil, err
	}
	if err := b.Catalogs.PublishCatalogVersion(ctx, songbookID, version.ID, opts...); err != nil {
		return nil, err
	}
	return version, nil
}

func (b Builder) uploadAndPublish(ctx context.Context, songbookID string, compiled *Compilation, leaseToken string, opts ...store.PublicationOptions) (*models.CatalogVersion, error) {
	epochToken := leaseToken
	if epochToken == "" {
		epochToken = uuid.NewString()
	}
	prefix := fmt.Sprintf("catalogs/epochs/%s/%s", compiled.CatalogID, epochToken)
	if err := b.Assets.Put(ctx, prefix, compiled.Assets); err != nil {
		_, _ = b.Catalogs.FailCatalogVersion(ctx, compiled.CatalogID, err.Error(), leaseToken)
		return nil, err
	}
	if err := b.putPrivateLocations(ctx, songbookID, compiled); err != nil {
		_, _ = b.Catalogs.FailCatalogVersion(ctx, compiled.CatalogID, err.Error(), leaseToken)
		return nil, err
	}
	ready, err := b.Catalogs.MarkCatalogVersionReady(ctx, compiled.CatalogID, prefix, compiled.Manifest.Total, leaseToken)
	if err != nil {
		return nil, err
	}
	if err := b.Catalogs.PublishCatalogVersion(ctx, songbookID, ready.ID, opts...); err != nil {
		return ready, err
	}
	return ready, nil
}
