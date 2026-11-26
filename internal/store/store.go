package store

import (
	"context"
	"errors"
	"time"

	"micup.live/server/internal/models"
)

var ErrSingerLimitExceeded = errors.New("singer active request limit exceeded")

// QueueVersionConflictError indicates that a conditional queue mutation was
// based on a stale snapshot. Current is safe for clients to use when retrying.
type QueueVersionConflictError struct {
	Current  int64
	Expected int64 // Snapshot version, when supplied by an event commit boundary.
}

func (e *QueueVersionConflictError) Error() string {
	return "queue version conflict"
}

var ErrInvalidPasswordReset = errors.New("invalid or expired reset link")

// ErrAccountDeleting indicates that an account is undergoing irreversible deletion
// and cannot authenticate, mint credentials, or modify profile/authentication state.
var ErrAccountDeleting = errors.New("account is being deleted")

type SongbookDeletingError struct{}

func (e SongbookDeletingError) Error() string { return "songbook is being deleted" }
func (e SongbookDeletingError) Unwrap() error { return ErrAccountDeleting }

var ErrSongbookDeleting error = SongbookDeletingError{}

type EventDeletingError struct{}

func (e EventDeletingError) Error() string { return "event is being deleted" }
func (e EventDeletingError) Unwrap() error { return ErrAccountDeleting }

var ErrEventDeleting error = EventDeletingError{}

// PasswordResetCommit replaces the password and advances authority atomically.
// Missing epochs on legacy records are equivalent to the empty epoch.
type PasswordResetCommit struct {
	UserID, Email, ExpectedAuthEpoch, AuthEpoch, PasswordHash string
	ExpiresAt                                                 time.Time
}

// FavoritesStore persists user-curated favorite songs and artists.
type FavoritesStore interface {
	GetUserFavorites(ctx context.Context, userID string) (*models.UserFavorites, error)
	SaveUserFavorites(ctx context.Context, userID string, favs *models.UserFavorites) error
}

// AuthStore persists users and refresh sessions. ResetPassword must atomically
// validate identity/epoch and change the password/epoch; no weaker fallback exists.
type AuthStore interface {
	FavoritesStore
	ResetPassword(ctx context.Context, reset PasswordResetCommit) error
	CreateUser(ctx context.Context, user *models.User) error
	UpdateUser(ctx context.Context, user *models.User) error
	ChangeUserEmail(ctx context.Context, userID, oldEmail, newEmail string, user *models.User) error
	GetUserByEmail(ctx context.Context, email string) (*models.User, error)
	GetUser(ctx context.Context, id string) (*models.User, error)
	CreateAuthSession(ctx context.Context, session *models.AuthSession) error
	ConsumeAuthSession(ctx context.Context, tokenHash []byte) (*models.User, error)
	DeleteAuthSessionsForUser(ctx context.Context, userID string) error
	BeginAccountDeletion(ctx context.Context, userID string, pendingCatalogVersions []string) (*models.User, error)
	RecordAccountDeletionProgress(ctx context.Context, userID, step, errMsg string, resolvedCatalogVersions []string) error
	ListDeletingUsers(ctx context.Context) ([]*models.User, error)
	DeleteUser(ctx context.Context, userID string) error
}

var ErrPasskeyChanged = errors.New("passkey is no longer current")

// PasskeyUsage contains metadata derived from a verified assertion.
type PasskeyUsage struct {
	SignCount                   uint32
	BackupEligible, BackupState bool
}

// PasskeyStore persists WebAuthn credentials and ceremony session challenges.
type PasskeyStore interface {
	CreateUserWithPasskey(ctx context.Context, user *models.User, cred *models.PasskeyCredential) error
	SavePasskeyCredential(ctx context.Context, cred *models.PasskeyCredential) error
	GetPasskeyCredential(ctx context.Context, id string) (*models.PasskeyCredential, error)
	ListPasskeyCredentialsForUser(ctx context.Context, userID string) ([]*models.PasskeyCredential, error)
	// RecordPasskeyUse updates only assertion metadata on the observed, still-active credential.
	// Changed or missing credentials return ErrPasskeyChanged.
	RecordPasskeyUse(ctx context.Context, observed *models.PasskeyCredential, usage PasskeyUsage) error
	DeletePasskeyCredential(ctx context.Context, id string, userID string) error

	SaveWebAuthnSession(ctx context.Context, session *models.WebAuthnSession) error
	ConsumeWebAuthnSession(ctx context.Context, sessionID string) (*models.WebAuthnSession, error)
}

