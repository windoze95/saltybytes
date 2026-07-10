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

const (
	// mcpAppMIMEType is the MCP Apps standard MIME type for UI resources.
	mcpAppMIMEType = "text/html;profile=mcp-app"
	// ChatGPT discovers widgets through an output template using the Apps SDK
	// skybridge MIME type. The HTML is shared; the resource contracts are not.
	chatGPTMIMEType  = "text/html+skybridge"
	chatGPTWidgetURI = "ui://saltybytes/chatgpt.html"
)

//go:embed widget/app.html
var widgetHTML string

// serverInstructions is surfaced to connected MCP hosts to guide tool use.
const serverInstructions = `SaltyBytes finds REAL recipes from around the web and manages the user's saved recipe collection.
Typical flow: search_recipes to find candidates -> preview_recipe on the chosen result -> save_recipe when the user wants to keep it -> start_cooking when they are ready.
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
		// domain is the app's canonical origin; ChatGPT's Apps SDK requires it on
		// the widget resource for directory submission.
		"domain":        cfg.EnvVars.SiteBaseURL,
		"prefersBorder": true,
	}}
}

func chatGPTResourceMeta(cfg *config.Config) mcp.Meta {
	resourceDomains := []string{
		fmt.Sprintf("https://%s.s3.amazonaws.com", cfg.EnvVars.S3Bucket),
		fmt.Sprintf("https://%s.s3.%s.amazonaws.com", cfg.EnvVars.S3Bucket, cfg.EnvVars.AWSRegion),
	}
	return mcp.Meta{
		"openai/widgetDescription":   "Browse, save, and cook SaltyBytes recipes without leaving the conversation.",
		"openai/widgetPrefersBorder": true,
		"openai/widgetDomain":        cfg.EnvVars.SiteBaseURL,
		"openai/widgetCSP": map[string]any{
			"connect_domains":  []string{},
			"resource_domains": resourceDomains,
		},
	}
}

// registerWidget registers the single MCP Apps UI resource that renders all
// tool results.
func registerWidget(server *mcp.Server, cfg *config.Config) {
	register := func(uri, name, mimeType string, meta mcp.Meta) {
		server.AddResource(&mcp.Resource{
			URI:         uri,
			Name:        name,
			Title:       "SaltyBytes recipe browser and cook mode",
			Description: "Interactive recipe cards for search, previews, saved recipes, and focused cooking.",
			MIMEType:    mimeType,
			Meta:        meta,
		}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{{
					URI:      uri,
					MIMEType: mimeType,
					Text:     widgetHTML,
					Meta:     meta,
				}},
			}, nil
		})
	}
	register(widgetURI, "saltybytes-app", mcpAppMIMEType, widgetResourceMeta(cfg))
	register(chatGPTWidgetURI, "saltybytes-chatgpt", chatGPTMIMEType, chatGPTResourceMeta(cfg))
}

// BuildServer constructs the MCP server with all tools and widgets registered.
// Exported for tests.
func BuildServer(cfg *config.Config, deps *Deps) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{
			Name:       "saltybytes",
			Title:      "SaltyBytes",
			Version:    "1.0.0",
			WebsiteURL: "https://saltybytes.ai",
		},
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
	})(ensureReadOnlyHint(mcpHandler))
}
