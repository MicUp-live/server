package email

import (
	"strings"
	"testing"
	"time"
)

func TestBuildVerificationEmail_EscapesHTML(t *testing.T) {
	maliciousName := `<script>alert("xss")</script>`
	maliciousURL := `https://micup.live/verify?token=abc&evil="><img src=x onerror=alert(1)>`

	subject, textBody, htmlBody := buildVerificationEmail(maliciousName, maliciousURL)

	if !strings.Contains(subject, "Verify your email address") {
		t.Fatalf("unexpected subject: %s", subject)
	}

	// Plaintext should retain raw values
	if !strings.Contains(textBody, maliciousName) {
		t.Errorf("plaintext body should contain raw name, got: %s", textBody)
	}
	if !strings.Contains(textBody, maliciousURL) {
		t.Errorf("plaintext body should contain raw URL, got: %s", textBody)
	}

	// HTML body MUST escape dangerous characters
	if strings.Contains(htmlBody, "<script>") {
		t.Errorf("html body contains unescaped <script> tag: %s", htmlBody)
	}
	if !strings.Contains(htmlBody, "&lt;script&gt;alert(&#34;xss&#34;)&lt;/script&gt;") {
		t.Errorf("html body missing escaped name: %s", htmlBody)
	}
	if strings.Contains(htmlBody, `onerror=alert(1)>`) {
		t.Errorf("html body contains unescaped attributes/tags from url: %s", htmlBody)
	}
	if !strings.Contains(htmlBody, `&amp;evil=&#34;&gt;&lt;img src=x onerror=alert(1)&gt;`) {
		t.Errorf("html body missing escaped URL: %s", htmlBody)
	}
}

func TestBuildPasswordResetEmail_EscapesHTML(t *testing.T) {
	maliciousName := `<b onmouseover="alert('pwn')">Singer</b>`
	maliciousURL := `https://micup.live/reset?token=123&test='><script>`

	subject, textBody, htmlBody := buildPasswordResetEmail(maliciousName, maliciousURL)

	if !strings.Contains(subject, "Reset your MicUp.live password") {
		t.Fatalf("unexpected subject: %s", subject)
	}

	// Plaintext should retain raw values
	if !strings.Contains(textBody, maliciousName) {
		t.Errorf("plaintext body should contain raw name, got: %s", textBody)
	}
	if !strings.Contains(textBody, maliciousURL) {
		t.Errorf("plaintext body should contain raw URL, got: %s", textBody)
	}

	// HTML body MUST escape
	if strings.Contains(htmlBody, `<b onmouseover=`) {
		t.Errorf("html body contains unescaped html tag: %s", htmlBody)
	}
	if !strings.Contains(htmlBody, `&lt;b onmouseover=&#34;alert(&#39;pwn&#39;)&#34;&gt;Singer&lt;/b&gt;`) {
		t.Errorf("html body missing escaped name: %s", htmlBody)
	}
	if strings.Contains(htmlBody, `<script>`) {
		t.Errorf("html body contains unescaped script tag: %s", htmlBody)
	}
}

func TestBuildSongbookInvitationEmail_EscapesHTMLAndIncludesDetails(t *testing.T) {
	exp := time.Date(2026, 12, 1, 18, 30, 0, 0, time.UTC)
	msg := SongbookInvitationEmail{
		ToEmail:        "collab@example.com",
		SongbookName:   `<script>Rock & Roll</script>`,
		InviterName:    `<b>DJ Alex</b>`,
		CanEditCatalog: true,
		CanHostShows:   true,
		ExpiresAt:      exp,
		AcceptURL:      `https://micup.live/invitations/accept?token=tok123&x="><img src=x>`,
	}
	subject, textBody, htmlBody := buildSongbookInvitationEmail(msg)
	if !strings.Contains(subject, msg.InviterName) || !strings.Contains(subject, msg.SongbookName) {
		t.Fatalf("subject missing inviter or songbook: %s", subject)
	}
	for _, want := range []string{msg.InviterName, msg.SongbookName, "Edit catalog", "Host shows", "Dec 1, 2026", msg.AcceptURL} {
		if !strings.Contains(textBody, want) {
			t.Errorf("textBody missing %q: %s", want, textBody)
		}
	}
	if strings.Contains(htmlBody, "<script>") || strings.Contains(htmlBody, "<b>DJ Alex</b>") || strings.Contains(htmlBody, `<img src=x>`) {
		t.Errorf("htmlBody contains unescaped HTML: %s", htmlBody)
	}
	if !strings.Contains(htmlBody, "&lt;script&gt;Rock &amp; Roll&lt;/script&gt;") {
		t.Errorf("htmlBody missing escaped songbook name: %s", htmlBody)
	}
}
