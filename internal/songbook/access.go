package songbook

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"micup.live/server/internal/email"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

// Action represents a capability check against a songbook.
type Action int

const (
	ActionDiscover Action = iota
	ActionEditCatalog
	ActionHostShows
	ActionManageBook
	ActionManageAccess
)

const DefaultInvitationTTL = 7 * 24 * time.Hour

var (
	ErrAlreadyCollaborator = errors.New("this user already has access to the songbook")
	ErrCannotInviteOwner   = errors.New("cannot invite the songbook owner")
	ErrPermissionRequired  = errors.New("select at least one permission")
	ErrInvalidEmail        = errors.New("valid recipient email is required")
	ErrHostRequired        = errors.New("a host account is required to accept this invitation")
	ErrEmailNotVerified    = errors.New("please verify your email address before accepting this invitation")
)

// HashInvitationToken computes the SHA-256 hex digest persisted for an invitation token.
func HashInvitationToken(rawToken string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(rawToken)))
	return hex.EncodeToString(sum[:])
}

func generateInvitationToken() (raw string, digest string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	return raw, HashInvitationToken(raw), nil
}

func normalizeEmail(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
}

func ownerPermissions() *models.SongbookPermissions {
	return &models.SongbookPermissions{
		IsOwner:         true,
		CanEditCatalog:  true,
		CanHostShows:    true,
		CanManageBook:   true,
		CanManageAccess: true,
	}
}

func grantPermissions(g *models.SongbookGrant) *models.SongbookPermissions {
	return &models.SongbookPermissions{
		IsOwner:         false,
		CanEditCatalog:  g.CanEditCatalog,
		CanHostShows:    g.CanHostShows,
		CanManageBook:   false,
		CanManageAccess: false,
	}
}

func permissionAllows(p *models.SongbookPermissions, action Action) bool {
	if p == nil {
		return false
	}
	switch action {
	case ActionDiscover:
		return p.IsOwner || p.CanEditCatalog || p.CanHostShows
	case ActionEditCatalog:
		return p.IsOwner || p.CanEditCatalog
	case ActionHostShows:
		return p.IsOwner || p.CanHostShows
	case ActionManageBook:
		return p.IsOwner && p.CanManageBook
	case ActionManageAccess:
		return p.IsOwner && p.CanManageAccess
	default:
		return false
	}
}

// Authorize evaluates the single canonical songbook permission policy for actorID on songbookID.
// Strangers receive ErrSongbookNotFound; collaborators lacking the specific action receive store.ErrPermissionDenied.
func (s *Service) Authorize(ctx context.Context, actorID, songbookID string, action Action) (*models.Songbook, *models.SongbookPermissions, error) {
	if s.Songbooks == nil || strings.TrimSpace(actorID) == "" || strings.TrimSpace(songbookID) == "" {
		return nil, nil, ErrSongbookNotFound
	}
	sb, err := s.Songbooks.GetSongbook(ctx, songbookID)
	if err != nil || sb == nil {
		return nil, nil, ErrSongbookNotFound
	}
	if sb.Deleting {
		// Allow only the owner to resume deletion via ActionManageBook.
		if sb.OwnerID == actorID && action == ActionManageBook {
			perms := ownerPermissions()
			sb.Permissions = perms
			return sb, perms, nil
		}
		return nil, nil, store.ErrSongbookDeleting
	}
	if sb.OwnerID == actorID {
		perms := ownerPermissions()
		sb.Permissions = perms
		return sb, perms, nil
	}
	return s.authorizeCollaborator(ctx, sb, songbookID, actorID, action)
}

func (s *Service) authorizeCollaborator(ctx context.Context, sb *models.Songbook, songbookID, actorID string, action Action) (*models.Songbook, *models.SongbookPermissions, error) {
	if s.Access == nil {
		return nil, nil, ErrSongbookNotFound
	}
	grant, err := s.Access.GetSongbookGrant(ctx, songbookID, actorID)
	if err != nil {
		if errors.Is(err, store.ErrGrantNotFound) || errors.Is(err, os.ErrNotExist) {
			return nil, nil, ErrSongbookNotFound
		}
		return nil, nil, err
	}
	perms := grantPermissions(grant)
	sb.Permissions = perms
	if !permissionAllows(perms, action) {
		return sb, perms, store.ErrPermissionDenied
	}
	s.populateOwnerDisplayName(ctx, sb, nil)
	return sb, perms, nil
}

