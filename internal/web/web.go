// Package web serves the public marketing site and shareable recipe pages at
// the apex domain (saltybytes.ai). It renders server-side HTML straight from
// the canonical extraction cache, so every recipe SaltyBytes has ever
// extracted has a public, linkable page. Routes registered here live OUTSIDE
// the /v1 groups: browsers cannot send the app's shared ID header.
package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	htmlpkg "html"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"embed"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/middleware"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/repository"
	"github.com/windoze95/saltybytes-api/internal/service"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

var tmpl = template.Must(template.ParseFS(templateFS, "templates/*.html"))

// Handler renders the public website.
type Handler struct {
	cfg        *config.Config
	canonicals repository.CanonicalRecipeRepo
	cssTag     string
}

// NewHandler creates the public-site handler.
func NewHandler(cfg *config.Config, canonicals repository.CanonicalRecipeRepo) *Handler {
	tag := "dev"
	if css, err := staticFS.ReadFile("static/site.css"); err == nil {
		sum := sha256.Sum256(css)
		tag = hex.EncodeToString(sum[:4])
	}
	return &Handler{cfg: cfg, canonicals: canonicals, cssTag: tag}
}

// Register mounts all public-site routes on the bare engine. A dedicated
// per-IP limiter keeps anonymous web traffic out of the API's auth buckets.
func (h *Handler) Register(r *gin.Engine) {
	pages := middleware.RateLimitByIP(120, 240, 5*time.Minute, 15*time.Minute)
	r.GET("/", pages, h.Home)
	r.GET("/r/:id", pages, h.Recipe)
	r.GET("/r", pages, h.RecipeByURL)
	r.GET("/privacy", pages, h.Privacy)
	r.GET("/terms", pages, h.Terms)
	r.GET("/robots.txt", h.Robots)
	r.GET("/sitemap.xml", pages, h.Sitemap)
	r.GET("/static/*filepath", h.Static)
	// App association files (universal links / app links). The OAuth server
	// owns the other /.well-known routes; these two are the app's.
	r.GET("/.well-known/apple-app-site-association", h.AppleAASA)
	r.GET("/.well-known/assetlinks.json", h.AssetLinks)
	r.NoRoute(h.NotFound)
}

func (h *Handler) siteBase() string {
	return strings.TrimRight(h.cfg.EnvVars.SiteBaseURL, "/")
}

// pageMeta feeds the shared <head> partial.
type pageMeta struct {
	Title       string
	Description string
	Canonical   string
	OGImage     string
	OGType      string
	CSSTag      string
	JSONLD      template.JS
}

func (h *Handler) meta(title, desc, path string) pageMeta {
	return pageMeta{
		Title:       title,
		Description: desc,
		Canonical:   h.siteBase() + path,
		OGImage:     h.siteBase() + "/static/og.png",
		OGType:      "website",
		CSSTag:      h.cssTag,
	}
}

func (h *Handler) render(c *gin.Context, status int, name string, data any, cacheSeconds int) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	if cacheSeconds > 0 {
		c.Header("Cache-Control", fmt.Sprintf("public, max-age=%d, stale-while-revalidate=%d", cacheSeconds, cacheSeconds*2))
	} else {
		c.Header("Cache-Control", "no-store")
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
	c.Header("X-Frame-Options", "SAMEORIGIN")
	c.Header("Content-Security-Policy", "default-src 'self'; img-src 'self' https: data:; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; font-src 'self'; connect-src 'self'; base-uri 'self'; form-action 'self'; frame-ancestors 'self'")
	c.Status(status)
	if err := tmpl.ExecuteTemplate(c.Writer, name, data); err != nil {
		// Headers are already out; nothing safe to do but log via gin's writer.
		_ = c.Error(err)
	}
}

// --- Homepage ---

type recentItem struct {
	ID     uint
	Title  string
	Domain string
}

type homeData struct {
	Meta       pageMeta
	MCPURL     string
	IOSURL     string
	AndroidURL string
	Recent     []recentItem
	Year       int
}

