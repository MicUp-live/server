package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"micup.live/server/internal/models"
	pgstore "micup.live/server/internal/store/postgres"
)

const (
	sqlStateUniqueViolation      = "23505"
	sqlStateForeignKeyViolation  = "23503"
	sqlStateSerializationFailure = "40001"
	sqlStateDeadlockDetected     = "40P01"
	maxTxRetries                 = 5
	batchSize                    = 25
)

type migrationState struct {
	now             time.Time
	logger          *log.Logger
	dryRun          bool
	skipEphemeral   bool
	pg              *pgstore.Store
	ddb             *dynamodb.Client
	prefix          string
	users           map[string]bool
	userByEmail     map[string]string
	catalogVersions map[string]bool
	songbooks       map[string]bool
	events          map[string]bool
}

type preparedRow struct {
	label   string
	query   string
	args    []any
	onSaved func()
}

var tableCountQueries = map[string]string{
	"users": `SELECT COUNT(*) FROM users`, "auth_sessions": `SELECT COUNT(*) FROM auth_sessions`,
	"passkey_credentials": `SELECT COUNT(*) FROM passkey_credentials`, "webauthn_sessions": `SELECT COUNT(*) FROM webauthn_sessions`,
	"developer_keys": `SELECT COUNT(*) FROM developer_keys`, "catalog_versions": `SELECT COUNT(*) FROM catalog_versions`,
	"songbooks": `SELECT COUNT(*) FROM songbooks`, "songbook_grants": `SELECT COUNT(*) FROM songbook_grants`,
	"songbook_invitations": `SELECT COUNT(*) FROM songbook_invitations`, "catalog_changes": `SELECT COUNT(*) FROM catalog_changes`,
	"import_jobs": `SELECT COUNT(*) FROM import_jobs`, "events": `SELECT COUNT(*) FROM events`,
	"event_requests": `SELECT COUNT(*) FROM event_requests`, "idempotency_records": `SELECT COUNT(*) FROM idempotency_records`,
	"rate_limits": `SELECT COUNT(*) FROM rate_limits`,
}

// Migrate performs a one-time, idempotent, FK-ordered migration from DynamoDB to PostgreSQL.
func Migrate(ctx context.Context, ddb *dynamodb.Client, tablePrefix string, pg *pgstore.Store, opts Options) (*Report, error) {
	if ddb == nil {
		return nil, errors.New("dynamodb client is required")
	}
	if !opts.DryRun && pg == nil {
		return nil, errors.New("postgres store is required when dry-run is false")
	}
	now := opts.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	logger := opts.Logger
	if logger == nil {
		logger = log.Default()
	}
	if !opts.DryRun && opts.EnsureSchema {
		if err := pg.EnsureSchema(ctx); err != nil {
			return nil, fmt.Errorf("ensure schema: %w", err)
		}
	}

	st := &migrationState{
		now: now, logger: logger, dryRun: opts.DryRun, skipEphemeral: opts.SkipEphemeral,
		pg: pg, ddb: ddb, prefix: tablePrefix,
		users: make(map[string]bool), userByEmail: make(map[string]string),
		catalogVersions: make(map[string]bool), songbooks: make(map[string]bool), events: make(map[string]bool),
	}

	// <prefix>identity-claims is intentionally skipped; users.email unique index replaces it.
	steps := []func(context.Context, *migrationState) (TableStats, error){
		migrateUsers, migrateAuthSessions, migratePasskeys, migrateWebAuthnSessions, migrateDeveloperKeys,
		migrateCatalogVersions, migrateSongbooks, migrateSongbookGrants, migrateSongbookInvitations,
		migrateCatalogChanges, migrateImportJobs, migrateEvents, migrateEventRequests,
		migrateIdempotencyRecords, migrateRateLimits,
	}
	report := &Report{DryRun: opts.DryRun, Tables: make([]TableStats, 0, len(steps))}
	for _, step := range steps {
		stats, err := step(ctx, st)
		if err != nil {
			return nil, err
		}
		if !st.dryRun {
			countQuery, ok := tableCountQueries[stats.Table]
			if !ok {
				return nil, fmt.Errorf("missing count query for table %s", stats.Table)
			}
			if err := st.pg.Pool().QueryRow(ctx, countQuery).Scan(&stats.VerifiedCount); err != nil {
				return nil, fmt.Errorf("verify count for %s: %w", stats.Table, err)
			}
		}
		report.Tables = append(report.Tables, stats)
	}
	return report, nil
}

