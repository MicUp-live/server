package dynamo

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
	"github.com/google/uuid"

	"micup.live/server/internal/app"
	"micup.live/server/internal/config"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

type passkeyLoginStore struct {
	*Store
	afterCredentialRead func()
	writeError          error
	sessionWrites       int
}

func (s *passkeyLoginStore) ListPasskeyCredentialsForUser(ctx context.Context, userID string) ([]*models.PasskeyCredential, error) {
	stored, err := s.Store.ListPasskeyCredentialsForUser(ctx, userID)
	if err == nil && s.afterCredentialRead != nil {
		s.afterCredentialRead()
	}
	return stored, err
}

func (s *passkeyLoginStore) RecordPasskeyUse(ctx context.Context, observed *models.PasskeyCredential, usage store.PasskeyUsage) error {
	if s.writeError != nil {
		return s.writeError
	}
	return s.Store.RecordPasskeyUse(ctx, observed, usage)
}

func (s *passkeyLoginStore) CreateAuthSession(ctx context.Context, session *models.AuthSession) error {
	s.sessionWrites++
	return s.Store.CreateAuthSession(ctx, session)
}

func passkeyAssertion(t *testing.T, private ed25519.PrivateKey, rawID []byte, userID, challenge string) protocol.CredentialAssertionResponse {
	t.Helper()
	client, err := json.Marshal(protocol.CollectedClientData{Type: protocol.AssertCeremony, Challenge: challenge, Origin: "http://localhost:3000"})
	if err != nil {
		t.Fatal(err)
	}
	rp := sha256.Sum256([]byte("localhost"))
	data := append(rp[:], byte(protocol.FlagUserPresent|protocol.FlagUserVerified|protocol.FlagBackupEligible|protocol.FlagBackupState))
	data = binary.BigEndian.AppendUint32(data, 7)
	hash := sha256.Sum256(client)
	return protocol.CredentialAssertionResponse{
		PublicKeyCredential: protocol.PublicKeyCredential{
			Credential: protocol.Credential{ID: base64.RawURLEncoding.EncodeToString(rawID), Type: "public-key"},
			RawID:      rawID,
		},
		AssertionResponse: protocol.AuthenticatorAssertionResponse{
			AuthenticatorResponse: protocol.AuthenticatorResponse{ClientDataJSON: client},
			AuthenticatorData:     data, UserHandle: []byte(userID),
			Signature: ed25519.Sign(private, append(data, hash[:]...)),
		},
	}
}