func (s *Service) populateOwnerDisplayName(ctx context.Context, sb *models.Songbook, cache map[string]string) {
	if s.Auth == nil || sb.OwnerID == "" {
		return
	}
	if cache != nil {
		if name, ok := cache[sb.OwnerID]; ok {
			sb.OwnerDisplayName = name
			return
		}
	}
	owner, err := s.Auth.GetUser(ctx, sb.OwnerID)
	if err != nil || owner == nil {
		return
	}
	if cache != nil {
		cache[sb.OwnerID] = owner.DisplayName
	}
	sb.OwnerDisplayName = owner.DisplayName
}

// WithEditContext attaches the collaborator edit-grant fence when actorID is not the book owner.
func WithEditContext(ctx context.Context, actorID string, sb *models.Songbook) context.Context {
	if sb == nil || actorID == "" || sb.OwnerID == actorID {
		return ctx
	}
	return store.WithSongbookEditActor(ctx, actorID)
}

// ListAccessibleSongbooks returns all songbooks owned by or granted to actorID,
// with effective permissions populated on each item.
func (s *Service) ListAccessibleSongbooks(ctx context.Context, actorID string) ([]*models.Songbook, error) {
	if s.Songbooks == nil {
		return []*models.Songbook{}, nil
	}
	if strings.TrimSpace(actorID) == "" {
		return s.Songbooks.ListSongbooks(ctx)
	}
	owned, err := s.Songbooks.ListSongbooksByOwner(ctx, actorID)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(owned))
	result := make([]*models.Songbook, 0, len(owned))
	for _, sb := range owned {
		if sb.Deleting {
			continue
		}
		sb.Permissions = ownerPermissions()
		seen[sb.ID] = struct{}{}
		result = append(result, sb)
	}
	result, err = s.appendGrantedSongbooks(ctx, actorID, seen, result)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	return result, nil
}

func (s *Service) appendGrantedSongbooks(ctx context.Context, actorID string, seen map[string]struct{}, result []*models.Songbook) ([]*models.Songbook, error) {
	if s.Access == nil {
		return result, nil
	}
	grants, err := s.Access.ListSongbookGrantsByUser(ctx, actorID)
	if err != nil {
		return nil, err
	}
	ownerNames := make(map[string]string)
	for _, g := range grants {
		if _, ok := seen[g.SongbookID]; ok || (!g.CanEditCatalog && !g.CanHostShows) {
			continue
		}
		sb, sbErr := s.Songbooks.GetSongbook(ctx, g.SongbookID)
		if sbErr != nil || sb == nil || sb.Deleting {
			continue
		}
		sb.Permissions = grantPermissions(g)
		s.populateOwnerDisplayName(ctx, sb, ownerNames)
		seen[sb.ID] = struct{}{}
		result = append(result, sb)
	}
	return result, nil
}

// GetAccessOverview returns accepted members and pending invitations for a songbook owner.
func (s *Service) GetAccessOverview(ctx context.Context, ownerID, songbookID string) (*models.SongbookAccessOverview, error) {
	if s.Access == nil {
		return nil, fmt.Errorf("songbook access store unavailable")
	}
	if _, _, err := s.Authorize(ctx, ownerID, songbookID, ActionManageAccess); err != nil {
		return nil, err
	}
	grants, err := s.Access.ListSongbookGrantsBySongbook(ctx, songbookID)
	if err != nil {
		return nil, err
	}
	members := make([]*models.SongbookMember, 0, len(grants))
	for _, g := range grants {
		m := &models.SongbookMember{
			UserID:         g.UserID,
			CanEditCatalog: g.CanEditCatalog,
			CanHostShows:   g.CanHostShows,
			CreatedAt:      g.CreatedAt,
			UpdatedAt:      g.UpdatedAt,
		}
		if s.Auth != nil {
			if u, uErr := s.Auth.GetUser(ctx, g.UserID); uErr == nil && u != nil {
				m.Email = u.Email
				m.DisplayName = u.DisplayName
			}
		}
		members = append(members, m)
	}
	allInvites, err := s.Access.ListSongbookInvitationsBySongbook(ctx, songbookID)
	if err != nil {
		return nil, err
	}
	pending := make([]*models.SongbookInvitation, 0, len(allInvites))
	for _, inv := range allInvites {
		if inv.Status == models.InvitationStatusPending {
			pending = append(pending, inv)
		}
	}
	return &models.SongbookAccessOverview{
		Members:     members,
		Invitations: pending,
	}, nil
}