// ErrStalePublication indicates that catalog publication was rejected because
// the songbook was modified by newer or concurrent work.
var ErrStalePublication = errors.New("stale catalog publication")

// ErrPublicationSuperseded indicates that a catalog publication was rejected because
// the songbook was already advanced to a newer job generation. This is terminal.
var ErrPublicationSuperseded = errors.New("catalog publication superseded")

// ErrPublicationBaseConflict indicates that a catalog publication was rejected because
// the songbook's base catalog version changed concurrently. This is retryable.
var ErrPublicationBaseConflict = errors.New("catalog publication base version conflict")

// ErrJobGenerationConflict indicates that an import job already has a different generation assigned.
var ErrJobGenerationConflict = errors.New("import job generation conflict")

// ErrImportJobAlreadyCompleted indicates that an import job cannot transition or republish because it is already completed.
var ErrImportJobAlreadyCompleted = errors.New("import job is already completed")

// ErrImportJobTerminal indicates that an import job is in a terminal failed state and cannot be resumed or published.
var ErrImportJobTerminal = errors.New("import job is in a terminal state")

// ErrImportJobClaimConflict indicates that an active dispatch claim is held by another worker/dispatcher.
var ErrImportJobClaimConflict = errors.New("import job dispatch claim conflict")

// ErrImportJobAlreadyDispatched indicates that an import job has already been confirmed as dispatched.
var ErrImportJobAlreadyDispatched = errors.New("import job is already dispatched")

// ErrImportJobStaleClaim indicates that a dispatch confirmation or error record failed because the claim token is invalid or expired.
var ErrImportJobStaleClaim = errors.New("stale import job dispatch claim")

// ErrImportJobNotReady indicates that an import job is not marked dispatch-ready (e.g. unfinished upload).
var ErrImportJobNotReady = errors.New("import job is not ready for dispatch")

// PublicationOptions controls publication fencing conditions on a songbook.
type PublicationOptions struct {
	ExpectedBaseVersion *string // If non-nil, songbook.catalog_version_id must match (*ExpectedBaseVersion == "" requires no current version)
	JobGeneration       int64   // If > 0, songbook.catalog_generation must be <= JobGeneration
	JobID               string  // If non-empty, atomically commits import job completion in the same transaction
	ProcessedSongs      int     // Song count to commit on the import job upon publication
}

// SongbookStore persists songbook metadata, ownership, and deletion barriers.
type SongbookStore interface {
	CreateSongbook(ctx context.Context, sb *models.Songbook) error
	UpdateSongbook(ctx context.Context, sb *models.Songbook) error
	DeleteSongbook(ctx context.Context, id string) error
	GetSongbook(ctx context.Context, id string) (*models.Songbook, error)
	GetPublicSongbook(ctx context.Context, publicID string) (*models.Songbook, error)
	GetSongbookBySlug(ctx context.Context, slug string) (*models.Songbook, error)
	ListSongbooks(ctx context.Context) ([]*models.Songbook, error)
	ListSongbooksByOwner(ctx context.Context, ownerID string) ([]*models.Songbook, error)
	NextCatalogGeneration(ctx context.Context, songbookID string) (int64, error)
	BeginSongbookDeletion(ctx context.Context, id string) (*models.Songbook, error)
	ConfirmSongbookDeletion(ctx context.Context, id string) error
	AbortSongbookDeletion(ctx context.Context, id string) error
}

// CatalogChangeStore persists only pending additions, edits, and deletions
// awaiting periodic publication into an immutable S3 catalog.
type CatalogChangeStore interface {
	PutCatalogChange(ctx context.Context, change *models.CatalogChange) error
	ListCatalogChanges(ctx context.Context, songbookID string) ([]*models.CatalogChange, error)
	DeleteCatalogChange(ctx context.Context, songbookID, songID string, updatedAt time.Time) error
}

