package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/email"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/notify"
	"github.com/windoze95/saltybytes-api/internal/repository"
	"golang.org/x/crypto/bcrypt"
)

// Sentinel errors the handlers map to HTTP responses.
var (
	ErrVerificationDisabled = errors.New("email verification is not enabled")
	ErrAlreadyVerified      = errors.New("email is already verified")
	ErrNoEmailOnAccount     = errors.New("no email address on this account")
	ErrResendCooldown       = errors.New("a code was just sent — wait a minute before requesting another")
	ErrDailySendLimit       = errors.New("daily verification email limit reached — try again tomorrow")
	ErrCodeInvalid          = errors.New("that code didn't match")
	ErrCodeExpired          = errors.New("that code expired — request a new one")
	ErrTooManyAttempts      = errors.New("too many wrong codes — request a new one")
)

const (
	codeTTL          = 15 * time.Minute
	resendCooldown   = 60 * time.Second
	maxDailySends    = 6
	maxCodeAttempts  = 5
	staleSignupAfter = 48 * time.Hour
)

// EmailVerificationService owns the signup email-verification flow: sending
// 6-digit codes, confirming them, and the rules that keep the flow from
// being abused (resend cooldown, daily send cap, attempt cap).
type EmailVerificationService struct {
	Cfg    *config.Config
	Users  repository.UserRepo
	Codes  repository.EmailVerificationRepo
	Sender email.Sender
}

// NewEmailVerificationService constructs the service. Sender may be nil when
// email is disabled; Enabled() guards every path that would send.
func NewEmailVerificationService(cfg *config.Config, users repository.UserRepo, codes repository.EmailVerificationRepo, sender email.Sender) *EmailVerificationService {
	return &EmailVerificationService{Cfg: cfg, Users: users, Codes: codes, Sender: sender}
}

// Enabled reports whether the verification flow is live.
func (s *EmailVerificationService) Enabled() bool {
	return s.Cfg.EmailVerificationActive() && s.Sender != nil
}

// StartVerification generates a fresh code for the user and emails it,
// subject to the resend cooldown and daily send cap.
func (s *EmailVerificationService) StartVerification(ctx context.Context, user *models.User) error {
	if !s.Enabled() {
		return ErrVerificationDisabled
	}
	if user.EmailVerified() {
		return ErrAlreadyVerified
	}
	if user.Email == "" {
		return ErrNoEmailOnAccount
	}

	existing, err := s.Codes.GetByUserID(user.ID)
	if err != nil {
		return fmt.Errorf("loading pending verification: %w", err)
	}

	now := time.Now()
	sendCount := 0
	if existing != nil {
		if now.Sub(existing.LastSentAt) < resendCooldown {
			return ErrResendCooldown
		}
		if sameUTCDay(existing.LastSentAt, now) {
			if existing.SendCount >= maxDailySends {
				return ErrDailySendLimit
			}
			sendCount = existing.SendCount
		}
	}

	code, err := generateVerificationCode()
	if err != nil {
		return fmt.Errorf("generating code: %w", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(code), 10)
	if err != nil {
		return fmt.Errorf("hashing code: %w", err)
	}

	if err := s.Codes.Upsert(&models.EmailVerification{
		UserID:     user.ID,
		CodeHash:   string(hash),
		ExpiresAt:  now.Add(codeTTL),
		Attempts:   0,
		SendCount:  sendCount + 1,
		LastSentAt: now,
	}); err != nil {
		return fmt.Errorf("saving verification: %w", err)
	}

	subject := "Your SaltyBytes verification code"
	text := fmt.Sprintf("Your SaltyBytes verification code is %s\n\nIt expires in 15 minutes. If you didn't create a SaltyBytes account, you can ignore this email.", code)
	html := fmt.Sprintf(`<div style="font-family:sans-serif;max-width:420px;margin:0 auto">
<h2 style="color:#E91E63">SaltyBytes</h2>
<p>Your verification code is:</p>
<p style="font-size:32px;font-weight:700;letter-spacing:8px;margin:16px 0">%s</p>
<p style="color:#666">It expires in 15 minutes. If you didn't create a SaltyBytes account, you can ignore this email.</p>
</div>`, code)

	if err := s.Sender.Send(ctx, user.Email, subject, text, html); err != nil {
		// New users can't verify (and eventually can't use AI features) while
		// this is broken — page the operator with a link to the SES console.
		notify.Alert(
			"ses-send-failure",
			"SaltyBytes: verification emails failing",
			fmt.Sprintf("SES send failed: %v\n\nNew signups can't receive their codes until this is fixed (check domain identity, sending quota, and account status).", err),
			"https://us-east-2.console.aws.amazon.com/ses/home?region=us-east-2",
			time.Hour,
		)
		return fmt.Errorf("sending verification email: %w", err)
	}
	return nil
}

// ConfirmCode checks the entered code and, on match, marks the user's email
// verified. Idempotent for already-verified users.
func (s *EmailVerificationService) ConfirmCode(user *models.User, code string) error {
	if !s.Enabled() {
		return ErrVerificationDisabled
	}
	if user.EmailVerified() {
		return nil
	}

	pending, err := s.Codes.GetByUserID(user.ID)
	if err != nil {
		return fmt.Errorf("loading pending verification: %w", err)
	}
	if pending == nil || time.Now().After(pending.ExpiresAt) {
		return ErrCodeExpired
	}
	if pending.Attempts >= maxCodeAttempts {
		return ErrTooManyAttempts
	}

	if bcrypt.CompareHashAndPassword([]byte(pending.CodeHash), []byte(code)) != nil {
		if err := s.Codes.IncrementAttempts(user.ID); err != nil {
			return fmt.Errorf("recording failed attempt: %w", err)
		}
		return ErrCodeInvalid
	}

	if err := s.Users.SetEmailVerified(user.ID); err != nil {
		return fmt.Errorf("marking email verified: %w", err)
	}
	_ = s.Codes.DeleteByUserID(user.ID) // best-effort cleanup

	now := time.Now()
	user.EmailVerifiedAt = &now
	return nil
}

// generateVerificationCode returns a crypto-random 6-digit code.
func generateVerificationCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// sameUTCDay reports whether two instants fall on the same UTC calendar day.
func sameUTCDay(a, b time.Time) bool {
	ay, am, ad := a.UTC().Date()
	by, bm, bd := b.UTC().Date()
	return ay == by && am == bm && ad == bd
}