// CreateInvitation persists a pending invitation (replacing any earlier pending invitation
// for the same recipient) and sends the acceptance email. If email delivery fails, the
// invitation remains pending with send_error recorded so the owner can retry.
func (s *Service) CreateInvitation(ctx context.Context, owner *models.User, songbookID, recipientEmail string, canEdit, canHost bool, baseURL string) (*models.SongbookInvitation, error) {
	if s.Access == nil {
		return nil, fmt.Errorf("songbook access store unavailable")
	}
	if owner == nil {
		return nil, ErrSongbookNotFound
	}
	sb, _, err := s.Authorize(ctx, owner.ID, songbookID, ActionManageAccess)
	if err != nil {
		return nil, err
	}
	if !canEdit && !canHost {
		return nil, ErrPermissionRequired
	}
	normEmail, err := validateInvitationEmail(recipientEmail, owner.Email)
	if err != nil {
		return nil, err
	}
	rawToken, tokenHash, err := generateInvitationToken()
	if err != nil {
		return nil, err
	}
	inv, err := s.createPendingInvitationWithRetry(ctx, sb.ID, owner.ID, normEmail, tokenHash, canEdit, canHost)
	if err != nil {
		return nil, err
	}
	if err := s.deliverInvitationEmail(ctx, inv, sb.Name, owner.DisplayName, rawToken, tokenHash, baseURL); err != nil {
		return nil, err
	}
	return inv, nil
}

func validateInvitationEmail(recipientEmail, ownerEmail string) (string, error) {
	normEmail := normalizeEmail(recipientEmail)
	if normEmail == "" || !strings.Contains(normEmail, "@") || strings.HasPrefix(normEmail, "@") || strings.HasSuffix(normEmail, "@") {
		return "", ErrInvalidEmail
	}
	if normEmail == normalizeEmail(ownerEmail) {
		return "", ErrCannotInviteOwner
	}
	return normEmail, nil
}

func (s *Service) invitationTTL() time.Duration {
	if s.InvitationTTL > 0 {
		return s.InvitationTTL
	}
	return DefaultInvitationTTL
}

func (s *Service) ensureNotCollaborator(ctx context.Context, songbookID, normEmail string) error {
	if s.Auth == nil {
		return nil
	}
	existingUser, uErr := s.Auth.GetUserByEmail(ctx, normEmail)
	if uErr != nil || existingUser == nil || existingUser.Deleting {
		return nil
	}
	if _, gErr := s.Access.GetSongbookGrant(ctx, songbookID, existingUser.ID); gErr == nil {
		return ErrAlreadyCollaborator
	}
	return nil
}

func (s *Service) pendingInvitationIDsForEmail(ctx context.Context, songbookID, normEmail string) ([]string, error) {
	existingInvites, err := s.Access.ListSongbookInvitationsBySongbook(ctx, songbookID)
	if err != nil {
		return nil, err
	}
	var replaceIDs []string
	for _, existing := range existingInvites {
		if existing.Status == models.InvitationStatusPending && normalizeEmail(existing.RecipientEmail) == normEmail {
			replaceIDs = append(replaceIDs, existing.ID)
		}
	}
	return replaceIDs, nil
}

