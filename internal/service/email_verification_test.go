package service

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/testutil"
)

var codePattern = regexp.MustCompile(`\b\d{6}\b`)

func verificationEnabledConfig() *config.Config {
	return &config.Config{EnvVars: config.EnvVars{
		EmailVerificationEnabled: true,
		EmailFrom:                "SaltyBytes <no-reply@saltybytes.ai>",
	}}
}

func newVerificationHarness(cfg *config.Config) (*EmailVerificationService, *testutil.MockUserRepo, *testutil.MockEmailVerificationRepo, *testutil.MockEmailSender) {
	users := testutil.NewMockUserRepo()
	codes := testutil.NewMockEmailVerificationRepo()
	sender := &testutil.MockEmailSender{}
	svc := NewEmailVerificationService(cfg, users, codes, sender)
	return svc, users, codes, sender
}

func seedUnverifiedUser(users *testutil.MockUserRepo) *models.User {
	user := &models.User{
		Username: "newchef",
		Email:    "newchef@example.com",
		Auth:     &models.UserAuth{AuthType: models.Standard},
	}
	users.CreateUser(user)
	return user
}

func TestEmailVerification_DisabledWithoutFlagOrFrom(t *testing.T) {
	for _, cfg := range []*config.Config{
		{EnvVars: config.EnvVars{EmailVerificationEnabled: false, EmailFrom: "x@y.z"}},
		{EnvVars: config.EnvVars{EmailVerificationEnabled: true, EmailFrom: ""}},
	} {
		svc, users, _, _ := newVerificationHarness(cfg)
		if svc.Enabled() {
			t.Errorf("Enabled() = true for cfg %+v, want false", cfg.EnvVars)
		}
		user := seedUnverifiedUser(users)
		if err := svc.StartVerification(context.Background(), user); !errors.Is(err, ErrVerificationDisabled) {
			t.Errorf("StartVerification = %v, want ErrVerificationDisabled", err)
		}
	}
}

func TestStartVerification_SendsCodeEmail(t *testing.T) {
	svc, users, codes, sender := newVerificationHarness(verificationEnabledConfig())
	user := seedUnverifiedUser(users)

	if err := svc.StartVerification(context.Background(), user); err != nil {
		t.Fatalf("StartVerification error: %v", err)
	}

	sent := sender.LastSent()
	if sent == nil {
		t.Fatal("no email was sent")
	}
	if sent.To != "newchef@example.com" {
		t.Errorf("sent to %q", sent.To)
	}
	if !codePattern.MatchString(sent.Text) {
		t.Errorf("email text has no 6-digit code: %q", sent.Text)
	}

	row, _ := codes.GetByUserID(user.ID)
	if row == nil {
		t.Fatal("no verification row persisted")
	}
	if row.CodeHash == codePattern.FindString(sent.Text) {
		t.Error("code must be stored hashed, not in plaintext")
	}
	if time.Until(row.ExpiresAt) <= 0 {
		t.Error("expiry must be in the future")
	}
}

func TestConfirmCode_Roundtrip(t *testing.T) {
	svc, users, codes, sender := newVerificationHarness(verificationEnabledConfig())
	user := seedUnverifiedUser(users)

	if err := svc.StartVerification(context.Background(), user); err != nil {
		t.Fatalf("StartVerification error: %v", err)
	}
	code := codePattern.FindString(sender.LastSent().Text)

	if err := svc.ConfirmCode(user, code); err != nil {
		t.Fatalf("ConfirmCode error: %v", err)
	}
	if !user.EmailVerified() {
		t.Error("user should be marked verified in memory")
	}
	stored, _ := users.GetUserByID(user.ID)
	if !stored.EmailVerified() {
		t.Error("user should be marked verified in the repo")
	}
	if row, _ := codes.GetByUserID(user.ID); row != nil {
		t.Error("verification row should be deleted after success")
	}

	// Idempotent for already-verified users.
	if err := svc.ConfirmCode(user, "000000"); err != nil {
		t.Errorf("ConfirmCode on verified user = %v, want nil", err)
	}
}

func TestConfirmCode_WrongCodeCountsAttempts(t *testing.T) {
	svc, users, codes, sender := newVerificationHarness(verificationEnabledConfig())
	user := seedUnverifiedUser(users)
	svc.StartVerification(context.Background(), user)
	right := codePattern.FindString(sender.LastSent().Text)
	wrong := "000000"
	if wrong == right {
		wrong = "000001"
	}

	for i := 0; i < 5; i++ {
		if err := svc.ConfirmCode(user, wrong); !errors.Is(err, ErrCodeInvalid) {
			t.Fatalf("attempt %d: err = %v, want ErrCodeInvalid", i+1, err)
		}
	}
	// Attempt cap reached: even the RIGHT code is now rejected.
	if err := svc.ConfirmCode(user, right); !errors.Is(err, ErrTooManyAttempts) {
		t.Errorf("after cap: err = %v, want ErrTooManyAttempts", err)
	}
	if row, _ := codes.GetByUserID(user.ID); row.Attempts != 5 {
		t.Errorf("attempts = %d, want 5", row.Attempts)
	}
}

func TestConfirmCode_Expired(t *testing.T) {
	svc, users, codes, _ := newVerificationHarness(verificationEnabledConfig())
	user := seedUnverifiedUser(users)
	codes.Upsert(&models.EmailVerification{
		UserID:     user.ID,
		CodeHash:   "irrelevant",
		ExpiresAt:  time.Now().Add(-time.Minute),
		LastSentAt: time.Now().Add(-20 * time.Minute),
	})

	if err := svc.ConfirmCode(user, "123456"); !errors.Is(err, ErrCodeExpired) {
		t.Errorf("err = %v, want ErrCodeExpired", err)
	}
}

