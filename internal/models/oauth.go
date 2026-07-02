package models

import (
	"time"
)

// OAuthClient is a registered OAuth 2.1 client (an MCP host like Claude or
// ChatGPT, registered via RFC 7591 dynamic client registration or manually).
// Public clients (TokenEndpointAuthMethod "none") have no secret and are
// required to use PKCE; confidential clients store a bcrypt-free sha256 hash
// of their secret (secrets are high-entropy random, so a fast hash is fine).
type OAuthClient struct {
	ID       uint   `gorm:"primaryKey" json:"-"`
	ClientID string `gorm:"uniqueIndex;size:64" json:"client_id"`
	// SecretHash is the sha256 hex of the client secret; empty for public clients.
	SecretHash              string     `gorm:"size:64" json:"-"`
	Name                    string     `gorm:"size:255" json:"client_name"`
	RedirectURIs            StringList `gorm:"type:jsonb" json:"redirect_uris"`
	TokenEndpointAuthMethod string     `gorm:"size:32" json:"token_endpoint_auth_method"`
	ClientURI               string     `gorm:"size:512" json:"client_uri,omitempty"`
	LogoURI                 string     `gorm:"size:512" json:"logo_uri,omitempty"`
	CreatedAt               time.Time  `json:"-"`
	UpdatedAt               time.Time  `json:"-"`
}

// OAuthAuthCode is a single-use authorization code issued after user consent.
// Only the sha256 hex of the code is stored. Codes expire quickly and are
// bound to the client, redirect URI, and PKCE challenge presented at
// authorization time.
type OAuthAuthCode struct {
	ID                  uint      `gorm:"primaryKey"`
	CodeHash            string    `gorm:"uniqueIndex;size:64"`
	ClientID            string    `gorm:"index;size:64"`
	UserID              uint      `gorm:"index"`
	RedirectURI         string    `gorm:"size:512"`
	Scope               string    `gorm:"size:255"`
	CodeChallenge       string    `gorm:"size:128"`
	CodeChallengeMethod string    `gorm:"size:16"`
	Resource            string    `gorm:"size:512"`
	ExpiresAt           time.Time `gorm:"index"`
	UsedAt              *time.Time
	CreatedAt           time.Time
}

// OAuth token type enum values.
const (
	OAuthTokenAccess  = "access"
	OAuthTokenRefresh = "refresh"
)

// OAuthToken is an issued access or refresh token. Only the sha256 hex of the
// opaque token string is stored. Refresh tokens rotate on use: the old row is
// revoked and a new pair issued. Presenting a revoked refresh token is treated
// as replay and revokes every outstanding token for that user+client.
type OAuthToken struct {
	ID        uint      `gorm:"primaryKey"`
	TokenHash string    `gorm:"uniqueIndex;size:64"`
	TokenType string    `gorm:"index;size:16"`
	ClientID  string    `gorm:"index;size:64"`
	UserID    uint      `gorm:"index"`
	Scope     string    `gorm:"size:255"`
	ExpiresAt time.Time `gorm:"index"`
	RevokedAt *time.Time
	CreatedAt time.Time
}