func (s *Service) createPendingInvitationWithRetry(ctx context.Context, songbookID, ownerID, normEmail, tokenHash string, canEdit, canHost bool) (*models.SongbookInvitation, error) {
	ttl := s.invitationTTL()
	const maxConflictAttempts = 5
	for attempt := 0; attempt < maxConflictAttempts; attempt++ {
		if err := s.ensureNotCollaborator(ctx, songbookID, normEmail); err != nil {
			return nil, err
		}
		replaceIDs, err := s.pendingInvitationIDsForEmail(ctx, songbookID, normEmail)
		if err != nil {
			return nil, err
		}
		now := s.now()
		candidate := &models.SongbookInvitation{
			ID:             uuid.NewString(),
			SongbookID:     songbookID,
			OwnerID:        ownerID,
			RecipientEmail: normEmail,
			CanEditCatalog: canEdit,
			CanHostShows:   canHost,
			TokenHash:      tokenHash,
			Status:         models.InvitationStatusPending,
			ExpiresAt:      now.Add(ttl),
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		createErr := s.Access.CreateSongbookInvitation(ctx, candidate, replaceIDs)
		if createErr == nil {
			return candidate, nil
		}
		if !errors.Is(createErr, store.ErrInvitationConflict) {
			return nil, createErr
		}
	}
	return nil, store.ErrInvitationConflict
}

// ResendInvitation issues a fresh single-use link (invalidating the previous one) and sends the email.
func (s *Service) ResendInvitation(ctx context.Context, owner *models.User, songbookID, invitationID, baseURL string) (*models.SongbookInvitation, error) {
	if s.Access == nil {
		return nil, fmt.Errorf("songbook access store unavailable")
	}
	if owner == nil {
		return nil, ErrSongbookNotFound
	}
	sb, _, err := s.Authorize(ctx, owner.ID, songbookID, ActionManageAccess)
	if err != nil {
		return nil, err
	}
	rawToken, tokenHash, err := generateInvitationToken()
	if err != nil {
		return nil, err
	}
	ttl := s.InvitationTTL
	if ttl <= 0 {
		ttl = DefaultInvitationTTL
	}
	inv, err := s.Access.RotateSongbookInvitationToken(ctx, songbookID, invitationID, tokenHash, s.now().Add(ttl))
	if err != nil {
		return nil, err
	}
	if err := s.deliverInvitationEmail(ctx, inv, sb.Name, owner.DisplayName, rawToken, tokenHash, baseURL); err != nil {
		return nil, err
	}
	return inv, nil
}

func (s *Service) deliverInvitationEmail(ctx context.Context, inv *models.SongbookInvitation, bookName, inviterName, rawToken, tokenHash, baseURL string) error {
	sender := s.EmailSender
	if sender == nil {
		sender = email.NewLogSender()
	}
	acceptURL := strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/invitations/accept?token=" + url.QueryEscape(rawToken)
	deliveryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	err := sender.SendSongbookInvitationEmail(deliveryCtx, email.SongbookInvitationEmail{
		ToEmail:        inv.RecipientEmail,
		SongbookName:   bookName,
		InviterName:    inviterName,
		CanEditCatalog: inv.CanEditCatalog,
		CanHostShows:   inv.CanHostShows,
		ExpiresAt:      inv.ExpiresAt,
		AcceptURL:      acceptURL,
	})
	if err != nil {
		persistErr := s.Access.RecordSongbookInvitationDelivery(ctx, inv.ID, tokenHash, nil, err.Error())
		if errors.Is(persistErr, store.ErrInvitationDeliverySuperseded) {
			latest, getErr := s.Access.GetSongbookInvitation(ctx, inv.ID)
			if getErr != nil {
				return fmt.Errorf("reload superseded invitation: %w", getErr)
			}
			*inv = *latest
			return nil
		}
		if persistErr != nil {
			return fmt.Errorf("record invitation delivery failure: %w", persistErr)
		}
		inv.SendError = err.Error()
		inv.LastSentAt = nil
		return nil
	}
	sentAt := s.now()
	persistErr := s.Access.RecordSongbookInvitationDelivery(ctx, inv.ID, tokenHash, &sentAt, "")
	if errors.Is(persistErr, store.ErrInvitationDeliverySuperseded) {
		latest, getErr := s.Access.GetSongbookInvitation(ctx, inv.ID)
		if getErr != nil {
			return fmt.Errorf("reload superseded invitation: %w", getErr)
		}
		*inv = *latest
		return nil
	}
	if persistErr != nil {
		return fmt.Errorf("record invitation delivery status: %w", persistErr)
	}
	inv.LastSentAt = &sentAt
	inv.SendError = ""
	return nil
}

// CancelInvitation cancels a pending invitation and invalidates its link.
func (s *Service) CancelInvitation(ctx context.Context, ownerID, songbookID, invitationID string) error {
	if s.Access == nil {
		return fmt.Errorf("songbook access store unavailable")
	}
	if _, _, err := s.Authorize(ctx, ownerID, songbookID, ActionManageAccess); err != nil {
		return err
	}
	return s.Access.CancelSongbookInvitation(ctx, songbookID, invitationID)
}

// UpdateMember updates permissions on an accepted collaborator grant.
func (s *Service) UpdateMember(ctx context.Context, ownerID, songbookID, memberUserID string, canEdit, canHost bool) (*models.SongbookGrant, error) {
	if s.Access == nil {
		return nil, fmt.Errorf("songbook access store unavailable")
	}
	if _, _, err := s.Authorize(ctx, ownerID, songbookID, ActionManageAccess); err != nil {
		return nil, err
	}
	if !canEdit && !canHost {
		return nil, ErrPermissionRequired
	}
	if memberUserID == ownerID {
		return nil, fmt.Errorf("cannot modify owner permissions: %w", os.ErrInvalid)
	}
	grant := &models.SongbookGrant{
		SongbookID:     songbookID,
		UserID:         memberUserID,
		CanEditCatalog: canEdit,
		CanHostShows:   canHost,
		GrantedBy:      ownerID,
	}
	if err := s.Access.UpdateSongbookGrant(ctx, grant); err != nil {
		return nil, err
	}
	return s.Access.GetSongbookGrant(ctx, songbookID, memberUserID)
}

// RemoveMember revokes an accepted collaborator's access to a songbook.
func (s *Service) RemoveMember(ctx context.Context, ownerID, songbookID, memberUserID string) error {
	if s.Access == nil {
		return fmt.Errorf("songbook access store unavailable")
	}
	if _, _, err := s.Authorize(ctx, ownerID, songbookID, ActionManageAccess); err != nil {
		return err
	}
	if memberUserID == ownerID {
		return fmt.Errorf("cannot remove songbook owner: %w", os.ErrInvalid)
	}
	return s.Access.DeleteSongbookGrant(ctx, songbookID, memberUserID)
}

// PreviewInvitation resolves recipient-facing invitation metadata without consuming the link.
func (s *Service) PreviewInvitation(ctx context.Context, rawToken string, currentUser *models.User) (*models.SongbookInvitationPreview, error) {
	if s.Access == nil {
		return nil, fmt.Errorf("songbook access store unavailable")
	}
	tokenHash := HashInvitationToken(rawToken)
	inv, err := s.Access.GetSongbookInvitationByTokenHash(ctx, tokenHash)
	if err != nil {
		return nil, err
	}
	sb, sbErr := s.Songbooks.GetSongbook(ctx, inv.SongbookID)
	if sbErr != nil || sb == nil || sb.Deleting {
		return nil, store.ErrInvitationInvalid
	}
	inviterName := "A MicUp host"
	if s.Auth != nil && inv.OwnerID != "" {
		if owner, uErr := s.Auth.GetUser(ctx, inv.OwnerID); uErr == nil && owner != nil && strings.TrimSpace(owner.DisplayName) != "" {
			inviterName = owner.DisplayName
		}
	}
	now := s.now()
	preview := &models.SongbookInvitationPreview{
		SongbookID:     inv.SongbookID,
		SongbookName:   sb.Name,
		InviterName:    inviterName,
		RecipientEmail: inv.RecipientEmail,
		CanEditCatalog: inv.CanEditCatalog,
		CanHostShows:   inv.CanHostShows,
		Status:         inv.Status,
		ExpiresAt:      inv.ExpiresAt,
		Expired:        !inv.ExpiresAt.After(now),
	}
	if currentUser != nil {
		preview.Authenticated = true
		preview.EmailMatches = normalizeEmail(currentUser.Email) == normalizeEmail(inv.RecipientEmail)
		preview.EmailVerified = currentUser.EmailVerified
		preview.IsHost = currentUser.IsHost
	}
	return preview, nil
}

// AcceptInvitation validates recipient eligibility and atomically consumes the invitation
// and creates the collaborator grant in one transaction.
func (s *Service) AcceptInvitation(ctx context.Context, rawToken string, user *models.User) (*models.SongbookGrant, *models.SongbookInvitation, error) {
	if s.Access == nil {
		return nil, nil, fmt.Errorf("songbook access store unavailable")
	}
	if user == nil || user.Deleting {
		return nil, nil, store.ErrPermissionDenied
	}
	if !user.IsHost {
		return nil, nil, ErrHostRequired
	}
	if !user.EmailVerified {
		return nil, nil, ErrEmailNotVerified
	}
	tokenHash := HashInvitationToken(rawToken)
	return s.Access.AcceptSongbookInvitation(ctx, tokenHash, user.ID, normalizeEmail(user.Email), s.now())
}
