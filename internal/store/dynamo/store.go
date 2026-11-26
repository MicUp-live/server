// Package dynamo implements the application stores with AWS DynamoDB.
package dynamo

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"micup.live/server/internal/models"
	parent "micup.live/server/internal/store"
)

const (
	publicIndex    = "public-id-index"
	slugIndex      = "slug-index"
	joinCodeIndex  = "join-code-index"
	requestIndex   = "request-id-index"
	tokenHashIndex = "token-hash-index"
)

// Config controls the DynamoDB connection and table namespace.
type Config struct {
	Region      string
	Endpoint    string
	TablePrefix string
}

// Store implements all persistence contracts over explicit DynamoDB tables.
type Store struct {
	db                      *dynamodb.Client
	endpoint                string
	tables                  tableNames
	testDetachRetentionHook func(ctx context.Context, eventID string, expiresAt time.Time) error
}

type tableNames struct {
	users               string
	identities          string
	sessions            string
	songbooks           string
	eventRequests       string
	events              string
	passkeys            string
	webauthn            string
	catalogs            string
	importJobs          string
	changes             string
	developerKeys       string
	rateLimits          string
	idempotency         string
	songbookGrants      string
	songbookInvitations string
}

type userItem struct {
	ID                     string                  `dynamodbav:"id"`
	Email                  string                  `dynamodbav:"email"`
	DisplayName            string                  `dynamodbav:"display_name"`
	AvatarURL              string                  `dynamodbav:"avatar_url,omitempty"`
	PasswordHash           string                  `dynamodbav:"password_hash"`
	AuthEpoch              string                  `dynamodbav:"auth_epoch,omitempty"`
	IsHost                 bool                    `dynamodbav:"is_host"`
	EmailVerified          bool                    `dynamodbav:"email_verified"`
	Deleting               bool                    `dynamodbav:"deleting,omitempty"`
	DeletionEpoch          string                  `dynamodbav:"deletion_epoch,omitempty"`
	DeletionAuthEpoch      string                  `dynamodbav:"deletion_auth_epoch,omitempty"`
	DeletionStartedAt      string                  `dynamodbav:"deletion_started_at,omitempty"`
	DeletionStep           string                  `dynamodbav:"deletion_step,omitempty"`
	DeletionError          string                  `dynamodbav:"deletion_error,omitempty"`
	PendingCatalogVersions []string                `dynamodbav:"pending_catalog_versions,omitempty"`
	FavoriteSongs          []models.FavoriteSong   `dynamodbav:"favorite_songs,omitempty"`
	FavoriteArtists        []models.FavoriteArtist `dynamodbav:"favorite_artists,omitempty"`
	CreatedAt              string                  `dynamodbav:"created_at"`
	UpdatedAt              string                  `dynamodbav:"updated_at"`
}

type identityItem struct {
	Email  string `dynamodbav:"email"`
	UserID string `dynamodbav:"user_id"`
}

type sessionItem struct {
	TokenHash string `dynamodbav:"token_hash"`
	UserID    string `dynamodbav:"user_id"`
	AuthEpoch string `dynamodbav:"auth_epoch,omitempty"`
	ExpiresAt string `dynamodbav:"expires_at"`
	TTL       int64  `dynamodbav:"ttl"`
}

type songbookItem struct {
	ID                     string `dynamodbav:"id"`
	OwnerID                string `dynamodbav:"owner_id,omitempty"`
	PublicID               string `dynamodbav:"public_id,omitempty"`
	Slug                   string `dynamodbav:"slug,omitempty"`
	VenueID                string `dynamodbav:"venue_id,omitempty"`
	Name                   string `dynamodbav:"name"`
	IsPublic               bool   `dynamodbav:"is_public"`
	CreatedAt              string `dynamodbav:"created_at"`
	CreatedKey             string `dynamodbav:"created_key"`
	UpdatedAt              string `dynamodbav:"updated_at"`
	ActiveGeneration       int64  `dynamodbav:"active_generation"`
	AvailabilityGeneration int64  `dynamodbav:"availability_generation"`
	CatalogVersionID       string `dynamodbav:"catalog_version_id,omitempty"`
	SongCount              int    `dynamodbav:"song_count"`
	CatalogGeneration      int64  `dynamodbav:"catalog_generation,omitempty"`
	LastImportGeneration   int64  `dynamodbav:"last_import_generation,omitempty"`
	Deleting               bool   `dynamodbav:"deleting,omitempty"`
	DeletionConfirmed      bool   `dynamodbav:"deletion_confirmed,omitempty"`
}

