package handlers

import (
	"embed"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/service"
	"go.uber.org/zap"
)

//go:embed templates/oauth_authorize.html templates/oauth_error.html
var oauthTemplates embed.FS

var oauthTmpl = template.Must(template.ParseFS(oauthTemplates,
	"templates/oauth_authorize.html", "templates/oauth_error.html"))

// OAuthHandler serves the OAuth 2.1 authorization-server endpoints that let
// MCP hosts (Claude, ChatGPT, ...) connect on behalf of a SaltyBytes user.
type OAuthHandler struct {
	Service     *service.OAuthService
	UserService *service.UserService
}

// NewOAuthHandler creates a new OAuthHandler.
func NewOAuthHandler(oauthService *service.OAuthService, userService *service.UserService) *OAuthHandler {
	return &OAuthHandler{Service: oauthService, UserService: userService}
}

// scopeDescriptions maps scopes to the human copy shown on the consent page.
var scopeDescriptions = map[string]string{
	"recipes:read":  "View your saved recipes",
	"recipes:write": "Save new recipes to your collection",
	"search":        "Search the web for recipes on your behalf",
}

// AuthorizationServerMetadata handles GET /.well-known/oauth-authorization-server (RFC 8414).
func (h *OAuthHandler) AuthorizationServerMetadata(c *gin.Context) {
	issuer := h.Service.Issuer()
	c.JSON(http.StatusOK, gin.H{
		"issuer":                                issuer,
		"authorization_endpoint":                issuer + "/oauth/authorize",
		"token_endpoint":                        issuer + "/oauth/token",
		"registration_endpoint":                 issuer + "/oauth/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none", "client_secret_basic", "client_secret_post"},
		"scopes_supported":                      service.OAuthScopes,
	})
}

// ProtectedResourceMetadata handles GET /.well-known/oauth-protected-resource
// and its /mcp path variant (RFC 9728).
func (h *OAuthHandler) ProtectedResourceMetadata(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"resource":                 h.Service.ResourceURL(),
		"authorization_servers":    []string{h.Service.Issuer()},
		"scopes_supported":         service.OAuthScopes,
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "SaltyBytes",
	})
}

// RegisterClient handles POST /oauth/register (RFC 7591 dynamic registration).
func (h *OAuthHandler) RegisterClient(c *gin.Context) {
	var req service.RegisterClientRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_client_metadata", "error_description": "malformed JSON body"})
		return
	}
	client, secret, err := h.Service.RegisterClient(&req)
	if err != nil {
		code := "invalid_client_metadata"
		if errors.Is(err, service.ErrOAuthInvalidRedirectURI) {
			code = "invalid_redirect_uri"
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": code, "error_description": err.Error()})
		return
	}
	resp := gin.H{
		"client_id":                  client.ClientID,
		"client_id_issued_at":        client.CreatedAt.Unix(),
		"client_name":                client.Name,
		"redirect_uris":              client.RedirectURIs,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": client.TokenEndpointAuthMethod,
	}
	if secret != "" {
		resp["client_secret"] = secret
		resp["client_secret_expires_at"] = 0
	}
	c.JSON(http.StatusCreated, resp)
}

// authorizeParams carries the (re)validated authorization-request parameters
// between the GET page render and the POST form submission.
type authorizeParams struct {
	ClientID            string
	RedirectURI         string
	ResponseType        string
	State               string
	Scope               string
	CodeChallenge       string
	CodeChallengeMethod string
	Resource            string
}

func authorizeParamsFrom(get func(string) string) authorizeParams {
	return authorizeParams{
		ClientID:            get("client_id"),
		RedirectURI:         get("redirect_uri"),
		ResponseType:        get("response_type"),
		State:               get("state"),
		Scope:               get("scope"),
		CodeChallenge:       get("code_challenge"),
		CodeChallengeMethod: get("code_challenge_method"),
		Resource:            get("resource"),
	}
}

// renderOAuthError renders the non-redirect error page (only for errors where
// redirecting back to the client would be unsafe: bad client or redirect URI).
func renderOAuthError(c *gin.Context, status int, message string) {
	c.Status(status)
	c.Header("Content-Type", "text/html; charset=utf-8")
	if err := oauthTmpl.ExecuteTemplate(c.Writer, "oauth_error.html", gin.H{"Message": message}); err != nil {
		logger.Get().Error("failed to render oauth error page", zap.Error(err))
	}
	c.Abort()
}

// redirectWithError sends the RFC 6749 error redirect back to the client.
func redirectWithError(c *gin.Context, redirectURI, errCode, description, state string) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		renderOAuthError(c, http.StatusBadRequest, "The application supplied an invalid return address.")
		return
	}
	q := u.Query()
	q.Set("error", errCode)
	if description != "" {
		q.Set("error_description", description)
	}
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	c.Redirect(http.StatusFound, u.String())
}

// validateAuthorize performs the shared GET/POST validation. It renders or
// redirects the error itself and returns ok=false when the flow must stop.
func (h *OAuthHandler) validateAuthorize(c *gin.Context, p authorizeParams) (clientName string, redirectURI string, ok bool) {
	client, resolvedRedirect, err := h.Service.ValidateAuthRequest(p.ClientID, p.RedirectURI)
	if err != nil {
		logger.Get().Warn("oauth authorize rejected", zap.String("client_id", p.ClientID), zap.Error(err))
		renderOAuthError(c, http.StatusBadRequest, "This connection request came from an unknown application, so it was blocked to keep your account safe.")
		return "", "", false
	}
	if p.ResponseType != "code" {
		redirectWithError(c, resolvedRedirect, "unsupported_response_type", "only response_type=code is supported", p.State)
		return "", "", false
	}
	if p.CodeChallenge == "" || p.CodeChallengeMethod != "S256" {
		redirectWithError(c, resolvedRedirect, "invalid_request", "PKCE with code_challenge_method=S256 is required", p.State)
		return "", "", false
	}
	return client.Name, resolvedRedirect, true
}

