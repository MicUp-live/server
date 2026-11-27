package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

const (
	invitationColumns = `id, songbook_id, owner_id, recipient_email, can_edit_catalog, can_host_shows,
		token_hash, status, expires_at, last_sent_at, send_error,
		COALESCE(accepted_by_user_id, ''), accepted_at, created_at, updated_at`
	invitationColumnsAliased = `si.id, si.songbook_id, si.owner_id, si.recipient_email, si.can_edit_catalog, si.can_host_shows,
		si.token_hash, si.status, si.expires_at, si.last_sent_at, si.send_error,
		COALESCE(si.accepted_by_user_id, ''), si.accepted_at, si.created_at, si.updated_at`
	revokedTokenPrefix = "revoked:"
)

type invitationScanner interface {
	Scan(dest ...any) error
}

var errInviteFKViolation = errors.New("invitation foreign key violation")

func normalizeInviteEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func scanSongbookInvitation(row invitationScanner) (*models.SongbookInvitation, error) {
	var (
		inv                  models.SongbookInvitation
		status               string
		lastSent, acceptedAt *time.Time
	)
	err := row.Scan(
		&inv.ID, &inv.SongbookID, &inv.OwnerID, &inv.RecipientEmail,
		&inv.CanEditCatalog, &inv.CanHostShows, &inv.TokenHash, &status,
		&inv.ExpiresAt, &lastSent, &inv.SendError, &inv.AcceptedByUserID,
		&acceptedAt, &inv.CreatedAt, &inv.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	inv.Status = models.SongbookInvitationStatus(status)
	if strings.HasPrefix(inv.TokenHash, revokedTokenPrefix) {
		inv.TokenHash = ""
	}
	inv.ExpiresAt = inv.ExpiresAt.UTC()
	inv.CreatedAt = inv.CreatedAt.UTC()
	inv.UpdatedAt = inv.UpdatedAt.UTC()
	if lastSent != nil {
		t := lastSent.UTC()
		inv.LastSentAt = &t
	}
	if acceptedAt != nil {
		t := acceptedAt.UTC()
		inv.AcceptedAt = &t
	}
	return &inv, nil
}

func hasExistingCollaboratorGrantTx(ctx context.Context, tx pgx.Tx, songbookID, normEmail string) (bool, error) {
	var exists int
	err := tx.QueryRow(ctx, `
		SELECT 1 FROM users AS u
		JOIN songbook_grants AS g ON g.user_id = u.id
		WHERE u.email = $1 AND g.songbook_id = $2 LIMIT 1`,
		normEmail, songbookID,
	).Scan(&exists)
	if isNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

func diagnoseInvitationMissTx(ctx context.Context, tx pgx.Tx, songbookID, invitationID string) error {
	var existingSongbookID string
	err := tx.QueryRow(ctx, `SELECT songbook_id FROM songbook_invitations WHERE id = $1`, invitationID).Scan(&existingSongbookID)
	if isNotFound(err) {
		return store.ErrInvitationNotFound
	}
	if err != nil {
		return fmt.Errorf("inspect invitation: %w", err)
	}
	if existingSongbookID != songbookID {
		return store.ErrInvitationNotFound
	}
	return store.ErrInvitationInvalid
}

func cleanReplaceInvitationIDs(selfID string, replaceIDs []string) []string {
	seen := make(map[string]struct{}, len(replaceIDs))
	out := make([]string, 0, len(replaceIDs))
	for _, oldID := range replaceIDs {
		if _, dup := seen[oldID]; dup || strings.TrimSpace(oldID) == "" || oldID == selfID {
			continue
		}
		seen[oldID] = struct{}{}
		out = append(out, oldID)
	}
	return out
}

// CreateSongbookInvitation atomically persists a pending invitation and marks any
// prior pending invitations in replaceIDs as replaced, enforcing one pending
// invitation per (songbook_id, recipient_email) via idx_songbook_invitations_pending.
func (s *Store) CreateSongbookInvitation(ctx context.Context, inv *models.SongbookInvitation, replaceIDs []string) error {
	now := s.currentTime()
	created := now
	if !inv.CreatedAt.IsZero() {
		created = inv.CreatedAt.UTC()
	}
	inv.CreatedAt = created
	inv.UpdatedAt = now
	inv.Status = models.InvitationStatusPending
	inv.RecipientEmail = normalizeInviteEmail(inv.RecipientEmail)
	cleanReplace := cleanReplaceInvitationIDs(inv.ID, replaceIDs)

	err := s.withTx(ctx, func(tx pgx.Tx) error {
		if err := lockSongbookForInvitationTx(ctx, tx, inv.SongbookID, inv.OwnerID); err != nil {
			return err
		}
		if err := ensureNoCollaboratorGrantTx(ctx, tx, inv.SongbookID, inv.RecipientEmail, "check existing collaborator grant"); err != nil {
			return err
		}
		if err := replacePendingInvitationsTx(ctx, tx, inv, cleanReplace, now); err != nil {
			return err
		}
		if err := insertSongbookInvitationTx(ctx, tx, inv, created, now); err != nil {
			return err
		}
		// Re-check collaborator grant after insert in case an accept committed
		// concurrently while this insert waited on a pending invitation lock.
		return ensureNoCollaboratorGrantTx(ctx, tx, inv.SongbookID, inv.RecipientEmail, "verify collaborator absence")
	})
	if errors.Is(err, errInviteFKViolation) {
		return s.resolveInvitationFKViolation(ctx, inv.SongbookID)
	}
	return err
}

func lockSongbookForInvitationTx(ctx context.Context, tx pgx.Tx, songbookID, expectedOwnerID string) error {
	var ownerID string
	var deleting bool
	if err := tx.QueryRow(ctx, `SELECT owner_id, deleting FROM songbooks WHERE id = $1 FOR UPDATE`, songbookID).Scan(&ownerID, &deleting); err != nil {
		if isNotFound(err) {
			return store.ErrInvitationConflict
		}
		return fmt.Errorf("check songbook for invitation: %w", err)
	}
	if deleting {
		return store.ErrSongbookDeleting
	}
	if ownerID != expectedOwnerID {
		return store.ErrInvitationConflict
	}
	return nil
}

func ensureNoCollaboratorGrantTx(ctx context.Context, tx pgx.Tx, songbookID, recipientEmail, errContext string) error {
	isCollab, err := hasExistingCollaboratorGrantTx(ctx, tx, songbookID, recipientEmail)
	if err != nil {
		return fmt.Errorf("%s: %w", errContext, err)
	}
	if isCollab {
		return store.ErrAlreadyCollaborator
	}
	return nil
}

func replacePendingInvitationsTx(ctx context.Context, tx pgx.Tx, inv *models.SongbookInvitation, replaceIDs []string, now time.Time) error {
	for _, oldID := range replaceIDs {
		tag, err := tx.Exec(ctx, `
			UPDATE songbook_invitations
			SET status = $1, token_hash = $2, updated_at = $3
			WHERE id = $4 AND songbook_id = $5 AND status = $6`,
			string(models.InvitationStatusReplaced), revokedTokenPrefix+oldID, now,
			oldID, inv.SongbookID, string(models.InvitationStatusPending),
		)
		if err != nil {
			return fmt.Errorf("replace pending invitation: %w", err)
		}
		if tag.RowsAffected() == 0 {
			if collabNow, cErr := hasExistingCollaboratorGrantTx(ctx, tx, inv.SongbookID, inv.RecipientEmail); cErr == nil && collabNow {
				return store.ErrAlreadyCollaborator
			}
			return store.ErrInvitationConflict
		}
	}
	return nil
}

func insertSongbookInvitationTx(ctx context.Context, tx pgx.Tx, inv *models.SongbookInvitation, created, now time.Time) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO songbook_invitations (
			id, songbook_id, owner_id, recipient_email, can_edit_catalog, can_host_shows,
			token_hash, status, expires_at, send_error, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, '', $10, $11)`,
		inv.ID, inv.SongbookID, inv.OwnerID, inv.RecipientEmail, inv.CanEditCatalog, inv.CanHostShows,
		inv.TokenHash, string(inv.Status), inv.ExpiresAt.UTC(), created, now,
	)
	if err == nil {
		return nil
	}
	if isUniqueViolation(err) {
		return store.ErrInvitationConflict
	}
	if isForeignKeyViolation(err) {
		return errInviteFKViolation
	}
	return fmt.Errorf("create songbook invitation: %w", err)
}

func (s *Store) resolveInvitationFKViolation(ctx context.Context, songbookID string) error {
	var sbDeleting bool
	if sbErr := s.pool.QueryRow(ctx, `SELECT deleting FROM songbooks WHERE id = $1`, songbookID).Scan(&sbDeleting); sbErr != nil {
		if isNotFound(sbErr) {
			return os.ErrNotExist
		}
		return fmt.Errorf("check songbook after invitation foreign key violation: %w", sbErr)
	}
	if sbDeleting {
		return store.ErrSongbookDeleting
	}
	return store.ErrInvitationConflict
}

// GetSongbookInvitation loads an invitation by ID.
func (s *Store) GetSongbookInvitation(ctx context.Context, id string) (*models.SongbookInvitation, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+invitationColumns+` FROM songbook_invitations WHERE id = $1`, id)
	inv, err := scanSongbookInvitation(row)
	if err != nil {
		if isNotFound(err) {
			return nil, store.ErrInvitationNotFound
		}
		return nil, fmt.Errorf("get songbook invitation: %w", err)
	}
	return inv, nil
}

// GetSongbookInvitationByTokenHash resolves a pending invitation by its SHA-256 token digest.
func (s *Store) GetSongbookInvitationByTokenHash(ctx context.Context, tokenHash string) (*models.SongbookInvitation, error) {
	if strings.TrimSpace(tokenHash) == "" || strings.HasPrefix(tokenHash, revokedTokenPrefix) {
		return nil, store.ErrInvitationNotFound
	}
	row := s.pool.QueryRow(ctx, `SELECT `+invitationColumns+` FROM songbook_invitations WHERE token_hash = $1`, tokenHash)
	inv, err := scanSongbookInvitation(row)
	if err != nil {
		if isNotFound(err) {
			return nil, store.ErrInvitationNotFound
		}
		return nil, fmt.Errorf("get songbook invitation by token: %w", err)
	}
	if inv.Status != models.InvitationStatusPending || inv.TokenHash != tokenHash {
		return nil, store.ErrInvitationNotFound
	}
	return inv, nil
}

// ListSongbookInvitationsBySongbook lists all invitations for a songbook newest-first.
func (s *Store) ListSongbookInvitationsBySongbook(ctx context.Context, songbookID string) ([]*models.SongbookInvitation, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+invitationColumns+`
		FROM songbook_invitations
		WHERE songbook_id = $1
		ORDER BY created_at DESC`,
		songbookID,
	)
	if err != nil {
		return nil, fmt.Errorf("list songbook invitations: %w", err)
	}
	defer rows.Close()

	var invitations []*models.SongbookInvitation
	for rows.Next() {
		inv, err := scanSongbookInvitation(rows)
		if err != nil {
			return nil, fmt.Errorf("scan songbook invitation: %w", err)
		}
		invitations = append(invitations, inv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate songbook invitations: %w", err)
	}
	return invitations, nil
}

// RotateSongbookInvitationToken issues a fresh token digest and expiry on a pending invitation,
// immediately invalidating any previous acceptance link.
func (s *Store) RotateSongbookInvitationToken(ctx context.Context, songbookID, invitationID, newTokenHash string, newExpiresAt time.Time) (*models.SongbookInvitation, error) {
	now := s.currentTime()
	var rotated *models.SongbookInvitation

	err := s.withTx(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			UPDATE songbook_invitations AS si
			SET token_hash = $1, expires_at = $2, updated_at = $3, send_error = '', last_sent_at = NULL
			FROM songbooks AS sb
			WHERE si.id = $4 AND si.songbook_id = $5 AND si.status = $6
			  AND sb.id = si.songbook_id AND sb.deleting = FALSE
			RETURNING `+invitationColumnsAliased,
			newTokenHash, newExpiresAt.UTC(), now,
			invitationID, songbookID, string(models.InvitationStatusPending),
		)
		inv, err := scanSongbookInvitation(row)
		if err == nil {
			rotated = inv
			return nil
		}
		if !isNotFound(err) {
			return fmt.Errorf("rotate songbook invitation token: %w", err)
		}

		var deleting bool
		if sbErr := tx.QueryRow(ctx, `SELECT deleting FROM songbooks WHERE id = $1`, songbookID).Scan(&deleting); sbErr != nil && !isNotFound(sbErr) {
			return fmt.Errorf("check songbook for rotate invitation: %w", sbErr)
		} else if sbErr == nil && deleting {
			return store.ErrSongbookDeleting
		}
		return diagnoseInvitationMissTx(ctx, tx, songbookID, invitationID)
	})
	if err != nil {
		return nil, err
	}
	return rotated, nil
}