// Home renders the marketing front page.
func (h *Handler) Home(c *gin.Context) {
	data := homeData{
		Meta: h.meta(
			"SaltyBytes — Real recipes, found for you",
			"That TikTok recipe? Probably already in here. SaltyBytes finds real recipes from across the web — no ads, no pop-ups, no life stories. And your AI assistant can use it for you.",
			"/",
		),
		MCPURL:     strings.TrimRight(h.cfg.EnvVars.PublicBaseURL, "/") + "/mcp",
		IOSURL:     h.cfg.EnvVars.SiteIOSAppURL,
		AndroidURL: h.cfg.EnvVars.SiteAndroidAppURL,
		Year:       time.Now().Year(),
	}
	if rows, err := h.canonicals.ListServableSummaries(6); err == nil {
		for _, row := range rows {
			data.Recent = append(data.Recent, recentItem{ID: row.ID, Title: row.Title, Domain: domainOf(row.OriginalURL)})
		}
	}
	h.render(c, http.StatusOK, "home.html", data, 600)
}

// --- Recipe pages ---

type ingredientLine struct {
	Qty  string // amount + unit, bolded in the template; empty when the raw line is shown
	Body string
}

type recipeData struct {
	Meta        pageMeta
	ID          uint
	Title       string
	Domain      string
	SourceURL   string
	CookTime    int
	Portions    int
	PortionSize string
	ImageURL    string
	Ingredients []ingredientLine
	Steps       []string
	IOSURL      string
	AndroidURL  string
	Year        int
}

// Recipe renders the public page for one extracted recipe.
func (h *Handler) Recipe(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		h.notFoundPage(c, "That link doesn't look like one of ours.")
		return
	}
	canonical, err := h.canonicals.GetByID(uint(id))
	if err != nil || canonical == nil || canonical.IsMultiPage || canonical.RecipeData.Title == "" {
		// IsMultiPage rows are collection markers with empty RecipeData and
		// must never be rendered as a recipe.
		h.notFoundPage(c, "We couldn't find that recipe. It may not have been extracted yet.")
		return
	}

	def := canonical.RecipeData
	def = decodeEntities(def)
	sourceURL := def.SourceURL
	if sourceURL == "" {
		sourceURL = canonical.OriginalURL
	}
	data := recipeData{
		ID:          canonical.ID,
		Title:       def.Title,
		Domain:      domainOf(sourceURL),
		SourceURL:   sourceURL,
		CookTime:    def.CookTime,
		Portions:    def.Portions,
		PortionSize: def.PortionSize,
		Ingredients: ingredientLines(def.Ingredients),
		Steps:       def.Instructions,
		IOSURL:      h.cfg.EnvVars.SiteIOSAppURL,
		AndroidURL:  h.cfg.EnvVars.SiteAndroidAppURL,
		Year:        time.Now().Year(),
	}
	path := fmt.Sprintf("/r/%d", canonical.ID)
	data.Meta = h.meta(def.Title+" — SaltyBytes", recipeDescription(def, data.Domain), path)
	data.Meta.OGType = "article"
	data.Meta.JSONLD = recipeJSONLD(def, h.siteBase()+path, sourceURL)
	h.render(c, http.StatusOK, "recipe.html", data, 300)
}

// RecipeByURL redirects ?u=<source url> to the recipe's page when that URL is
// already in the extraction cache. This is the share-link glue for clients
// that know a source URL but not a canonical ID.
func (h *Handler) RecipeByURL(c *gin.Context) {
	raw := strings.TrimSpace(c.Query("u"))
	if raw == "" {
		c.Redirect(http.StatusFound, "/")
		return
	}
	normalized, err := service.NormalizeURL(raw)
	if err != nil {
		h.notFoundPage(c, "That doesn't look like a recipe link.")
		return
	}
	canonical, err := h.canonicals.GetByNormalizedURL(normalized)
	if err != nil || canonical == nil || canonical.IsMultiPage || canonical.RecipeData.Title == "" {
		h.notFoundPage(c, "We haven't cooked this one yet — open it in the app to extract it.")
		return
	}
	c.Redirect(http.StatusFound, fmt.Sprintf("/r/%d", canonical.ID))
}

