package mcpserver

import (
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/windoze95/saltybytes-api/internal/config"
)

// mcpAppMIMEType is the MCP Apps standard mime type for UI resources.
const mcpAppMIMEType = "text/html;profile=mcp-app"

//go:embed widget/app.html
var widgetHTML string

// serverInstructions is surfaced to connected MCP hosts to guide tool use.
const serverInstructions = `SaltyBytes finds REAL recipes from around the web and manages the user's saved recipe collection.
Typical flow: search_recipes to find candidates -> preview_recipe on the chosen result -> save_recipe when the user wants to keep it.
Every tool renders an interactive widget in the conversation; prefer letting the widget present recipe details instead of restating them in text.`

// widgetResourceMeta declares the widget's MCP Apps metadata (CSP etc.).
// Recipe imagery comes from the user's own S3 uploads plus arbitrary recipe
// sites; the widget degrades gracefully (branded placeholder) when a host's
// CSP blocks an external image.
func widgetResourceMeta(cfg *config.Config) mcp.Meta {
	resourceDomains := []string{
		fmt.Sprintf("https://%s.s3.amazonaws.com", cfg.EnvVars.S3Bucket),
		fmt.Sprintf("https://%s.s3.%s.amazonaws.com", cfg.EnvVars.S3Bucket, cfg.EnvVars.AWSRegion),
	}
	return mcp.Meta{"ui": map[string]any{
		"csp": map[string]any{
			"connectDomains":  []string{},
			"resourceDomains": resourceDomains,
		},
		"prefersBorder": true,
	}}
}

// registerWidget registers the single MCP Apps UI resource that renders all
// tool results.
func registerWidget(server *mcp.Server, cfg *config.Config) {
	meta := widgetResourceMeta(cfg)
	server.AddResource(&mcp.Resource{
		URI:         widgetURI,
		Name:        "saltybytes-app",
		Title:       "SaltyBytes recipe browser",
		Description: "Interactive recipe cards for search results, previews, and the user's saved collection.",
		MIMEType:    mcpAppMIMEType,
		Meta:        meta,
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      widgetURI,
				MIMEType: mcpAppMIMEType,
				Text:     widgetHTML,
				Meta:     meta,
			}},
		}, nil
	})
}

// BuildServer constructs the MCP server with all tools and widgets registered.
// Exported for tests.
func BuildServer(cfg *config.Config, deps *Deps) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: "saltybytes", Version: "1.0.0"},
		&mcp.ServerOptions{Instructions: serverInstructions},
	)
	registerTools(server, deps)
	registerWidget(server, cfg)
	return server
}

// NewHandler returns the /mcp endpoint: a stateless Streamable HTTP handler
// (safe behind a load balancer with multiple instances) wrapped in bearer-token
// auth against the OAuth service. 401s carry the RFC 9728 resource-metadata
// pointer so MCP hosts can discover the authorization server automatically.
func NewHandler(cfg *config.Config, deps *Deps) http.Handler {
	server := BuildServer(cfg, deps)
	mcpHandler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true},
	)

	verifier := func(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		record, err := deps.OAuth.ValidateAccessToken(token)
		if err != nil {
			return nil, auth.ErrInvalidToken
		}
		return &auth.TokenInfo{
			Scopes:     strings.Fields(record.Scope),
			Expiration: record.ExpiresAt,
			UserID:     strconv.FormatUint(uint64(record.UserID), 10),
		}, nil
	}

	return auth.RequireBearerToken(verifier, &auth.RequireBearerTokenOptions{
		ResourceMetadataURL: deps.OAuth.Issuer() + "/.well-known/oauth-protected-resource/mcp",
	})(mcpHandler)
}