func TestConfirmCode_NoPendingRow(t *testing.T) {
	svc, users, _, _ := newVerificationHarness(verificationEnabledConfig())
	user := seedUnverifiedUser(users)

	if err := svc.ConfirmCode(user, "123456"); !errors.Is(err, ErrCodeExpired) {
		t.Errorf("err = %v, want ErrCodeExpired (request a new code)", err)
	}
}

func TestStartVerification_ResendCooldown(t *testing.T) {
	svc, users, _, _ := newVerificationHarness(verificationEnabledConfig())
	user := seedUnverifiedUser(users)

	if err := svc.StartVerification(context.Background(), user); err != nil {
		t.Fatalf("first send error: %v", err)
	}
	if err := svc.StartVerification(context.Background(), user); !errors.Is(err, ErrResendCooldown) {
		t.Errorf("immediate resend err = %v, want ErrResendCooldown", err)
	}
}

func TestStartVerification_DailySendCap(t *testing.T) {
	svc, users, codes, _ := newVerificationHarness(verificationEnabledConfig())
	user := seedUnverifiedUser(users)
	codes.Upsert(&models.EmailVerification{
		UserID:     user.ID,
		CodeHash:   "x",
		ExpiresAt:  time.Now().Add(10 * time.Minute),
		SendCount:  6,
		LastSentAt: time.Now().Add(-5 * time.Minute), // past cooldown, same day
	})

	if err := svc.StartVerification(context.Background(), user); !errors.Is(err, ErrDailySendLimit) {
		t.Errorf("err = %v, want ErrDailySendLimit", err)
	}
}

func TestStartVerification_AlreadyVerifiedAndNoEmail(t *testing.T) {
	svc, users, _, _ := newVerificationHarness(verificationEnabledConfig())

	verified := seedUnverifiedUser(users)
	now := time.Now()
	verified.EmailVerifiedAt = &now
	if err := svc.StartVerification(context.Background(), verified); !errors.Is(err, ErrAlreadyVerified) {
		t.Errorf("err = %v, want ErrAlreadyVerified", err)
	}

	noEmail := &models.User{Username: "squatter"}
	users.CreateUser(noEmail)
	if err := svc.StartVerification(context.Background(), noEmail); !errors.Is(err, ErrNoEmailOnAccount) {
		t.Errorf("err = %v, want ErrNoEmailOnAccount", err)
	}
}

func TestCreateUser_AutoVerifiedWhileFeatureOff(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	svc := newTestUserService(repo) // empty config = verification off

	user, err := svc.CreateUser("chefbob42", "", "bob@example.com", "Password1!")
	if err != nil {
		t.Fatalf("CreateUser error: %v", err)
	}
	if !user.EmailVerified() {
		t.Error("signups must be auto-verified while verification is disabled")
	}
}

func TestCreateUser_UnverifiedWhileFeatureOn(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	svc := &UserService{Cfg: verificationEnabledConfig(), Repo: repo}

	user, err := svc.CreateUser("chefbob42", "", "bob@example.com", "Password1!")
	if err != nil {
		t.Fatalf("CreateUser error: %v", err)
	}
	if user.EmailVerified() {
		t.Error("signups must start unverified while verification is enabled")
	}
}

func TestEmailTakenForSignup_ReleasesStaleUnverifiedSquatter(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	svc := &UserService{Cfg: verificationEnabledConfig(), Repo: repo}

	squatter := &models.User{
		Username: "squatter",
		Email:    "victim@example.com",
		Auth:     &models.UserAuth{AuthType: models.Standard},
	}
	repo.CreateUser(squatter)
	squatter.CreatedAt = time.Now().Add(-72 * time.Hour) // stale

	taken, err := svc.EmailTakenForSignup("Victim@Example.com")
	if err != nil {
		t.Fatalf("EmailTakenForSignup error: %v", err)
	}
	if taken {
		t.Error("stale unverified signup must release the email")
	}
	freed, _ := repo.GetUserByID(squatter.ID)
	if freed.Email != "" {
		t.Errorf("squatter email = %q, want cleared", freed.Email)
	}
}

func TestEmailTakenForSignup_KeepsFreshAndVerifiedHolders(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	svc := &UserService{Cfg: verificationEnabledConfig(), Repo: repo}

	fresh := &models.User{Username: "fresh", Email: "fresh@example.com", Auth: &models.UserAuth{AuthType: models.Standard}}
	repo.CreateUser(fresh)
	fresh.CreatedAt = time.Now().Add(-1 * time.Hour)

	now := time.Now()
	verified := &models.User{Username: "settled", Email: "settled@example.com", EmailVerifiedAt: &now, Auth: &models.UserAuth{AuthType: models.Standard}}
	repo.CreateUser(verified)
	verified.CreatedAt = time.Now().Add(-100 * 24 * time.Hour)

	for _, email := range []string{"fresh@example.com", "settled@example.com"} {
		taken, err := svc.EmailTakenForSignup(email)
		if err != nil {
			t.Fatalf("EmailTakenForSignup(%q) error: %v", email, err)
		}
		if !taken {
			t.Errorf("EmailTakenForSignup(%q) = false, want true", email)
		}
	}
}

func TestEmailTakenForSignup_NeverReleasesWhileFeatureOff(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	svc := newTestUserService(repo) // verification off

	holder := &models.User{Username: "holder", Email: "held@example.com", Auth: &models.UserAuth{AuthType: models.Standard}}
	repo.CreateUser(holder)
	holder.CreatedAt = time.Now().Add(-72 * time.Hour)

	taken, err := svc.EmailTakenForSignup("held@example.com")
	if err != nil {
		t.Fatalf("EmailTakenForSignup error: %v", err)
	}
	if !taken {
		t.Error("emails must never be released while verification is off")
	}
}
