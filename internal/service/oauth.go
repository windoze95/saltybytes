package service

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/repository"
	"go.uber.org/zap"
)

// OAuth 2.1 authorization server for MCP connectors (Claude, ChatGPT, and any
// other MCP host). Users authorize a host once via the consent page; the host
// then acts as that user against the /mcp endpoint using bearer tokens issued
// here. Tokens are opaque high-entropy strings stored as sha256 hashes.

// Token/code lifetimes and formats.
const (
	oauthAuthCodeTTL     = 5 * time.Minute
	oauthAccessTokenTTL  = 1 * time.Hour
	oauthRefreshTokenTTL = 90 * 24 * time.Hour

	oauthAccessTokenPrefix  = "sb_at_"
	oauthRefreshTokenPrefix = "sb_rt_"
	oauthAuthCodePrefix     = "sb_ac_"
)

// OAuthScopes is the full set of scopes this server supports. An empty or
// unrecognized scope request is granted the full set — MCP hosts vary in what
// they send, and every scope here is within what the user consents to on the
// authorization page.
var OAuthScopes = []string{"recipes:read", "recipes:write", "search"}

// Sentinel errors mapped to OAuth error codes by the handler layer.
var (
	ErrOAuthInvalidClient      = errors.New("invalid_client")
	ErrOAuthInvalidGrant       = errors.New("invalid_grant")
	ErrOAuthInvalidRequest     = errors.New("invalid_request")
	ErrOAuthInvalidRedirectURI = errors.New("invalid_redirect_uri")
)

// OAuthService implements the authorization-server business logic.
type OAuthService struct {
	Cfg   *config.Config
	Repo  repository.OAuthRepo
	Users *UserService
}

// NewOAuthService creates a new OAuthService.
func NewOAuthService(cfg *config.Config, repo repository.OAuthRepo, users *UserService) *OAuthService {
	return &OAuthService{Cfg: cfg, Repo: repo, Users: users}
}

// Issuer is the OAuth issuer identifier (the public base URL of this API).
func (s *OAuthService) Issuer() string {
	return strings.TrimRight(s.Cfg.EnvVars.PublicBaseURL, "/")
}

// ResourceURL is the MCP protected-resource identifier.
func (s *OAuthService) ResourceURL() string {
	return s.Issuer() + "/mcp"
}

// randomToken returns a URL-safe 256-bit random string with the given prefix,
// plus its sha256 hex for storage.
func randomToken(prefix string) (raw string, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("failed to generate token: %w", err)
	}
	raw = prefix + base64.RawURLEncoding.EncodeToString(b)
	return raw, HashOAuthToken(raw), nil
}

