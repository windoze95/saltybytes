package repository

import (
	"time"

	"github.com/windoze95/saltybytes-api/internal/models"
	"gorm.io/gorm"
)

// OAuthRepo is the interface for OAuth client/code/token persistence.
type OAuthRepo interface {
	CreateClient(client *models.OAuthClient) error
	GetClientByClientID(clientID string) (*models.OAuthClient, error)
	CreateAuthCode(code *models.OAuthAuthCode) error
	GetAuthCodeByHash(codeHash string) (*models.OAuthAuthCode, error)
	// ConsumeAuthCode marks a code used atomically; returns false when the
	// code was already used (replay).
	ConsumeAuthCode(codeID uint) (bool, error)
	CreateToken(token *models.OAuthToken) error
	GetTokenByHash(tokenHash string) (*models.OAuthToken, error)
	// RevokeToken revokes a token by ID; returns false when it was already revoked.
	RevokeToken(tokenID uint) (bool, error)
	RevokeAllForUserClient(userID uint, clientID string) error
	DeleteExpired(cutoff time.Time) error
}

// OAuthRepository is the GORM implementation of OAuthRepo.
type OAuthRepository struct {
	DB *gorm.DB
}

// NewOAuthRepository creates a new OAuthRepository.
func NewOAuthRepository(db *gorm.DB) *OAuthRepository {
	return &OAuthRepository{DB: db}
}

// CreateClient persists a newly registered OAuth client.
func (r *OAuthRepository) CreateClient(client *models.OAuthClient) error {
	return r.DB.Create(client).Error
}

// GetClientByClientID looks up a client by its public client_id.
func (r *OAuthRepository) GetClientByClientID(clientID string) (*models.OAuthClient, error) {
	var client models.OAuthClient
	if err := r.DB.Where("client_id = ?", clientID).First(&client).Error; err != nil {
		return nil, err
	}
	return &client, nil
}

// CreateAuthCode persists an authorization code record.
func (r *OAuthRepository) CreateAuthCode(code *models.OAuthAuthCode) error {
	return r.DB.Create(code).Error
}

// GetAuthCodeByHash looks up an authorization code by its sha256 hex.
func (r *OAuthRepository) GetAuthCodeByHash(codeHash string) (*models.OAuthAuthCode, error) {
	var code models.OAuthAuthCode
	if err := r.DB.Where("code_hash = ?", codeHash).First(&code).Error; err != nil {
		return nil, err
	}
	return &code, nil
}

// ConsumeAuthCode marks a code used atomically. The WHERE used_at IS NULL
// guard makes concurrent redemption race-safe: exactly one caller wins.
func (r *OAuthRepository) ConsumeAuthCode(codeID uint) (bool, error) {
	res := r.DB.Model(&models.OAuthAuthCode{}).
		Where("id = ? AND used_at IS NULL", codeID).
		Update("used_at", time.Now())
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

// CreateToken persists an access or refresh token record.
func (r *OAuthRepository) CreateToken(token *models.OAuthToken) error {
	return r.DB.Create(token).Error
}

// GetTokenByHash looks up a token by its sha256 hex.
func (r *OAuthRepository) GetTokenByHash(tokenHash string) (*models.OAuthToken, error) {
	var token models.OAuthToken
	if err := r.DB.Where("token_hash = ?", tokenHash).First(&token).Error; err != nil {
		return nil, err
	}
	return &token, nil
}

// RevokeToken revokes a token atomically; false means it was already revoked.
func (r *OAuthRepository) RevokeToken(tokenID uint) (bool, error) {
	res := r.DB.Model(&models.OAuthToken{}).
		Where("id = ? AND revoked_at IS NULL", tokenID).
		Update("revoked_at", time.Now())
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

// RevokeAllForUserClient revokes every outstanding token for a user+client
// pair (used on refresh-token replay detection).
func (r *OAuthRepository) RevokeAllForUserClient(userID uint, clientID string) error {
	return r.DB.Model(&models.OAuthToken{}).
		Where("user_id = ? AND client_id = ? AND revoked_at IS NULL", userID, clientID).
		Update("revoked_at", time.Now()).Error
}

// DeleteExpired removes token and code rows that expired before cutoff —
// housekeeping so the tables don't grow unbounded.
func (r *OAuthRepository) DeleteExpired(cutoff time.Time) error {
	if err := r.DB.Where("expires_at < ?", cutoff).Delete(&models.OAuthAuthCode{}).Error; err != nil {
		return err
	}
	return r.DB.Where("expires_at < ?", cutoff).Delete(&models.OAuthToken{}).Error
}
