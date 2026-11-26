// Package email provides transactional email deliverability via AWS SES with local dev fallback.
package email

import (
	"context"
	"fmt"
	"html"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

// SongbookInvitationEmail contains the details needed to deliver a songbook collaboration invitation.
type SongbookInvitationEmail struct {
	ToEmail        string
	SongbookName   string
	InviterName    string
	CanEditCatalog bool
	CanHostShows   bool
	ExpiresAt      time.Time
	AcceptURL      string
}

// Sender sends transactional emails (verification, notifications, password reset, invitations).
type Sender interface {
	SendVerificationEmail(ctx context.Context, toEmail, displayName, verifyURL string) error
	SendPasswordResetEmail(ctx context.Context, toEmail, displayName, resetURL string) error
	SendSongbookInvitationEmail(ctx context.Context, msg SongbookInvitationEmail) error
}

// LogSender logs emails to stdout (used in development, CI, and test environments).
type LogSender struct{}

// NewLogSender returns an email sender that writes to standard log output.
func NewLogSender() *LogSender {
	return &LogSender{}
}

func (s *LogSender) SendVerificationEmail(_ context.Context, toEmail, displayName, verifyURL string) error {
	log.Printf("[EMAIL] Verification link for %s <%s>: %s", displayName, toEmail, verifyURL)
	return nil
}

func (s *LogSender) SendPasswordResetEmail(_ context.Context, toEmail, displayName, resetURL string) error {
	log.Printf("[EMAIL] Password reset link for %s <%s>: %s", displayName, toEmail, resetURL)
	return nil
}

func (s *LogSender) SendSongbookInvitationEmail(_ context.Context, msg SongbookInvitationEmail) error {
	log.Printf("[EMAIL] Songbook invitation for %s (%s by %s, edit=%v, host=%v, expires=%s): %s",
		msg.ToEmail, msg.SongbookName, msg.InviterName, msg.CanEditCatalog, msg.CanHostShows, msg.ExpiresAt.UTC().Format(time.RFC3339), msg.AcceptURL)
	return nil
}

// SESSender sends emails via AWS SESv2.
type SESSender struct {
	client    *sesv2.Client
	fromEmail string
}

// NewSESSender initializes an AWS SESv2 email sender.
func NewSESSender(client *sesv2.Client, fromEmail string) *SESSender {
	if fromEmail == "" {
		fromEmail = "MicUp.live <noreply@micup.live>"
	}
	return &SESSender{
		client:    client,
		fromEmail: fromEmail,
	}
}

func (s *SESSender) SendVerificationEmail(ctx context.Context, toEmail, displayName, verifyURL string) error {
	subject, textBody, htmlBody := buildVerificationEmail(displayName, verifyURL)

	input := &sesv2.SendEmailInput{
		FromEmailAddress: aws.String(s.fromEmail),
		Destination: &types.Destination{
			ToAddresses: []string{toEmail},
		},
		Content: &types.EmailContent{
			Simple: &types.Message{
				Subject: &types.Content{
					Data:    aws.String(subject),
					Charset: aws.String("UTF-8"),
				},
				Body: &types.Body{
					Html: &types.Content{
						Data:    aws.String(htmlBody),
						Charset: aws.String("UTF-8"),
					},
					Text: &types.Content{
						Data:    aws.String(textBody),
						Charset: aws.String("UTF-8"),
					},
				},
			},
		},
	}

	_, err := s.client.SendEmail(ctx, input)
	if err != nil {
		log.Printf("[EMAIL WARN] Failed to send verification email via SES to %s: %v", toEmail, err)
		return fmt.Errorf("send email via ses: %w", err)
	}

	log.Printf("[EMAIL] Successfully sent verification email to %s", toEmail)
	return nil
}

func (s *SESSender) SendPasswordResetEmail(ctx context.Context, toEmail, displayName, resetURL string) error {
	subject, textBody, htmlBody := buildPasswordResetEmail(displayName, resetURL)

	input := &sesv2.SendEmailInput{
		FromEmailAddress: aws.String(s.fromEmail),
		Destination: &types.Destination{
			ToAddresses: []string{toEmail},
		},
		Content: &types.EmailContent{
			Simple: &types.Message{
				Subject: &types.Content{
					Data:    aws.String(subject),
					Charset: aws.String("UTF-8"),
				},
				Body: &types.Body{
					Html: &types.Content{
						Data:    aws.String(htmlBody),
						Charset: aws.String("UTF-8"),
					},
					Text: &types.Content{
						Data:    aws.String(textBody),
						Charset: aws.String("UTF-8"),
					},
				},
			},
		},
	}

	_, err := s.client.SendEmail(ctx, input)
	if err != nil {
		log.Printf("[EMAIL WARN] Failed to send password reset email via SES to %s: %v", toEmail, err)
		return fmt.Errorf("send password reset email via ses: %w", err)
	}

	log.Printf("[EMAIL] Successfully sent password reset email to %s", toEmail)
	return nil
}

func (s *SESSender) SendSongbookInvitationEmail(ctx context.Context, msg SongbookInvitationEmail) error {
	subject, textBody, htmlBody := buildSongbookInvitationEmail(msg)

	input := &sesv2.SendEmailInput{
		FromEmailAddress: aws.String(s.fromEmail),
		Destination: &types.Destination{
			ToAddresses: []string{msg.ToEmail},
		},
		Content: &types.EmailContent{
			Simple: &types.Message{
				Subject: &types.Content{
					Data:    aws.String(subject),
					Charset: aws.String("UTF-8"),
				},
				Body: &types.Body{
					Html: &types.Content{
						Data:    aws.String(htmlBody),
						Charset: aws.String("UTF-8"),
					},
					Text: &types.Content{
						Data:    aws.String(textBody),
						Charset: aws.String("UTF-8"),
					},
				},
			},
		},
	}

	_, err := s.client.SendEmail(ctx, input)
	if err != nil {
		log.Printf("[EMAIL WARN] Failed to send songbook invitation email via SES to %s: %v", msg.ToEmail, err)
		return fmt.Errorf("send songbook invitation email via ses: %w", err)
	}

	log.Printf("[EMAIL] Successfully sent songbook invitation email to %s", msg.ToEmail)
	return nil
}

func buildVerificationEmail(displayName, verifyURL string) (subject, textBody, htmlBody string) {
	greeting := "there"
	if displayName != "" {
		greeting = displayName
	}

	subject = "Verify your email address — MicUp.live"
	textBody = fmt.Sprintf(
		"Hi %s,\n\nWelcome to MicUp.live! Please verify your email address by clicking the link below:\n\n%s\n\nThis verification link will expire in 24 hours.\n\nKeep on singing!\nThe MicUp Team\nhttps://micup.live",
		greeting, verifyURL,
	)

	safeGreeting := html.EscapeString(greeting)
	safeVerifyURL := html.EscapeString(verifyURL)

	htmlBody = fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <title>Verify your email</title>
</head>
<body style="margin:0;padding:0;background-color:#090a10;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;color:#e2e8f0;">
  <table width="100%%" border="0" cellspacing="0" cellpadding="0" style="background-color:#090a10;padding:40px 20px;">
    <tr>
      <td align="center">
        <table width="100%%" border="0" cellspacing="0" cellpadding="0" style="max-width:520px;background-color:#131520;border:1px solid #232738;border-radius:16px;overflow:hidden;box-shadow:0 20px 40px rgba(0,0,0,0.6);">
          <tr>
            <td style="padding:32px 32px 24px;text-align:center;background:linear-gradient(180deg, rgba(255,42,95,0.12) 0%%, transparent 100%%);border-bottom:1px solid #232738;">
              <h1 style="margin:0;font-size:24px;font-weight:800;letter-spacing:-0.02em;color:#ffffff;">
                🎤 MicUp<span style="color:#ff2a5f;">.live</span>
              </h1>
            </td>
          </tr>
          <tr>
            <td style="padding:32px;">
              <h2 style="margin:0 0 16px;font-size:20px;font-weight:700;color:#ffffff;">Verify your email address</h2>
              <p style="margin:0 0 20px;font-size:15px;line-height:1.6;color:#94a3b8;">
                Hi <strong style="color:#ffffff;">%s</strong>, welcome to MicUp! Please confirm your email address to secure your account and receive show notifications.
              </p>
              <table width="100%%" border="0" cellspacing="0" cellpadding="0" style="margin:28px 0;">
                <tr>
                  <td align="center">
                    <a href="%s" target="_blank" style="display:inline-block;padding:14px 32px;background:linear-gradient(135deg, #ff2a5f 0%%, #e01749 100%%);color:#ffffff;font-size:15px;font-weight:600;text-decoration:none;border-radius:10px;box-shadow:0 4px 15px rgba(255,42,95,0.4);">
                      Verify My Email
                    </a>
                  </td>
                </tr>
              </table>
              <p style="margin:0 0 8px;font-size:13px;line-height:1.5;color:#64748b;">
                Or copy and paste this link into your browser:
              </p>
              <p style="margin:0;font-size:12px;line-height:1.4;word-break:break-all;color:#ff2a5f;">
                %s
              </p>
              <div style="margin-top:32px;padding-top:20px;border-top:1px solid #232738;font-size:12px;color:#64748b;line-height:1.5;">
                This link expires in 24 hours. If you did not create a MicUp account, you can safely ignore this email.
              </div>
            </td>
          </tr>
        </table>
      </td>
    </tr>
  </table>
</body>
</html>`, safeGreeting, safeVerifyURL, safeVerifyURL)
	return subject, textBody, htmlBody
}

func buildPasswordResetEmail(displayName, resetURL string) (subject, textBody, htmlBody string) {
	greeting := "there"
	if displayName != "" {
		greeting = displayName
	}

	subject = "Reset your MicUp.live password"
	textBody = fmt.Sprintf(
		"Hi %s,\n\nWe received a request to reset your password for MicUp.live. Click the link below to choose a new password:\n\n%s\n\nThis link will expire in 1 hour.\n\nIf you did not request a password reset, you can safely ignore this email.\n\nKeep on singing!\nThe MicUp Team\nhttps://micup.live",
		greeting, resetURL,
	)

	safeGreeting := html.EscapeString(greeting)
	safeResetURL := html.EscapeString(resetURL)

	htmlBody = fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <title>Reset your password</title>
</head>
<body style="margin:0;padding:0;background-color:#090a10;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;color:#e2e8f0;">
  <table width="100%%" border="0" cellspacing="0" cellpadding="0" style="background-color:#090a10;padding:40px 20px;">
    <tr>
      <td align="center">
        <table width="100%%" border="0" cellspacing="0" cellpadding="0" style="max-width:520px;background-color:#131520;border:1px solid #232738;border-radius:16px;overflow:hidden;box-shadow:0 20px 40px rgba(0,0,0,0.6);">
          <tr>
            <td style="padding:32px 32px 24px;text-align:center;background:linear-gradient(180deg, rgba(255,42,95,0.12) 0%%, transparent 100%%);border-bottom:1px solid #232738;">
              <h1 style="margin:0;font-size:24px;font-weight:800;letter-spacing:-0.02em;color:#ffffff;">
                🎤 MicUp<span style="color:#ff2a5f;">.live</span>
              </h1>
            </td>
          </tr>
          <tr>
            <td style="padding:32px;">
              <h2 style="margin:0 0 16px;font-size:20px;font-weight:700;color:#ffffff;">Reset your password</h2>
              <p style="margin:0 0 20px;font-size:15px;line-height:1.6;color:#94a3b8;">
                Hi <strong style="color:#ffffff;">%s</strong>, we received a request to reset your password. Click the button below to choose a new password:
              </p>
              <table width="100%%" border="0" cellspacing="0" cellpadding="0" style="margin:28px 0;">
                <tr>
                  <td align="center">
                    <a href="%s" target="_blank" style="display:inline-block;padding:14px 32px;background:linear-gradient(135deg, #ff2a5f 0%%, #e01749 100%%);color:#ffffff;font-size:15px;font-weight:600;text-decoration:none;border-radius:10px;box-shadow:0 4px 15px rgba(255,42,95,0.4);">
                      Reset Password
                    </a>
                  </td>
                </tr>
              </table>
              <p style="margin:0 0 8px;font-size:13px;line-height:1.5;color:#64748b;">
                Or copy and paste this link into your browser:
              </p>
              <p style="margin:0;font-size:12px;line-height:1.4;word-break:break-all;color:#ff2a5f;">
                %s
              </p>
              <div style="margin-top:32px;padding-top:20px;border-top:1px solid #232738;font-size:12px;color:#64748b;line-height:1.5;">
                This link expires in 1 hour. If you did not request a password reset, you can safely ignore this email.
              </div>
            </td>
          </tr>
        </table>
      </td>
    </tr>
  </table>
</body>
</html>`, safeGreeting, safeResetURL, safeResetURL)
	return subject, textBody, htmlBody
}

