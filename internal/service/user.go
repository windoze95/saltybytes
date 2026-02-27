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
	UnitSystem   int    `json:"unit_system"`
	Requirements string `json:"requirements"`
	UID          string `json:"uid"`
}

// NewUserService is the constructor function for initializing a new UserService
func NewUserService(cfg *config.Config, repo repository.UserRepo) *UserService {
	return &UserService{
		Cfg:  cfg,
		Repo: repo,
	}
}

// CreateUser creates a new user.
func (s *UserService) CreateUser(username, firstName, email, password string) (*models.User, error) {
	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), 10)
	if err != nil {
		return nil, fmt.Errorf("error hashing password: %v", err)
	}

	hashedPasswordStr := string(hashedPassword)

	// Create User and UserSettings
	user := &models.User{
		Username:  username,
		FirstName: firstName,
		Email:     email,
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
			UnitSystem: models.USCustomary, // Default value
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

// LoginUser logs in a user.
func (s *UserService) LoginUser(username, password string) (*models.User, error) {
	user, err := s.Repo.GetUserAuthByUsername(username)
	if err != nil {
		return nil, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Auth.HashedPassword), []byte(password)); err != nil {
		return nil, errors.New("invalid username or password")
	}

	return user, nil
}

// ToUserResponse converts a User to a UserResponse.
func ToUserResponse(user *models.User) *UserResponse {
	resp := &UserResponse{
		ID:        strconv.FormatUint(uint64(user.ID), 10),
		Username:  user.Username,
		FirstName: user.FirstName,
		Email:     user.Email,
		CreatedAt: user.CreatedAt,
		UpdatedAt: user.UpdatedAt,
	}
	if user.Settings != nil {
		resp.Settings = SettingsResponse{
			KeepScreenAwake: user.Settings.KeepScreenAwake,
		}
	}
	if user.Personalization != nil {
		resp.Personalization = PersonalizationResponse{
			UnitSystem:   int(user.Personalization.UnitSystem),
			Requirements: user.Personalization.Requirements,
			UID:          user.Personalization.UID.String(),
		}
	}
	return resp
}

// GetUserByID gets a user by their ID.
func (s *UserService) GetUserByID(userID uint) (*models.User, error) {
	return s.Repo.GetUserByID(userID)
}

// UpdatePersonalization updates a user's personalization settings.
func (s *UserService) UpdatePersonalization(user *models.User, updatedPersonalization *models.Personalization) error {
	return s.Repo.UpdatePersonalization(user.ID, updatedPersonalization)
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
	hasSpecialChar, _ := regexp.MatchString(`[!@#$%^&*]`, password)
	if !hasSpecialChar {
		return errors.New("password must contain at least one special character")
	}
	return nil
}
