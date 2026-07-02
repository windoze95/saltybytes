package service

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/testutil"
)

func newTestOAuthService() (*OAuthService, *testutil.MockOAuthRepo) {
	repo := testutil.NewMockOAuthRepo()
	cfg := &config.Config{EnvVars: config.EnvVars{PublicBaseURL: "https://api.example.com"}}
	return NewOAuthService(cfg, repo, nil), repo
}

func pkcePair() (verifier, challenge string) {
	verifier = strings.Repeat("v", 43)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func registerPublicClient(t *testing.T, s *OAuthService, redirectURI string) *models.OAuthClient {
	t.Helper()
	client, secret, err := s.RegisterClient(&RegisterClientRequest{
		RedirectURIs: []string{redirectURI},
		ClientName:   "Claude",
	})
	if err != nil {
		t.Fatalf("RegisterClient failed: %v", err)
	}
	if secret != "" {
		t.Fatalf("public client should not receive a secret")
	}
	return client
}

// issueCode drives login+consent for tests: validate → issue a code.
func issueCode(t *testing.T, s *OAuthService, client *models.OAuthClient, redirectURI, challenge string) string {
	t.Helper()
	code, err := s.IssueAuthCode(7, client, redirectURI, "", challenge, "S256", s.ResourceURL())
	if err != nil {
		t.Fatalf("IssueAuthCode failed: %v", err)
	}
	return code
}

func TestRegisterClient_PublicDefault(t *testing.T) {
	s, _ := newTestOAuthService()
	client := registerPublicClient(t, s, "https://claude.ai/api/mcp/auth_callback")
	if client.ClientID == "" {
		t.Fatal("expected a client_id")
	}
	if client.TokenEndpointAuthMethod != "none" {
		t.Fatalf("expected auth method none, got %q", client.TokenEndpointAuthMethod)
	}
	if client.SecretHash != "" {
		t.Fatal("public client must not have a secret hash")
	}
}

func TestRegisterClient_ConfidentialGetsSecret(t *testing.T) {
	s, _ := newTestOAuthService()
	client, secret, err := s.RegisterClient(&RegisterClientRequest{
		RedirectURIs:            []string{"https://chatgpt.com/connector_platform_oauth_redirect"},
		ClientName:              "ChatGPT",
		TokenEndpointAuthMethod: "client_secret_post",
	})
	if err != nil {
		t.Fatalf("RegisterClient failed: %v", err)
	}
	if secret == "" || client.SecretHash == "" {
		t.Fatal("confidential client should receive a secret")
	}
	if HashOAuthToken(secret) != client.SecretHash {
		t.Fatal("stored hash should match the issued secret")
	}
}

func TestRegisterClient_RejectsBadRedirects(t *testing.T) {
	s, _ := newTestOAuthService()
	cases := [][]string{
		nil,
		{"http://evil.example.com/cb"},
		{"https://ok.example.com/cb#frag"},
		{"ftp://files.example.com/cb"},
	}
	for _, uris := range cases {
		if _, _, err := s.RegisterClient(&RegisterClientRequest{RedirectURIs: uris}); !errors.Is(err, ErrOAuthInvalidRedirectURI) {
			t.Fatalf("redirect_uris %v: expected ErrOAuthInvalidRedirectURI, got %v", uris, err)
		}
	}
	// Loopback http is allowed for dev tooling.
	if _, _, err := s.RegisterClient(&RegisterClientRequest{RedirectURIs: []string{"http://localhost:33418/cb"}}); err != nil {
		t.Fatalf("loopback redirect should be allowed: %v", err)
	}
}

func TestRegisterClient_RejectsBadAuthMethod(t *testing.T) {
	s, _ := newTestOAuthService()
	_, _, err := s.RegisterClient(&RegisterClientRequest{
		RedirectURIs:            []string{"https://ok.example.com/cb"},
		TokenEndpointAuthMethod: "private_key_jwt",
	})
	if !errors.Is(err, ErrOAuthInvalidRequest) {
		t.Fatalf("expected ErrOAuthInvalidRequest, got %v", err)
	}
}

func TestValidateAuthRequest(t *testing.T) {
	s, _ := newTestOAuthService()
	client := registerPublicClient(t, s, "https://claude.ai/cb")

	if _, _, err := s.ValidateAuthRequest("nope", "https://claude.ai/cb"); !errors.Is(err, ErrOAuthInvalidClient) {
		t.Fatalf("unknown client: expected ErrOAuthInvalidClient, got %v", err)
	}
	if _, _, err := s.ValidateAuthRequest(client.ClientID, "https://attacker.example.com/cb"); !errors.Is(err, ErrOAuthInvalidRedirectURI) {
		t.Fatalf("unregistered redirect: expected ErrOAuthInvalidRedirectURI, got %v", err)
	}
	// Omitted redirect_uri resolves when exactly one is registered.
	_, resolved, err := s.ValidateAuthRequest(client.ClientID, "")
	if err != nil || resolved != "https://claude.ai/cb" {
		t.Fatalf("expected resolved single redirect, got %q err %v", resolved, err)
	}
}

func TestAuthCodeFlow_HappyPath(t *testing.T) {
	s, _ := newTestOAuthService()
	client := registerPublicClient(t, s, "https://claude.ai/cb")
	verifier, challenge := pkcePair()
	code := issueCode(t, s, client, "https://claude.ai/cb", challenge)

	pair, err := s.ExchangeAuthCode(client.ClientID, "", code, verifier, "https://claude.ai/cb")
	if err != nil {
		t.Fatalf("ExchangeAuthCode failed: %v", err)
	}
	if !strings.HasPrefix(pair.AccessToken, "sb_at_") || !strings.HasPrefix(pair.RefreshToken, "sb_rt_") {
		t.Fatalf("unexpected token formats: %q %q", pair.AccessToken, pair.RefreshToken)
	}
	if pair.TokenType != "Bearer" || pair.ExpiresIn != 3600 {
		t.Fatalf("unexpected pair metadata: %+v", pair)
	}
	if pair.Scope != strings.Join(OAuthScopes, " ") {
		t.Fatalf("empty scope request should grant all scopes, got %q", pair.Scope)
	}

	record, err := s.ValidateAccessToken(pair.AccessToken)
	if err != nil {
		t.Fatalf("ValidateAccessToken failed: %v", err)
	}
	if record.UserID != 7 || record.ClientID != client.ClientID {
		t.Fatalf("token bound to wrong principal: %+v", record)
	}
}

func TestExchangeAuthCode_WrongVerifier(t *testing.T) {
	s, _ := newTestOAuthService()
	client := registerPublicClient(t, s, "https://claude.ai/cb")
	_, challenge := pkcePair()
	code := issueCode(t, s, client, "https://claude.ai/cb", challenge)

	wrong := strings.Repeat("w", 43)
	if _, err := s.ExchangeAuthCode(client.ClientID, "", code, wrong, "https://claude.ai/cb"); !errors.Is(err, ErrOAuthInvalidGrant) {
		t.Fatalf("expected invalid_grant for wrong verifier, got %v", err)
	}
}

func TestExchangeAuthCode_RedirectMismatch(t *testing.T) {
	s, _ := newTestOAuthService()
	client := registerPublicClient(t, s, "https://claude.ai/cb")
	verifier, challenge := pkcePair()
	code := issueCode(t, s, client, "https://claude.ai/cb", challenge)

	if _, err := s.ExchangeAuthCode(client.ClientID, "", code, verifier, "https://claude.ai/other"); !errors.Is(err, ErrOAuthInvalidGrant) {
		t.Fatalf("expected invalid_grant for redirect mismatch, got %v", err)
	}
}

func TestExchangeAuthCode_WrongClient(t *testing.T) {
	s, _ := newTestOAuthService()
	clientA := registerPublicClient(t, s, "https://claude.ai/cb")
	clientB := registerPublicClient(t, s, "https://claude.ai/cb")
	verifier, challenge := pkcePair()
	code := issueCode(t, s, clientA, "https://claude.ai/cb", challenge)

	if _, err := s.ExchangeAuthCode(clientB.ClientID, "", code, verifier, "https://claude.ai/cb"); !errors.Is(err, ErrOAuthInvalidGrant) {
		t.Fatalf("expected invalid_grant for cross-client redemption, got %v", err)
	}
}

func TestExchangeAuthCode_ReplayRevokesTokens(t *testing.T) {
	s, _ := newTestOAuthService()
	client := registerPublicClient(t, s, "https://claude.ai/cb")
	verifier, challenge := pkcePair()
	code := issueCode(t, s, client, "https://claude.ai/cb", challenge)

	pair, err := s.ExchangeAuthCode(client.ClientID, "", code, verifier, "https://claude.ai/cb")
	if err != nil {
		t.Fatalf("first exchange failed: %v", err)
	}
	if _, err := s.ExchangeAuthCode(client.ClientID, "", code, verifier, "https://claude.ai/cb"); !errors.Is(err, ErrOAuthInvalidGrant) {
		t.Fatalf("expected invalid_grant on replay, got %v", err)
	}
	// The replay must have revoked the previously issued tokens.
	if _, err := s.ValidateAccessToken(pair.AccessToken); err == nil {
		t.Fatal("access token should be revoked after code replay")
	}
}

func TestExchangeAuthCode_Expired(t *testing.T) {
	s, repo := newTestOAuthService()
	client := registerPublicClient(t, s, "https://claude.ai/cb")
	verifier, challenge := pkcePair()
	code := issueCode(t, s, client, "https://claude.ai/cb", challenge)

	repo.Codes[HashOAuthToken(code)].ExpiresAt = time.Now().Add(-time.Minute)
	if _, err := s.ExchangeAuthCode(client.ClientID, "", code, verifier, "https://claude.ai/cb"); !errors.Is(err, ErrOAuthInvalidGrant) {
		t.Fatalf("expected invalid_grant for expired code, got %v", err)
	}
}

func TestRefreshTokens_RotatesAndDetectsReplay(t *testing.T) {
	s, _ := newTestOAuthService()
	client := registerPublicClient(t, s, "https://claude.ai/cb")
	verifier, challenge := pkcePair()
	code := issueCode(t, s, client, "https://claude.ai/cb", challenge)
	pair, err := s.ExchangeAuthCode(client.ClientID, "", code, verifier, "https://claude.ai/cb")
	if err != nil {
		t.Fatalf("exchange failed: %v", err)
	}

	rotated, err := s.RefreshTokens(client.ClientID, "", pair.RefreshToken)
	if err != nil {
		t.Fatalf("refresh failed: %v", err)
	}
	if rotated.RefreshToken == pair.RefreshToken {
		t.Fatal("refresh token should rotate")
	}

	// Replaying the old refresh token is theft: everything gets revoked.
	if _, err := s.RefreshTokens(client.ClientID, "", pair.RefreshToken); !errors.Is(err, ErrOAuthInvalidGrant) {
		t.Fatalf("expected invalid_grant on refresh replay, got %v", err)
	}
	if _, err := s.ValidateAccessToken(rotated.AccessToken); err == nil {
		t.Fatal("rotated access token should be revoked after refresh replay")
	}
}

func TestRefreshTokens_ConfidentialClientAuth(t *testing.T) {
	s, _ := newTestOAuthService()
	client, secret, err := s.RegisterClient(&RegisterClientRequest{
		RedirectURIs:            []string{"https://chatgpt.com/cb"},
		TokenEndpointAuthMethod: "client_secret_post",
	})
	if err != nil {
		t.Fatalf("register failed: %v", err)
	}
	verifier, challenge := pkcePair()
	code := issueCode(t, s, client, "https://chatgpt.com/cb", challenge)

	if _, err := s.ExchangeAuthCode(client.ClientID, "", code, verifier, "https://chatgpt.com/cb"); !errors.Is(err, ErrOAuthInvalidClient) {
		t.Fatalf("missing secret: expected ErrOAuthInvalidClient, got %v", err)
	}
	if _, err := s.ExchangeAuthCode(client.ClientID, "sb_cs_wrong", code, verifier, "https://chatgpt.com/cb"); !errors.Is(err, ErrOAuthInvalidClient) {
		t.Fatalf("wrong secret: expected ErrOAuthInvalidClient, got %v", err)
	}
	pair, err := s.ExchangeAuthCode(client.ClientID, secret, code, verifier, "https://chatgpt.com/cb")
	if err != nil {
		t.Fatalf("exchange with secret failed: %v", err)
	}
	if _, err := s.RefreshTokens(client.ClientID, secret, pair.RefreshToken); err != nil {
		t.Fatalf("refresh with secret failed: %v", err)
	}
}

func TestValidateAccessToken_Rejections(t *testing.T) {
	s, repo := newTestOAuthService()
	client := registerPublicClient(t, s, "https://claude.ai/cb")
	verifier, challenge := pkcePair()
	code := issueCode(t, s, client, "https://claude.ai/cb", challenge)
	pair, err := s.ExchangeAuthCode(client.ClientID, "", code, verifier, "https://claude.ai/cb")
	if err != nil {
		t.Fatalf("exchange failed: %v", err)
	}

	if _, err := s.ValidateAccessToken(pair.RefreshToken); err == nil {
		t.Fatal("refresh token must not validate as an access token")
	}
	if _, err := s.ValidateAccessToken("sb_at_garbage"); err == nil {
		t.Fatal("unknown token must not validate")
	}
	repo.Tokens[HashOAuthToken(pair.AccessToken)].ExpiresAt = time.Now().Add(-time.Minute)
	if _, err := s.ValidateAccessToken(pair.AccessToken); err == nil {
		t.Fatal("expired token must not validate")
	}
}

func TestIssueAuthCode_RequiresS256(t *testing.T) {
	s, _ := newTestOAuthService()
	client := registerPublicClient(t, s, "https://claude.ai/cb")
	if _, err := s.IssueAuthCode(7, client, "https://claude.ai/cb", "", "", "", ""); !errors.Is(err, ErrOAuthInvalidRequest) {
		t.Fatalf("missing PKCE: expected ErrOAuthInvalidRequest, got %v", err)
	}
	if _, err := s.IssueAuthCode(7, client, "https://claude.ai/cb", "", "challenge", "plain", ""); !errors.Is(err, ErrOAuthInvalidRequest) {
		t.Fatalf("plain PKCE: expected ErrOAuthInvalidRequest, got %v", err)
	}
}

func TestNormalizeScope(t *testing.T) {
	all := strings.Join(OAuthScopes, " ")
	cases := map[string]string{
		"":                         all,
		"recipes:read":             "recipes:read",
		"recipes:read search":      "recipes:read search",
		"claudeai":                 all, // unknown-only requests fall back to full grant
		"recipes:write unknown_sc": "recipes:write",
	}
	for in, want := range cases {
		if got := NormalizeScope(in); got != want {
			t.Fatalf("NormalizeScope(%q) = %q, want %q", in, got, want)
		}
	}
	if !ScopeAllowed(all, "search") || ScopeAllowed("recipes:read", "search") {
		t.Fatal("ScopeAllowed misbehaving")
	}
}