// ErrReclaimConflict indicates that a catalog version cannot be reclaimed because
// another worker holds an active lease or the version is already ready.
var ErrReclaimConflict = errors.New("catalog build reclaim conflict")

// ErrCatalogVersionInUse indicates that a catalog version cannot be deleted because
// it is currently referenced by one or more active songbooks.
var ErrCatalogVersionInUse = errors.New("catalog version is currently in use")

// ErrCatalogDeletionConflict indicates that a catalog version cannot be reserved for deletion
// because another worker holds an active deletion lease or the version is not in a deletable state.
var ErrCatalogDeletionConflict = errors.New("catalog version deletion conflict")

// PurgeCatalogOptions configures catalog version purging.
type PurgeCatalogOptions struct {
	LeaseDuration       time.Duration
	ExcludeSongbookIDs  []string
	ReleaseLeaseOnError bool
}

// CatalogStore persists immutable catalog build metadata and publishes versions to songbooks.
type CatalogStore interface {
	CreateCatalogVersion(ctx context.Context, version *models.CatalogVersion) error
	GetCatalogVersion(ctx context.Context, id string) (*models.CatalogVersion, error)
	MarkCatalogVersionReady(ctx context.Context, id, assetPrefix string, songCount int, leaseToken ...string) (*models.CatalogVersion, error)
	FailCatalogVersion(ctx context.Context, id, reason string, leaseToken ...string) (*models.CatalogVersion, error)
	ReclaimCatalogVersion(ctx context.Context, id string, leaseDuration time.Duration) (*models.CatalogVersion, error)
	PublishCatalogVersion(ctx context.Context, songbookID, catalogVersionID string, opts ...PublicationOptions) error
	DeleteCatalogVersion(ctx context.Context, id string, leaseToken ...string) error
	ReserveCatalogVersionForDeletion(ctx context.Context, id string, leaseDuration time.Duration) (*models.CatalogVersion, error)
	ReleaseCatalogVersionReservation(ctx context.Context, id, leaseToken string) error
	IsCatalogVersionReferenced(ctx context.Context, id string, excludeSongbookIDs ...string) (bool, error)
	PurgeCatalogVersion(ctx context.Context, id string, purgeAssets func(assetPrefix string) error, opts ...PurgeCatalogOptions) error
	PurgeOrphanCatalogPrefix(ctx context.Context, prefix string, purgeAssets func(assetPrefix string) error, opts ...PurgeCatalogOptions) error
}

// ImportJobStore persists asynchronous Assistant Importer jobs.
type ImportJobStore interface {
	CreateImportJob(ctx context.Context, job *models.ImportJob) error
	GetImportJob(ctx context.Context, id string) (*models.ImportJob, error)
	ListImportJobs(ctx context.Context) ([]*models.ImportJob, error)
	DeleteImportJob(ctx context.Context, id string) error
	UpdateImportJob(ctx context.Context, job *models.ImportJob) error
	LatestImportJob(ctx context.Context, songbookID string) (*models.ImportJob, error)
	AssignImportJobGeneration(ctx context.Context, jobID string, generation int64) (int64, error)

	UpdateImportJobProgress(ctx context.Context, jobID string, processedSongs int) error
	ResetImportJobDispatch(ctx context.Context, jobID string) error
	MarkImportJobDispatchReady(ctx context.Context, jobID string) error
	ClaimImportJobDispatch(ctx context.Context, jobID string, leaseDuration time.Duration) (*models.ImportJob, string, error)
	ConfirmImportJobDispatch(ctx context.Context, jobID string, claimToken string) error
	RecordDispatchError(ctx context.Context, jobID string, claimToken string, errMsg string) error
	ListDispatchableImportJobs(ctx context.Context, maxAttempts int) ([]*models.ImportJob, error)
	ListExhaustedImportJobs(ctx context.Context, maxAttempts int) ([]*models.ImportJob, error)
	ClaimImportJobProcessing(ctx context.Context, jobID string) (*models.ImportJob, error)
	FailImportJob(ctx context.Context, jobID string, reason string) error
}

