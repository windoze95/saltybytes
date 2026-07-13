package service

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/asaskevich/govalidator"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/repository"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// EmailVerificationStatus is the sliver of EmailVerificationService that
// UserService needs: whether verification is actually live (switched on in
// config AND backed by a working sender). A nil implementation means not live.
type EmailVerificationStatus interface {
	Enabled() bool
}

// UserService is the business logic layer for user-related operations.
type UserService struct {
	Cfg  *config.Config
	Repo repository.UserRepo
	// Verification is assigned by the router once the verification service
	// exists. Nil (in tests, or before wiring) reads as "not live".
	Verification EmailVerificationStatus
}

// verificationLive reports whether an address has to be proved before it counts
// as verified.
func (s *UserService) verificationLive() bool {
	return s.Verification != nil && s.Verification.Enabled()
}

// Profile field bounds. Both columns are unbounded text, so these are the only
// thing between a client and a 5,000-character display name. Username bounds
// live in username_policy.go.
const (
	maxFirstNameLength = 50
	maxEmailLength     = 254 // RFC 5321 caps the whole path at 254
)

// UserResponse is the response object for user-related operations.
type UserResponse struct {
	ID              string                  `json:"id"`
	Username        string                  `json:"username"`
	FirstName       string                  `json:"first_name"`
	Email           string                  `json:"email"`
	EmailVerified   bool                    `json:"email_verified"`
	Settings        SettingsResponse        `json:"settings"`
	Personalization PersonalizationResponse `json:"personalization"`
	CreatedAt       time.Time               `json:"createdAt"`
	UpdatedAt       time.Time               `json:"updatedAt"`
}

// SettingsResponse is the response object for user settings.
type SettingsResponse struct {
	KeepScreenAwake bool `json:"keep_screen_awake"`
}

// PersonalizationResponse is the response object for user personalization.
type PersonalizationResponse struct {
	UnitSystem     string `json:"unit_system"`
	Requirements   string `json:"requirements"`
	CookingContext string `json:"cooking_context"`
	UID            string `json:"uid"`
}

// NewUserService is the constructor function for initializing a new UserService
func NewUserService(cfg *config.Config, repo repository.UserRepo) *UserService {
	return &UserService{
		Cfg:  cfg,
		Repo: repo,
	}
}

// CreateUser creates a new user. Identifier fields are trimmed here as well
// as in the handler so no caller can store padded values; the password is
// never trimmed (spaces are legitimate password characters).
func (s *UserService) CreateUser(username, firstName, email, password string) (*models.User, error) {
	username = strings.TrimSpace(username)
	firstName = strings.TrimSpace(firstName)
	email = strings.TrimSpace(email)

	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), 10)
	if err != nil {
		return nil, fmt.Errorf("error hashing password: %v", err)
	}

	hashedPasswordStr := string(hashedPassword)

	// While email verification is disabled, accounts are verified at birth so
	// nothing gates on it; once enabled, new accounts start unverified and
	// the signup handler sends the code.
	var emailVerifiedAt *time.Time
	if !s.Cfg.EmailVerificationActive() {
		now := time.Now()
		emailVerifiedAt = &now
	}

	// Create User and UserSettings
	user := &models.User{
		Username:        username,
		FirstName:       firstName,
		Email:           email,
		EmailVerifiedAt: emailVerifiedAt,
		Auth: &models.UserAuth{
			HashedPassword: hashedPasswordStr,
			AuthType:       models.Standard,
		},
		Subscription: &models.Subscription{
			Tier:           models.TierFree,
			MonthlyResetAt: time.Now().AddDate(0, 1, 0),
		},
		Settings: &models.UserSettings{
			KeepScreenAwake: true, // Default value
		},
		Personalization: &models.Personalization{
			UnitSystem: "us_customary", // Default value
			// UID:        uuid.New(),
		},
		// CollectedRecipes: []*models.Recipe{},
	}

	user, err = s.Repo.CreateUser(user)
	if err != nil {
		return nil, err
	}

	return user, nil
}