type catalogVersionItem struct {
	ID             string `dynamodbav:"id"`
	SchemaVersion  int    `dynamodbav:"schema_version"`
	Status         string `dynamodbav:"status"`
	SongCount      int    `dynamodbav:"song_count"`
	AssetPrefix    string `dynamodbav:"asset_prefix,omitempty"`
	FailureReason  string `dynamodbav:"failure_reason,omitempty"`
	CreatedAt      string `dynamodbav:"created_at"`
	UpdatedAt      string `dynamodbav:"updated_at,omitempty"`
	ReadyAt        string `dynamodbav:"ready_at,omitempty"`
	LeaseToken     string `dynamodbav:"lease_token,omitempty"`
	LeaseExpiresAt string `dynamodbav:"lease_expires_at,omitempty"`
}

type passkeyItem struct {
	ID              string   `dynamodbav:"id"`
	UserID          string   `dynamodbav:"user_id"`
	PublicKey       []byte   `dynamodbav:"public_key"`
	AttestationType string   `dynamodbav:"attestation_type"`
	AAGUID          []byte   `dynamodbav:"aaguid"`
	SignCount       uint32   `dynamodbav:"sign_count"`
	Transports      []string `dynamodbav:"transports,omitempty"`
	BackupEligible  bool     `dynamodbav:"backup_eligible"`
	BackupState     bool     `dynamodbav:"backup_state"`
	UserPresent     bool     `dynamodbav:"user_present"`
	UserVerified    bool     `dynamodbav:"user_verified"`
	FriendlyName    string   `dynamodbav:"friendly_name"`
	CreatedAt       string   `dynamodbav:"created_at"`
	LastUsedAt      string   `dynamodbav:"last_used_at,omitempty"`
}

type webauthnSessionItem struct {
	SessionID   string `dynamodbav:"session_id"`
	UserID      string `dynamodbav:"user_id,omitempty"`
	Challenge   string `dynamodbav:"challenge"`
	SessionData []byte `dynamodbav:"session_data"`
	ExpiresAt   string `dynamodbav:"expires_at"`
	TTL         int64  `dynamodbav:"ttl"`
}

var (
	_ parent.AuthStore           = (*Store)(nil)
	_ parent.PasskeyStore        = (*Store)(nil)
	_ parent.SongbookStore       = (*Store)(nil)
	_ parent.CatalogStore        = (*Store)(nil)
	_ parent.RequestStore        = (*Store)(nil)
	_ parent.EventStore          = (*Store)(nil)
	_ parent.DeveloperKeyStore   = (*Store)(nil)
	_ parent.SongbookAccessStore = (*Store)(nil)
	_ parent.FavoritesStore      = (*Store)(nil)
)

// New creates a DynamoDB-backed Store. It does not create tables.
func New(ctx context.Context, cfg Config) (*Store, error) {
	region := strings.TrimSpace(cfg.Region)
	if region == "" {
		region = "us-east-2"
	}
	options := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(region)}
	if cfg.Endpoint != "" {
		endpoint := strings.TrimRight(cfg.Endpoint, "/")
		options = append(options, awsconfig.WithBaseEndpoint(endpoint))
		options = append(options, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("local", "local", "")))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	prefix := cfg.TablePrefix
	return &Store{
		db:       dynamodb.NewFromConfig(awsCfg),
		endpoint: cfg.Endpoint,
		tables: tableNames{
			users:               prefix + "users",
			identities:          prefix + "identity-claims",
			sessions:            prefix + "sessions",
			songbooks:           prefix + "songbooks",
			eventRequests:       prefix + "event-requests",
			events:              prefix + "events",
			passkeys:            prefix + "passkeys",
			webauthn:            prefix + "webauthn-sessions",
			catalogs:            prefix + "catalog-versions",
			importJobs:          prefix + "import-jobs",
			changes:             prefix + "catalog-changes",
			developerKeys:       prefix + "developer-keys",
			rateLimits:          prefix + "rate-limits",
			idempotency:         prefix + "idempotency",
			songbookGrants:      prefix + "songbook-grants",
			songbookInvitations: prefix + "songbook-invitations",
		},
	}, nil
}

