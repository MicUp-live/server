package queue

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
)

type cancelClaims struct {
	RequestID string `json:"r"`
	EventID   string `json:"e"`
}

// GenerateCancelToken issues a cryptographic HMAC-SHA256 cancellation token for a request.
func GenerateCancelToken(secret []byte, requestID, eventID string) string {
	if len(secret) == 0 {
		secret = []byte("micup-production-queue-cancellation-secret-key-32b")
	}
	payload, _ := json.Marshal(cancelClaims{RequestID: requestID, EventID: eventID})
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// ValidateCancelToken validates an HMAC-SHA256 cancellation token against a request and event.
func ValidateCancelToken(secret []byte, token, requestID, eventID string) bool {
	if len(secret) == 0 {
		secret = []byte("micup-production-queue-cancellation-secret-key-32b")
	}
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 2 {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(parts[0]))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return false
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	var claims cancelClaims
	if err := json.Unmarshal(data, &claims); err != nil {
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
