package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"micup.live/server/internal/models"
)

func (h *AuthHandler) publicBaseURL(r *http.Request) string {
	if h.PublicBaseURL != "" {
		return strings.TrimRight(h.PublicBaseURL, "/")
	}
	// Local development has no configured public URL. Production must set one.
	proto := "https"
	if r.TLS == nil {
		if forwardedProto := r.Header.Get("X-Forwarded-Proto"); forwardedProto != "" {
			proto = forwardedProto
		} else if strings.HasPrefix(r.Host, "localhost") || strings.HasPrefix(r.Host, "127.0.0.1") {
			proto = "http"
		}
	}
	host := r.Host
	if host == "" {
		host = "micup.live"
	}
	return fmt.Sprintf("%s://%s", proto, host)
}

func (h *AuthHandler) setRefreshCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{Name: "micup_refresh", Value: token, Path: "/api/v1/auth",
		MaxAge: int(refreshTokenLifetime.Seconds()), HttpOnly: true, Secure: h.CookieSecure, SameSite: http.SameSiteLaxMode})
}

func (h *AuthHandler) clearRefreshCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: "micup_refresh", Value: "", Path: "/api/v1/auth",
		MaxAge: -1, HttpOnly: true, Secure: h.CookieSecure, SameSite: http.SameSiteLaxMode})
}

// SendVerificationEmailSafe generates a token and delivers the verification email with a short timeout.
// Running synchronously ensures execution completes in serverless runtimes (AWS Lambda) before the container freezes.
func (h *AuthHandler) SendVerificationEmailSafe(baseURL string, user *models.User) {
	if h == nil || h.EmailSender == nil || user == nil || user.Email == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	token, err := h.newVerificationToken(user.ID, user.Email)
	if err != nil {
		log.Printf("[AUTH ERROR] failed to generate verification token for %s: %v", user.Email, err)
		return
	}
	verifyURL := fmt.Sprintf("%s/verify-email?token=%s", baseURL, token)
	if err := h.EmailSender.SendVerificationEmail(ctx, user.Email, user.DisplayName, verifyURL); err != nil {
		log.Printf("[EMAIL WARN] failed to send verification email to %s: %v", user.Email, err)
	} else {
		log.Printf("[AUTH] Verification email sent to %s", user.Email)
	}
}

func newRefreshToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func hashToken(token string) []byte {
	digest := sha256.Sum256([]byte(token))
	return digest[:]
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func writeAuthJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}

func writeAuthError(w http.ResponseWriter, status int, detail string) {
	if status >= 500 {
		log.Printf("[AUTH ERROR] %d %s", status, detail)
	} else if status >= 400 {
		log.Printf("[AUTH WARN]  %d %s", status, detail)
	}
	writeAuthJSON(w, status, map[string]string{"detail": detail})
}