func skipEphemeralTable(st *migrationState, pgTable, dynamoSuffix string) TableStats {
	dynamoTable := st.prefix + dynamoSuffix
	st.logger.Printf("INFO [%s]: skipping ephemeral table %s (--skip-ephemeral=true)", pgTable, dynamoTable)
	return TableStats{Table: pgTable, DynamoTable: dynamoTable}
}

func scanAndMigrate[T any](ctx context.Context, st *migrationState, pgTable, dynamoSuffix string, prepare func(T) (preparedRow, bool, string, error)) (TableStats, error) {
	dynamoTable := st.prefix + dynamoSuffix
	stats := TableStats{Table: pgTable, DynamoTable: dynamoTable}
	var startKey map[string]types.AttributeValue
	var batch []preparedRow
	for {
		out, err := st.ddb.Scan(ctx, &dynamodb.ScanInput{
			TableName: aws.String(dynamoTable), ConsistentRead: aws.Bool(true), ExclusiveStartKey: startKey,
		})
		if err != nil {
			return stats, fmt.Errorf("scan %s: %w", dynamoTable, err)
		}
		for _, raw := range out.Items {
			stats.Scanned++
			var item T
			if err := attributevalue.UnmarshalMap(raw, &item); err != nil {
				return stats, fmt.Errorf("unmarshal %s item: %w", dynamoTable, err)
			}
			row, keep, reason, err := prepare(item)
			if err != nil {
				return stats, fmt.Errorf("prepare %s row: %w", pgTable, err)
			}
			if !keep {
				stats.Skipped++
				if reason != "" {
					st.logger.Printf("WARN [%s]: %s", pgTable, reason)
				}
				continue
			}
			if st.dryRun {
				stats.Migrated++
				if row.onSaved != nil {
					row.onSaved()
				}
				continue
			}
			batch = append(batch, row)
			if len(batch) >= batchSize {
				if err := flushBatch(ctx, st, pgTable, batch, &stats); err != nil {
					return stats, err
				}
				batch = batch[:0]
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		startKey = out.LastEvaluatedKey
	}
	if len(batch) > 0 && !st.dryRun {
		if err := flushBatch(ctx, st, pgTable, batch, &stats); err != nil {
			return stats, err
		}
	}
	return stats, nil
}

func flushBatch(ctx context.Context, st *migrationState, pgTable string, batch []preparedRow, stats *TableStats) error {
	if len(batch) == 0 {
		return nil
	}
	err := withTx(ctx, st.pg.Pool(), func(tx pgx.Tx) error {
		for _, row := range batch {
			if _, err := tx.Exec(ctx, row.query, row.args...); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		stats.Migrated += len(batch)
		for _, row := range batch {
			if row.onSaved != nil {
				row.onSaved()
			}
		}
		return nil
	}
	if !isConstraintViolation(err) {
		return fmt.Errorf("upsert batch into %s: %w", pgTable, err)
	}
	// Fall back to single-row transactions when a batch hits a constraint error
	// (savepoints are not supported on Aurora DSQL).
	for _, row := range batch {
		singleErr := withTx(ctx, st.pg.Pool(), func(tx pgx.Tx) error {
			_, execErr := tx.Exec(ctx, row.query, row.args...)
			return execErr
		})
		if singleErr != nil {
			if isConstraintViolation(singleErr) {
				stats.Skipped++
				st.logger.Printf("WARN [%s]: skipped %s (%v)", pgTable, row.label, singleErr)
				continue
			}
			return fmt.Errorf("upsert %s (%s): %w", pgTable, row.label, singleErr)
		}
		stats.Migrated++
		if row.onSaved != nil {
			row.onSaved()
		}
	}
	return nil
}

func withTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	var lastErr error
	for attempt := 0; attempt < maxTxRetries; attempt++ {
		tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
		if err == nil {
			if err = fn(tx); err != nil {
				_ = tx.Rollback(ctx)
			} else {
				err = tx.Commit(ctx)
			}
		}
		if err == nil {
			return nil
		}
		if isSerializationFailure(err) && attempt+1 < maxTxRetries {
			lastErr = err
			base := time.Duration(10*(1<<attempt)) * time.Millisecond
			timer := time.NewTimer(base + time.Duration(rand.IntN(15))*time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			continue
		}
		return err
	}
	return fmt.Errorf("transaction failed after %d retries: %w", maxTxRetries, lastErr)
}

func pgErrCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func isSerializationFailure(err error) bool {
	c := pgErrCode(err)
	return c == sqlStateSerializationFailure || c == sqlStateDeadlockDetected
}

func isConstraintViolation(err error) bool {
	c := pgErrCode(err)
	return c == sqlStateUniqueViolation || c == sqlStateForeignKeyViolation
}

func isExpiredTTL(ttl int64, now time.Time) bool { return ttl > 0 && ttl <= now.Unix() }

func parseTimeOrFallback(raw string, fallback time.Time) (time.Time, error) {
	if raw == "" {
		return fallback.UTC(), nil
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

func parseTimestamps(createdRaw, updatedRaw string, fallback time.Time, optRaws ...string) (time.Time, time.Time, []*time.Time, error) {
	created, err := parseTimeOrFallback(createdRaw, fallback)
	if err != nil {
		return time.Time{}, time.Time{}, nil, err
	}
	updated, err := parseTimeOrFallback(updatedRaw, created)
	if err != nil {
		return time.Time{}, time.Time{}, nil, err
	}
	opt := make([]*time.Time, len(optRaws))
	for i, raw := range optRaws {
		if raw != "" {
			t, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil {
				return time.Time{}, time.Time{}, nil, err
			}
			u := t.UTC()
			opt[i] = &u
		}
	}
	return created, updated, opt, nil
}

func parseSessionExpiry(expRaw string, ttl int64, now time.Time) (time.Time, bool, error) {
	if isExpiredTTL(ttl, now) {
		return time.Time{}, false, nil
	}
	exp, err := parseTimeOrFallback(expRaw, time.Time{})
	if err != nil {
		return time.Time{}, false, err
	}
	if exp.IsZero() && ttl > 0 {
		exp = time.Unix(ttl, 0).UTC()
	}
	if exp.IsZero() || !exp.After(now) {
		return time.Time{}, false, nil
	}
	return exp, true, nil
}

func unixToNullTime(sec int64) *time.Time {
	if sec <= 0 {
		return nil
	}
	t := time.Unix(sec, 0).UTC()
	return &t
}

func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func marshalJSONSlice[T any](items []T) ([]byte, error) {
	if items == nil {
		items = []T{}
	}
	return json.Marshal(items)
}

func skipRow(reason string) (preparedRow, bool, string, error) {
	return preparedRow{}, false, reason, nil
}
func errRow(err error) (preparedRow, bool, string, error) { return preparedRow{}, false, "", err }
func okRow(label, query string, args []any, onSaved func()) (preparedRow, bool, string, error) {
	return preparedRow{label: label, query: query, args: args, onSaved: onSaved}, true, "", nil
}

func nullifyMissing(st *migrationState, table, col, rowID, val string, valid map[string]bool) *string {
	if val == "" {
		return nil
	}
	if !valid[val] {
		st.logger.Printf("WARN [%s]: nullifying missing %s %q on row %q", table, col, val, rowID)
		return nil
	}
	return &val
}

func upsertSQL(table string, cols []string, pkCols ...string) string {
	pkSet := make(map[string]bool, len(pkCols))
	for _, pk := range pkCols {
		pkSet[pk] = true
	}
	ph := make([]string, len(cols))
	upd := make([]string, 0, len(cols)-len(pkCols))
	for i, c := range cols {
		ph[i] = fmt.Sprintf("$%d", i+1)
		if !pkSet[c] {
			upd = append(upd, c+" = EXCLUDED."+c)
		}
	}
	return fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (%s) DO UPDATE SET %s",
		table, strings.Join(cols, ", "), strings.Join(ph, ", "), strings.Join(pkCols, ", "), strings.Join(upd, ", "))
}

type dynamoUserItem struct {
	ID                     string                  `dynamodbav:"id"`
	Email                  string                  `dynamodbav:"email"`
	DisplayName            string                  `dynamodbav:"display_name"`
	AvatarURL              string                  `dynamodbav:"avatar_url"`
	PasswordHash           string                  `dynamodbav:"password_hash"`
	AuthEpoch              string                  `dynamodbav:"auth_epoch"`
	IsHost                 bool                    `dynamodbav:"is_host"`
	EmailVerified          bool                    `dynamodbav:"email_verified"`
	Deleting               bool                    `dynamodbav:"deleting"`
	DeletionEpoch          string                  `dynamodbav:"deletion_epoch"`
	DeletionAuthEpoch      string                  `dynamodbav:"deletion_auth_epoch"`
	DeletionStartedAt      string                  `dynamodbav:"deletion_started_at"`
	DeletionStep           string                  `dynamodbav:"deletion_step"`
	DeletionError          string                  `dynamodbav:"deletion_error"`
	PendingCatalogVersions []string                `dynamodbav:"pending_catalog_versions"`
	FavoriteSongs          []models.FavoriteSong   `dynamodbav:"favorite_songs"`
	FavoriteArtists        []models.FavoriteArtist `dynamodbav:"favorite_artists"`
	CreatedAt              string                  `dynamodbav:"created_at"`
	UpdatedAt              string                  `dynamodbav:"updated_at"`
}

func migrateUsers(ctx context.Context, st *migrationState) (TableStats, error) {
	q := upsertSQL("users", []string{"id", "email", "display_name", "avatar_url", "password_hash", "auth_epoch", "is_host", "email_verified", "deleting", "deletion_epoch", "deletion_auth_epoch", "deletion_started_at", "deletion_step", "deletion_error", "pending_catalog_versions", "favorite_songs", "favorite_artists", "created_at", "updated_at"}, "id")
	return scanAndMigrate(ctx, st, "users", "users", func(it dynamoUserItem) (preparedRow, bool, string, error) {
		if it.ID == "" {
			return skipRow("missing user id")
		}
		email := strings.ToLower(strings.TrimSpace(it.Email))
		if email != "" {
			if prev, ok := st.userByEmail[email]; ok && prev != it.ID {
				return skipRow(fmt.Sprintf("user %s duplicates email %q already owned by %s", it.ID, email, prev))
			}
		}
		created, updated, opt, err := parseTimestamps(it.CreatedAt, it.UpdatedAt, st.now, it.DeletionStartedAt)
		if err != nil {
			return errRow(err)
		}
		pendingJSON, err := marshalJSONSlice(it.PendingCatalogVersions)
		if err != nil {
			return errRow(err)
		}
		songsJSON, err := marshalJSONSlice(it.FavoriteSongs)
		if err != nil {
			return errRow(err)
		}
		artistsJSON, err := marshalJSONSlice(it.FavoriteArtists)
		if err != nil {
			return errRow(err)
		}
		return okRow(it.ID, q, []any{it.ID, email, it.DisplayName, it.AvatarURL, it.PasswordHash, it.AuthEpoch, it.IsHost, it.EmailVerified, it.Deleting, it.DeletionEpoch, it.DeletionAuthEpoch, opt[0], it.DeletionStep, it.DeletionError, pendingJSON, songsJSON, artistsJSON, created, updated}, func() {
			st.users[it.ID] = true
			if email != "" {
				st.userByEmail[email] = it.ID
			}
		})
	})
}

type dynamoSessionItem struct {
	TokenHash string `dynamodbav:"token_hash"`
	UserID    string `dynamodbav:"user_id"`
	AuthEpoch string `dynamodbav:"auth_epoch"`
	ExpiresAt string `dynamodbav:"expires_at"`
	TTL       int64  `dynamodbav:"ttl"`
}

func migrateAuthSessions(ctx context.Context, st *migrationState) (TableStats, error) {
	q := upsertSQL("auth_sessions", []string{"token_hash", "user_id", "auth_epoch", "expires_at"}, "token_hash")
	return scanAndMigrate(ctx, st, "auth_sessions", "sessions", func(it dynamoSessionItem) (preparedRow, bool, string, error) {
		exp, ok, err := parseSessionExpiry(it.ExpiresAt, it.TTL, st.now)
		if err != nil {
			return errRow(err)
		}
		if !ok {
			return skipRow("")
		}
		tokenBytes, err := hex.DecodeString(strings.TrimSpace(it.TokenHash))
		if err != nil || len(tokenBytes) == 0 {
			return skipRow(fmt.Sprintf("invalid session token_hash %q", it.TokenHash))
		}
		if !st.users[it.UserID] {
			return skipRow(fmt.Sprintf("orphaned auth_session for missing user %q", it.UserID))
		}
		return okRow(it.TokenHash, q, []any{tokenBytes, it.UserID, it.AuthEpoch, exp}, nil)
	})
}