// EventStore persists live karaoke show events, session lifecycles, and atomic queue transitions.
type EventStore interface {
	CreateEvent(ctx context.Context, ev *models.Event) error
	GetEvent(ctx context.Context, id string) (*models.Event, error)
	GetEventByJoinCode(ctx context.Context, joinCode string) (*models.Event, error)
	ListEventsByOwner(ctx context.Context, ownerID string) ([]*models.Event, error)
	ListEventsBySongbook(ctx context.Context, songbookID string) ([]*models.Event, error)
	// Snapshot updates compare QueueVersion and MetadataVersion, reject restart,
	// and advance MetadataVersion atomically. Conflict changes no stored state.
	UpdateEvent(ctx context.Context, ev *models.Event) error
	UpdateEventWithVersion(ctx context.Context, ev *models.Event, bumpVersion bool) error
	DeleteEvent(ctx context.Context, id string) error
	BeginEventDeletion(ctx context.Context, id string) error
	BumpQueueVersion(ctx context.Context, eventID string) (int64, error)
	TransitionQueueRotation(ctx context.Context, event *models.Event, requests []*models.SongRequest) error
	BeginQueueRestart(ctx context.Context, event *models.Event) error
	ArchiveRestartBatch(ctx context.Context, eventID string, expectedVersion int64, requests []*models.SongRequest) error
	DeleteRestartSlots(ctx context.Context, eventID string, expectedVersion int64, slotIDs []string) error
	ListRestartSlots(ctx context.Context, eventID string) ([]string, error)
	FinishQueueRestart(ctx context.Context, eventID string, expectedVersion int64) error
	DetachEventFromSongbook(ctx context.Context, eventID, songbookID, songbookName string, retention time.Duration) (*models.Event, error)
}

// QueueMutationResult is the replayable HTTP result persisted for an
// idempotent queue mutation.
type QueueMutationResult struct {
	StatusCode   int
	Body         []byte
	QueueVersion int64
	Replay       bool
}

// IdempotencyConflictError indicates that a key was already used for a
// different tenant, route, entity, or normalized mutation payload.
type IdempotencyConflictError struct{}

func (e *IdempotencyConflictError) Error() string { return "idempotency key conflict" }

// ErrAdmissionConflict means an admission plan lost a concurrent transaction.
// Callers must reload the event and queue before retrying.
var ErrAdmissionConflict = errors.New("request admission conflict")

// ErrEventNotActive means an admission transaction found that its event no longer accepts requests.
var ErrEventNotActive = errors.New("event is not active")

// RequestStore persists live song requests and atomic, versioned, idempotent queue mutations.
type RequestStore interface {
	CreateRequest(ctx context.Context, req *models.SongRequest) error
	CreateRequestAtVersion(ctx context.Context, req *models.SongRequest, maxActivePerSinger int, reorderIDs []string, expectedVersion int64) error
	GetRequest(ctx context.Context, id string) (*models.SongRequest, error)
	ListRequestsByEvent(ctx context.Context, eventID string) ([]*models.SongRequest, error)
	UpdateRequest(ctx context.Context, req *models.SongRequest) error
	DeleteRequest(ctx context.Context, id string) error
	DeleteEventRequestAtomic(ctx context.Context, eventID, requestID string) error
	ReorderRequests(ctx context.Context, eventID string, orderedIDs []string) error
	UpdateRequestAndReorder(ctx context.Context, req *models.SongRequest, reorderIDs []string) error
	UpdateRequestWithQueueVersion(ctx context.Context, req *models.SongRequest, expectedVersion int64) error
	ReorderRequestsWithQueueVersion(ctx context.Context, eventID string, orderedIDs []string, expectedVersion int64) error
	UpdateRequestAndReorderWithQueueVersion(ctx context.Context, req *models.SongRequest, reorderIDs []string, expectedVersion int64) error
	GetIdempotencyResult(ctx context.Context, idempotencyKey, ownerID, route, entityID, requestHash string) (*QueueMutationResult, bool, error)
	UpdateRequestWithIdempotency(ctx context.Context, req *models.SongRequest, expectedVersion *int64, idempotencyKey, ownerID, route, requestHash string) (*QueueMutationResult, error)
	ReorderRequestsWithIdempotency(ctx context.Context, eventID string, orderedIDs []string, expectedVersion *int64, idempotencyKey, ownerID, route, requestHash string) (*QueueMutationResult, error)
	UpdateRequestAndReorderWithIdempotency(ctx context.Context, req *models.SongRequest, reorderIDs []string, expectedVersion *int64, idempotencyKey, ownerID, route, requestHash string) (*QueueMutationResult, error)
	SetRequestsTTLByEvent(ctx context.Context, eventID string, expiresAt time.Time) error
}