func TestDynamoHTTPPasskeyLoginCommitBoundary(t *testing.T) {
	t.Setenv("WEBAUTHN_RP_ID", "localhost")
	t.Setenv("WEBAUTHN_ORIGINS", "http://localhost:3000")
	s := newLocalStore(t)
	ctx := context.Background()
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := webauthncbor.Marshal(webauthncose.OKPPublicKeyData{
		PublicKeyData: webauthncose.PublicKeyData{KeyType: int64(webauthncose.OctetKey), Algorithm: int64(webauthncose.AlgEdDSA)},
		Curve:         int64(webauthncose.Ed25519), XCoord: pub,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"discoverable", "email"} {
		for _, tc := range []struct {
			name   string
			status int
		}{
			{"verified", http.StatusOK},
			{"revoked after read", http.StatusUnauthorized},
			{"invalid assertion", http.StatusUnauthorized},
			{"write failure", http.StatusInternalServerError},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				fixture := &passkeyLoginStore{Store: s}
				router, err := app.NewRouter(config.Config{Environment: config.Development, AuthSecret: resetTestSecret}, app.Dependencies{Auth: fixture, Passkeys: fixture})
				if err != nil {
					t.Fatal(err)
				}
				user := &models.User{ID: uuid.NewString(), Email: uuid.NewString() + "@example.com", DisplayName: "Test singer"}
				rawID := uuid.New()
				// An existing padded credential must retain its original storage key.
				cred := &models.PasskeyCredential{ID: base64.URLEncoding.EncodeToString(rawID[:]), UserID: user.ID, PublicKey: key, FriendlyName: "Phone"}
				if err := s.CreateUserWithPasskey(ctx, user, cred); err != nil {
					t.Fatal(err)
				}

				options := map[string]string{}
				if mode == "email" {
					options["email"] = user.Email
				}
				response := requestJSON(t, router, http.MethodPost, "/api/v1/auth/passkey/login-options", options, "")
				if response.Code != http.StatusOK {
					t.Fatalf("login options: %d %s", response.Code, response.Body.String())
				}
				var ceremony struct {
					SessionID string                       `json:"session_id"`
					Options   protocol.CredentialAssertion `json:"options"`
				}
				decodeResponse(t, response, &ceremony)
				challenge := base64.RawURLEncoding.EncodeToString(ceremony.Options.Response.Challenge)
				assertion := passkeyAssertion(t, private, rawID[:], user.ID, challenge)
				switch tc.name {
				case "revoked after read":
					fixture.afterCredentialRead = func() {
						if err := s.DeletePasskeyCredential(ctx, cred.ID, user.ID); err != nil {
							t.Fatal(err)
						}
					}
				case "invalid assertion":
					assertion.AssertionResponse.Signature[0] ^= 1
				case "write failure":
					fixture.writeError = errors.New("injected credential write failure")
				}
				response = requestJSON(t, router, http.MethodPost, "/api/v1/auth/passkey/login-verify?session_id="+ceremony.SessionID, assertion, "")
				if response.Code != tc.status {
					t.Fatalf("login verify: %d %s, want %d", response.Code, response.Body.String(), tc.status)
				}
				got, err := s.GetPasskeyCredential(ctx, cred.ID)
				if tc.status == http.StatusOK {
					if err != nil || got.ID != cred.ID || got.SignCount != 7 || got.LastUsedAt.IsZero() || !got.BackupEligible || !got.BackupState || got.FriendlyName != cred.FriendlyName || !bytes.Equal(got.PublicKey, key) {
						t.Fatalf("verified credential: %+v, %v", got, err)
					}
					if fixture.sessionWrites != 1 || response.Header().Get("Set-Cookie") == "" || !bytes.Contains(response.Body.Bytes(), []byte(`"access_token"`)) {
						t.Fatalf("expected a session, got %d writes and %s", fixture.sessionWrites, response.Body.String())
					}
					return
				}
				if fixture.sessionWrites != 0 || response.Header().Get("Set-Cookie") != "" || bytes.Contains(response.Body.Bytes(), []byte(`"access_token"`)) {
					t.Fatal("rejected ceremony issued a session")
				}
				if tc.name == "revoked after read" {
					if !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("revoked credential still present: %+v, %v", got, err)
					}
				} else if err != nil || got.BackupEligible || got.BackupState || got.SignCount != 0 || !got.LastUsedAt.IsZero() {
					t.Fatalf("rejected ceremony changed credential: %+v, %v", got, err)
				}
			})
		}
	}
}

func TestDynamoPasskeyUseFencesChangedCredentialAndAccount(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	user := resetUser(t, s)
	other := resetUser(t, s)
	cred := &models.PasskeyCredential{ID: uuid.NewString(), UserID: user.ID, PublicKey: []byte("fixture key")}
	if err := s.SavePasskeyCredential(ctx, cred); err != nil {
		t.Fatal(err)
	}
	// Legacy rows can predate the assertion metadata fields.
	if _, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.tables.passkeys), Key: stringKey("id", cred.ID),
		UpdateExpression: aws.String("REMOVE sign_count, backup_eligible"),
	}); err != nil {
		t.Fatal(err)
	}
	first := store.PasskeyUsage{SignCount: 5, BackupEligible: true}
	if err := s.RecordPasskeyUse(ctx, cred, first); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetPasskeyCredential(ctx, cred.ID)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		change func(*models.PasskeyCredential)
	}{
		{"another owner", func(c *models.PasskeyCredential) { c.UserID = other.ID }},
		{"another public key", func(c *models.PasskeyCredential) { c.PublicKey = []byte("different key") }},
		{"stale counter", func(c *models.PasskeyCredential) { c.SignCount = 0 }},
		{"stale eligibility", func(c *models.PasskeyCredential) { c.BackupEligible = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observed := *current
			tc.change(&observed)
			if err := s.RecordPasskeyUse(ctx, &observed, store.PasskeyUsage{}); !errors.Is(err, store.ErrPasskeyChanged) {
				t.Fatalf("record stale assertion: %v", err)
			}
		})
	}
	if _, err := s.BeginAccountDeletion(ctx, user.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordPasskeyUse(ctx, current, store.PasskeyUsage{}); !errors.Is(err, store.ErrAccountDeleting) {
		t.Fatalf("record assertion for deleting account: %v", err)
	}
	after, err := s.GetPasskeyCredential(ctx, cred.ID)
	if err != nil || after.SignCount != first.SignCount || !after.BackupEligible || !after.LastUsedAt.Equal(current.LastUsedAt) {
		t.Fatalf("failed assertions changed credential: %+v, %v", after, err)
	}
}