// decodeEntities unescapes HTML entities that some source pages leave inside
// extracted text ("&#8211;", "&amp;"). Rendering escapes everything again, so
// without this the entity shows up literally on the page.
func decodeEntities(def models.RecipeDef) models.RecipeDef {
	def.Title = htmlpkg.UnescapeString(def.Title)
	def.PortionSize = htmlpkg.UnescapeString(def.PortionSize)
	for i := range def.Ingredients {
		def.Ingredients[i].Name = htmlpkg.UnescapeString(def.Ingredients[i].Name)
		def.Ingredients[i].OriginalText = htmlpkg.UnescapeString(def.Ingredients[i].OriginalText)
		def.Ingredients[i].Unit = htmlpkg.UnescapeString(def.Ingredients[i].Unit)
	}
	for i := range def.Instructions {
		def.Instructions[i] = htmlpkg.UnescapeString(def.Instructions[i])
	}
	return def
}

func ingredientLines(ingredients models.Ingredients) []ingredientLine {
	lines := make([]ingredientLine, 0, len(ingredients))
	for _, ing := range ingredients {
		if ing.OriginalText != "" {
			lines = append(lines, ingredientLine{Body: ing.OriginalText})
			continue
		}
		qty := formatAmount(ing.Amount)
		if qty != "" && ing.AmountHigh > 0 {
			qty += "–" + formatAmount(ing.AmountHigh)
		}
		if ing.Unit != "" {
			if qty != "" {
				qty += " "
			}
			qty += ing.Unit
		}
		lines = append(lines, ingredientLine{Qty: qty, Body: ing.Name})
	}
	return lines
}

// commonFractions maps fractional parts to cookbook glyphs.
var commonFractions = []struct {
	value float64
	glyph string
}{
	{0.25, "¼"}, {0.5, "½"}, {0.75, "¾"},
	{1.0 / 3.0, "⅓"}, {2.0 / 3.0, "⅔"},
	{0.125, "⅛"}, {0.375, "⅜"}, {0.625, "⅝"}, {0.875, "⅞"},
}

func formatAmount(amount float64) string {
	if amount == 0 {
		return ""
	}
	whole := int(amount)
	frac := amount - float64(whole)
	for _, f := range commonFractions {
		// Tight tolerance: snap 0.33/0.67-style thirds, but leave a real
		// 1.37 as a decimal rather than lying with 1⅜.
		if frac > f.value-0.004 && frac < f.value+0.004 {
			if whole == 0 {
				return f.glyph
			}
			return strconv.Itoa(whole) + f.glyph
		}
	}
	s := strconv.FormatFloat(amount, 'f', 2, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s
}

func recipeDescription(def models.RecipeDef, domain string) string {
	parts := []string{fmt.Sprintf("%d ingredients", len(def.Ingredients))}
	if def.CookTime > 0 {
		parts = append(parts, fmt.Sprintf("about %d minutes", def.CookTime))
	}
	if def.Portions > 0 {
		parts = append(parts, fmt.Sprintf("serves %d", def.Portions))
	}
	desc := strings.Join(parts, " · ")
	if domain != "" {
		desc += " — a real recipe from " + domain
	}
	return desc + ". Cook it hands-free with SaltyBytes."
}

func recipeJSONLD(def models.RecipeDef, pageURL, sourceURL string) template.JS {
	steps := make([]map[string]string, 0, len(def.Instructions))
	for _, s := range def.Instructions {
		steps = append(steps, map[string]string{"@type": "HowToStep", "text": s})
	}
	ingredients := make([]string, 0, len(def.Ingredients))
	for _, line := range ingredientLines(def.Ingredients) {
		text := strings.TrimSpace(strings.TrimSpace(line.Qty) + " " + line.Body)
		if text != "" {
			ingredients = append(ingredients, text)
		}
	}
	doc := map[string]any{
		"@context":           "https://schema.org",
		"@type":              "Recipe",
		"name":               def.Title,
		"url":                pageURL,
		"mainEntityOfPage":   pageURL,
		"recipeIngredient":   ingredients,
		"recipeInstructions": steps,
	}
	if def.CookTime > 0 {
		doc["cookTime"] = fmt.Sprintf("PT%dM", def.CookTime)
	}
	if def.Portions > 0 {
		doc["recipeYield"] = strconv.Itoa(def.Portions)
	}
	if sourceURL != "" {
		doc["isBasedOn"] = sourceURL
	}
	// json.Marshal escapes <, > and & — safe to embed in a <script> block.
	encoded, err := json.Marshal(doc)
	if err != nil {
		return ""
	}
	return template.JS(encoded)
}

func domainOf(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return ""
	}
	return strings.TrimPrefix(parsed.Host, "www.")
}