func formatInvitationPermissions(canEdit, canHost bool) string {
	var parts []string
	if canEdit {
		parts = append(parts, "Edit catalog (add, edit, remove, import, and sync songs)")
	}
	if canHost {
		parts = append(parts, "Host shows (start and run live karaoke shows from this book)")
	}
	return strings.Join(parts, "; ")
}

func buildSongbookInvitationEmail(msg SongbookInvitationEmail) (subject, textBody, htmlBody string) {
	inviter := strings.TrimSpace(msg.InviterName)
	if inviter == "" {
		inviter = "A MicUp host"
	}
	bookName := strings.TrimSpace(msg.SongbookName)
	if bookName == "" {
		bookName = "a songbook"
	}
	perms := formatInvitationPermissions(msg.CanEditCatalog, msg.CanHostShows)
	expiresStr := msg.ExpiresAt.UTC().Format("Jan 2, 2006 at 15:04 UTC")

	subject = fmt.Sprintf("%s invited you to collaborate on %q — MicUp.live", inviter, bookName)
	textBody = fmt.Sprintf(
		"Hi,\n\n%s invited you to collaborate on the songbook %q on MicUp.live.\n\nPermissions granted:\n- %s\n\nAccept this invitation (expires %s):\n%s\n\nThis is a single-use link. Sign in or create a MicUp host account with this verified email address to accept.\n\nKeep on singing!\nThe MicUp Team\nhttps://micup.live",
		inviter, bookName, perms, expiresStr, msg.AcceptURL,
	)

	safeInviter := html.EscapeString(inviter)
	safeBook := html.EscapeString(bookName)
	safePerms := html.EscapeString(perms)
	safeExpires := html.EscapeString(expiresStr)
	safeURL := html.EscapeString(msg.AcceptURL)

	htmlBody = fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <title>Songbook collaboration invitation</title>
</head>
<body style="margin:0;padding:0;background-color:#090a10;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;color:#e2e8f0;">
  <table width="100%%" border="0" cellspacing="0" cellpadding="0" style="background-color:#090a10;padding:40px 20px;">
    <tr>
      <td align="center">
        <table width="100%%" border="0" cellspacing="0" cellpadding="0" style="max-width:520px;background-color:#131520;border:1px solid #232738;border-radius:16px;overflow:hidden;box-shadow:0 20px 40px rgba(0,0,0,0.6);">
          <tr>
            <td style="padding:32px 32px 24px;text-align:center;background:linear-gradient(180deg, rgba(255,42,95,0.12) 0%%, transparent 100%%);border-bottom:1px solid #232738;">
              <h1 style="margin:0;font-size:24px;font-weight:800;letter-spacing:-0.02em;color:#ffffff;">
                🎤 MicUp<span style="color:#ff2a5f;">.live</span>
              </h1>
            </td>
          </tr>
          <tr>
            <td style="padding:32px;">
              <h2 style="margin:0 0 16px;font-size:20px;font-weight:700;color:#ffffff;">You're invited to collaborate</h2>
              <p style="margin:0 0 16px;font-size:15px;line-height:1.6;color:#94a3b8;">
                <strong style="color:#ffffff;">%s</strong> invited you to collaborate on <strong style="color:#ffffff;">%s</strong>.
              </p>
              <p style="margin:0 0 20px;font-size:14px;line-height:1.6;color:#cbd5e1;background:#1b1f30;padding:12px 16px;border-radius:10px;border:1px solid #2d3348;">
                <strong style="color:#ffffff;">Permissions:</strong> %s
              </p>
              <table width="100%%" border="0" cellspacing="0" cellpadding="0" style="margin:28px 0;">
                <tr>
                  <td align="center">
                    <a href="%s" target="_blank" style="display:inline-block;padding:14px 32px;background:linear-gradient(135deg, #ff2a5f 0%%, #e01749 100%%);color:#ffffff;font-size:15px;font-weight:600;text-decoration:none;border-radius:10px;box-shadow:0 4px 15px rgba(255,42,95,0.4);">
                      Review &amp; Accept Invitation
                    </a>
                  </td>
                </tr>
              </table>
              <p style="margin:0 0 8px;font-size:13px;line-height:1.5;color:#64748b;">
                Or copy and paste this single-use link into your browser:
              </p>
              <p style="margin:0;font-size:12px;line-height:1.4;word-break:break-all;color:#ff2a5f;">
                %s
              </p>
              <div style="margin-top:32px;padding-top:20px;border-top:1px solid #232738;font-size:12px;color:#64748b;line-height:1.5;">
                This invitation expires on %s. Sign in or create an account with this verified email address to accept.
              </div>
            </td>
          </tr>
        </table>
      </td>
    </tr>
  </table>
</body>
</html>`, safeInviter, safeBook, safePerms, safeURL, safeURL, safeExpires)
	return subject, textBody, htmlBody
}
