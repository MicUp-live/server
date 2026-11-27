package queue

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
)

const (
	tokenPurposeCancel = "cancel"
	tokenPurposeEdit   = "edit"
)

type requestTokenClaims struct {
	RequestID string `json:"r"`
	EventID   string `json:"e"`
	Purpose   string `json:"p,omitempty"`
}

func defaultTokenSecret(secret []byte) []byte {
	if len(secret) == 0 {
		return []byte("micup-production-queue-cancellation-secret-key-32b")
	}
	return secret
}

func signRequestToken(secret []byte, claims requestTokenClaims) string {
	secret = defaultTokenSecret(secret)
	payload, _ := json.Marshal(claims)
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func parseRequestToken(secret []byte, token string) (*requestTokenClaims, bool) {
	secret = defaultTokenSecret(secret)
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 2 {
		return nil, false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(parts[0]))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return nil, false
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, false
	}
	var claims requestTokenClaims
	if err := json.Unmarshal(data, &claims); err != nil {
		return nil, false
	}
	return &claims, true
}

// GenerateCancelToken issues a cryptographic HMAC-SHA256 cancellation token for a request.
func GenerateCancelToken(secret []byte, requestID, eventID string) string {
	return signRequestToken(secret, requestTokenClaims{
		RequestID: requestID,
		EventID:   eventID,
	})
}

// ValidateCancelToken validates an HMAC-SHA256 cancellation token against a request and event.
// Edit tokens (purpose == "edit") are explicitly rejected.
func ValidateCancelToken(secret []byte, token, requestID, eventID string) bool {
	claims, ok := parseRequestToken(secret, token)
	if !ok {
		return false
	}
	if claims.Purpose != "" && claims.Purpose != tokenPurposeCancel {
		return false
	}
	if claims.RequestID != requestID {
		return false
	}
	if eventID != "" && claims.EventID != "" && claims.EventID != eventID {
		return false
	}
	return true
}

// GenerateEditToken issues a domain-separated HMAC-SHA256 capability token for
// replacing a request's song before the singer is called to stage.
func GenerateEditToken(secret []byte, requestID, eventID string) string {
	return signRequestToken(secret, requestTokenClaims{
		RequestID: requestID,
		EventID:   eventID,
		Purpose:   tokenPurposeEdit,
	})
}

// ValidateEditToken validates a domain-separated edit capability token against a request and event.
// Cancellation tokens cannot satisfy this check.
func ValidateEditToken(secret []byte, token, requestID, eventID string) bool {
	if requestID == "" || eventID == "" {
		return false
	}
	claims, ok := parseRequestToken(secret, token)
	if !ok {
		return false
	}
	if claims.Purpose != tokenPurposeEdit {
		return false
	}
	return claims.RequestID == requestID && claims.EventID == eventID
}
