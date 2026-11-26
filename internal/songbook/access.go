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
	if s.Auth != nil && sb.OwnerID != "" {
		if owner, uErr := s.Auth.GetUser(ctx, sb.OwnerID); uErr == nil && owner != nil {
			sb.OwnerDisplayName = owner.DisplayName
		}
	}
	return sb, perms, nil
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
	if s.Access != nil {
		grants, err := s.Access.ListSongbookGrantsByUser(ctx, actorID)
		if err != nil {
			return nil, err
		}
		ownerNames := make(map[string]string)
		for _, g := range grants {
			if _, ok := seen[g.SongbookID]; ok {
				continue
			}
			if !g.CanEditCatalog && !g.CanHostShows {
				continue
			}
			sb, sbErr := s.Songbooks.GetSongbook(ctx, g.SongbookID)
			if sbErr != nil || sb == nil || sb.Deleting {
				continue
			}
			sb.Permissions = grantPermissions(g)
			if s.Auth != nil && sb.OwnerID != "" {
				if name, ok := ownerNames[sb.OwnerID]; ok {
					sb.OwnerDisplayName = name
				} else if owner, uErr := s.Auth.GetUser(ctx, sb.OwnerID); uErr == nil && owner != nil {
					ownerNames[sb.OwnerID] = owner.DisplayName
					sb.OwnerDisplayName = owner.DisplayName
				}
			}
			seen[sb.ID] = struct{}{}
			result = append(result, sb)
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
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
	normEmail := normalizeEmail(recipientEmail)
	if normEmail == "" || !strings.Contains(normEmail, "@") || strings.HasPrefix(normEmail, "@") || strings.HasSuffix(normEmail, "@") {
		return nil, ErrInvalidEmail
	}
	if normEmail == normalizeEmail(owner.Email) {
		return nil, ErrCannotInviteOwner
	}
	rawToken, tokenHash, err := generateInvitationToken()
	if err != nil {
		return nil, err
	}
	ttl := s.InvitationTTL
	if ttl <= 0 {
		ttl = DefaultInvitationTTL
	}
	const maxConflictAttempts = 5
	var inv *models.SongbookInvitation
	for attempt := 0; attempt < maxConflictAttempts; attempt++ {
		if s.Auth != nil {
			if existingUser, uErr := s.Auth.GetUserByEmail(ctx, normEmail); uErr == nil && existingUser != nil && !existingUser.Deleting {
				if _, gErr := s.Access.GetSongbookGrant(ctx, songbookID, existingUser.ID); gErr == nil {
					return nil, ErrAlreadyCollaborator
				}
			}
		}
		existingInvites, listErr := s.Access.ListSongbookInvitationsBySongbook(ctx, songbookID)
		if listErr != nil {
			return nil, listErr
		}
		var replaceIDs []string
		for _, existing := range existingInvites {
			if existing.Status == models.InvitationStatusPending && normalizeEmail(existing.RecipientEmail) == normEmail {
				replaceIDs = append(replaceIDs, existing.ID)
			}
		}
		now := s.now()
		candidate := &models.SongbookInvitation{
			ID:             uuid.NewString(),
			SongbookID:     sb.ID,
			OwnerID:        owner.ID,
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
			inv = candidate
			break
		}
		if errors.Is(createErr, store.ErrInvitationConflict) {
			continue
		}
		return nil, createErr
	}
	if inv == nil {
		return nil, store.ErrInvitationConflict
	}

	if err := s.deliverInvitationEmail(ctx, inv, sb.Name, owner.DisplayName, rawToken, tokenHash, baseURL); err != nil {
		return nil, err
	}
	return inv, nil
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
