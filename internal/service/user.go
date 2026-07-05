package service

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	goaway "github.com/TwiN/go-away"
	"github.com/asaskevich/govalidator"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/repository"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// UserService is the business logic layer for user-related operations.
type UserService struct {
	Cfg  *config.Config
	Repo repository.UserRepo
}

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

// UpdateUser updates a user's profile fields (first name, email).
func (s *UserService) UpdateUser(user *models.User, firstName, email string) error {
	if email != "" && email != user.Email {
		if err := s.ValidateEmail(email); err != nil {
			return err
		}
		if err := s.Repo.UpdateUserEmail(user.ID, email); err != nil {
			return err
		}
	}
	if firstName != "" {
		if err := s.Repo.UpdateUserFirstName(user.ID, firstName); err != nil {
			return err
		}
	}
	return nil
}

// UpdateSettings updates a user's settings.
func (s *UserService) UpdateSettings(user *models.User, keepScreenAwake bool) error {
	return s.Repo.UpdateUserSettingsKeepScreenAwake(user.ID, keepScreenAwake)
}

// ValidateUsername validates a username against a set of rules.
func (s *UserService) ValidateUsername(username string) error {
	// Check if the username already exists.
	// This is also caught as a known error in the repository.
	exists, err := s.Repo.UsernameExists(username)
	if err != nil {
		return fmt.Errorf("error checking username: %v", err)
	}
	if exists {
		return fmt.Errorf("username is already taken")
	}

	// Check if the username is long enough
	minLength := 3
	if len(username) < minLength {
		return fmt.Errorf("username must be at least %d characters", minLength)
	}

	// Check if the username is alphanumeric
	if !govalidator.IsAlphanumeric(username) {
		return fmt.Errorf("username can only contain alphanumeric characters")
	}

	// Define a list of forbidden usernames
	var forbiddenUsernames = []string{
		"admin",
		"administrator",
		"root",
		// "julian",
		"awfulbits",
		"windoze95",
		// "yana",
		"russianminx",
		"russianminxx",
		"sys",
		"sysadmin",
		"system",
		"test",
		"testuser",
		"test-user",
		"test_user",
		"login",
		"logout",
		"register",
		"password",
		"user",
		"newuser",
		"yourapp",
		"yourcompany",
		"yourbrand",
		"support",
		"help",
		"faq",
		"saltybytes",
		"saltybytes_ai",
		"saltybytes-ai",
		"saltybytesadmin",
		"saltybytes_admin",
		"saltybytes-admin",
		"saltybytesroot",
		"saltybytes_root",
		"saltybytes-root",
	}

	// Check if the username is in the forbidden list
	lowercaseUsername := strings.ToLower(username)
	for _, forbiddenUsername := range forbiddenUsernames {
		if strings.EqualFold(lowercaseUsername, forbiddenUsername) {
			return fmt.Errorf("username '%s' is not allowed", username)
		}
	}

	// Profanity check
	profanityDetector := goaway.NewProfanityDetector().WithSanitizeLeetSpeak(true).WithSanitizeSpecialCharacters(true).WithSanitizeAccents(false)
	if profanityDetector.IsProfane(username) {
		return fmt.Errorf("username contains inappropriate language")
	}

	// If we've passed all checks, the username is valid.
	return nil
}

// ValidateEmail validates an email address against a set of rules.
func (s *UserService) ValidateEmail(email string) error {
	if !govalidator.IsEmail(email) {
		return fmt.Errorf("invalid email format")
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
