package handlers

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/testutil"
	"golang.org/x/crypto/bcrypt"
)

const testPassword = "Password1!"

// newOAuthTestRouter builds a gin router with the OAuth endpoints mounted at
// their production paths, backed by in-memory repos and one known user.
func newOAuthTestRouter(t *testing.T) (*gin.Engine, *service.OAuthService) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	userRepo := testutil.NewMockUserRepo()
	hash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	user := testutil.TestUser()
	user.Auth.HashedPassword = string(hash)
	userRepo.Users[user.ID] = user

	cfg := &config.Config{EnvVars: config.EnvVars{PublicBaseURL: "https://api.example.com"}}
	userService := service.NewUserService(cfg, userRepo)
	oauthService := service.NewOAuthService(cfg, testutil.NewMockOAuthRepo(), userService)
	handler := NewOAuthHandler(oauthService, userService)

	r := gin.New()
	r.GET("/.well-known/oauth-authorization-server", handler.AuthorizationServerMetadata)
	r.GET("/.well-known/oauth-protected-resource/mcp", handler.ProtectedResourceMetadata)
	r.POST("/oauth/register", handler.RegisterClient)
	r.GET("/oauth/authorize", handler.AuthorizePage)
	r.POST("/oauth/authorize", handler.AuthorizeSubmit)
	r.POST("/oauth/token", handler.Token)
	return r, oauthService
}

func doForm(t *testing.T, r *gin.Engine, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func registerTestClient(t *testing.T, r *gin.Engine) string {
	t.Helper()
	w := doJSON(r, http.MethodPost, "/oauth/register",
		`{"redirect_uris":["https://claude.ai/api/mcp/auth_callback"],"client_name":"Claude"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("register: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("register response: %v", err)
	}
	clientID, _ := resp["client_id"].(string)
	if clientID == "" {
		t.Fatal("register response missing client_id")
	}
	if _, hasSecret := resp["client_secret"]; hasSecret {
		t.Fatal("public client should not receive a secret")
	}
	return clientID
}

func testPKCE() (verifier, challenge string) {
	verifier = strings.Repeat("k", 50)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func TestOAuthMetadataEndpoints(t *testing.T) {
	r, _ := newOAuthTestRouter(t)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("AS metadata: expected 200, got %d", w.Code)
	}
	var meta map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &meta); err != nil {
		t.Fatalf("AS metadata JSON: %v", err)
	}
	if meta["issuer"] != "https://api.example.com" {
		t.Fatalf("unexpected issuer %v", meta["issuer"])
	}
	if meta["token_endpoint"] != "https://api.example.com/oauth/token" {
		t.Fatalf("unexpected token endpoint %v", meta["token_endpoint"])
	}

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource/mcp", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("PRM: expected 200, got %d", w.Code)
	}
	var prm map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &prm); err != nil {
		t.Fatalf("PRM JSON: %v", err)
	}
	if prm["resource"] != "https://api.example.com/mcp" {
		t.Fatalf("unexpected resource %v", prm["resource"])
	}
}

func TestRegister_RejectsBadRedirect(t *testing.T) {
	r, _ := newOAuthTestRouter(t)
	w := doJSON(r, http.MethodPost, "/oauth/register",
		`{"redirect_uris":["http://evil.example.com/cb"]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "invalid_redirect_uri") {
		t.Fatalf("expected invalid_redirect_uri error, got %s", w.Body.String())
	}
}

func TestAuthorizePage_RendersConsent(t *testing.T) {
	r, _ := newOAuthTestRouter(t)
	clientID := registerTestClient(t, r)
	_, challenge := testPKCE()

	q := url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {"https://claude.ai/api/mcp/auth_callback"},
		"response_type":         {"code"},
		"state":                 {"xyz"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+q.Encode(), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{"Claude", "code_challenge", "xyz", "username", "password"} {
		if !strings.Contains(body, want) {
			t.Fatalf("consent page missing %q", want)
		}
	}
}

func TestAuthorizePage_UnknownClientRendersErrorPage(t *testing.T) {
	r, _ := newOAuthTestRouter(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/oauth/authorize?client_id=nope&redirect_uri=https://x.example.com", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "" {
		t.Fatalf("must not redirect for unknown client, got Location %q", loc)
	}
}

func TestAuthorizePage_MissingPKCERedirectsError(t *testing.T) {
	r, _ := newOAuthTestRouter(t)
	clientID := registerTestClient(t, r)
	q := url.Values{
		"client_id":     {clientID},
		"redirect_uri":  {"https://claude.ai/api/mcp/auth_callback"},
		"response_type": {"code"},
		"state":         {"xyz"},
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+q.Encode(), nil))
	if w.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", w.Code)
	}
	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil || loc.Query().Get("error") != "invalid_request" || loc.Query().Get("state") != "xyz" {
		t.Fatalf("expected invalid_request redirect with state, got %q", w.Header().Get("Location"))
	}
}

func authorizeForm(clientID, challenge, action, username, password string) url.Values {
	return url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {"https://claude.ai/api/mcp/auth_callback"},
		"response_type":         {"code"},
		"state":                 {"st4te"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"action":                {action},
		"username":              {username},
		"password":              {password},
	}
}

func TestAuthorizeSubmit_Deny(t *testing.T) {
	r, _ := newOAuthTestRouter(t)
	clientID := registerTestClient(t, r)
	_, challenge := testPKCE()

	w := doForm(t, r, "/oauth/authorize", authorizeForm(clientID, challenge, "deny", "", ""))
	if w.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", w.Code)
	}
	loc, _ := url.Parse(w.Header().Get("Location"))
	if loc.Query().Get("error") != "access_denied" {
		t.Fatalf("expected access_denied, got %q", w.Header().Get("Location"))
	}
}