// ErrInvalidCredentials is returned for every login failure mode (unknown
// username, missing auth record, or wrong password) so the API cannot be
// used to enumerate usernames and never leaks internal error details.
var ErrInvalidCredentials = errors.New("invalid username or password")

// dummyBcryptHash is a cost-10 bcrypt hash (of a fixed throwaway string) that
// is compared against when the username does not exist, so unknown-user and
// wrong-password failures take comparable time. Without it, the early return
// skips the ~50-100ms bcrypt compare and response latency leaks whether a
// username exists.
var dummyBcryptHash = []byte("$2a$10$eTN04vDBzQvPQeH2t2QGHe/dB4sezgOEiN7Jd9/BB4cv7Hkgimei6")

// LoginUser logs in a user. The identifier may be a username or an email
// address — usernames are alphanumeric-only, so anything containing '@' can
// only be an email. Both are matched case-insensitively. Unknown identifier
// and wrong password return ErrInvalidCredentials; any other error is an
// internal failure the handler should report as such rather than blaming
// the user's credentials.
func (s *UserService) LoginUser(identifier, password string) (*models.User, error) {
	identifier = strings.TrimSpace(identifier)

	var user *models.User
	var err error
	if strings.Contains(identifier, "@") {
		user, err = s.Repo.GetUserAuthByEmail(identifier)
	} else {
		user, err = s.Repo.GetUserAuthByUsername(identifier)
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("login lookup failed: %w", err)
	}
	if err != nil || user == nil || user.Auth == nil {
		// Burn a bcrypt compare so this path takes as long as a real
		// password check (prevents username enumeration via timing).
		_ = bcrypt.CompareHashAndPassword(dummyBcryptHash, []byte(password))
		return nil, ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Auth.HashedPassword), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}

	return user, nil
}

// EmailInUse reports whether an account already exists with the given email
// address, ignoring case.
func (s *UserService) EmailInUse(email string) (bool, error) {
	return s.Repo.EmailExists(strings.TrimSpace(email))
}

// EmailTakenForSignup reports whether an email is unavailable to a new
// signup. Unlike EmailInUse, a stale squatter — an account that never
// verified the address within 48 hours of signup (only possible while email
// verification is on) — releases the address and does not block the signup.
// The squatting account keeps working via username login.
func (s *UserService) EmailTakenForSignup(email string) (bool, error) {
	holder, err := s.Repo.GetUserAuthByEmail(strings.TrimSpace(email))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return true, err
	}
	if holder == nil {
		return false, nil
	}
	if holder.EmailVerified() || !s.Cfg.EmailVerificationActive() {
		return true, nil
	}
	if time.Since(holder.CreatedAt) < staleSignupAfter {
		return true, nil
	}
	if err := s.Repo.ClearUserEmail(holder.ID); err != nil {
		return true, err
	}
	return false, nil
}

// GetUserWithAuthByID gets a user with their auth record (token version) loaded.
func (s *UserService) GetUserWithAuthByID(userID uint) (*models.User, error) {
	return s.Repo.GetUserWithAuthByID(userID)
}

// LogoutUser revokes all outstanding refresh tokens for a user by
// incrementing their token version.
func (s *UserService) LogoutUser(userID uint) error {
	return s.Repo.IncrementTokenVersion(userID)
}

// abandonedAccountAfter is how long an unverified, contentless signup may
// sit before the cleanup sweeper garbage-collects it (freeing its username
// and email for reuse).
const abandonedAccountAfter = 30 * 24 * time.Hour

// CleanupAbandonedAccounts runs one sweep of abandoned unverified signups.
// Returns how many accounts were removed.
func (s *UserService) CleanupAbandonedAccounts() (int64, error) {
	return s.Repo.DeleteAbandonedUnverifiedUsers(time.Now().Add(-abandonedAccountAfter))
}