// DeveloperKeyStore persists scoped Personal Access Tokens for third-party integrations.
type DeveloperKeyStore interface {
	// CreateDeveloperKey hashes rawKey (SHA-256 hex) and persists it as the key's ID.
	CreateDeveloperKey(ctx context.Context, rawKey string, key *models.DeveloperKey) error
	// GetDeveloperKey resolves a raw key token to its stored record.
	GetDeveloperKey(ctx context.Context, rawKey string) (*models.DeveloperKey, error)
	ListDeveloperKeys(ctx context.Context, ownerID string) ([]*models.DeveloperKey, error)
	RevokeDeveloperKey(ctx context.Context, id, ownerID string) error
	TouchDeveloperKeyLastUsed(ctx context.Context, id string) error
}

// RateLimitResult conveys the rate limit decision and remaining capacity.
type RateLimitResult struct {
	Allowed   bool
	Limit     int
	Remaining int
	ResetAt   time.Time
}

// RateLimitStore enforces distributed rate limits across multiple runtime instances.
type RateLimitStore interface {
	Allow(ctx context.Context, scope, key string, limit int, window time.Duration) (RateLimitResult, error)
}

var (
	ErrPermissionDenied             = errors.New("permission denied")
	ErrGrantNotFound                = errors.New("songbook grant not found")
	ErrAlreadyCollaborator          = errors.New("user is already a collaborator on this songbook")
	ErrInvitationNotFound           = errors.New("songbook invitation not found")
	ErrInvitationInvalid            = errors.New("songbook invitation is no longer valid")
	ErrInvitationConflict           = errors.New("concurrent songbook invitation update")
	ErrInvitationExpired            = errors.New("songbook invitation has expired")
	ErrInvitationEmailMismatch      = errors.New("signed-in account email does not match invitation")
	ErrInvitationDeliverySuperseded = errors.New("songbook invitation send attempt was superseded")
)

// SongbookAccessStore persists collaborator grants and single-use invitations.
type SongbookAccessStore interface {
	GetSongbookGrant(ctx context.Context, songbookID, userID string) (*models.SongbookGrant, error)
	ListSongbookGrantsBySongbook(ctx context.Context, songbookID string) ([]*models.SongbookGrant, error)
	ListSongbookGrantsByUser(ctx context.Context, userID string) ([]*models.SongbookGrant, error)
	UpdateSongbookGrant(ctx context.Context, grant *models.SongbookGrant) error
	DeleteSongbookGrant(ctx context.Context, songbookID, userID string) error

	CreateSongbookInvitation(ctx context.Context, inv *models.SongbookInvitation, replaceIDs []string) error
	GetSongbookInvitation(ctx context.Context, id string) (*models.SongbookInvitation, error)
	GetSongbookInvitationByTokenHash(ctx context.Context, tokenHash string) (*models.SongbookInvitation, error)
	ListSongbookInvitationsBySongbook(ctx context.Context, songbookID string) ([]*models.SongbookInvitation, error)
	RotateSongbookInvitationToken(ctx context.Context, songbookID, invitationID, newTokenHash string, newExpiresAt time.Time) (*models.SongbookInvitation, error)
	RecordSongbookInvitationDelivery(ctx context.Context, invitationID, expectedTokenHash string, sentAt *time.Time, sendErr string) error
	CancelSongbookInvitation(ctx context.Context, songbookID, invitationID string) error
	AcceptSongbookInvitation(ctx context.Context, tokenHash, userID, normalizedEmail string, now time.Time) (*models.SongbookGrant, *models.SongbookInvitation, error)
	DeleteSongbookInvitation(ctx context.Context, invitationID string) error
}
