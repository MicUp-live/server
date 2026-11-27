// Package postgres implements MicUp.live operational persistence on PostgreSQL 16
// with schema and transaction semantics compatible with Amazon Aurora DSQL.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"micup.live/server/internal/store"
)

const (
	sqlStateUniqueViolation      = "23505"
	sqlStateForeignKeyViolation  = "23503"
	sqlStateSerializationFailure = "40001"
	sqlStateDeadlockDetected     = "40P01"

	maxTxRetries = 5
)

// Config configures the PostgreSQL / Aurora DSQL store.
type Config struct {
	DSN             string
	DSQLMode        bool
	DSQLEndpoint    string
	DSQLRegion      string
	DSQLUser        string
	DSQLCredentials aws.CredentialsProvider
	MaxConns        int32
}

// Store implements all store.* interfaces backed by PostgreSQL / Aurora DSQL.
type Store struct {
	pool     *pgxpool.Pool
	dsqlMode bool
	now      func() time.Time
}

// Compile-time interface verification (enabled as domain files are added).
var (
	_ store.AuthStore           = (*Store)(nil)
	_ store.FavoritesStore      = (*Store)(nil)
	_ store.PasskeyStore        = (*Store)(nil)
	_ store.DeveloperKeyStore   = (*Store)(nil)
	_ store.SongbookStore       = (*Store)(nil)
	_ store.CatalogStore        = (*Store)(nil)
	_ store.CatalogChangeStore  = (*Store)(nil)
	_ store.ImportJobStore      = (*Store)(nil)
	_ store.EventStore          = (*Store)(nil)
	_ store.RequestStore        = (*Store)(nil)
	_ store.SongbookAccessStore = (*Store)(nil)
	_ store.RateLimitStore      = (*Store)(nil)
)

// New connects to PostgreSQL / Aurora DSQL and verifies connectivity.
func New(ctx context.Context, cfg Config) (*Store, error) {
	dsn := cfg.DSN
	if dsn == "" && cfg.DSQLMode && cfg.DSQLEndpoint != "" {
		dsn = buildDSQLDSN(cfg.DSQLEndpoint, cfg.DSQLUser)
	}
	if dsn == "" {
		return nil, errors.New("postgres DSN or DSQL endpoint is required")
	}
	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse postgres DSN: %w", err)
	}
	if cfg.MaxConns > 0 {
		poolCfg.MaxConns = cfg.MaxConns
	}
	if cfg.DSQLMode {
		if err := configureDSQLPool(ctx, cfg, poolCfg); err != nil {
			return nil, err
		}
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &Store{
		pool:     pool,
		dsqlMode: cfg.DSQLMode,
		now:      func() time.Time { return time.Now().UTC() },
	}, nil
}

// Close releases all pooled connections.
func (s *Store) Close() error {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
	return nil
}

// Pool exposes the underlying pgxpool for test setup/teardown.
func (s *Store) Pool() *pgxpool.Pool {
	return s.pool
}

func (s *Store) currentTime() time.Time {
	if s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}

// withTx executes fn inside a transaction, automatically retrying bounded
// times on serialization failures (SQLSTATE 40001 / 40P01) for Aurora DSQL OCC parity.
func (s *Store) withTx(ctx context.Context, fn func(pgx.Tx) error) error {
	var lastErr error
	for attempt := 0; attempt < maxTxRetries; attempt++ {
		err := s.runTxAttempt(ctx, fn)
		if err == nil {
			return nil
		}
		if !isSerializationFailure(err) || attempt+1 >= maxTxRetries {
			return err
		}
		lastErr = err
		if sleepErr := retryBackoff(ctx, attempt); sleepErr != nil {
			return sleepErr
		}
	}
	return fmt.Errorf("transaction failed after %d retries: %w", maxTxRetries, lastErr)
}

func (s *Store) runTxAttempt(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}

func retryBackoff(ctx context.Context, attempt int) error {
	base := time.Duration(10*(1<<attempt)) * time.Millisecond
	jitter := time.Duration(rand.IntN(15)) * time.Millisecond
	timer := time.NewTimer(base + jitter)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func pgErrCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func pgConstraintName(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.ConstraintName
	}
	return ""
}

func isUniqueViolation(err error) bool {
	return pgErrCode(err) == sqlStateUniqueViolation
}

func isForeignKeyViolation(err error) bool {
	return pgErrCode(err) == sqlStateForeignKeyViolation
}

func isSerializationFailure(err error) bool {
	code := pgErrCode(err)
	return code == sqlStateSerializationFailure || code == sqlStateDeadlockDetected
}

func isNotFound(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func nullTimePtr(t *time.Time) *time.Time {
	if t == nil || t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.UTC()
}

func unixToNullTime(sec int64) *time.Time {
	if sec <= 0 {
		return nil
	}
	t := time.Unix(sec, 0).UTC()
	return &t
}

func timePtrToUnix(t *time.Time) int64 {
	if t == nil || t.IsZero() {
		return 0
	}
	return t.UTC().Unix()
}

func marshalJSON(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func unmarshalJSON(data []byte, dst any) error {
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, dst)
}