// StartAbandonedAccountCleanup sweeps shortly after boot and then every 12
// hours. The boot sweep matters because frequent deploys restart the task —
// a ticker alone might never fire between deployments.
func (s *UserService) StartAbandonedAccountCleanup() {
	sweep := func() {
		n, err := s.CleanupAbandonedAccounts()
		if err != nil {
			logger.Get().Warn("abandoned-account cleanup failed", zap.Error(err))
			return
		}
		if n > 0 {
			logger.Get().Info("abandoned-account cleanup removed empty unverified signups", zap.Int64("count", n))
		}
	}
	go func() {
		time.Sleep(2 * time.Minute)
		sweep()
		ticker := time.NewTicker(12 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			sweep()
		}
	}()
}

// ToUserResponse converts a User to a UserResponse.
func ToUserResponse(user *models.User) *UserResponse {
	resp := &UserResponse{
		ID:            strconv.FormatUint(uint64(user.ID), 10),
		Username:      user.Username,
		FirstName:     user.FirstName,
		Email:         user.Email,
		EmailVerified: user.EmailVerified(),
		CreatedAt:     user.CreatedAt,
		UpdatedAt:     user.UpdatedAt,
	}
	if user.Settings != nil {
		resp.Settings = SettingsResponse{
			KeepScreenAwake: user.Settings.KeepScreenAwake,
		}
	}
	if user.Personalization != nil {
		resp.Personalization = PersonalizationResponse{
			UnitSystem:     user.Personalization.UnitSystem,
			Requirements:   user.Personalization.Requirements,
			CookingContext: user.Personalization.CookingContext,
			UID:            user.Personalization.UID.String(),
		}
	}
	return resp
}

// GetUserByID gets a user by their ID.
func (s *UserService) GetUserByID(userID uint) (*models.User, error) {
	return s.Repo.GetUserByID(userID)
}

// UpdatePersonalization partially updates a user's personalization settings.
// Nil fields in the update are left unchanged.
func (s *UserService) UpdatePersonalization(user *models.User, update *models.PersonalizationUpdate) error {
	if update.UnitSystem != nil && *update.UnitSystem != "us_customary" && *update.UnitSystem != "metric" {
		return fmt.Errorf("unit_system must be 'us_customary' or 'metric'")
	}
	return s.Repo.UpdatePersonalization(user.ID, update)
}

// UpdateUser updates a user's profile fields (first name, email). Callers are
// expected to have validated both already.
//
// Changing the address un-verifies the account. EmailVerifiedAt is what gates
// the AI-cost endpoints and what the stale-signup sweep keys off, so it has to
// mean "we verified THIS address" — otherwise anyone could verify a throwaway
// and then swap in an address nobody ever proved. The caller sends the fresh
// code; when verification isn't live the new address is marked verified
// immediately, mirroring what signup does in that mode.
//
// The in-memory user is updated to match, so the caller mails the new address
// and not the old one.
func (s *UserService) UpdateUser(user *models.User, firstName, email string) error {
	// EqualFold: retyping the same address with different capitalisation is not
	// a change, and must not cost the user their verified status.
	if email != "" && !strings.EqualFold(email, user.Email) {
		if err := s.ValidateEmail(email); err != nil {
			return err
		}

		// Same question signup asks — is this address claimable? — including
		// the release of one squatted by a stale unverified signup, which also
		// keeps the unique constraint from firing underneath us.
		taken, err := s.EmailTakenForSignup(email)
		if err != nil {
			return err
		}
		if taken {
			return repository.ErrEmailTaken
		}

		var verifiedAt *time.Time
		if !s.verificationLive() {
			now := time.Now()
			verifiedAt = &now
		}
		if err := s.Repo.UpdateUserEmail(user.ID, email, verifiedAt); err != nil {
			return err
		}
		user.Email = email
		user.EmailVerifiedAt = verifiedAt
	}

	if firstName != "" {
		if err := s.Repo.UpdateUserFirstName(user.ID, firstName); err != nil {
			return err
		}
		user.FirstName = firstName
	}
	return nil
}

// UpdateSettings updates a user's settings.
func (s *UserService) UpdateSettings(user *models.User, keepScreenAwake bool) error {
	return s.Repo.UpdateUserSettingsKeepScreenAwake(user.ID, keepScreenAwake)
}