// --- Everything else ---

type simplePage struct {
	Meta pageMeta
	Year int
}

// Privacy renders the privacy policy.
func (h *Handler) Privacy(c *gin.Context) {
	data := simplePage{
		Meta: h.meta("Privacy — SaltyBytes", "What SaltyBytes collects, why, and the choices you have. Short version: the minimum to run your collection, no ads, no data selling.", "/privacy"),
		Year: time.Now().Year(),
	}
	h.render(c, http.StatusOK, "privacy.html", data, 3600)
}

// Terms renders the terms of service.
func (h *Handler) Terms(c *gin.Context) {
	data := simplePage{
		Meta: h.meta("Terms — SaltyBytes", "The terms for using SaltyBytes: your account, your content, AI and food-safety disclaimers, and the usual legal basics.", "/terms"),
		Year: time.Now().Year(),
	}
	h.render(c, http.StatusOK, "terms.html", data, 3600)
}

type notFoundData struct {
	Meta    pageMeta
	Message string
	Year    int
}

func (h *Handler) notFoundPage(c *gin.Context, message string) {
	data := notFoundData{Meta: h.meta("Not found — SaltyBytes", "This page boiled over.", c.Request.URL.Path), Message: message, Year: time.Now().Year()}
	h.render(c, http.StatusNotFound, "notfound.html", data, 60)
}

// NotFound is the engine-wide fallback: friendly HTML for browsers, JSON for
// anything that looks like an API caller.
func (h *Handler) NotFound(c *gin.Context) {
	path := c.Request.URL.Path
	apiPath := strings.HasPrefix(path, "/v1") || strings.HasPrefix(path, "/oauth") ||
		strings.HasPrefix(path, "/mcp") || strings.HasPrefix(path, "/.well-known")
	if apiPath || !strings.Contains(c.GetHeader("Accept"), "text/html") {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	h.notFoundPage(c, "This page boiled over. Let's get you back to something edible.")
}

// Robots serves robots.txt.
func (h *Handler) Robots(c *gin.Context) {
	c.Header("Cache-Control", "public, max-age=86400")
	c.String(http.StatusOK, "User-agent: *\nAllow: /\nDisallow: /v1/\nDisallow: /oauth/\nDisallow: /mcp\n\nSitemap: %s/sitemap.xml\n", h.siteBase())
}

// Sitemap lists the homepage plus every servable recipe page.
func (h *Handler) Sitemap(c *gin.Context) {
	rows, err := h.canonicals.ListServableSummaries(5000)
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	base := h.siteBase()
	fmt.Fprintf(&b, "  <url><loc>%s/</loc></url>\n", base)
	for _, row := range rows {
		fmt.Fprintf(&b, "  <url><loc>%s/r/%d</loc><lastmod>%s</lastmod></url>\n", base, row.ID, row.UpdatedAt.UTC().Format("2006-01-02"))
	}
	b.WriteString("</urlset>\n")
	c.Header("Cache-Control", "public, max-age=3600")
	c.Data(http.StatusOK, "application/xml; charset=utf-8", []byte(b.String()))
}

// Static serves embedded assets (fonts, images, stylesheet).
func (h *Handler) Static(c *gin.Context) {
	c.Header("Cache-Control", "public, max-age=86400")
	c.FileFromFS("static/"+strings.TrimPrefix(c.Param("filepath"), "/"), http.FS(staticFS))
}
