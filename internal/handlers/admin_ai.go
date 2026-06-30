package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/service"
)

// AdminAIHandler exposes the light-tier model registry + live switch to the
// admin dashboard. All routes sit behind RequireAdminToken.
type AdminAIHandler struct {
	Manager *service.AIModelManager
}

// NewAdminAIHandler creates a new AdminAIHandler.
func NewAdminAIHandler(manager *service.AIModelManager) *AdminAIHandler {
	return &AdminAIHandler{Manager: manager}
}

// modelOptionRequest is the editable shape of a registry entry. API keys are
// never accepted here — they live in env/SSM.
type modelOptionRequest struct {
	Provider           string  `json:"provider"`
	ModelID            string  `json:"model_id"`
	Label              string  `json:"label"`
	BaseURL            string  `json:"base_url"`
	InputPricePerMTok  float64 `json:"input_price_per_mtok"`
	OutputPricePerMTok float64 `json:"output_price_per_mtok"`
	Enabled            bool    `json:"enabled"`
}

func (r modelOptionRequest) toModel() *models.AIModelOption {
	return &models.AIModelOption{
		Provider:           r.Provider,
		ModelID:            r.ModelID,
		Label:              r.Label,
		BaseURL:            r.BaseURL,
		InputPricePerMTok:  r.InputPricePerMTok,
		OutputPricePerMTok: r.OutputPricePerMTok,
		Enabled:            r.Enabled,
	}
}

// ListModels returns every registered model option plus the active selection.
func (h *AdminAIHandler) ListModels(c *gin.Context) {
	opts, err := h.Manager.ListModels()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"models": opts,
		"active": h.Manager.GetActive(),
	})
}

// CreateModel validation-probes a candidate then saves it. The probe outcome is
// recorded on the returned option (validated + validation_error); a failed probe
// still saves the option (so it's visible) but it cannot be activated until it
// probes green.
func (h *AdminAIHandler) CreateModel(c *gin.Context) {
	var req modelOptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if req.Provider == "" || req.ModelID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "provider and model_id are required"})
		return
	}

	opt := req.toModel()
	if err := h.Manager.AddModel(c.Request.Context(), opt); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, opt)
}

// UpdateModel edits an option, re-probing if the model identity changed.
func (h *AdminAIHandler) UpdateModel(c *gin.Context) {
	id, err := parseUintParam(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var req modelOptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	opt, err := h.Manager.UpdateModel(c.Request.Context(), id, req.toModel())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, opt)
}

// DeleteModel removes an option.
func (h *AdminAIHandler) DeleteModel(c *gin.Context) {
	id, err := parseUintParam(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if err := h.Manager.DeleteModel(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// GetActive returns the currently-active light-tier spec.
func (h *AdminAIHandler) GetActive(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"active": h.Manager.GetActive()})
}

// activateRequest selects which registered option to activate.
type activateRequest struct {
	ID uint `json:"id"`
}

// SetActive switches the live light tier to a registered option after a green
// validation probe. On a failed probe it returns 400 and leaves the active
// model unchanged (fail-closed) — a misconfigured switch can never break prod.
func (h *AdminAIHandler) SetActive(c *gin.Context) {
	var req activateRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.ID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id is required"})
		return
	}
	opt, err := h.Manager.Activate(c.Request.Context(), req.ID)
	if err != nil {
		// Validation/probe failure → 400 with the reason; active model untouched.
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"active": h.Manager.GetActive(),
		"option": opt,
	})
}