// ValidateUsername validates a username against a set of rules.
func (s *UserService) ValidateUsername(username string) error {
	// Format, reserved words and profanity first — see username_policy.go. All
	// of it is local, and the uniqueness check below is a database round trip:
	// don't spend one on input that can never be valid.
	if err := checkUsernamePolicy(username); err != nil {
		return err
	}

	// Check if the username already exists.
	// This is also caught as a known error in the repository.
	exists, err := s.Repo.UsernameExists(username)
	if err != nil {
		return fmt.Errorf("error checking username: %v", err)
	}
	if exists {
		return fmt.Errorf("username is already taken")
	}

	return nil
}

// ValidateEmail validates an email address against a set of rules.
func (s *UserService) ValidateEmail(email string) error {
	if !govalidator.IsEmail(email) {
		return fmt.Errorf("invalid email format")
	}
	// RFC 5321 caps a path at 254 characters. govalidator's pattern doesn't,
	// and the column is unbounded text.
	if utf8.RuneCountInString(email) > maxEmailLength {
		return fmt.Errorf("email must be %d characters or less", maxEmailLength)
	}
	return nil
}

// ValidateFirstName validates the optional display name.
//
// Names are a minefield, so the rule is "any letter, plus the punctuation that
// shows up in real names": Mary-Jane, O'Brien, J. R., José, Zoë, 李 all pass.
// Everything else is rejected — digits, emoji, symbols, and control characters,
// which is what makes a newline-stuffed or 5,000-character name impossible.
//
// Deliberately NOT profanity-checked. The name is private: the app only ever
// shows it back to its owner (the settings screen prefers it over the username),
// it is not published and never reaches an AI prompt. A filter would buy no
// moderation here and would reject people genuinely named Dick or Fanny. If
// first names ever become visible to other users — a family roster, an author
// byline on a shared recipe — revisit this.
func (s *UserService) ValidateFirstName(firstName string) error {
	if firstName == "" {
		return nil // optional
	}
	if utf8.RuneCountInString(firstName) > maxFirstNameLength {
		return fmt.Errorf("first name must be %d characters or less", maxFirstNameLength)
	}

	hasLetter := false
	for _, r := range firstName {
		switch {
		case unicode.IsLetter(r):
			hasLetter = true
		case unicode.IsMark(r): // combining accents
		case r == ' ' || r == '-' || r == '\'' || r == '’' || r == '.':
		default:
			return fmt.Errorf("first name can only contain letters, spaces, hyphens, apostrophes and periods")
		}
	}
	if !hasLetter {
		return fmt.Errorf("first name must contain at least one letter")
	}
	return nil
}

// ValidatePassword validates a password against a set of rules.
func (s *UserService) ValidatePassword(password string) error {
	if len(password) < 8 {
		return errors.New("password must be at least 8 characters long")
	}
	// bcrypt only reads the first 72 bytes and errors on longer input, so
	// reject here with a clear message instead of failing at hash time.
	if len(password) > 72 {
		return errors.New("password must be at most 72 characters long")
	}
	hasUppercase, _ := regexp.MatchString(`[A-Z]`, password)
	if !hasUppercase {
		return errors.New("password must contain at least one uppercase letter")
	}
	hasLowercase, _ := regexp.MatchString(`[a-z]`, password)
	if !hasLowercase {
		return errors.New("password must contain at least one lowercase letter")
	}
	hasNumber, _ := regexp.MatchString(`\d`, password)
	if !hasNumber {
		return errors.New("password must contain at least one digit")
	}
	// Any non-alphanumeric character counts as special. An allowlist here
	// (the old [!@#$%^&*]) silently rejected common choices like '.' or '?',
	// which read to users as "signup doesn't work".
	hasSpecialChar, _ := regexp.MatchString(`[^a-zA-Z0-9]`, password)
	if !hasSpecialChar {
		return errors.New("password must contain at least one special character (e.g. ! @ # $ %)")
	}
	return nil
}