func TestAuthorizeSubmit_BadCredentials(t *testing.T) {
	r, _ := newOAuthTestRouter(t)
	clientID := registerTestClient(t, r)
	_, challenge := testPKCE()

	w := doForm(t, r, "/oauth/authorize", authorizeForm(clientID, challenge, "approve", "testuser", "wrongpass"))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 re-render, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "didn&#39;t match") && !strings.Contains(w.Body.String(), "didn't match") {
		t.Fatalf("expected login error message in page")
	}
}

// TestFullFlow_AuthorizeThenTokenThenRefresh drives the whole dance through
// the HTTP surface exactly like an MCP host would.
func TestFullFlow_AuthorizeThenTokenThenRefresh(t *testing.T) {
	r, oauthService := newOAuthTestRouter(t)
	clientID := registerTestClient(t, r)
	verifier, challenge := testPKCE()

	// 1. Login + consent.
	w := doForm(t, r, "/oauth/authorize", authorizeForm(clientID, challenge, "approve", "testuser", testPassword))
	if w.Code != http.StatusFound {
		t.Fatalf("authorize: expected 302, got %d: %s", w.Code, w.Body.String())
	}
	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatalf("bad redirect: %v", err)
	}
	code := loc.Query().Get("code")
	if code == "" || loc.Query().Get("state") != "st4te" {
		t.Fatalf("redirect missing code/state: %q", w.Header().Get("Location"))
	}
	if loc.Host != "claude.ai" {
		t.Fatalf("redirected to wrong host %q", loc.Host)
	}

	// 2. Exchange the code.
	w = doForm(t, r, "/oauth/token", url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {"https://claude.ai/api/mcp/auth_callback"},
		"client_id":     {clientID},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("token: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var pair service.TokenPair
	if err := json.Unmarshal(w.Body.Bytes(), &pair); err != nil {
		t.Fatalf("token JSON: %v", err)
	}
	if pair.AccessToken == "" || pair.RefreshToken == "" || pair.TokenType != "Bearer" {
		t.Fatalf("incomplete token pair: %+v", pair)
	}

	// 3. The access token resolves to the right user.
	record, err := oauthService.ValidateAccessToken(pair.AccessToken)
	if err != nil {
		t.Fatalf("issued access token failed validation: %v", err)
	}
	if record.UserID != 1 {
		t.Fatalf("token bound to wrong user: %d", record.UserID)
	}

	// 4. Refresh rotates.
	w = doForm(t, r, "/oauth/token", url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {pair.RefreshToken},
		"client_id":     {clientID},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("refresh: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var rotated service.TokenPair
	if err := json.Unmarshal(w.Body.Bytes(), &rotated); err != nil {
		t.Fatalf("refresh JSON: %v", err)
	}
	if rotated.RefreshToken == pair.RefreshToken {
		t.Fatal("refresh token did not rotate")
	}
}

func TestToken_UnsupportedGrantType(t *testing.T) {
	r, _ := newOAuthTestRouter(t)
	w := doForm(t, r, "/oauth/token", url.Values{"grant_type": {"password"}})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "unsupported_grant_type") {
		t.Fatalf("expected unsupported_grant_type, got %s", w.Body.String())
	}
}

func TestToken_InvalidCode(t *testing.T) {
	r, _ := newOAuthTestRouter(t)
	clientID := registerTestClient(t, r)
	verifier, _ := testPKCE()
	w := doForm(t, r, "/oauth/token", url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {"sb_ac_bogus"},
		"code_verifier": {verifier},
		"redirect_uri":  {"https://claude.ai/api/mcp/auth_callback"},
		"client_id":     {clientID},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || resp["error"] != "invalid_grant" {
		t.Fatalf("expected invalid_grant, got %s", w.Body.String())
	}
}