// Close exists for lifecycle symmetry; AWS SDK clients hold no closeable resources.
func (s *Store) Close() error { return nil }

func marshal(v interface{}) (map[string]types.AttributeValue, error) {
	item, err := attributevalue.MarshalMap(v)
	if err != nil {
		return nil, fmt.Errorf("marshal DynamoDB item: %w", err)
	}
	return item, nil
}

func unmarshal(item map[string]types.AttributeValue, v interface{}) error {
	if err := attributevalue.UnmarshalMap(item, v); err != nil {
		return fmt.Errorf("unmarshal DynamoDB item: %w", err)
	}
	return nil
}

func stringKey(name, value string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{name: &types.AttributeValueMemberS{Value: value}}
}

func fingerprintKey(songbookID, fingerprint string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{
		"songbook_id": &types.AttributeValueMemberS{Value: songbookID},
		"fingerprint": &types.AttributeValueMemberS{Value: fingerprint},
	}
}

func tokenKey(hash []byte) string { return hex.EncodeToString(hash) }

func normalizedEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

func timestamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, value)
}

func missing(item map[string]types.AttributeValue) error {
	if len(item) == 0 {
		return os.ErrNotExist
	}
	return nil
}

func errNotExist() error { return os.ErrNotExist }

func isConditional(err error) bool {
	var conditional *types.ConditionalCheckFailedException
	var transaction *types.TransactionCanceledException
	return errors.As(err, &conditional) || errors.As(err, &transaction)
}

func nowForCreate(created time.Time) (time.Time, time.Time) {
	now := time.Now().UTC()
	if created.IsZero() {
		created = now
	}
	return created, now
}

func modelUser(item userItem) (*models.User, error) {
	created, err := parseTime(item.CreatedAt)
	if err != nil {
		return nil, err
	}
	updated, err := parseTime(item.UpdatedAt)
	if err != nil {
		return nil, err
	}
	deletionStarted, err := parseTime(item.DeletionStartedAt)
	if err != nil {
		return nil, err
	}
	return &models.User{
		ID:                     item.ID,
		Email:                  item.Email,
		DisplayName:            item.DisplayName,
		AvatarURL:              item.AvatarURL,
		PasswordHash:           item.PasswordHash,
		AuthEpoch:              item.AuthEpoch,
		IsHost:                 item.IsHost,
		EmailVerified:          item.EmailVerified,
		Deleting:               item.Deleting,
		DeletionEpoch:          item.DeletionEpoch,
		DeletionAuthEpoch:      item.DeletionAuthEpoch,
		DeletionStartedAt:      deletionStarted,
		DeletionStep:           item.DeletionStep,
		DeletionError:          item.DeletionError,
		PendingCatalogVersions: item.PendingCatalogVersions,
		FavoriteSongs:          item.FavoriteSongs,
		FavoriteArtists:        item.FavoriteArtists,
		CreatedAt:              created,
		UpdatedAt:              updated,
	}, nil
}

func avString(value string) types.AttributeValue { return &types.AttributeValueMemberS{Value: value} }
func avNumber(value int64) types.AttributeValue {
	return &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", value)}
}
func avBool(value bool) types.AttributeValue { return &types.AttributeValueMemberBOOL{Value: value} }
func boolValue(value bool) *bool             { return aws.Bool(value) }
