package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"micup.live/server/internal/store"
)

// Allow atomically increments and evaluates a fixed-window token counter.
func (s *Store) Allow(ctx context.Context, scope, key string, limit int, window time.Duration) (store.RateLimitResult, error) {
	if limit < 1 {
		limit = 1
	}
	if window < time.Second {
		window = time.Minute
	}

	now := s.currentTime()
	windowSecs := int64(window.Seconds())
	bucket := now.Unix() / windowSecs
	resetAt := time.Unix((bucket+1)*windowSecs, 0).UTC()
	expiresAt := time.Unix((bucket+2)*windowSecs, 0).UTC()

	bucketKey := fmt.Sprintf("rl#%s#%s#%d", scope, key, bucket)

	var hits int
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO rate_limits (bucket_key, hits, expires_at)
			VALUES ($1, 1, $2)
			ON CONFLICT (bucket_key) DO UPDATE
			SET hits = rate_limits.hits + 1
			RETURNING hits`,
			bucketKey, expiresAt,
		).Scan(&hits)
	})
	if err != nil {
		return store.RateLimitResult{}, fmt.Errorf("update rate limit counter: %w", err)
	}

	remaining := limit - hits
	if remaining < 0 {
		remaining = 0
	}

	return store.RateLimitResult{
		Allowed:   hits <= limit,
		Limit:     limit,
		Remaining: remaining,
		ResetAt:   resetAt,
	}, nil
}