// RecordSongbookInvitationDelivery records delivery timestamp or provider error for the
// specific send attempt identified by expectedTokenHash without altering status.
func (s *Store) RecordSongbookInvitationDelivery(ctx context.Context, invitationID, expectedTokenHash string, sentAt *time.Time, sendErr string) error {
	if strings.TrimSpace(expectedTokenHash) == "" || strings.HasPrefix(expectedTokenHash, revokedTokenPrefix) {
		return store.ErrInvitationDeliverySuperseded
	}
	now := s.currentTime()
	cleanErr := ""
	if strings.TrimSpace(sendErr) != "" {
		cleanErr = sendErr
	}

	return s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE songbook_invitations
			SET updated_at = $1, last_sent_at = COALESCE($2, last_sent_at), send_error = $3
			WHERE id = $4 AND status = $5 AND token_hash = $6`,
			now, nullTimePtr(sentAt), cleanErr,
			invitationID, string(models.InvitationStatusPending), expectedTokenHash,
		)
		if err != nil {
			return fmt.Errorf("record songbook invitation delivery: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return store.ErrInvitationDeliverySuperseded
		}
		return nil
	})
}

// CancelSongbookInvitation cancels a pending invitation and invalidates its token digest.
func (s *Store) CancelSongbookInvitation(ctx context.Context, songbookID, invitationID string) error {
	now := s.currentTime()
	return s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE songbook_invitations
			SET status = $1, token_hash = $2, updated_at = $3
			WHERE id = $4 AND songbook_id = $5 AND status = $6`,
			string(models.InvitationStatusCanceled), revokedTokenPrefix+invitationID, now,
			invitationID, songbookID, string(models.InvitationStatusPending),
		)
		if err != nil {
			return fmt.Errorf("cancel songbook invitation: %w", err)
		}
		if tag.RowsAffected() > 0 {
			return nil
		}
		return diagnoseInvitationMissTx(ctx, tx, songbookID, invitationID)
	})
}