// renderConsent renders the login+consent page.
func (h *OAuthHandler) renderConsent(c *gin.Context, p authorizeParams, clientName, redirectURI, errMsg string, status int) {
	granted := service.NormalizeScope(p.Scope)
	var descs []string
	for _, sc := range strings.Fields(granted) {
		if d, found := scopeDescriptions[sc]; found {
			descs = append(descs, d)
		}
	}
	c.Status(status)
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Cache-Control", "no-store")
	data := gin.H{
		"ClientName":          clientName,
		"ScopeDescriptions":   descs,
		"Error":               errMsg,
		"FormAction":          "/oauth/authorize",
		"ClientID":            p.ClientID,
		"RedirectURI":         redirectURI,
		"ResponseType":        p.ResponseType,
		"State":               p.State,
		"Scope":               p.Scope,
		"CodeChallenge":       p.CodeChallenge,
		"CodeChallengeMethod": p.CodeChallengeMethod,
		"Resource":            p.Resource,
	}
	if err := oauthTmpl.ExecuteTemplate(c.Writer, "oauth_authorize.html", data); err != nil {
		logger.Get().Error("failed to render oauth consent page", zap.Error(err))
	}
}

// AuthorizePage handles GET /oauth/authorize — validates the request and
// shows the login+consent page.
func (h *OAuthHandler) AuthorizePage(c *gin.Context) {
	p := authorizeParamsFrom(c.Query)
	clientName, redirectURI, ok := h.validateAuthorize(c, p)
	if !ok {
		return
	}
	h.renderConsent(c, p, clientName, redirectURI, "", http.StatusOK)
}

// AuthorizeSubmit handles POST /oauth/authorize — authenticates the user and
// issues the authorization code.
func (h *OAuthHandler) AuthorizeSubmit(c *gin.Context) {
	p := authorizeParamsFrom(c.PostForm)
	clientName, redirectURI, ok := h.validateAuthorize(c, p)
	if !ok {
		return
	}

	if c.PostForm("action") != "approve" {
		redirectWithError(c, redirectURI, "access_denied", "the user declined the connection", p.State)
		return
	}

	user, err := h.UserService.LoginUser(strings.TrimSpace(c.PostForm("username")), c.PostForm("password"))
	if err != nil {
		h.renderConsent(c, p, clientName, redirectURI, "That username and password didn't match. Please try again.", http.StatusUnauthorized)
		return
	}

	client, _, err := h.Service.ValidateAuthRequest(p.ClientID, p.RedirectURI)
	if err != nil {
		renderOAuthError(c, http.StatusBadRequest, "This connection request came from an unknown application, so it was blocked to keep your account safe.")
		return
	}
	code, err := h.Service.IssueAuthCode(user.ID, client, redirectURI, p.Scope, p.CodeChallenge, p.CodeChallengeMethod, p.Resource)
	if err != nil {
		logger.Get().Error("failed to issue auth code", zap.Error(err))
		redirectWithError(c, redirectURI, "server_error", "could not issue authorization code", p.State)
		return
	}

	u, err := url.Parse(redirectURI)
	if err != nil {
		renderOAuthError(c, http.StatusBadRequest, "The application supplied an invalid return address.")
		return
	}
	q := u.Query()
	q.Set("code", code)
	if p.State != "" {
		q.Set("state", p.State)
	}
	u.RawQuery = q.Encode()
	c.Redirect(http.StatusFound, u.String())
}

// tokenClientCredentials extracts client credentials from the Basic auth
// header (values are form-urlencoded per RFC 6749 §2.3.1) or the form body.
func tokenClientCredentials(c *gin.Context) (clientID, clientSecret string) {
	if id, secret, ok := c.Request.BasicAuth(); ok {
		if decoded, err := url.QueryUnescape(id); err == nil {
			id = decoded
		}
		if decoded, err := url.QueryUnescape(secret); err == nil {
			secret = decoded
		}
		return id, secret
	}
	return c.PostForm("client_id"), c.PostForm("client_secret")
}

// tokenError writes an RFC 6749 §5.2 error response.
func tokenError(c *gin.Context, err error) {
	c.Header("Cache-Control", "no-store")
	switch {
	case errors.Is(err, service.ErrOAuthInvalidClient):
		c.Header("WWW-Authenticate", `Basic realm="saltybytes"`)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_client", "error_description": err.Error()})
	case errors.Is(err, service.ErrOAuthInvalidGrant):
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_grant", "error_description": err.Error()})
	case errors.Is(err, service.ErrOAuthInvalidRequest):
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "error_description": err.Error()})
	default:
		logger.Get().Error("oauth token endpoint error", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "server_error"})
	}
}

// Token handles POST /oauth/token for authorization_code and refresh_token grants.
func (h *OAuthHandler) Token(c *gin.Context) {
	clientID, clientSecret := tokenClientCredentials(c)

	var pair *service.TokenPair
	var err error
	switch c.PostForm("grant_type") {
	case "authorization_code":
		pair, err = h.Service.ExchangeAuthCode(clientID, clientSecret,
			c.PostForm("code"), c.PostForm("code_verifier"), c.PostForm("redirect_uri"))
	case "refresh_token":
		pair, err = h.Service.RefreshTokens(clientID, clientSecret, c.PostForm("refresh_token"))
	default:
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported_grant_type"})
		return
	}
	if err != nil {
		tokenError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	c.JSON(http.StatusOK, pair)
}
