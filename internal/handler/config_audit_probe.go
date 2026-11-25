package handler

import (
	"net/http"
	"net/url"
	"strings"

	"cyberstrike-ai/internal/config"

	"github.com/gin-gonic/gin"
)

// TestAuditModelRequest overrides the saved audit model for this test only.
// Omitted fields retain their saved values; blank URL/model fields use the
// backend's normal defaults. Blank/masked keys retain the saved audit key
// unless ClearAPIKey explicitly clears it for inheritance testing.
type TestAuditModelRequest struct {
	AuditBackend *string `json:"audit_backend,omitempty"`
	Provider     *string `json:"provider,omitempty"`
	BaseURL      *string `json:"base_url"`
	APIKey       *string `json:"api_key"`
	Model        *string `json:"model"`
	ClearAPIKey  bool    `json:"clear_api_key,omitempty"`
}

// TestAuditModel selects the protocol from the configured audit backend, using
// the same configuration inheritance as the audit agent, not the selected UI channel.
func (h *ConfigHandler) TestAuditModel(c *gin.Context) {
	var req TestAuditModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的请求参数: " + err.Error()})
		return
	}
	h.mu.RLock()
	if h.config == nil {
		h.mu.RUnlock()
		c.JSON(http.StatusBadRequest, gin.H{"error": "审计模型未配置"})
		return
	}
	hitl, main := cloneHitlConfig(h.config.Hitl), h.config.OpenAI
	savedProvider, savedURL := hitl.AuditModel.Provider, hitl.AuditModel.BaseURL
	if req.AuditBackend != nil {
		hitl.AuditBackend = *req.AuditBackend
	}
	if req.Provider != nil {
		hitl.AuditModel.Provider = strings.TrimSpace(*req.Provider)
	}
	if req.BaseURL != nil {
		hitl.AuditModel.BaseURL = strings.TrimSpace(*req.BaseURL)
	}
	if probeHeaderProvider(hitl.AuditModel.Provider) != probeHeaderProvider(savedProvider) ||
		probeHeaderURL(hitl.AuditModel.Provider, hitl.AuditModel.BaseURL) != probeHeaderURL(savedProvider, savedURL) {
		hitl.AuditModel.Headers = nil
	}
	if req.Model != nil {
		hitl.AuditModel.Model = strings.TrimSpace(*req.Model)
	}
	if req.ClearAPIKey {
		hitl.AuditModel.APIKey = ""
	} else if req.APIKey != nil {
		if key := strings.TrimSpace(*req.APIKey); key != "" && key != maskedSecret {
			hitl.AuditModel.APIKey = key
		}
	}
	backend := hitl.EffectiveAuditBackend()
	var model config.OpenAIConfig
	if backend == config.HitlAuditBackendTypeSafe {
		model.BaseURL, model.APIKey, model.Model = hitl.TypeSafeConfigEffective()
	} else {
		model = hitl.AuditModelEffective(main)
	}
	h.mu.RUnlock()

	if raw := strings.TrimSpace(model.BaseURL); raw != "" {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "API 地址必须是包含 https:// 或 http:// 的完整 URL"})
			return
		}
	}
	if backend == config.HitlAuditBackendTypeSafe {
		h.testTypeSafe(c, TestTypeSafeRequest{BaseURL: model.BaseURL, APIKey: model.APIKey, Model: model.Model})
		return
	}
	h.testOpenAI(c, TestOpenAIRequest{
		Provider: model.Provider, BaseURL: model.BaseURL, APIKey: model.APIKey,
		Model: model.Model, Headers: model.Headers,
		MaxTotalTokens: model.MaxTotalTokens, MaxCompletionTokens: model.MaxCompletionTokens,
		ContextProfile: model.ContextProfile,
	})
}