func verifyAcceptTargetsTx(ctx context.Context, tx pgx.Tx, inv *models.SongbookInvitation, userID, normEmail string) error {
	var sbOwnerID string
	var sbDeleting bool
	if err := tx.QueryRow(ctx, `SELECT owner_id, deleting FROM songbooks WHERE id = $1 FOR UPDATE`, inv.SongbookID).Scan(&sbOwnerID, &sbDeleting); err != nil {
		if isNotFound(err) {
			return store.ErrSongbookDeleting
		}
		return fmt.Errorf("check songbook for acceptance: %w", err)
	}
	if sbDeleting {
		return store.ErrSongbookDeleting
	}
	if sbOwnerID != inv.OwnerID {
		return store.ErrInvitationInvalid
	}

	var userEmail string
	var emailVerified, isHost, userDeleting bool
	if err := tx.QueryRow(ctx, `SELECT email, email_verified, is_host, deleting FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&userEmail, &emailVerified, &isHost, &userDeleting); err != nil {
		if isNotFound(err) {
			return store.ErrInvitationInvalid
		}
		return fmt.Errorf("check user for acceptance: %w", err)
	}
	if userDeleting {
		return store.ErrAccountDeleting
	}
	if normalizeInviteEmail(userEmail) != normEmail {
		return store.ErrInvitationEmailMismatch
	}
	if !emailVerified || !isHost {
		return store.ErrPermissionDenied
	}
	return nil
}

// AcceptSongbookInvitation atomically verifies invitation state, expiry, recipient email,
// verified host account state, and non-deleting songbook state while consuming the
// single-use invitation and inserting the collaborator grant in one transaction.
func (s *Store) AcceptSongbookInvitation(ctx context.Context, tokenHash, userID, normalizedEmail string, now time.Time) (*models.SongbookGrant, *models.SongbookInvitation, error) {
	if now.IsZero() {
		now = s.currentTime()
	} else {
		now = now.UTC()
	}
	normEmail := normalizeInviteEmail(normalizedEmail)
	if strings.TrimSpace(tokenHash) == "" || strings.HasPrefix(tokenHash, revokedTokenPrefix) {
		return nil, nil, store.ErrInvitationNotFound
	}

	var outGrant *models.SongbookGrant
	var outInv *models.SongbookInvitation

	err := s.withTx(ctx, func(tx pgx.Tx) error {
		inv, err := loadPendingInvitationForAcceptTx(ctx, tx, tokenHash, normEmail, now)
		if err != nil {
			return err
		}
		if err := verifyAcceptTargetsTx(ctx, tx, inv, userID, normEmail); err != nil {
			return err
		}
		if err := insertSongbookGrantForAcceptTx(ctx, tx, inv, userID, now); err != nil {
			return err
		}
		if err := markInvitationAcceptedTx(ctx, tx, inv, tokenHash, userID, now); err != nil {
			return err
		}

		acceptedAt := now
		inv.Status = models.InvitationStatusAccepted
		inv.TokenHash = ""
		inv.AcceptedByUserID = userID
		inv.AcceptedAt = &acceptedAt
		inv.UpdatedAt = now

		outGrant = &models.SongbookGrant{
			SongbookID: inv.SongbookID, UserID: userID,
			CanEditCatalog: inv.CanEditCatalog, CanHostShows: inv.CanHostShows,
			GrantedBy: inv.OwnerID, CreatedAt: now, UpdatedAt: now,
		}
		outInv = inv
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return outGrant, outInv, nil
}

func loadPendingInvitationForAcceptTx(ctx context.Context, tx pgx.Tx, tokenHash, normEmail string, now time.Time) (*models.SongbookInvitation, error) {
	row := tx.QueryRow(ctx, `SELECT `+invitationColumns+` FROM songbook_invitations WHERE token_hash = $1 FOR UPDATE`, tokenHash)
	inv, err := scanSongbookInvitation(row)
	if err != nil {
		if isNotFound(err) {
			return nil, store.ErrInvitationNotFound
		}
		return nil, fmt.Errorf("load invitation for acceptance: %w", err)
	}
	if inv.Status != models.InvitationStatusPending || inv.TokenHash != tokenHash {
		return nil, store.ErrInvitationInvalid
	}
	if !inv.ExpiresAt.After(now) {
		return nil, store.ErrInvitationExpired
	}
	if normalizeInviteEmail(inv.RecipientEmail) != normEmail {
		return nil, store.ErrInvitationEmailMismatch
	}
	return inv, nil
}

func insertSongbookGrantForAcceptTx(ctx context.Context, tx pgx.Tx, inv *models.SongbookInvitation, userID string, now time.Time) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO songbook_grants (
			songbook_id, user_id, can_edit_catalog, can_host_shows, granted_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		inv.SongbookID, userID, inv.CanEditCatalog, inv.CanHostShows, nullString(inv.OwnerID), now, now,
	)
	if err == nil {
		return nil
	}
	if isUniqueViolation(err) {
		return store.ErrAlreadyCollaborator
	}
	if isForeignKeyViolation(err) {
		return store.ErrSongbookDeleting
	}
	return fmt.Errorf("insert songbook grant: %w", err)
}

func markInvitationAcceptedTx(ctx context.Context, tx pgx.Tx, inv *models.SongbookInvitation, tokenHash, userID string, now time.Time) error {
	tag, err := tx.Exec(ctx, `
		UPDATE songbook_invitations
		SET status = $1, token_hash = $2, accepted_by_user_id = $3, accepted_at = $4, updated_at = $5
		WHERE id = $6 AND status = $7 AND token_hash = $8 AND expires_at > $9`,
		string(models.InvitationStatusAccepted), revokedTokenPrefix+inv.ID,
		userID, now, now, inv.ID, string(models.InvitationStatusPending), tokenHash, now,
	)
	if err != nil {
		return fmt.Errorf("accept songbook invitation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrInvitationInvalid
	}
	return nil
}

// DeleteSongbookInvitation removes an invitation record during songbook cleanup.
func (s *Store) DeleteSongbookInvitation(ctx context.Context, invitationID string) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM songbook_invitations WHERE id = $1`, invitationID); err != nil {
			return fmt.Errorf("delete songbook invitation: %w", err)
		}
		return nil
	})
}