// HashOAuthToken returns the sha256 hex of a token string. Tokens are
// high-entropy random values, so a fast unsalted hash is appropriate.
func HashOAuthToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// RegisterClientRequest is the RFC 7591 dynamic client registration payload
// (the subset we support).
type RegisterClientRequest struct {
	RedirectURIs            []string `json:"redirect_uris"`
	ClientName              string   `json:"client_name"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	ClientURI               string   `json:"client_uri"`
	LogoURI                 string   `json:"logo_uri"`
}

// validateRedirectURI enforces OAuth 2.1 redirect rules: https for real
// hosts, plain http only for loopback (dev tooling), and no fragments.
func validateRedirectURI(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("unparseable redirect_uri")
	}
	if u.Fragment != "" {
		return fmt.Errorf("redirect_uri must not contain a fragment")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		host := u.Hostname()
		if host == "localhost" || host == "127.0.0.1" || host == "::1" {
			return nil
		}
		return fmt.Errorf("http redirect_uri only allowed for loopback")
	default:
		return fmt.Errorf("redirect_uri scheme must be https")
	}
}

// RegisterClient handles dynamic client registration. Returns the stored
// client and, for confidential clients, the plaintext secret (shown once).
func (s *OAuthService) RegisterClient(req *RegisterClientRequest) (*models.OAuthClient, string, error) {
	if len(req.RedirectURIs) == 0 || len(req.RedirectURIs) > 10 {
		return nil, "", fmt.Errorf("%w: between 1 and 10 redirect_uris required", ErrOAuthInvalidRedirectURI)
	}
	for _, uri := range req.RedirectURIs {
		if err := validateRedirectURI(uri); err != nil {
			return nil, "", fmt.Errorf("%w: %v", ErrOAuthInvalidRedirectURI, err)
		}
	}

	authMethod := req.TokenEndpointAuthMethod
	if authMethod == "" {
		authMethod = "none"
	}
	switch authMethod {
	case "none", "client_secret_basic", "client_secret_post":
	default:
		return nil, "", fmt.Errorf("%w: unsupported token_endpoint_auth_method", ErrOAuthInvalidRequest)
	}

	name := strings.TrimSpace(req.ClientName)
	if name == "" {
		name = "MCP Client"
	}
	if len(name) > 120 {
		name = name[:120]
	}

	client := &models.OAuthClient{
		ClientID:                uuid.NewString(),
		Name:                    name,
		RedirectURIs:            models.StringList(req.RedirectURIs),
		TokenEndpointAuthMethod: authMethod,
		ClientURI:               req.ClientURI,
		LogoURI:                 req.LogoURI,
	}

	var secret string
	if authMethod != "none" {
		raw, hash, err := randomToken("sb_cs_")
		if err != nil {
			return nil, "", err
		}
		secret = raw
		client.SecretHash = hash
	}

	if err := s.Repo.CreateClient(client); err != nil {
		return nil, "", fmt.Errorf("failed to store client: %w", err)
	}
	return client, secret, nil
}

// ValidateAuthRequest checks client_id + redirect_uri before any consent UI is
// shown. Failures here must render an error page, never redirect. When the
// client registered exactly one redirect URI and the request omits it, that
// URI is used.
func (s *OAuthService) ValidateAuthRequest(clientID, redirectURI string) (*models.OAuthClient, string, error) {
	if clientID == "" {
		return nil, "", fmt.Errorf("%w: client_id is required", ErrOAuthInvalidRequest)
	}
	client, err := s.Repo.GetClientByClientID(clientID)
	if err != nil {
		return nil, "", fmt.Errorf("%w: unknown client_id", ErrOAuthInvalidClient)
	}
	if redirectURI == "" {
		if len(client.RedirectURIs) == 1 {
			return client, client.RedirectURIs[0], nil
		}
		return nil, "", fmt.Errorf("%w: redirect_uri is required", ErrOAuthInvalidRequest)
	}
	for _, registered := range client.RedirectURIs {
		if registered == redirectURI {
			return client, redirectURI, nil
		}
	}
	return nil, "", fmt.Errorf("%w: redirect_uri not registered for client", ErrOAuthInvalidRedirectURI)
}

// NormalizeScope filters a requested scope string down to supported scopes.
// Empty or fully-unrecognized requests grant the full supported set.
func NormalizeScope(requested string) string {
	if strings.TrimSpace(requested) == "" {
		return strings.Join(OAuthScopes, " ")
	}
	supported := make(map[string]bool, len(OAuthScopes))
	for _, sc := range OAuthScopes {
		supported[sc] = true
	}
	var granted []string
	for _, sc := range strings.Fields(requested) {
		if supported[sc] {
			granted = append(granted, sc)
		}
	}
	if len(granted) == 0 {
		return strings.Join(OAuthScopes, " ")
	}
	return strings.Join(granted, " ")
}

// ScopeAllowed reports whether a granted scope string contains the given scope.
func ScopeAllowed(granted, scope string) bool {
	for _, sc := range strings.Fields(granted) {
		if sc == scope {
			return true
		}
	}
	return false
}

// IssueAuthCode creates a single-use authorization code after successful
// login + consent. PKCE (S256) is mandatory.
func (s *OAuthService) IssueAuthCode(userID uint, client *models.OAuthClient, redirectURI, scope, codeChallenge, codeChallengeMethod, resource string) (string, error) {
	if codeChallenge == "" || codeChallengeMethod != "S256" {
		return "", fmt.Errorf("%w: PKCE with S256 is required", ErrOAuthInvalidRequest)
	}
	raw, hash, err := randomToken(oauthAuthCodePrefix)
	if err != nil {
		return "", err
	}
	code := &models.OAuthAuthCode{
		CodeHash:            hash,
		ClientID:            client.ClientID,
		UserID:              userID,
		RedirectURI:         redirectURI,
		Scope:               NormalizeScope(scope),
		CodeChallenge:       codeChallenge,
		CodeChallengeMethod: codeChallengeMethod,
		Resource:            resource,
		ExpiresAt:           time.Now().Add(oauthAuthCodeTTL),
	}
	if err := s.Repo.CreateAuthCode(code); err != nil {
		return "", fmt.Errorf("failed to store auth code: %w", err)
	}
	return raw, nil
}

// TokenPair is the token endpoint response payload.
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

// authenticateClient verifies client credentials for the token endpoint.
// Public clients present only their client_id (PKCE proves possession);
// confidential clients must present their secret.
func (s *OAuthService) authenticateClient(clientID, clientSecret string) (*models.OAuthClient, error) {
	if clientID == "" {
		return nil, fmt.Errorf("%w: client_id is required", ErrOAuthInvalidClient)
	}
	client, err := s.Repo.GetClientByClientID(clientID)
	if err != nil {
		return nil, fmt.Errorf("%w: unknown client", ErrOAuthInvalidClient)
	}
	if client.TokenEndpointAuthMethod == "none" {
		return client, nil
	}
	if clientSecret == "" {
		return nil, fmt.Errorf("%w: client secret required", ErrOAuthInvalidClient)
	}
	presented := HashOAuthToken(clientSecret)
	if subtle.ConstantTimeCompare([]byte(presented), []byte(client.SecretHash)) != 1 {
		return nil, fmt.Errorf("%w: bad client secret", ErrOAuthInvalidClient)
	}
	return client, nil
}

// verifyPKCE checks an S256 code_verifier against the stored challenge.
func verifyPKCE(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}

// issueTokenPair mints and stores a fresh access + refresh token pair.
func (s *OAuthService) issueTokenPair(userID uint, clientID, scope string) (*TokenPair, error) {
	accessRaw, accessHash, err := randomToken(oauthAccessTokenPrefix)
	if err != nil {
		return nil, err
	}
	refreshRaw, refreshHash, err := randomToken(oauthRefreshTokenPrefix)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if err := s.Repo.CreateToken(&models.OAuthToken{
		TokenHash: accessHash,
		TokenType: models.OAuthTokenAccess,
		ClientID:  clientID,
		UserID:    userID,
		Scope:     scope,
		ExpiresAt: now.Add(oauthAccessTokenTTL),
	}); err != nil {
		return nil, fmt.Errorf("failed to store access token: %w", err)
	}
	if err := s.Repo.CreateToken(&models.OAuthToken{
		TokenHash: refreshHash,
		TokenType: models.OAuthTokenRefresh,
		ClientID:  clientID,
		UserID:    userID,
		Scope:     scope,
		ExpiresAt: now.Add(oauthRefreshTokenTTL),
	}); err != nil {
		return nil, fmt.Errorf("failed to store refresh token: %w", err)
	}
	return &TokenPair{
		AccessToken:  accessRaw,
		TokenType:    "Bearer",
		ExpiresIn:    int(oauthAccessTokenTTL.Seconds()),
		RefreshToken: refreshRaw,
		Scope:        scope,
	}, nil
}

// ExchangeAuthCode implements grant_type=authorization_code.
func (s *OAuthService) ExchangeAuthCode(clientID, clientSecret, rawCode, codeVerifier, redirectURI string) (*TokenPair, error) {
	client, err := s.authenticateClient(clientID, clientSecret)
	if err != nil {
		return nil, err
	}
	if rawCode == "" || codeVerifier == "" {
		return nil, fmt.Errorf("%w: code and code_verifier are required", ErrOAuthInvalidRequest)
	}
	code, err := s.Repo.GetAuthCodeByHash(HashOAuthToken(rawCode))
	if err != nil {
		return nil, fmt.Errorf("%w: unknown code", ErrOAuthInvalidGrant)
	}
	if code.ClientID != client.ClientID {
		return nil, fmt.Errorf("%w: code was issued to a different client", ErrOAuthInvalidGrant)
	}
	if code.UsedAt != nil {
		// Replay of a consumed code: revoke everything issued to this
		// user+client pairing (RFC 6749 §4.1.2 SHOULD).
		if err := s.Repo.RevokeAllForUserClient(code.UserID, client.ClientID); err != nil {
			logger.Get().Error("failed to revoke tokens after code replay", zap.Error(err))
		}
		return nil, fmt.Errorf("%w: code already used", ErrOAuthInvalidGrant)
	}
	if time.Now().After(code.ExpiresAt) {
		return nil, fmt.Errorf("%w: code expired", ErrOAuthInvalidGrant)
	}
	if code.RedirectURI != redirectURI {
		return nil, fmt.Errorf("%w: redirect_uri mismatch", ErrOAuthInvalidGrant)
	}
	if !verifyPKCE(codeVerifier, code.CodeChallenge) {
		return nil, fmt.Errorf("%w: PKCE verification failed", ErrOAuthInvalidGrant)
	}
	consumed, err := s.Repo.ConsumeAuthCode(code.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to consume code: %w", err)
	}
	if !consumed {
		// Lost a redemption race — treat exactly like replay.
		if err := s.Repo.RevokeAllForUserClient(code.UserID, client.ClientID); err != nil {
			logger.Get().Error("failed to revoke tokens after code replay", zap.Error(err))
		}
		return nil, fmt.Errorf("%w: code already used", ErrOAuthInvalidGrant)
	}
	return s.issueTokenPair(code.UserID, client.ClientID, code.Scope)
}

// RefreshTokens implements grant_type=refresh_token with rotation. Presenting
// a previously-rotated (revoked) refresh token is treated as theft and
// revokes every outstanding token for the user+client.
func (s *OAuthService) RefreshTokens(clientID, clientSecret, rawRefresh string) (*TokenPair, error) {
	client, err := s.authenticateClient(clientID, clientSecret)
	if err != nil {
		return nil, err
	}
	if rawRefresh == "" {
		return nil, fmt.Errorf("%w: refresh_token is required", ErrOAuthInvalidRequest)
	}
	token, err := s.Repo.GetTokenByHash(HashOAuthToken(rawRefresh))
	if err != nil {
		return nil, fmt.Errorf("%w: unknown refresh token", ErrOAuthInvalidGrant)
	}
	if token.TokenType != models.OAuthTokenRefresh || token.ClientID != client.ClientID {
		return nil, fmt.Errorf("%w: not a refresh token for this client", ErrOAuthInvalidGrant)
	}
	if token.RevokedAt != nil {
		if err := s.Repo.RevokeAllForUserClient(token.UserID, client.ClientID); err != nil {
			logger.Get().Error("failed to revoke tokens after refresh replay", zap.Error(err))
		}
		return nil, fmt.Errorf("%w: refresh token already rotated", ErrOAuthInvalidGrant)
	}
	if time.Now().After(token.ExpiresAt) {
		return nil, fmt.Errorf("%w: refresh token expired", ErrOAuthInvalidGrant)
	}
	rotated, err := s.Repo.RevokeToken(token.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to rotate refresh token: %w", err)
	}
	if !rotated {
		if err := s.Repo.RevokeAllForUserClient(token.UserID, client.ClientID); err != nil {
			logger.Get().Error("failed to revoke tokens after refresh replay", zap.Error(err))
		}
		return nil, fmt.Errorf("%w: refresh token already rotated", ErrOAuthInvalidGrant)
	}
	return s.issueTokenPair(token.UserID, client.ClientID, token.Scope)
}

// ValidateAccessToken resolves a bearer token to its stored record. Used by
// the /mcp bearer-auth middleware.
func (s *OAuthService) ValidateAccessToken(raw string) (*models.OAuthToken, error) {
	if !strings.HasPrefix(raw, oauthAccessTokenPrefix) {
		return nil, fmt.Errorf("%w: not an access token", ErrOAuthInvalidGrant)
	}
	token, err := s.Repo.GetTokenByHash(HashOAuthToken(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: unknown token", ErrOAuthInvalidGrant)
	}
	if token.TokenType != models.OAuthTokenAccess {
		return nil, fmt.Errorf("%w: not an access token", ErrOAuthInvalidGrant)
	}
	if token.RevokedAt != nil {
		return nil, fmt.Errorf("%w: token revoked", ErrOAuthInvalidGrant)
	}
	if time.Now().After(token.ExpiresAt) {
		return nil, fmt.Errorf("%w: token expired", ErrOAuthInvalidGrant)
	}
	return token, nil
}

// StartCleanup launches a daily janitor that deletes long-expired codes and
// tokens (kept 24h past expiry for debuggability).
func (s *OAuthService) StartCleanup() {
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			if err := s.Repo.DeleteExpired(time.Now().Add(-24 * time.Hour)); err != nil {
				logger.Get().Warn("oauth cleanup failed", zap.Error(err))
			}
		}
	}()
}
